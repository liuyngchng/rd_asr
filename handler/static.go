package handler

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var staticDirs = []string{"static", filepath.Join("..", "llm_agent", "common", "static")}

var mimeByExt = map[string]string{
	".css":  "text/css",
	".js":   "text/javascript",
	".html": "text/html",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webm": "video/webm",
	".svg":  "image/svg+xml",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".map":   "application/json",
	".json":  "application/json",
}

func serveStaticPrefix(w http.ResponseWriter, r *http.Request, prefix string) {
	relPath := strings.TrimPrefix(r.URL.Path, prefix)
	if relPath == "" || relPath == "/" {
		http.NotFound(w, r)
		return
	}
	for _, dir := range staticDirs {
		fullPath := filepath.Join(dir, relPath)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		ctype, ok := mimeByExt[filepath.Ext(relPath)]
		if !ok {
			ctype = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ctype)
		w.Write(data)
		slog.Debug("static_serve", "path", relPath, "from", dir)
		return
	}
	slog.Warn("static_not_found", "path", r.URL.Path)
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