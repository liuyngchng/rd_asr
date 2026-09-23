package handler

import (
	"fmt"
	"log"
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
		log.Printf("[upload] create file error: %v", err)
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
	log.Printf("[upload] file saved: %s (original: %s)", inputPath, header.Filename)

	taskID, err := s.Store.CreateTask(header.Filename, inputPath, "", uid)
	if err != nil {
		log.Printf("[upload] create task error: %v", err)
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
	log.Printf("[%s] === START task, file=%s ===", taskID, originalName)
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[%s] PANIC: %v", taskID, r)
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

	// Step 2: VAD
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
	log.Printf("[%s] wav_loaded samples=%d (%.1fs)", taskID, len(samples), float64(len(samples))/16000)

	segments := vad.Detect(vd, samples)
	log.Printf("[%s] vad_done segments=%d", taskID, len(segments))
	if len(segments) == 0 {
		s.Store.UpdateTask(taskID, map[string]interface{}{"status": "failed", "error": "未检测到语音"})
		return
	}

	// Step 3: ASR
	var allText strings.Builder
	for i, seg := range segments {
		pct := int(float64(i) / float64(len(segments)) * 100)
		s.Store.UpdateTask(taskID, map[string]interface{}{"progress": pct})
		text, err := funasr.Send(asrHost, asrPort, seg.Samples, i)
		if err != nil {
			s.Store.UpdateTask(taskID, map[string]interface{}{"status": "failed", "error": fmt.Sprintf("ASR识别失败(segment %d): %v", i+1, err)})
			return
		}
		if i > 0 {
			allText.WriteString("\n")
		}
		allText.WriteString(text)
	}

	resultText := allText.String()
	resultFile := filepath.Join("results", taskID+".txt")
	os.WriteFile(resultFile, []byte(resultText), 0644)
	s.Store.UpdateTask(taskID, map[string]interface{}{"status": "completed", "result_text": resultText, "progress": 100})
	log.Printf("[%s] === DONE, text_length=%d ===", taskID, len(resultText))
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