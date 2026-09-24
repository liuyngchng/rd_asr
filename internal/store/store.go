package store

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"

	"rd_asr/internal/auth"

	_ "modernc.org/sqlite"
)

type Task struct {
	TaskID                   string  `json:"task_id"`
	UID                      int     `json:"uid"`
	OriginalFilename         string  `json:"original_filename"`
	OriginalPath             string  `json:"original_path"`
	ConvertedPath            string  `json:"converted_path"`
	Status                   string  `json:"status"`
	ResultText               *string `json:"result_text"`
	Progress                 int     `json:"progress"`
	CompletedSegments        int     `json:"completed_segments"`
	Error                    *string `json:"error"`
	AudioDurationSec         int     `json:"audio_duration_sec"`
	TranscriptionDurationSec int     `json:"transcription_duration_sec"`
	CreatedAt                string  `json:"created_at"`
	UpdatedAt                string  `json:"updated_at"`
}

// User 用户信息
type User struct {
	UID          int       `json:"uid"`
	UserName     string    `json:"user_name"`
	UserPwd      string    `json:"-"` // bcrypt 哈希，不返回给客户端
	Role         int       `json:"role"`
	Note         string    `json:"note"`
	PwdExpiresAt time.Time `json:"-"` // 密码过期时间，零值 = 无过期限制
}

type Store struct {
	mu sync.Mutex
	db *sql.DB
}

func New(dbPath string) (*Store, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS asr_tasks (
			task_id            TEXT PRIMARY KEY,
			uid                INTEGER NOT NULL,
			original_filename  TEXT NOT NULL,
			original_path      TEXT NOT NULL DEFAULT '',
			converted_path     TEXT NOT NULL DEFAULT '',
			status             TEXT NOT NULL DEFAULT 'converting',
			result_text        TEXT,
			progress           INTEGER DEFAULT 0,
			completed_segments INTEGER DEFAULT 0,
			error              TEXT,
			audio_duration_sec         INTEGER DEFAULT 0,
			transcription_duration_sec INTEGER DEFAULT 0,
			created_at         TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			updated_at         TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		);

		CREATE TABLE IF NOT EXISTS users (
			uid             INTEGER PRIMARY KEY AUTOINCREMENT,
			user_name       TEXT NOT NULL UNIQUE,
			user_pwd        TEXT NOT NULL DEFAULT '',
			role            INTEGER NOT NULL DEFAULT 0,
			note            TEXT NOT NULL DEFAULT '',
			pwd_expires_at  TEXT NOT NULL DEFAULT ''
		);
	`)
	if err != nil {
		return err
	}
	return s.seedAdminIfMissing()
}

// ============================================================
// 任务相关 CRUD
// ============================================================

func (s *Store) CreateTask(originalFilename, originalPath, convertedPath string, uid int) (string, error) {
	taskID := fmt.Sprintf("%d", time.Now().UnixMilli())
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO asr_tasks (task_id, uid, original_filename, original_path, converted_path, status)
		 VALUES (?, ?, ?, ?, ?, 'converting')`,
		taskID, uid, originalFilename, originalPath, convertedPath,
	)
	if err != nil {
		return "", fmt.Errorf("insert task: %w", err)
	}
	slog.Info("create_task", "task_id", taskID, "uid", uid, "file", originalFilename)
	return taskID, nil
}

func (s *Store) UpdateTask(taskID string, fields map[string]interface{}) error {
	allowed := map[string]bool{
		"status": true, "result_text": true, "progress": true,
		"error": true, "converted_path": true, "original_path": true,
		"completed_segments": true,
		"audio_duration_sec": true, "transcription_duration_sec": true,
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var setClauses string
	args := make([]interface{}, 0)
	for k, v := range fields {
		if !allowed[k] {
			continue
		}
		if setClauses != "" {
			setClauses += ", "
		}
		setClauses += fmt.Sprintf("%s = ?", k)
		args = append(args, v)
	}
	if setClauses == "" {
		return nil
	}
	setClauses += ", updated_at = ?"
	args = append(args, time.Now().Format("2006-01-02 15:04:05"))
	args = append(args, taskID)
	_, err := s.db.Exec(fmt.Sprintf("UPDATE asr_tasks SET %s WHERE task_id = ?", setClauses), args...)
	return err
}

func (s *Store) GetTask(taskID string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow("SELECT * FROM asr_tasks WHERE task_id = ?", taskID)
	return scanTask(row)
}

func (s *Store) GetUserTasks(uid, limit int) ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.Query("SELECT * FROM asr_tasks WHERE uid = ? ORDER BY created_at DESC LIMIT ?", uid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]Task, 0)
	for rows.Next() {
		t, err := scanTaskRow(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *t)
	}
	return tasks, rows.Err()
}

func (s *Store) DeleteTask(taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM asr_tasks WHERE task_id = ?", taskID)
	if err != nil {
		return err
	}
	slog.Info("delete_task", "task_id", taskID)
	return nil
}

