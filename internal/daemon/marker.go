package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// markerFile - the pointer file name inside a session directory.
const markerFile = ".cdx/session"

// writeSessionMarker writes a session pointer into a directory.
func writeSessionMarker(dir, sessionID string) error {
	path := filepath.Join(dir, markerFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create marker dir: %w", err)
	}
	content := fmt.Sprintf("id=%s\nroot=%s\n", sessionID, dir)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}
	return nil
}

// removeSessionMarker removes the session pointer.
func removeSessionMarker(dir string) error {
	path := filepath.Join(dir, markerFile)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove marker: %w", err)
	}
	// An empty .cdx directory is not needed.
	_ = os.Remove(filepath.Dir(path))
	return nil
}

// readSessionMarker reads the session id from the pointer.
func readSessionMarker(dir string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, markerFile))
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "id=") {
			id := strings.TrimSpace(strings.TrimPrefix(line, "id="))
			if id != "" {
				return id, true
			}
		}
	}
	return "", false
}
