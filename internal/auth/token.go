package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// token 有效期 2 小时
const TokenTTL = 2 * time.Hour

// TokenBlacklist 全局 token 黑名单（退出登录后立即失效）
var TokenBlacklist = &tokenBlacklist{}

// Payload token 中携带的用户信息
type Payload struct {
	UID      int     `json:"uid"`
	UserName string  `json:"user_name"`
	Role     int     `json:"role"`
	Exp      float64 `json:"exp"`
}

// tokenBlacklist 存储已注销的 token 签名及其过期时间
type tokenBlacklist struct {
	mu    sync.Mutex
	items map[string]time.Time // sig → expiry
	once  sync.Once
}

// Add 将 token 加入黑名单
func (b *tokenBlacklist) Add(tokenStr string, secret []byte) {
	data, err := base64.RawURLEncoding.DecodeString(tokenStr)
	if err != nil {
		return
	}
	parts := strings.SplitN(string(data), "|", 5)
	if len(parts) != 5 {
		return
	}
	expiryUnix, _ := strconv.ParseInt(parts[3], 10, 64)
	sig := parts[4]

	b.startCleanup()
	b.mu.Lock()
	if b.items == nil {
		b.items = make(map[string]time.Time)
	}
	b.items[sig] = time.Unix(expiryUnix, 0)
	b.mu.Unlock()
}

func (b *tokenBlacklist) isBlacklisted(sig string) bool {
	b.startCleanup()
	b.mu.Lock()
	defer b.mu.Unlock()
	expiry, ok := b.items[sig]
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		delete(b.items, sig)
		return false
	}
	return true
}

func (b *tokenBlacklist) startCleanup() {
	b.once.Do(func() {
		go func() {
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				now := time.Now()
				b.mu.Lock()
				for sig, expiry := range b.items {
					if now.After(expiry) {
						delete(b.items, sig)
					}
				}
				b.mu.Unlock()
			}
		}()
	})
}

// GetTokenSecret 获取当前 token 签名密钥
func GetTokenSecret(cfgSecret string) []byte {
	return []byte(cfgSecret)
}

// CreateToken 生成 HMAC 签名 token
// 格式: base64(uid|user_name|role|expiry|hmac_sig)
func CreateToken(uid int, userName string, role int, ttl time.Duration, secret []byte) string {
	expiry := time.Now().Add(ttl)
	payload := fmt.Sprintf("%d|%s|%d|%d", uid, userName, role, expiry.Unix())
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	full := fmt.Sprintf("%s|%s", payload, sig)
	return base64.RawURLEncoding.EncodeToString([]byte(full))
}

// DecodeToken 解析并验证 token，返回 Payload 或 nil。
func DecodeToken(tokenStr string, secret []byte) *Payload {
	if len(secret) == 0 {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(tokenStr)
	if err != nil {
		return nil
	}
	parts := strings.SplitN(string(data), "|", 5)
	if len(parts) != 5 {
		return nil
	}
	uid, _ := strconv.Atoi(parts[0])
	userName := parts[1]
	role, _ := strconv.Atoi(parts[2])
	expiryUnix, _ := strconv.ParseInt(parts[3], 10, 64)
	sig := parts[4]

	// 检查是否在黑名单中
	if TokenBlacklist.isBlacklisted(sig) {
		return nil
	}

	// 检查过期
	if time.Now().Unix() > expiryUnix {
		return nil
	}

	// 验证 HMAC 签名
	payload := fmt.Sprintf("%d|%s|%d|%d", uid, userName, role, expiryUnix)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return nil
	}

	return &Payload{
		UID:      uid,
		UserName: userName,
		Role:     role,
		Exp:      float64(expiryUnix),
	}
}