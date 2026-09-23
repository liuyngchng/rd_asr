package handler

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"rd_asr/internal/config"
	"rd_asr/internal/i18n"
	"rd_asr/internal/store"
	"rd_asr/internal/token"
)

const AppTypeASR = "asr"

type context struct {
	Lang        string
	Dir         string
	SysName     string
	UID         string
	Token       string
	AppSource   string
	WarningInfo string
	HackAdmin   string
	I18NJS      template.JS
}

type Server struct {
	Store  *store.Store
	Config *config.Config
}

// WriteJSON sends a JSON response.
func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// WriteJSON writes JSON (convenience method on Server).
func (s *Server) WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	WriteJSON(w, status, data)
}

func (s *Server) render(w http.ResponseWriter, tmplName string, ctx context) {
	tmplPath := filepath.Join("templates", tmplName)
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		log.Printf("[template] parse error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, ctx); err != nil {
		log.Printf("[template] execute error: %v", err)
	}
}

func (s *Server) buildContext(payload *token.Payload, tokenStr string) context {
	uid := fmt.Sprintf("%d", payload.UID)
	hackAdmin := "0"
	if payload.Role == 2 {
		hackAdmin = "1"
	}
	i18nJSON, _ := json.Marshal(i18n.Map)
	return context{
		Lang:      "zh",
		Dir:       "ltr",
		SysName:   s.sysName(),
		UID:       uid,
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
	return "语音识别"
}

func (s *Server) redirectPortal(w http.ResponseWriter, r *http.Request) {
	portalURL := fmt.Sprintf("http://127.0.0.1:19000/login?app_source=%s", AppTypeASR)
	if warningInfo := r.URL.Query().Get("warning_info"); warningInfo != "" {
		portalURL += "&warning_info=" + warningInfo
	}
	http.Redirect(w, r, portalURL, http.StatusFound)
}

func (s *Server) addAccessCount(uid int) {
	statsURI := s.Config.Api.StatsAPI
	if statsURI == "" {
		return
	}
	body := fmt.Sprintf(`{"uid":%d,"count":1,"app":"%s"}`, uid, AppTypeASR)
	resp, err := http.Post(statsURI+"/statistics/access", "application/json", strings.NewReader(body))
	if err != nil {
		log.Printf("[stats] post error: %v", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
}

// GetClientIP extracts the client IP.
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