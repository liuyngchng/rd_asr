package auth

import (
	"fmt"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// adminPwdExpiry 内置 admin 初始密码有效期：启动后 2 小时内必须登录并修改密码
const AdminPwdExpiry = 2 * 60 * 60 // 秒

// adminPwdExpiryText adminPwdExpiry 的中文显示文案
const AdminPwdExpiryText = "2 小时"

// bcryptCost 计算成本，12 是当下推荐的安全值
const bcryptCost = 12

// minPasswordLen 最小密码长度
const minPasswordLen = 6

// HashPassword 使用 bcrypt 对密码做哈希
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword 验证密码是否匹配哈希
func VerifyPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// ValidatePassword 验证密码复杂度：长度至少 6 个字符
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < minPasswordLen {
		return fmt.Errorf("密码长度至少 %d 个字符", minPasswordLen)
	}
	return nil
}