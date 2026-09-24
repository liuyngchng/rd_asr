package handler

import (
	"log/slog"
	"net/http"

	"rd_asr/internal/auth"
)

func (s *Server) HandleIndex(w http.ResponseWriter, r *http.Request) {
	tok := tokenFromRequest(r)

	// ?t= 回退：设置为 Cookie 后重定向去掉 URL 参数
	if tok == "" {
		tok = r.URL.Query().Get("t")
	}
	if tok == "" {
		slog.Info("index_no_token_redirect")
		s.redirectPortal(w, r)
		return
	}

	payload := auth.DecodeToken(tok, auth.GetTokenSecret(s.Config.Sys.TokenSecret))
	if payload == nil {
		slog.Info("index_invalid_token_redirect")
		clearAuthCookie(w)
		s.redirectPortal(w, r)
		return
	}

	// 如果 token 来自 URL，存入 Cookie 后重定向到 / 去掉 ?t=...
	if _, err := r.Cookie(cookieAuthToken); err != nil {
		setAuthCookie(w, tok, int(auth.TokenTTL.Seconds()), r)
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	ctx := s.buildContext(payload, tok)
	slog.Info("index_render", "uid", ctx.UID, "role", payload.Role)
	s.render(w, "asr_index.html", ctx)
}

func (s *Server) HandleTaskPage(w http.ResponseWriter, r *http.Request) {
	tok := tokenFromRequest(r)

	if tok == "" {
		tok = r.URL.Query().Get("t")
	}
	if tok == "" {
		slog.Info("task_page_no_token_redirect")
		s.redirectPortal(w, r)
		return
	}

	payload := auth.DecodeToken(tok, auth.GetTokenSecret(s.Config.Sys.TokenSecret))
	if payload == nil {
		slog.Info("task_page_invalid_token_redirect")
		clearAuthCookie(w)
		s.redirectPortal(w, r)
		return
	}

	// URL token → Cookie redirect
	if _, err := r.Cookie(cookieAuthToken); err != nil {
		setAuthCookie(w, tok, int(auth.TokenTTL.Seconds()), r)
		http.Redirect(w, r, "/asr/task", http.StatusFound)
		return
	}

	ctx := s.buildContext(payload, tok)
	ctx.WarningInfo = r.URL.Query().Get("warning_info")
	slog.Info("task_page_render", "uid", ctx.UID)
	s.render(w, "asr_my_task.html", ctx)
}