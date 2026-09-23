package store

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Task struct {
	TaskID           string  `json:"task_id"`
	UID              int     `json:"uid"`
	OriginalFilename string  `json:"original_filename"`
	OriginalPath     string  `json:"original_path"`
	ConvertedPath    string  `json:"converted_path"`
	Status           string  `json:"status"`
	ResultText       *string `json:"result_text"`
	Progress         int     `json:"progress"`
	Error            *string `json:"error"`
	CreatedAt        string  `json:"created_at"`
	UpdatedAt        string  `json:"updated_at"`
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
			task_id          TEXT PRIMARY KEY,
			uid              INTEGER NOT NULL,
			original_filename TEXT NOT NULL,
			original_path    TEXT NOT NULL DEFAULT '',
			converted_path   TEXT NOT NULL DEFAULT '',
			status           TEXT NOT NULL DEFAULT 'converting',
			result_text      TEXT,
			progress         INTEGER DEFAULT 0,
			error            TEXT,
			created_at       TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			updated_at       TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		);
	`)
	return err
}

func (s *Store) CreateTask(originalFilename, originalPath, convertedPath string, uid int) (string, error) {
	taskID := UUID()
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

func (s *Store) Close() error { return s.db.Close() }

func UUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func scanTask(row *sql.Row) (*Task, error) {
	t := &Task{}
	var rt, et sql.NullString
	var op, cp sql.NullString
	err := row.Scan(&t.TaskID, &t.UID, &t.OriginalFilename, &op, &cp, &t.Status, &rt, &t.Progress, &et, &t.CreatedAt, &t.UpdatedAt)
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
	err := rows.Scan(&t.TaskID, &t.UID, &t.OriginalFilename, &op, &cp, &t.Status, &rt, &t.Progress, &et, &t.CreatedAt, &t.UpdatedAt)
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