package plugin

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// maxPluginFile caps a single downloaded file.
const maxPluginFile = 8 << 20

// Fetch downloads a plugin manifest and its entry file over HTTP.
//
// Two address forms are accepted: a direct manifest URL, or a directory URL
// holding manifest.json next to the entry file.
func Fetch(rawURL string) (*Manifest, map[string][]byte, error) {
	if rawURL == "" {
		return nil, nil, fmt.Errorf("url is required")
	}

	base := strings.TrimSuffix(rawURL, "/")
	manifestURL := base
	if !strings.HasSuffix(manifestURL, "manifest.json") {
		manifestURL = base + "/manifest.json"
	}
	base = strings.TrimSuffix(manifestURL, "/manifest.json")

	data, err := download(manifestURL)
	if err != nil {
		return nil, nil, fmt.Errorf("download manifest: %w", err)
	}

	var m Manifest
	if err := unmarshalManifest(data, &m); err != nil {
		return nil, nil, err
	}

	entryURL := base + "/" + strings.TrimPrefix(m.Entry, "./")
	code, err := download(entryURL)
	if err != nil {
		return nil, nil, fmt.Errorf("download %s: %w", m.Entry, err)
	}

	files := map[string][]byte{
		"manifest.json": data,
		m.Entry:         code,
	}
	return &m, files, nil
}

// download reads a file over HTTP, enforcing the size cap.
func download(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, maxPluginFile+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(data) > maxPluginFile {
		return nil, fmt.Errorf("file is larger than %d bytes", maxPluginFile)
	}
	return data, nil
}

func unmarshalManifest(data []byte, m *Manifest) error {
	if err := jsonUnmarshal(data, m); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	return nil
}

// ValidateFiles checks a plugin before anything is written to disk.
func (m *Manifest) ValidateFiles(files map[string][]byte) error {
	if m.Name == "" {
		return fmt.Errorf("name: required")
	}
	if !reName.MatchString(m.Name) {
		return fmt.Errorf("name: expected lowercase latin, digits, dot, dash or underscore")
	}
	if _, ok := files[m.Entry]; !ok {
		return fmt.Errorf("entry %q is missing", m.Entry)
	}
	if filepath.IsAbs(m.Entry) || strings.Contains(m.Entry, "..") {
		return fmt.Errorf("entry: must stay inside the plugin directory")
	}
	return validateManifestFields(m)
}

// Install writes plugin files into its own directory.
func Install(root, name string, files map[string][]byte) error {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid plugin name %q", name)
	}

	dir := filepath.Join(root, name)
	if !strings.HasPrefix(filepath.Clean(dir), filepath.Clean(root)) {
		return fmt.Errorf("plugin path escapes the plugins directory")
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clean plugin dir: %w", err)
	}

	for rel, data := range files {
		target := filepath.Join(dir, path.Clean(rel))
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(dir)) {
			return fmt.Errorf("file %q escapes the plugin directory", rel)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create dir for %s: %w", rel, err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}
