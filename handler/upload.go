package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"rd_asr/internal/funasr"
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

	safeFilename := fmt.Sprintf("%d%s", time.Now().UnixMilli(), ext)
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

	go s.processAudio(context.Background(), taskID, inputPath, s.Config.Funasr.Host, s.Config.Funasr.Port)

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"task_id": taskID,
		"status":  StatusConverting,
		"message": "文件已上传，后台开始处理...",
	})
}

func (s *Server) processAudio(parent context.Context, taskID, inputPath, asrHost string, asrPort int) {
	// 注册可取消 context
	ctx, cancel := context.WithCancel(parent)
	s.registerCancel(taskID, cancel)
	defer s.unregisterCancel(taskID)

	originalName := filepath.Base(inputPath)
	slog.Info("task_start", "task_id", taskID, "file", originalName)
	defer func() {
		if r := recover(); r != nil {
			slog.Error("task_panic", "task_id", taskID, "panic", r)
			s.Store.UpdateTask(taskID, map[string]interface{}{"status": StatusFailed, "error": fmt.Sprintf("内部错误: %v", r)})
		}
		os.Remove(inputPath)
	}()

	task, _ := s.Store.GetTask(taskID)
	completed := 0
	if task != nil {
		completed = task.CompletedSegments
	}

	wavPath := task.ConvertedPath

	// Step 1: ffmpeg（已完成转换则跳过）
	if wavPath == "" || !fileExists(wavPath) {
		s.Store.UpdateTask(taskID, map[string]interface{}{"status": StatusConverting, "progress": 0})
		originalStem := strings.TrimSuffix(originalName, filepath.Ext(originalName))
		wavFilename := fmt.Sprintf("%s_%d.wav", originalStem, time.Now().UnixMilli())
		wavPath = filepath.Join("converted", wavFilename)
		if err := convertToWav(inputPath, wavPath); err != nil {
			s.Store.UpdateTask(taskID, map[string]interface{}{"status": StatusFailed, "error": fmt.Sprintf("音频转换失败: %v", err)})
			return
		}
		slog.Info("task_convert_done", "task_id", taskID)
		s.Store.UpdateTask(taskID, map[string]interface{}{"converted_path": wavPath, "progress": 100})
	}

	// Step 2: VAD（每次必跑，因分段数据不持久化）
	s.Store.UpdateTask(taskID, map[string]interface{}{"status": StatusSplitting, "progress": 0})
	vd, err := vad.LoadModel("silero_vad.onnx")
	if err != nil {
		s.Store.UpdateTask(taskID, map[string]interface{}{"status": StatusFailed, "error": fmt.Sprintf("加载VAD模型失败: %v", err)})
		return
	}
	defer sherpa.DeleteVoiceActivityDetector(vd)

	samples, err := vad.ReadWav(wavPath)
	if err != nil {
		s.Store.UpdateTask(taskID, map[string]interface{}{"status": StatusFailed, "error": fmt.Sprintf("读取音频失败: %v", err)})
		return
	}
	totalDur := dur(samples)
	// 记录音频时长（秒），用于任务列表展示
	s.Store.UpdateTask(taskID, map[string]interface{}{"audio_duration_sec": int(float64(len(samples)) / 16000)})
	slog.Info("task_wav_loaded", "task_id", taskID, "duration", totalDur)

	var lastPct int
	segments := vad.Detect(vd, samples, func(processed, total int) {
		pct := processed * 100 / total
		if pct-lastPct >= 10 || pct >= 100 {
			lastPct = pct
			slog.Info("task_vad_progress", "task_id", taskID,
				"processed", durSec(float64(processed)), "total", durSec(float64(total)), "pct", pct)
			s.Store.UpdateTask(taskID, map[string]interface{}{"progress": pct})
		}
	})

	if len(segments) == 0 {
		s.Store.UpdateTask(taskID, map[string]interface{}{"status": StatusFailed, "error": "未检测到语音"})
		return
	}

	totalSpeech := 0
	for _, seg := range segments {
		totalSpeech += len(seg.Samples)
	}
	slog.Info("task_vad_done", "task_id", taskID,
		"segments", len(segments), "total_speech", durSec(float64(totalSpeech)), "resume_from", completed)
	s.Store.UpdateTask(taskID, map[string]interface{}{"progress": 100})

	// Step 3: ASR — 断点续传，跳过已完成段
	s.Store.UpdateTask(taskID, map[string]interface{}{"status": StatusTranscribing, "progress": 0})
	transcribeStart := time.Now()

	// 从结果文件恢复已转录文本
	var allText strings.Builder
	resultFile := filepath.Join("results", taskID+".txt")
	if data, err := os.ReadFile(resultFile); err == nil {
		allText.Write(data)
	}

	totalSeg := len(segments)
	processedSpeech := 0
	for i, seg := range segments {
		if i < completed {
			processedSpeech += len(seg.Samples)
			continue
		}
		segNum := i + 1
		segDur := durSec(float64(len(seg.Samples)))
		slog.Info("task_asr_segment", "task_id", taskID,
			"segment", fmt.Sprintf("%d/%d", segNum, totalSeg), "duration", segDur)

		text, err := funasr.Send(ctx, asrHost, asrPort, seg.Samples, i)
		if err != nil {
			// 被用户取消：不是错误，直接退出
			if ctx.Err() != nil {
				slog.Info("task_cancelled", "task_id", taskID,
					"segment", fmt.Sprintf("%d/%d", segNum, totalSeg))
				return
			}
			slog.Error("task_asr_failed", "task_id", taskID,
				"segment", fmt.Sprintf("%d/%d", segNum, totalSeg), "error", err)
			s.Store.UpdateTask(taskID, map[string]interface{}{
				"status": StatusFailed, "error": fmt.Sprintf("ASR识别失败(segment %d/%d): %v", segNum, totalSeg, err),
			})
			return
		}
		if allText.Len() > 0 {
			allText.WriteString("\n")
		}
		allText.WriteString(text)

		// 每完成一段就持久化：写结果文件 + 更新 completed_segments
		os.WriteFile(resultFile, []byte(allText.String()), 0644)
		completed = segNum
		s.Store.UpdateTask(taskID, map[string]interface{}{"completed_segments": completed})

		processedSpeech += len(seg.Samples)
		progress := processedSpeech * 100 / totalSpeech
		s.Store.UpdateTask(taskID, map[string]interface{}{"progress": progress})
		slog.Info("task_asr_done", "task_id", taskID,
			"segment", fmt.Sprintf("%d/%d", segNum, totalSeg),
			"processed", durSec(float64(processedSpeech)), "total", durSec(float64(totalSpeech)),
			"pct", progress, "result_len", len(text))
	}

	resultText := allText.String()
	s.Store.UpdateTask(taskID, map[string]interface{}{
		"status":                     StatusCompleted,
		"result_text":                resultText,
		"progress":                   100,
		"transcription_duration_sec": int(time.Since(transcribeStart).Seconds()),
	})
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ProcessResumable 扫描所有非终态任务，续传处理。
// 服务重启时由 main.go 调用。
func (s *Server) ProcessResumable() {
	tasks, err := s.Store.GetResumableTasks()
	if err != nil {
		slog.Warn("resume_scan_failed", "error", err)
		return
	}
	if len(tasks) == 0 {
		return
	}
	slog.Info("resume_tasks_found", "count", len(tasks))
	for _, t := range tasks {
		if t.OriginalPath == "" || !fileExists(t.OriginalPath) {
			slog.Warn("resume_skip_no_file", "task_id", t.TaskID, "status", t.Status)
			s.Store.UpdateTask(t.TaskID, map[string]interface{}{"status": StatusFailed, "error": "原始文件丢失"})
			continue
		}
		slog.Info("resume_task", "task_id", t.TaskID, "status", t.Status, "completed_segments", t.CompletedSegments)
		go s.processAudio(context.Background(), t.TaskID, t.OriginalPath, s.Config.Funasr.Host, s.Config.Funasr.Port)
	}
}
