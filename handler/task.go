package handler

import (
	"encoding/json"
	"fmt"
	"log"
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
	var body struct{ UID int `json:"uid"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的请求体"})
		return
	}
	log.Printf("[my_tasks] uid=%d", body.UID)
	tasks, err := s.Store.GetUserTasks(body.UID, 100)
	if err != nil {
		log.Printf("[my_tasks] query error: %v", err)
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
	log.Printf("[delete_task] task_id=%s", body.TaskID)
	task, _ := s.Store.GetTask(body.TaskID)
	if task != nil {
		cleanFiles(task)
	}
	if err := s.Store.DeleteTask(body.TaskID); err != nil {
		log.Printf("[delete_task] error: %v", err)
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
			"has_result":        t.Status == "completed",
		})
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"tasks": result})
}

func (s *Server) HandleClearTasks(w http.ResponseWriter, r *http.Request) {
	var body struct{ UID int `json:"uid"` }
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
		if t.Status == "completed" || t.Status == "failed" {
			cleanFiles(&t)
			s.Store.DeleteTask(t.TaskID)
			count++
		}
	}
	WriteJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("已清理 %d 个任务", count)})
}

func cleanFiles(task *store.Task) {
	if task.OriginalPath != "" {
		os.Remove(task.OriginalPath)
	}
	if task.ConvertedPath != "" {
		os.Remove(task.ConvertedPath)
	}
	os.Remove(filepath.Join("results", task.TaskID+".txt"))
	os.RemoveAll(filepath.Join("results", task.TaskID))
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
	if task.Status != "completed" || task.ResultText == nil {
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
	log.Printf("[download] task_id=%s, file=%s", taskID, downloadName)
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