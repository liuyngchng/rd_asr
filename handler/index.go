package handler

import (
	"log/slog"
	"net/http"

	"rd_asr/internal/token"
)

func (s *Server) HandleIndex(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("t")
	if tok == "" {
		slog.Info("index_no_token_redirect")
		s.redirectPortal(w, r)
		return
	}
	payload, err := token.Decode(tok, []byte(s.Config.Sys.CypherKey))
	if err != nil || payload == nil {
		slog.Info("index_invalid_token_redirect", "error", err)
		s.redirectPortal(w, r)
		return
	}
	ctx := s.buildContext(payload, tok)
	slog.Info("index_render", "uid", ctx.UID, "role", payload.Role)
	s.render(w, "asr_index.html", ctx)
}

func (s *Server) HandleTaskPage(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("t")
	if tok == "" {
		slog.Info("task_page_no_token_redirect")
		s.redirectPortal(w, r)
		return
	}
	payload, err := token.Decode(tok, []byte(s.Config.Sys.CypherKey))
	if err != nil || payload == nil {
		slog.Info("task_page_invalid_token_redirect", "error", err)
		s.redirectPortal(w, r)
		return
	}
	ctx := s.buildContext(payload, tok)
	ctx.WarningInfo = r.URL.Query().Get("warning_info")
	slog.Info("task_page_render", "uid", ctx.UID)
	s.render(w, "asr_my_task.html", ctx)
}