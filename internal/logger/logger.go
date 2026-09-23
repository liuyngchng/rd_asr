package logger

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
)

// Logger 全局 logger 实例
var Logger *slog.Logger

// SourceWidth 源码位置显示宽度（右对齐用）。
const SourceWidth = 24

// modulePrefix 模块名前缀，从完整函数名中剥离
const modulePrefix = "rd_asr/"

// customHandler 自定义日志格式
// 格式: 2026-07-25 09:39:40.638 INFO [manager.go:431] 消息内容 key=value ...
type customHandler struct {
	w      io.Writer
	level  slog.Leveler
	attrs  []slog.Attr
	groups []string
}

func (h *customHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *customHandler) Handle(_ context.Context, r slog.Record) error {
	buf := []byte(r.Time.Format("2006-01-02 15:04:05.000"))

	buf = append(buf, ' ')
	buf = append(buf, padLevel(r.Level.String())...)

	if r.PC != 0 {
		if src := sourceFromPC(r.PC); src != nil {
			loc := sourceLocation(src)
			buf = append(buf, ' ')
			buf = append(buf, '[')
			if len(loc) < SourceWidth {
				buf = append(buf, bytes.Repeat([]byte{' '}, SourceWidth-len(loc))...)
			}
			buf = append(buf, loc...)
			buf = append(buf, ']')
		}
	}

	buf = append(buf, ' ')
	buf = append(buf, r.Message...)

	r.Attrs(func(a slog.Attr) bool {
		buf = append(buf, ' ')
		buf = append(buf, a.Key...)
		buf = append(buf, '=')
		buf = append(buf, a.Value.String()...)
		return true
	})

	buf = append(buf, '\n')

	_, err := h.w.Write(buf)
	return err
}

func (h *customHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &customHandler{w: h.w, level: h.level, attrs: append(h.attrs, attrs...)}
}

func (h *customHandler) WithGroup(name string) slog.Handler {
	return &customHandler{w: h.w, level: h.level, attrs: h.attrs, groups: append(h.groups, name)}
}

// sourceFromPC 从 PC 解析 slog.Source 信息（Go 1.24 兼容，等价于 r.Source()）。
func sourceFromPC(pc uintptr) *slog.Source {
	fs := runtime.CallersFrames([]uintptr{pc})
	f, _ := fs.Next()
	if f.File == "" {
		return nil
	}
	return &slog.Source{Function: f.Function, File: f.File, Line: f.Line}
}

// sourceLocation 从 slog.Source 生成 "包路径/文件名:行号"。
func sourceLocation(src *slog.Source) string {
	pkg := strings.TrimPrefix(src.Function, modulePrefix)
	if i := strings.Index(pkg, "("); i > 0 {
		pkg = pkg[:i]
	} else if i := strings.LastIndex(pkg, "."); i > 0 {
		pkg = pkg[:i]
	}
	pkg = strings.TrimSuffix(pkg, ".")

	name := filepath.Base(src.File)
	name = name[:len(name)-len(filepath.Ext(name))]
	return abbreviatePath(pkg) + "/" + name + ":" + strconv.Itoa(src.Line)
}

// abbreviatePath 将包路径每段缩写为首字母，用 '/' 连接。
func abbreviatePath(pkg string) string {
	segs := strings.Split(pkg, "/")
	for i, seg := range segs {
		if seg == "" {
			continue
		}
		for _, r := range seg {
			segs[i] = string(r)
			break
		}
	}
	return strings.Join(segs, "/")
}

// padLevel 将日志级别补齐到 5 字符，保证对齐。
func padLevel(level string) string {
	if len(level) < 5 {
		return level + strings.Repeat(" ", 5-len(level))
	}
	return level
}

// Init 初始化日志，同时输出到控制台和按小时滚动的日志文件。
func Init(debug bool) error {
	fileWriter, err := rotatelogs.New(
		"log/app.%Y-%m-%d-%H.log",
		rotatelogs.WithLinkName("log/app.log"),
		rotatelogs.WithRotationTime(time.Hour),
		rotatelogs.WithMaxAge(7*24*time.Hour), // 保留 7 天
	)
	if err != nil {
		return err
	}

	multiWriter := io.MultiWriter(os.Stdout, fileWriter)

	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}

	handler := &customHandler{w: multiWriter, level: level}
	Logger = slog.New(handler)
	slog.SetDefault(Logger)
	return nil
}