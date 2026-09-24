package handler

import (
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// fileRetentionDir 参与保留期清理的目录。
var fileRetentionDir = []string{"uploads", "converted", "results"}

// CleanExpiredFiles 删除超过保留期的磁盘文件（基于文件修改时间）。
// 由 main.go 启动时调用一次，并启动后台定时任务周期性调用。
// 注意：只清理磁盘文件，不改动数据库记录；任务记录仍可查询历史。
func (s *Server) CleanExpiredFiles() {
	retentionDays := s.Config.Sys.FileRetentionDays
	if retentionDays <= 0 {
		retentionDays = 30
	}
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)

	for _, dir := range fileRetentionDir {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// 目录不存在（如首次运行前）属正常情况
			if !os.IsNotExist(err) {
				slog.Warn("clean_scan_dir_failed", "dir", dir, "error", err)
			}
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			path := filepath.Join(dir, e.Name())
			info, err := e.Info()
			if err != nil {
				slog.Warn("clean_stat_failed", "path", path, "error", err)
				continue
			}
			if info.ModTime().Before(cutoff) {
				if err := os.Remove(path); err != nil {
					if os.IsNotExist(err) {
						continue
					}
					slog.Warn("clean_expired_failed", "path", path, "error", err)
				} else {
					slog.Info("clean_expired", "path", path, "retention_days", retentionDays)
				}
			}
		}
	}
}

// StartFileCleaner 启动后台定时清理（每 24h 一次），返回 stop 函数。
func (s *Server) StartFileCleaner() func() {
	ticker := time.NewTicker(24 * time.Hour)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				s.CleanExpiredFiles()
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()
	return func() { close(done) }
}
