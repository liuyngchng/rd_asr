package auth

import (
	"sync"
	"time"
)

// 登录限流配置
const (
	loginMaxFailures     = 5                // IP 最多连续失败次数
	loginLockDuration    = 15 * time.Minute // 锁定时长
	loginFailuresCleanup = 15 * time.Minute // 过期失败纪录清理间隔
)

type loginFailRecord struct {
	count       int
	lockedUntil time.Time
}

// LoginLimiter IP 级登录限流器
type LoginLimiter struct {
	mu    sync.Mutex
	items map[string]*loginFailRecord
}

// NewLoginLimiter 创建限流器
func NewLoginLimiter() *LoginLimiter {
	return &LoginLimiter{items: make(map[string]*loginFailRecord)}
}

// IsLocked 检查 IP 是否被锁定
func (l *LoginLimiter) IsLocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.items[ip]
	if !ok {
		return false
	}
	return rec.count >= loginMaxFailures && time.Now().Before(rec.lockedUntil)
}

// RecordFailure 记录一次登录失败
func (l *LoginLimiter) RecordFailure(ip string) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.items[ip]
	if !ok {
		rec = &loginFailRecord{}
		l.items[ip] = rec
	}
	rec.count++
	if rec.count >= loginMaxFailures {
		rec.lockedUntil = now.Add(loginLockDuration)
	}
}

// ClearFailures 清除 IP 的失败记录（登录成功后调用）
func (l *LoginLimiter) ClearFailures(ip string) {
	l.mu.Lock()
	delete(l.items, ip)
	l.mu.Unlock()
}

// StartCleanup 启动后台清理过期记录
func (l *LoginLimiter) StartCleanup() {
	go func() {
		ticker := time.NewTicker(loginFailuresCleanup)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			l.mu.Lock()
			for ip, rec := range l.items {
				if rec.count >= loginMaxFailures && now.After(rec.lockedUntil) {
					delete(l.items, ip)
				}
			}
			l.mu.Unlock()
		}
	}()
}
