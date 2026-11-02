// Package logx writes structured logs to a rotating file and optional writer.
package logx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Level represents the severity of a log message.
type Level int

// Supported log levels in ascending order of severity.
const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	}
	return "unknown"
}

// ParseLevel converts a level name into a Level value.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug, nil
	case "info", "":
		return LevelInfo, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	}
	return LevelInfo, fmt.Errorf("unknown level %q", s)
}

// Options configures a Logger instance.
type Options struct {
	Dir      string
	Level    Level
	MaxSize  int64
	MaxFiles int
	Mirror   bool
	Stderr   io.Writer
}

// Logger handles leveled output and file rotation.
type Logger struct {
	mu      sync.Mutex
	opts    Options
	file    *os.File
	size    int64
	path    string
	lastRot time.Time
}

var std = New()

// New creates an in-memory logger writing to stderr at Info level.
func New() *Logger {
	return &Logger{
		opts: Options{
			Level:  LevelInfo,
			Stderr: os.Stderr,
		},
	}
}

// Init configures the global logger.
func Init(opts Options) error {
	return std.Init(opts)
}

// SetLevel updates the global logging level.
func SetLevel(l Level) {
	std.SetLevel(l)
}

// Close closes the global log file if open.
func Close() {
	std.Close()
}

// Debug writes a debug message to the global logger.
func Debug(msg string, args ...any) { std.Write(LevelDebug, msg, args...) }

// Info writes an info message to the global logger.
func Info(msg string, args ...any) { std.Write(LevelInfo, msg, args...) }

// Warn writes a warn message to the global logger.
func Warn(msg string, args ...any) { std.Write(LevelWarn, msg, args...) }

// Error writes an error message to the global logger.
func Error(msg string, args ...any) { std.Write(LevelError, msg, args...) }

// Tagged returns a helper that prefixes every message with [tag].
func Tagged(tag string) *Scope {
	return std.Tagged(tag)
}

// Init configures the logger instance and opens its log file if Dir is set.
func (l *Logger) Init(opts Options) error {
	if opts.MaxSize <= 0 {
		opts.MaxSize = 10 << 20
	}
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = 5
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	l.opts = opts

	if opts.Dir == "" {
		return nil
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	path := filepath.Join(opts.Dir, "cdx.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("stat log file: %w", err)
	}

	l.file = f
	l.path = path
	l.size = st.Size()
	l.lastRot = time.Now()
	return nil
}

// SetLevel changes the minimum severity level at runtime.
func (l *Logger) SetLevel(lvl Level) {
	l.mu.Lock()
	l.opts.Level = lvl
	l.mu.Unlock()
}

// Close closes the underlying file handle.
func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}

// Write formats and records a log entry.
func (l *Logger) Write(lvl Level, msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if lvl < l.opts.Level {
		return
	}
	if len(args) > 0 {
		msg = fmt.Sprintf(msg, args...)
	}

	line := fmt.Sprintf("%s %-5s %s\n", time.Now().Format("2006-01-02T15:04:05.000Z07:00"), lvl.String(), msg)

	if l.file != nil {
		n, err := l.file.WriteString(line)
		if err == nil {
			l.size += int64(n)
			if l.size >= l.opts.MaxSize {
				l.rotateLocked()
			}
		}
	}
	if l.opts.Mirror || l.file == nil {
		_, _ = io.WriteString(l.opts.Stderr, line)
	}
}

// Tagged returns a Scope bound to this logger with a fixed tag prefix.
func (l *Logger) Tagged(tag string) *Scope {
	clean := strings.TrimSpace(tag)
	clean = strings.TrimPrefix(clean, "[")
	clean = strings.TrimSuffix(clean, "]")
	return &Scope{
		logger: l,
		prefix: "[" + clean + "] ",
	}
}

func (l *Logger) rotateLocked() {
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}

	stamp := l.lastRot.Format("20060102-150405.000")
	archived := fmt.Sprintf("%s.%s", l.path, stamp)
	_ = os.Rename(l.path, archived)
	l.lastRot = time.Now()

	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	l.file = f
	l.size = 0

	l.pruneLocked()
}

func (l *Logger) pruneLocked() {
	dir := filepath.Dir(l.path)
	base := filepath.Base(l.path)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var archives []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, base+".") {
			archives = append(archives, filepath.Join(dir, name))
		}
	}
	if len(archives) <= l.opts.MaxFiles {
		return
	}

	sort.Strings(archives)
	drop := len(archives) - l.opts.MaxFiles
	for _, p := range archives[:drop] {
		_ = os.Remove(p)
	}
}

// Scope writes log entries with a fixed "[Tag] " prefix.
type Scope struct {
	logger *Logger
	prefix string
}

// Debug logs a debug message with the scope tag.
func (s *Scope) Debug(msg string, args ...any) {
	s.logger.Write(LevelDebug, s.prefix+msg, args...)
}

// Info logs an info message with the scope tag.
func (s *Scope) Info(msg string, args ...any) {
	s.logger.Write(LevelInfo, s.prefix+msg, args...)
}

// Warn logs a warn message with the scope tag.
func (s *Scope) Warn(msg string, args ...any) {
	s.logger.Write(LevelWarn, s.prefix+msg, args...)
}

// Error logs an error message with the scope tag.
func (s *Scope) Error(msg string, args ...any) {
	s.logger.Write(LevelError, s.prefix+msg, args...)
}
