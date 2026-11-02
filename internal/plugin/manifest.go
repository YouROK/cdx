// Package plugin loads external JS plugins into Goja VMs and exposes the cdx host API.
package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	rePluginID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	reSemver   = regexp.MustCompile(`^\d+\.\d+\.\d+(-[a-zA-Z0-9.-]+)?$`)
)

// Manifest describes a plugin's identity and entry script.
// Unlike the legacy design, plugins are not forced to declare tools in the manifest:
// they dynamically register tools, providers, routes, middleware, hooks, and UI at runtime.
type Manifest struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	Description   string `json:"description"`
	Author        string `json:"author,omitempty"`
	Homepage      string `json:"homepage,omitempty"`
	MinCDXVersion string `json:"min_cdx_version,omitempty"`
	SourceURL     string `json:"source_url,omitempty"`
	Entry         string `json:"entry"`
	Order         int    `json:"order,omitempty"`
	Workers       int    `json:"workers,omitempty"` // Number of isolated VMs for long-running provider and tool calls.
}

// LoadManifest reads and validates manifest.json from a plugin directory.
func LoadManifest(dir string) (*Manifest, error) {
	path := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.ID == "" && m.Name != "" {
		m.ID = strings.ToLower(strings.TrimSpace(m.Name))
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	if m.Entry == "" {
		m.Entry = "main.js"
	}
	if err := m.Validate(dir); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate checks manifest fields and ensures the entry script stays inside dir.
func (m *Manifest) Validate(dir string) error {
	if !rePluginID.MatchString(m.ID) {
		return fmt.Errorf("id %q: expected lowercase latin, digits, dot, dash or underscore", m.ID)
	}
	if !reSemver.MatchString(m.Version) {
		return fmt.Errorf("version %q: expected semantic version MAJOR.MINOR.PATCH", m.Version)
	}
	if m.MinCDXVersion != "" && !reSemver.MatchString(strings.TrimPrefix(m.MinCDXVersion, "v")) {
		return fmt.Errorf("min_cdx_version %q: expected semantic version MAJOR.MINOR.PATCH", m.MinCDXVersion)
	}
	if m.Workers < 0 || m.Workers > 16 {
		return fmt.Errorf("workers %d: expected a value between 0 and 16", m.Workers)
	}
	if filepath.IsAbs(m.Entry) || strings.Contains(m.Entry, "..") {
		return fmt.Errorf("entry %q: must be a relative path inside the plugin directory", m.Entry)
	}
	if dir != "" {
		if _, err := os.Stat(filepath.Join(dir, m.Entry)); err != nil {
			return fmt.Errorf("entry %q: %w", m.Entry, err)
		}
	}
	return nil
}

func saveManifestFile(dir string, m *Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), append(data, '\n'), 0o644)
}
