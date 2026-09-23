package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"rd_asr/handler"
	"rd_asr/internal/config"
	"rd_asr/internal/logger"
	"rd_asr/internal/store"
	"rd_asr/internal/token"
)

func main() {
	// 加载配置
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: config error: %v\n", err)
		os.Exit(1)
	}

	// 初始化日志（控制台 + 文件双输出）
	if err := logger.Init(false); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: logger init error: %v\n", err)
		os.Exit(1)
	}

	// 确保数据目录存在
	for _, dir := range []string{"uploads", "converted", "results"} {
		os.MkdirAll(dir, 0755)
	}

	// 初始化 SQLite 任务存储
	st, err := store.New("asr.db")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: db init error: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	// 创建 handler
	srv := &handler.Server{Store: st, Config: cfg}

	// HTTP 路由
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
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
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
	slog.Info("asr_service_listen", "port", port, "url", fmt.Sprintf("http://127.0.0.1:%d", port))
	if err := http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", port), mux); err != nil {
		slog.Error("server_start_failed", "error", err)
		os.Exit(1)
	}
}

func repeat(s string, n int) string {
	var r string
	for i := 0; i < n; i++ {
		r += s
	}
	return r
}