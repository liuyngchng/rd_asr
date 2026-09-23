package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"rd_asr/handler"
	"rd_asr/internal/config"
	"rd_asr/internal/store"
	"rd_asr/internal/token"
)

func main() {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: config error: %v\n", err)
		os.Exit(1)
	}

	// Ensure data directories
	for _, dir := range []string{"uploads", "converted", "results"} {
		os.MkdirAll(dir, 0755)
	}

	// Initialize SQLite store
	st, err := store.New("asr.db")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: db init error: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	// Create handler server
	srv := &handler.Server{Store: st, Config: cfg}

	// HTTP routes
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			srv.HandleIndex(w, r)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/asr/task", srv.HandleTaskPage)

	mux.HandleFunc("/static/", handler.HandleStatic)
	mux.HandleFunc("/webfonts/", handler.HandleWebfonts)

	mux.HandleFunc("/api/upload", srv.HandleUpload)
	mux.HandleFunc("/api/status/", srv.HandleStatus)
	mux.HandleFunc("/api/tasks", srv.HandleAllTasks)
	mux.HandleFunc("/api/clear_tasks", srv.HandleClearTasks)

	mux.HandleFunc("/asr/my/task", srv.HandleMyTasks)
	mux.HandleFunc("/asr/download/", srv.HandleDownload)
	mux.HandleFunc("/asr/del/task", srv.HandleDeleteTask)

	// Debug token
	debugToken, _ := token.Create(1, 0, 86400, cfg.Sys.CypherKey)
	fmt.Printf("\n%s\n", repeat("=", 70))
	fmt.Printf("  Debug访问链接（直接点击进入）:\n")
	fmt.Printf("  >>> http://127.0.0.1:19010?t=%s\n", debugToken)
	fmt.Printf("  uid=1, role=0, token有效期=24h\n")
	fmt.Printf("%s\n\n", repeat("=", 70))

	port := 19010
	log.Printf("[main] asr_service_listen_on_port %d", port)
	if err := http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", port), mux); err != nil {
		log.Fatalf("[main] server error: %v", err)
	}
}

func repeat(s string, n int) string {
	var r string
	for i := 0; i < n; i++ {
		r += s
	}
	return r
}