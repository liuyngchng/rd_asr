package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"rd_asr/internal/auth"
	"rd_asr/internal/config"
	"rd_asr/internal/i18n"
	"rd_asr/internal/store"
)

const AppTypeASR = "asr"

type pageCtx struct {
	Lang        string
	Dir         string
	SysName     string
	UID         string
	UserName    string
	Token       string
	AppSource   string
	WarningInfo string
	HackAdmin   string
	I18NJS      template.JS
}

type Server struct {
	Store  *store.Store
	Config *config.Config

	mu      sync.Mutex
	cancels map[string]context.CancelFunc // taskID → cancel
}

// WriteJSON sends a JSON response.
func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	WriteJSON(w, status, data)
}

func (s *Server) render(w http.ResponseWriter, tmplName string, ctx pageCtx) {
	tmplPath := filepath.Join("templates", tmplName)
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		slog.Error("template_parse", "error", err, "name", tmplName)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, ctx); err != nil {
		slog.Error("template_execute", "error", err, "name", tmplName)
	}
}

func (s *Server) buildContext(payload *auth.Payload, tokenStr string) pageCtx {
	uid := fmt.Sprintf("%d", payload.UID)
	hackAdmin := "0"
	if payload.Role == 2 {
		hackAdmin = "1"
	}
	i18nJSON, _ := json.Marshal(i18n.Map)
	return pageCtx{
		Lang:      "zh",
		Dir:       "ltr",
		SysName:   s.sysName(),
		UID:       uid,
		UserName:  payload.UserName,
		Token:     tokenStr,
		AppSource: AppTypeASR,
		HackAdmin: hackAdmin,
		I18NJS:    template.JS(i18nJSON),
	}
}

func (s *Server) sysName() string {
	if s.Config.Sys.Name != "" {
		return s.Config.Sys.Name
	}
	return "语音转写"
}

func (s *Server) redirectPortal(w http.ResponseWriter, r *http.Request) {
	portalURL := "/login"
	if warningInfo := r.URL.Query().Get("warning_info"); warningInfo != "" {
		portalURL += "?warning_info=" + warningInfo
	}
	http.Redirect(w, r, portalURL, http.StatusFound)
}

func GetClientIP(r *http.Request) string {
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		return strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
	}
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		return ip[:idx]
	}
	return ip
}

func extractTaskID(urlPath, prefix string) string {
	trimmed := strings.TrimPrefix(urlPath, prefix)
	if trimmed == urlPath {
		return ""
	}
	if idx := strings.Index(trimmed, "?"); idx != -1 {
		trimmed = trimmed[:idx]
	}
	return strings.TrimRight(trimmed, "/")
}

// registerCancel 注册一个可取消的任务。
func (s *Server) registerCancel(taskID string, cancel context.CancelFunc) {
	s.mu.Lock()
	if s.cancels == nil {
		s.cancels = make(map[string]context.CancelFunc)
	}
	s.cancels[taskID] = cancel
	s.mu.Unlock()
}

// cancelTask 取消正在运行的任务 goroutine（如果存在）。
// 返回 true 表示确实有任务被取消。
func (s *Server) cancelTask(taskID string) bool {
	s.mu.Lock()
	cancel, ok := s.cancels[taskID]
	if ok {
		delete(s.cancels, taskID)
	}
	s.mu.Unlock()
	if ok {
		cancel()
		return true
	}
	return false
}

// unregisterCancel 任务完成后注销 cancel（不在 cancelTask 时做，避免重复 delete）。
func (s *Server) unregisterCancel(taskID string) {
	s.mu.Lock()
	delete(s.cancels, taskID)
	s.mu.Unlock()
}