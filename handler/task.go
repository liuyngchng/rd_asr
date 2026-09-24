package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"rd_asr/internal/store"
)

func (s *Server) HandleStatus(w http.ResponseWriter, r *http.Request) {
	taskID := extractTaskID(r.URL.Path, "/api/status/")
	if taskID == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 task_id"})
		return
	}
	task, err := s.Store.GetTask(taskID)
	if err != nil || task == nil {
		WriteJSON(w, http.StatusNotFound, map[string]string{"error": "任务不存在"})
		return
	}
	resp := map[string]interface{}{
		"task_id":           task.TaskID,
		"status":            task.Status,
		"progress":          task.Progress,
		"original_filename": task.OriginalFilename,
	}
	if task.ResultText != nil {
		resp["result_text"] = *task.ResultText
	}
	if task.Error != nil {
		resp["error"] = *task.Error
	}
	WriteJSON(w, http.StatusOK, resp)
}

func (s *Server) HandleMyTasks(w http.ResponseWriter, r *http.Request) {
	var body struct{ UID int `json:"uid,string"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的请求体"})
		return
	}
	slog.Debug("my_tasks_query", "uid", body.UID)
	tasks, err := s.Store.GetUserTasks(body.UID, 100)
	if err != nil {
		slog.Warn("my_tasks_query_error", "error", err, "uid", body.UID)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "查询任务失败"})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"tasks": tasks})
}

func (s *Server) HandleDeleteTask(w http.ResponseWriter, r *http.Request) {
	var body struct{ TaskID string `json:"task_id"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的请求体"})
		return
	}
	if body.TaskID == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 task_id"})
		return
	}
	slog.Debug("delete_task", "task_id", body.TaskID)

	// 先取消正在运行的 goroutine（如果有的话）
	s.cancelTask(body.TaskID)

	task, _ := s.Store.GetTask(body.TaskID)
	if task != nil {
		cleanFiles(task)
	}
	if err := s.Store.DeleteTask(body.TaskID); err != nil {
		slog.Warn("delete_task_error", "error", err, "task_id", body.TaskID)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "删除失败"})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{"message": "已删除"})
}

func (s *Server) HandleAllTasks(w http.ResponseWriter, r *http.Request) {
	uid := 0
	if uidStr := r.URL.Query().Get("uid"); uidStr != "" {
		fmt.Sscanf(uidStr, "%d", &uid)
	}
	tasks, err := s.Store.GetUserTasks(uid, 20)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "查询任务失败"})
		return
	}
	result := make([]map[string]interface{}, 0, len(tasks))
	for _, t := range tasks {
		result = append(result, map[string]interface{}{
			"task_id":           t.TaskID,
			"original_filename": t.OriginalFilename,
			"status":            t.Status,
			"timestamp":         t.CreatedAt,
			"has_result": t.Status == string(StatusCompleted),
		})
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"tasks": result})
}

func (s *Server) HandleClearTasks(w http.ResponseWriter, r *http.Request) {
	var body struct{ UID int `json:"uid,string"` }
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&body)
	}
	tasks, err := s.Store.GetUserTasks(body.UID, 1000)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "查询任务失败"})
		return
	}
	count := 0
	for _, t := range tasks {
		if t.Status == string(StatusCompleted) || t.Status == string(StatusFailed) {
			// 稳妥起见：即使理论上已完成/失败的任务没有 goroutine 在跑，也先取消一次
			s.cancelTask(t.TaskID)
			cleanFiles(&t)
			s.Store.DeleteTask(t.TaskID)
			count++
		}
	}
	WriteJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("已清理 %d 个任务", count)})
}

func cleanFiles(task *store.Task) {
	removeFile(task.OriginalPath)
	removeFile(task.ConvertedPath)
	removeFile(filepath.Join("results", task.TaskID+".txt"))
	removeDir(filepath.Join("results", task.TaskID))
}

// removeFile 先检查文件是否存在，存在才删除；删除失败仅告警（与数据库删除非原子）。
func removeFile(path string) {
	if path == "" {
		return
	}
	if !fileExists(path) {
		return
	}
	if err := os.Remove(path); err != nil {
		slog.Warn("clean_file_failed", "path", path, "error", err)
	}
}

// removeDir 先检查目录是否存在，存在才删除。
func removeDir(path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return
	}
	if err := os.RemoveAll(path); err != nil {
		slog.Warn("clean_dir_failed", "path", path, "error", err)
	}
}

func (s *Server) HandleDownload(w http.ResponseWriter, r *http.Request) {
	taskID := extractTaskID(r.URL.Path, "/asr/download/")
	if taskID == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 task_id"})
		return
	}
	task, err := s.Store.GetTask(taskID)
	if err != nil || task == nil {
		WriteJSON(w, http.StatusNotFound, map[string]string{"error": "任务不存在"})
		return
	}
	if task.Status != string(StatusCompleted) || task.ResultText == nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "任务未完成或结果不存在"})
		return
	}

	resultFile := filepath.Join("results", taskID+".txt")
	if _, err := os.Stat(resultFile); os.IsNotExist(err) {
		os.WriteFile(resultFile, []byte(*task.ResultText), 0644)
	}

	originalStem := task.OriginalFilename
	if ext := filepath.Ext(originalStem); ext != "" {
		originalStem = originalStem[:len(originalStem)-len(ext)]
	}
	downloadName := fmt.Sprintf("%s_转写结果.txt", originalStem)
	downloadName = urlEncode(downloadName)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename*=UTF-8''%s`, downloadName))
	http.ServeFile(w, r, resultFile)
	slog.Info("download_result", "task_id", taskID)
}

func urlEncode(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && !shouldEncode(r) {
			b.WriteRune(r)
		} else {
			b.WriteString(fmt.Sprintf("%%%X", r))
		}
	}
	return b.String()
}

func shouldEncode(r rune) bool {
	return !(('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') ||
		r == '-' || r == '_' || r == '.' || r == '~')
}

// HandleRetryTask 重新处理失败的任务（利用断点续传，从出错处接着干）
func (s *Server) HandleRetryTask(w http.ResponseWriter, r *http.Request) {
	var body struct{ TaskID string `json:"task_id"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的请求体"})
		return
	}
	if body.TaskID == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 task_id"})
		return
	}

	task, err := s.Store.GetTask(body.TaskID)
	if err != nil || task == nil {
		WriteJSON(w, http.StatusNotFound, map[string]string{"error": "任务不存在"})
		return
	}
	if task.Status != string(StatusFailed) {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "只能重试失败的任务"})
		return
	}

	// 原始文件处理结束后已被删除，重试只能依赖已生成的中间产物：
	// 有 converted wav 即可跳过 ffmpeg 继续 VAD/ASR，否则无法恢复。
	if !fileExists(task.ConvertedPath) && !fileExists(task.OriginalPath) {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "原始文件已丢失，无法重试"})
		return
	}

	// 重置为 converting，重新跑流水线（ffmpeg/VAD 会跳过已存在的中间文件）
	s.Store.UpdateTask(task.TaskID, map[string]interface{}{
		"status": "converting", "error": nil, "progress": 0,
	})
	slog.Info("task_retry", "task_id", task.TaskID)

	go s.processAudio(context.Background(), task.TaskID, task.OriginalPath, s.Config.Funasr.Host, s.Config.Funasr.Port)

	WriteJSON(w, http.StatusOK, map[string]string{"message": "已提交重试"})
}