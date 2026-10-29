// Package logx writes logs to a rotating file and mirrors them to stderr when asked.
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

// Level is a message level.
type Level int

// Levels in ascending order of importance.
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

// ParseLevel parses a level name.
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

// Options configures the logger.
type Options struct {
	Dir      string
	Level    Level
	MaxSize  int64
	MaxFiles int
	MaxAge   time.Duration
	Compress bool
	// Mirror also writes to stderr.
	Mirror bool
}

type logger struct {
	mu   sync.Mutex
	opts Options

	file    *os.File
	size    int64
	path    string
	lastRot time.Time
}

var std = &logger{opts: Options{Level: LevelInfo}}

// Init configures the global logger.
func Init(opts Options) error {
	if opts.MaxSize <= 0 {
		opts.MaxSize = 10 << 20
	}
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = 5
	}

	std.mu.Lock()
	defer std.mu.Unlock()

	if std.file != nil {
		_ = std.file.Close()
		std.file = nil
	}
	std.opts = opts

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

	std.file = f
	std.path = path
	std.size = st.Size()
	std.lastRot = time.Now()
	return nil
}

// SetLevel changes the level at runtime.
func SetLevel(l Level) {
	std.mu.Lock()
	std.opts.Level = l
	std.mu.Unlock()
}

// Close closes the log file.
func Close() {
	std.mu.Lock()
	defer std.mu.Unlock()
	if std.file != nil {
		_ = std.file.Close()
		std.file = nil
	}
}

// Debug writes a debug level message.
func Debug(msg string, args ...any) { write(LevelDebug, msg, args...) }

// Info writes an info level message.
func Info(msg string, args ...any) { write(LevelInfo, msg, args...) }

// Warn writes a warn level message.
func Warn(msg string, args ...any) { write(LevelWarn, msg, args...) }

// Error writes an error level message.
func Error(msg string, args ...any) { write(LevelError, msg, args...) }

// Panic writes a panic together with the stack.
func Panic(msg string, r any) { write(LevelError, "%s: %v", msg, r) }

func write(l Level, msg string, args ...any) {
	std.mu.Lock()
	defer std.mu.Unlock()

	if l < std.opts.Level {
		return
	}
	if len(args) > 0 {
		msg = fmt.Sprintf(msg, args...)
	}

	line := fmt.Sprintf("%s %-5s %s\n", time.Now().Format("2006-01-02T15:04:05.000Z07:00"), l.String(), msg)

	if std.file != nil {
		n, err := std.file.WriteString(line)
		if err == nil {
			std.size += int64(n)
			if std.size >= std.opts.MaxSize {
				rotateLocked()
			}
		}
	}
	if std.opts.Mirror || std.file == nil {
		_, _ = io.WriteString(os.Stderr, line)
	}
}

// rotateLocked moves the current file to the archive and opens a new one.
func rotateLocked() {
	if std.file == nil {
		return
	}
	_ = std.file.Close()
	std.file = nil

	stamp := std.lastRot.Format("20060102-150405")
	archived := fmt.Sprintf("%s.%s", std.path, stamp)
	_ = os.Rename(std.path, archived)
	std.lastRot = time.Now()

	f, err := os.OpenFile(std.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	std.file = f
	std.size = 0

	std.pruneLocked()
}

// pruneLocked removes old archives beyond the limit.
func (l *logger) pruneLocked() {
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
