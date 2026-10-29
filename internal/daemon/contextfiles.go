package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Session selection errors.
var (
	errNoSessionID       = errors.New("session mode id requires a session id")
	errSessionNotFound   = errors.New("session not found")
	errContextFileTooBig = errors.New("context file is too large")
)

// contextFile is a loaded context file.
type contextFile struct {
	Dir     string
	Name    string
	Content string
}

// readTextFile reads a small text file.
//
// Files above the threshold are cut: a stray large file would otherwise
// eat the model context window.
func readTextFile(dir, name string, maxKB int) (string, bool) {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return "", false
	}

	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", false
	}

	limit := int64(maxKB) << 10
	if limit <= 0 {
		limit = 50 << 10
	}

	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()

	buf := make([]byte, limit)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return "", false
	}
	content := string(buf[:n])

	if info.Size() > limit {
		content += "\n[truncated, file is larger than " + name + " limit]\n"
	}
	if strings.TrimSpace(content) == "" {
		return "", false
	}
	return content, true
}
