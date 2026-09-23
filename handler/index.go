package handler

import (
	"log"
	"net/http"

	"rd_asr/internal/token"
)

func (s *Server) HandleIndex(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("t")
	if tok == "" {
		log.Printf("[index] no_token, redirect to portal login")
		s.redirectPortal(w, r)
		return
	}
	payload, err := token.Decode(tok, []byte(s.Config.Sys.CypherKey))
	if err != nil || payload == nil {
		log.Printf("[index] invalid token: %v, redirect to portal login", err)
		s.redirectPortal(w, r)
		return
	}
	go s.addAccessCount(payload.UID)
	ctx := s.buildContext(payload, tok)
	log.Printf("[index] render page uid=%s role=%d", ctx.UID, payload.Role)
	s.render(w, "asr_index.html", ctx)
}

func (s *Server) HandleTaskPage(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("t")
	if tok == "" {
		log.Printf("[task_page] no_token, redirect to portal login")
		s.redirectPortal(w, r)
		return
	}
	payload, err := token.Decode(tok, []byte(s.Config.Sys.CypherKey))
	if err != nil || payload == nil {
		log.Printf("[task_page] invalid token: %v, redirect to portal login", err)
		s.redirectPortal(w, r)
		return
	}
	go s.addAccessCount(payload.UID)
	ctx := s.buildContext(payload, tok)
	ctx.WarningInfo = r.URL.Query().Get("warning_info")
	log.Printf("[task_page] render page uid=%s", ctx.UID)
	s.render(w, "asr_my_task.html", ctx)
}