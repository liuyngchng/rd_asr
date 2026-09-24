package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"rd_asr/internal/auth"
)

// loginRequest 登录请求体
type loginRequest struct {
	UserName string `json:"user_name"`
	UserPwd  string `json:"user_pwd"`
}

// changePwdRequest 修改密码请求体
type changePwdRequest struct {
	OldPwd string `json:"old_pwd"`
	NewPwd string `json:"new_pwd"`
}

// registerRequest 注册请求体
type registerRequest struct {
	UserName string `json:"user_name"`
	UserPwd  string `json:"user_pwd"`
}

// HandleLoginPage 渲染登录页面
func (s *Server) HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	ctx := pageCtx{
		Lang:      "zh",
		Dir:       "ltr",
		SysName:   s.sysName(),
		AppSource: AppTypeASR,
	}
	s.render(w, "login.html", ctx)
}

// HandleLogin 处理登录请求
func (s *Server) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "参数错误"})
		return
	}
	if req.UserName == "" || req.UserPwd == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "用户名和密码不能为空"})
		return
	}

	user, err := s.Store.GetUserByLogin(req.UserName)
	if err != nil {
		slog.Error("login_query_user_failed", "user_name", req.UserName, "error", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "登录失败"})
		return
	}
	if user == nil || !auth.VerifyPassword(req.UserPwd, user.UserPwd) {
		slog.Warn("login_failed", "user_name", req.UserName)
		WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "用户名或密码错误"})
		return
	}

	// 检查密码是否过期
	mustChangePwd := false
	if !user.PwdExpiresAt.IsZero() && time.Now().After(user.PwdExpiresAt) {
		slog.Warn("login_password_expired", "user_name", req.UserName)
		WriteJSON(w, http.StatusForbidden, map[string]string{"error": "密码已过期，请联系管理员重置"})
		return
	}
	if !user.PwdExpiresAt.IsZero() {
		mustChangePwd = true
	}

	tokenStr := auth.CreateToken(user.UID, user.UserName, user.Role, auth.TokenTTL, auth.GetTokenSecret(s.Config.Sys.TokenSecret))

	slog.Info("login_success", "user_name", user.UserName, "uid", user.UID, "role", user.Role)

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status":          "ok",
		"token":           tokenStr,
		"uid":             user.UID,
		"user_name":       user.UserName,
		"role":            user.Role,
		"must_change_pwd": mustChangePwd,
	})
}

// HandleLogout 退出登录，将 token 加入黑名单后跳转到登录页
func (s *Server) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if tok := r.URL.Query().Get("t"); tok != "" {
		auth.TokenBlacklist.Add(tok, auth.GetTokenSecret(s.Config.Sys.TokenSecret))
		slog.Info("logout_token_blacklisted")
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}
func (s *Server) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("t")
	payload := auth.DecodeToken(tok, auth.GetTokenSecret(s.Config.Sys.TokenSecret))
	if payload == nil {
		WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "未认证或 token 无效"})
		return
	}

	var req changePwdRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "参数错误"})
		return
	}
	if err := auth.ValidatePassword(req.NewPwd); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	user, err := s.Store.GetUserByLogin(payload.UserName)
	if err != nil || user == nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "用户不存在"})
		return
	}
	if !auth.VerifyPassword(req.OldPwd, user.UserPwd) {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "原密码错误"})
		return
	}

	newHash, err := auth.HashPassword(req.NewPwd)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "密码哈希失败"})
		return
	}
	if err := s.Store.UpdatePassword(user.UserName, newHash); err != nil {
		slog.Error("change_password_failed", "user_name", user.UserName, "error", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "修改失败"})
		return
	}

	slog.Info("change_password_success", "user_name", user.UserName)
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleRegisterPage 渲染注册页面
func (s *Server) HandleRegisterPage(w http.ResponseWriter, r *http.Request) {
	ctx := pageCtx{
		Lang:      "zh",
		Dir:       "ltr",
		SysName:   s.sysName(),
		AppSource: AppTypeASR,
	}
	s.render(w, "register.html", ctx)
}

// HandleRegister 处理注册请求
func (s *Server) HandleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "参数错误"})
		return
	}
	if req.UserName == "" || req.UserPwd == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "用户名和密码不能为空"})
		return
	}
	if err := auth.ValidatePassword(req.UserPwd); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	exists, err := s.Store.CheckUserExists(req.UserName)
	if err != nil {
		slog.Error("register_check_user_failed", "user_name", req.UserName, "error", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "注册失败"})
		return
	}
	if exists {
		WriteJSON(w, http.StatusConflict, map[string]string{"error": "用户名已存在"})
		return
	}

	pwdHash, err := auth.HashPassword(req.UserPwd)
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "密码哈希失败"})
		return
	}
	if err := s.Store.CreateUser(req.UserName, pwdHash, 0, ""); err != nil {
		slog.Error("register_create_user_failed", "user_name", req.UserName, "error", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "注册失败"})
		return
	}

	slog.Info("register_success", "user_name", req.UserName)
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}