// GetResumableTasks 返回所有需要续传的任务
func (s *Store) GetResumableTasks() ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(
		"SELECT * FROM asr_tasks WHERE status IN ('converting','splitting','transcribing')",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]Task, 0)
	for rows.Next() {
		t, err := scanTaskRow(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *t)
	}
	return tasks, rows.Err()
}

// ============================================================
// 用户相关
// ============================================================

// seedAdminIfMissing 当 users 表中不存在 admin 时，创建内置管理员。
// 随机密码打印到控制台和日志，登录后 2 小时内未修改则过期。
func (s *Store) seedAdminIfMissing() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow("SELECT user_name FROM users WHERE user_name = ?", "admin")
	var name string
	switch err := row.Scan(&name); err {
	case nil:
		return nil // 已有 admin
	case sql.ErrNoRows:
		// 继续创建
	default:
		return err
	}

	adminPwd, err := randomPassword(12)
	if err != nil {
		return fmt.Errorf("生成 admin 随机密码失败: %w", err)
	}
	pwdHash, err := auth.HashPassword(adminPwd)
	if err != nil {
		return fmt.Errorf("admin 密码哈希失败: %w", err)
	}

	expiresAt := time.Now().Add(auth.AdminPwdExpiry * time.Second).Format(time.RFC3339)
	if _, err := s.db.Exec(
		"INSERT INTO users (user_name, user_pwd, role, note, pwd_expires_at) VALUES (?, ?, ?, ?, ?)",
		"admin", pwdHash, 2, "内置管理员", expiresAt,
	); err != nil {
		return fmt.Errorf("种子用户 admin 插入失败: %w", err)
	}

	slog.Info("store_sqlite_admin_account_created", "user_name", "admin", "initial_password", adminPwd, "expires_in", auth.AdminPwdExpiryText)
	fmt.Printf("\n========================================\n")
	fmt.Printf("  首次运行已创建管理员账号 admin\n")
	fmt.Printf("  初始密码: %s\n", adminPwd)
	fmt.Printf("  该密码 %s 内有效，登录后需立即修改密码\n", auth.AdminPwdExpiryText)
	fmt.Printf("  若忘记初始密码，请删除 asr.db 并重启\n")
	fmt.Printf("========================================\n\n")

	return nil
}

// GetUserByLogin 按用户名查询用户
func (s *Store) GetUserByLogin(userName string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow(
		"SELECT uid, user_name, user_pwd, role, note, pwd_expires_at FROM users WHERE user_name = ?",
		userName,
	)
	return scanUser(row)
}

// UpdatePassword 修改密码并清除密码过期时间
func (s *Store) UpdatePassword(userName, newPwdHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		"UPDATE users SET user_pwd = ?, pwd_expires_at = '' WHERE user_name = ?",
		newPwdHash, userName,
	)
	return err
}

// CreateUser 创建新用户（用户名已存在则返回错误）
func (s *Store) CreateUser(userName, pwdHash string, role int, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		"INSERT INTO users (user_name, user_pwd, role, note) VALUES (?, ?, ?, ?)",
		userName, pwdHash, role, note,
	)
	if err != nil {
		return fmt.Errorf("创建用户失败: %w", err)
	}
	return nil
}

// CheckUserExists 检查用户名是否已存在
func (s *Store) CheckUserExists(userName string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM users WHERE user_name = ?", userName).Scan(&count)
	return count > 0, err
}

// ============================================================
// 辅助函数
// ============================================================

func randomPassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		result[i] = charset[n.Int64()]
	}
	return string(result), nil
}

func scanTask(row *sql.Row) (*Task, error) {
	t := &Task{}
	var rt, et sql.NullString
	var op, cp sql.NullString
	err := row.Scan(&t.TaskID, &t.UID, &t.OriginalFilename, &op, &cp, &t.Status, &rt, &t.Progress, &t.CompletedSegments, &et, &t.AudioDurationSec, &t.TranscriptionDurationSec, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	t.OriginalPath = op.String
	t.ConvertedPath = cp.String
	if rt.Valid {
		t.ResultText = &rt.String
	}
	if et.Valid {
		t.Error = &et.String
	}
	return t, nil
}

func scanTaskRow(rows *sql.Rows) (*Task, error) {
	t := &Task{}
	var rt, et sql.NullString
	var op, cp sql.NullString
	err := rows.Scan(&t.TaskID, &t.UID, &t.OriginalFilename, &op, &cp, &t.Status, &rt, &t.Progress, &t.CompletedSegments, &et, &t.AudioDurationSec, &t.TranscriptionDurationSec, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	t.OriginalPath = op.String
	t.ConvertedPath = cp.String
	if rt.Valid {
		t.ResultText = &rt.String
	}
	if et.Valid {
		t.Error = &et.String
	}
	return t, nil
}

func scanUser(row *sql.Row) (*User, error) {
	var u User
	var expiresAt string
	err := row.Scan(&u.UID, &u.UserName, &u.UserPwd, &u.Role, &u.Note, &expiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if expiresAt != "" {
		if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			u.PwdExpiresAt = t
		}
	}
	return &u, nil
}

func (s *Store) Close() error { return s.db.Close() }
