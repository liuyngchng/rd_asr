package handler

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var staticDirs = []string{"static", filepath.Join("..", "llm_agent", "common", "static")}

func serveStaticPrefix(w http.ResponseWriter, r *http.Request, prefix string) {
	relPath := strings.TrimPrefix(r.URL.Path, prefix)
	if relPath == "" || relPath == "/" {
		http.NotFound(w, r)
		return
	}
	for _, dir := range staticDirs {
		fullPath := filepath.Join(dir, relPath)
		if _, err := os.Stat(fullPath); err == nil {
			log.Printf("[static] serving %s from %s", relPath, dir)
			http.ServeFile(w, r, fullPath)
			return
		}
	}
	log.Printf("[static] not found: %s", r.URL.Path)
	http.NotFound(w, r)
}

func HandleStatic(w http.ResponseWriter, r *http.Request) {
	serveStaticPrefix(w, r, "/static/")
}

func HandleWebfonts(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/webfonts/")
	r.URL.Path = "/static/webfonts/" + rel
	serveStaticPrefix(w, r, "/static/")
}