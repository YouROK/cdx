package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"strings"
)

// HashPrompt hashes the stable part of the prompt and the context files.
//
// The hash is stored in the turn history so a record shows which prompt
// produced the answer.
func HashPrompt(stable string, files []ContextFile) string {
	h := sha256.New()
	h.Write([]byte(strings.TrimSpace(stable)))
	for _, f := range files {
		h.Write([]byte{0})
		h.Write([]byte(f.Name))
		h.Write([]byte{0})
		h.Write([]byte(f.Content))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// gitBranch returns the current branch when the directory is a repository.
func gitBranch(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	if _, err := os.Stat(dir); err != nil {
		return "", false
	}

	cmd := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}

	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		return "", false
	}
	return branch, true
}
