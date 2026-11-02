package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/yourok/cdx/internal/version"
)

const (
	maxZipDownloadBytes = 32 << 20 // 32 MB max zip archive size
	maxExtractedBytes   = 64 << 20 // 64 MB max total extracted size
)

// InstallFromURL downloads a .zip archive from rawURL, validates its manifest, version, and JS syntax,
// installs it into targetPluginsDir/<id>, and loads it into the manager.
func (m *Manager) InstallFromURL(ctx context.Context, rawURL, targetPluginsDir string) (*PluginInfo, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("zip url is required")
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return nil, fmt.Errorf("url must start with http:// or https://")
	}

	reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}

	client := http.DefaultClient
	if m.deps.Sys != nil && m.deps.Sys.HTTPClient != nil {
		client = m.deps.Sys.HTTPClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download zip: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download failed with HTTP %d", resp.StatusCode)
	}

	zipBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxZipDownloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read zip stream: %w", err)
	}
	if len(zipBytes) > maxZipDownloadBytes {
		return nil, fmt.Errorf("zip archive exceeds maximum size of %d MB", maxZipDownloadBytes>>20)
	}

	return m.InstallFromZipBytes(ctx, zipBytes, targetPluginsDir, rawURL)
}

// InstallFromZipBytes validates and installs a plugin from raw zip archive bytes into targetPluginsDir.
func (m *Manager) InstallFromZipBytes(ctx context.Context, zipBytes []byte, targetPluginsDir, sourceURL string) (*PluginInfo, error) {
	if targetPluginsDir == "" {
		return nil, fmt.Errorf("target plugin directory is not configured")
	}
	if err := os.MkdirAll(targetPluginsDir, 0o700); err != nil {
		return nil, fmt.Errorf("create target plugin dir: %w", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("invalid zip archive: %w", err)
	}

	stageDir, err := os.MkdirTemp(targetPluginsDir, ".install-*")
	if err != nil {
		return nil, fmt.Errorf("create staging dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(stageDir)
	}()

	if err := extractZipSafe(zr, stageDir); err != nil {
		return nil, err
	}

	// Support both flat zips (manifest.json at root) and single-folder zips (my-plugin/manifest.json).
	pluginRoot, err := locateManifestRoot(stageDir)
	if err != nil {
		return nil, err
	}

	manifest, err := LoadManifest(pluginRoot)
	if err != nil {
		return nil, fmt.Errorf("manifest validation failed: %w", err)
	}

	if manifest.MinCDXVersion != "" {
		if !checkMinVersion(version.Version, manifest.MinCDXVersion) {
			return nil, fmt.Errorf("plugin requires cdx >= %s (current version is %s)", manifest.MinCDXVersion, version.Version)
		}
	}

	// Verify that the entry JS script compiles cleanly in Goja before installing.
	entryBytes, err := os.ReadFile(filepath.Join(pluginRoot, manifest.Entry))
	if err != nil {
		return nil, fmt.Errorf("read entry script %q: %w", manifest.Entry, err)
	}
	if _, err := goja.Compile(manifest.Entry, string(entryBytes), false); err != nil {
		return nil, fmt.Errorf("entry script syntax error in %s: %w", manifest.Entry, err)
	}

	if sourceURL != "" && manifest.SourceURL == "" {
		manifest.SourceURL = sourceURL
		_ = saveManifestFile(pluginRoot, manifest)
	}

	destDir := filepath.Join(targetPluginsDir, manifest.ID)

	// Stop existing runtime if upgrading.
	m.UnloadPlugin(manifest.ID)

	_ = os.RemoveAll(destDir)
	if err := os.Rename(pluginRoot, destDir); err != nil {
		return nil, fmt.Errorf("move plugin to %s: %w", destDir, err)
	}

	// Mark enabled and start the plugin.
	_ = m.setDisabledState(manifest.ID, false)
	if _, err := m.LoadPlugin(ctx, destDir); err != nil {
		return nil, fmt.Errorf("start installed plugin: %w", err)
	}

	info, ok := m.GetPluginInfo(manifest.ID)
	if !ok {
		return nil, fmt.Errorf("plugin %q installed but not found in state", manifest.ID)
	}
	return &info, nil
}

func extractZipSafe(zr *zip.Reader, dest string) error {
	var totalWritten int64
	cleanDest := filepath.Clean(dest) + string(os.PathSeparator)

	for _, f := range zr.File {
		rel := filepath.Clean(filepath.FromSlash(f.Name))
		if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			return fmt.Errorf("unsafe path in zip archive: %q", f.Name)
		}
		targetPath := filepath.Join(dest, rel)
		if !strings.HasPrefix(filepath.Clean(targetPath)+string(os.PathSeparator), cleanDest) && filepath.Clean(targetPath) != filepath.Clean(dest) {
			return fmt.Errorf("zip slip attempt detected: %q", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			_ = rc.Close()
			return err
		}

		written, copyErr := io.Copy(outFile, io.LimitReader(rc, maxExtractedBytes-totalWritten+1))
		_ = outFile.Close()
		_ = rc.Close()
		if copyErr != nil {
			return copyErr
		}

		totalWritten += written
		if totalWritten > maxExtractedBytes {
			return fmt.Errorf("extracted zip contents exceed limit of %d MB", maxExtractedBytes>>20)
		}
	}
	return nil
}

func locateManifestRoot(stageDir string) (string, error) {
	if _, err := os.Stat(filepath.Join(stageDir, "manifest.json")); err == nil {
		return stageDir, nil
	}
	entries, err := os.ReadDir(stageDir)
	if err != nil {
		return "", err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "__MACOSX" {
			dirs = append(dirs, filepath.Join(stageDir, e.Name()))
		}
	}
	if len(dirs) == 1 {
		if _, err := os.Stat(filepath.Join(dirs[0], "manifest.json")); err == nil {
			return dirs[0], nil
		}
	}
	return "", fmt.Errorf("manifest.json not found in zip archive")
}

func checkMinVersion(current, required string) bool {
	cur := parseSemverParts(current)
	req := parseSemverParts(required)
	for i := 0; i < 3; i++ {
		if cur[i] > req[i] {
			return true
		}
		if cur[i] < req[i] {
			return false
		}
	}
	return true
}

func parseSemverParts(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if idx := strings.IndexByte(v, '-'); idx != -1 {
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		out[i], _ = strconv.Atoi(parts[i])
	}
	return out
}
