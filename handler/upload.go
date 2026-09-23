package handler

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"rd_asr/internal/funasr"
	"rd_asr/internal/store"
	"rd_asr/internal/vad"

	sherpa "github.com/k2-fsa/sherpa-onnx-go-linux"
)

var supportedFormats = map[string]bool{
	".m4a": true, ".mp3": true, ".amr": true, ".wav": true,
	".flac": true, ".ogg": true, ".aac": true,
}

func (s *Server) HandleUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(500 << 20); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "无法解析上传数据"})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "没有文件"})
		return
	}
	defer file.Close()

	if header.Filename == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "文件名为空"})
		return
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !supportedFormats[ext] {
		formats := make([]string, 0, len(supportedFormats))
		for f := range supportedFormats {
			formats = append(formats, f)
		}
		WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("不支持的文件格式，支持: %s", strings.Join(formats, ", ")),
		})
		return
	}

	uid := 0
	if uidStr := r.FormValue("uid"); uidStr != "" {
		fmt.Sscanf(uidStr, "%d", &uid)
	}

	safeFilename := fmt.Sprintf("%s%s", store.UUID(), ext)
	inputPath := filepath.Join("uploads", safeFilename)
	saveFile, err := os.Create(inputPath)
	if err != nil {
		slog.Error("upload_create_file", "error", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存文件失败"})
		return
	}
	defer saveFile.Close()

	buf := make([]byte, 32*1024)
	for {
		n, err := file.Read(buf)
		if n > 0 {
			saveFile.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}

	taskID, err := s.Store.CreateTask(header.Filename, inputPath, "", uid)
	if err != nil {
		slog.Error("upload_create_task", "error", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "创建任务失败"})
		return
	}

	go s.processAudio(taskID, inputPath, s.Config.Funasr.Host, s.Config.Funasr.Port)

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"task_id": taskID,
		"status":  "converting",
		"message": "文件已上传，后台开始处理...",
	})
}

func (s *Server) processAudio(taskID, inputPath, asrHost string, asrPort int) {
	originalName := filepath.Base(inputPath)
	slog.Info("task_start", "task_id", taskID, "file", originalName)
	defer func() {
		if r := recover(); r != nil {
			slog.Error("task_panic", "task_id", taskID, "panic", r)
			s.Store.UpdateTask(taskID, map[string]interface{}{"status": "failed", "error": fmt.Sprintf("内部错误: %v", r)})
		}
		os.Remove(inputPath)
	}()

	// Step 1: ffmpeg
	s.Store.UpdateTask(taskID, map[string]interface{}{"status": "converting"})
	originalStem := strings.TrimSuffix(originalName, filepath.Ext(originalName))
	wavFilename := fmt.Sprintf("%s_%s.wav", originalStem, store.UUID()[:8])
	wavPath := filepath.Join("converted", wavFilename)

	if err := convertToWav(inputPath, wavPath); err != nil {
		s.Store.UpdateTask(taskID, map[string]interface{}{"status": "failed", "error": fmt.Sprintf("音频转换失败: %v", err)})
		return
	}
	s.Store.UpdateTask(taskID, map[string]interface{}{"converted_path": wavPath, "status": "processing", "progress": 0})

	// Step 2: VAD — 分块处理，记录进度
	vd, err := vad.LoadModel("silero_vad.onnx")
	if err != nil {
		s.Store.UpdateTask(taskID, map[string]interface{}{"status": "failed", "error": fmt.Sprintf("加载VAD模型失败: %v", err)})
		return
	}
	defer sherpa.DeleteVoiceActivityDetector(vd)

	samples, err := vad.ReadWav(wavPath)
	if err != nil {
		s.Store.UpdateTask(taskID, map[string]interface{}{"status": "failed", "error": fmt.Sprintf("读取音频失败: %v", err)})
		return
	}
	totalDur := dur(samples)
	slog.Info("task_wav_loaded", "task_id", taskID, "duration", totalDur)

	var lastPct int
	segments := vad.Detect(vd, samples, func(processed, total int) {
		pct := processed * 100 / total
		if pct-lastPct >= 10 || pct >= 100 {
			lastPct = pct
			slog.Info("task_vad_progress", "task_id", taskID,
				"processed", durSec(float64(processed)), "total", durSec(float64(total)), "pct", pct)
			// VAD 只是预处理，整体进度只占 0~5%
			s.Store.UpdateTask(taskID, map[string]interface{}{"progress": pct * 5 / 100})
		}
	})

	if len(segments) == 0 {
		s.Store.UpdateTask(taskID, map[string]interface{}{"status": "failed", "error": "未检测到语音"})
		return
	}

	// 统计总语音时长，用于 ASR 阶段按段时长加权计算进度
	totalSpeech := 0
	for _, seg := range segments {
		totalSpeech += len(seg.Samples)
	}
	slog.Info("task_vad_done", "task_id", taskID,
		"segments", len(segments), "total_speech", durSec(float64(totalSpeech)))

	// Step 3: ASR — 逐段串行发送 FunASR（等上一段返回结果后再发下一段）
	// 进度按段时长加权：5% ~ 100%，长段耗时久、进度推进慢，符合直觉。
	var allText strings.Builder
	totalSeg := len(segments)
	processedSpeech := 0
	for i, seg := range segments {
		segNum := i + 1
		segDur := durSec(float64(len(seg.Samples)))
		slog.Info("task_asr_segment", "task_id", taskID,
			"segment", fmt.Sprintf("%d/%d", segNum, totalSeg), "duration", segDur)

		text, err := funasr.Send(asrHost, asrPort, seg.Samples, i)
		if err != nil {
			s.Store.UpdateTask(taskID, map[string]interface{}{
				"status": "failed", "error": fmt.Sprintf("ASR识别失败(segment %d/%d): %v", segNum, totalSeg, err),
			})
			return
		}
		if i > 0 {
			allText.WriteString("\n")
		}
		allText.WriteString(text)

		// 段完成，累计已处理语音时长，更新加权进度
		processedSpeech += len(seg.Samples)
		progress := 5 + 95*processedSpeech/totalSpeech
		s.Store.UpdateTask(taskID, map[string]interface{}{"progress": progress})
		slog.Info("task_asr_done", "task_id", taskID,
			"segment", fmt.Sprintf("%d/%d", segNum, totalSeg),
			"processed", durSec(float64(processedSpeech)), "total", durSec(float64(totalSpeech)),
			"pct", progress, "result_len", len(text))
	}

	resultText := allText.String()
	resultFile := filepath.Join("results", taskID+".txt")
	os.WriteFile(resultFile, []byte(resultText), 0644)
	s.Store.UpdateTask(taskID, map[string]interface{}{"status": "completed", "result_text": resultText, "progress": 100})
	slog.Info("task_done", "task_id", taskID, "text_length", len(resultText))
}

func convertToWav(inputPath, outputPath string) error {
	cmd := exec.Command("ffmpeg",
		"-i", inputPath,
		"-acodec", "pcm_s16le",
		"-ar", "16000",
		"-ac", "1",
		"-y", outputPath,
	)
	stderr, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg error: %v (stderr: %s)", err, string(stderr))
	}
	return nil
}

// durSec 将样本数转换为可读时长字符串，如 "01:36:00"（1小时36分）。
func durSec(n float64) string {
	sec := int(n / 16000)
	return fmt.Sprintf("%02d:%02d:%02d", sec/3600, (sec%3600)/60, sec%60)
}

// dur 将样本切片转换为可读时长字符串。
func dur(samples []float32) string {
	return durSec(float64(len(samples)))
}