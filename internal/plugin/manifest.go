// Package plugin loads external JS plugins and runs their tools.
package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yourok/cdx/internal/registry"
)

// reservedPrefixes are owned by the bus, so a plugin cannot take them.
var reservedPrefixes = []string{"cdx", "system", "core", "mcp", "plugin"}

// Manifest schema limits.
const (
	maxTools      = 64
	maxDescLength = 1024
)

var (
	reName   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	reTool   = regexp.MustCompile(`^[a-z0-9_]{1,48}$`)
	reSemver = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
)

// jsReserved lists names that collide with JS object properties.
var jsReserved = map[string]bool{
	"__proto__":      true,
	"constructor":    true,
	"tostring":       true,
	"valueof":        true,
	"hasownproperty": true,
}

// Manifest describes a plugin.
type Manifest struct {
	Name              string     `json:"name"`
	Version           string     `json:"version"`
	Description       string     `json:"description"`
	Author            string     `json:"author"`
	Homepage          string     `json:"homepage"`
	Entry             string     `json:"entry"`
	MinCDX            string     `json:"min_cdx_version"`
	Permission        string     `json:"permission"`
	Capabilities      []string   `json:"capabilities"`
	Events            []string   `json:"events"`
	Tools             []ToolSpec `json:"tools"`
	InitTimeoutMS     int        `json:"init_timeout_ms"`
	ToolTimeoutMS     int        `json:"tool_timeout_ms"`
	ShutdownTimeoutMS int        `json:"shutdown_timeout_ms"`
}

// ToolSpec describes a plugin tool.
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Permission  string         `json:"permission"`
	InputSchema map[string]any `json:"input_schema"`
}

// Load reads and validates the manifest from a plugin directory.
func Load(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if err := m.validate(dir); err != nil {
		return nil, err
	}
	return &m, nil
}

// validateManifestFields checks manifest fields without touching the disk.
func validateManifestFields(m *Manifest) error {
	if m.Version == "" {
		return fmt.Errorf("version: required")
	}
	if !reSemver.MatchString(m.Version) {
		return fmt.Errorf("version: expected MAJOR.MINOR.PATCH, got %q", m.Version)
	}
	for i, t := range m.Tools {
		if !reTool.MatchString(t.Name) {
			return fmt.Errorf("tools[%d].name: expected lowercase latin, digits or underscore", i)
		}
		if jsReserved[strings.ToLower(t.Name)] {
			return fmt.Errorf("tools[%d].name: %q conflicts with a JS object property", i, t.Name)
		}
	}
	return nil
}

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// validate checks required fields and limits.
func (m *Manifest) validate(dir string) error {
	if m.Name == "" {
		return fmt.Errorf("name: required")
	}
	if !reName.MatchString(m.Name) {
		return fmt.Errorf("name: expected lowercase latin, digits, dot, dash or underscore")
	}
	for _, prefix := range reservedPrefixes {
		if strings.HasPrefix(m.Name, prefix) {
			return fmt.Errorf("name: prefix %q is reserved", prefix)
		}
	}

	if m.Version == "" {
		return fmt.Errorf("version: required")
	}
	if !reSemver.MatchString(m.Version) {
		return fmt.Errorf("version: expected MAJOR.MINOR.PATCH, got %q", m.Version)
	}

	if m.Entry == "" {
		return fmt.Errorf("entry: required")
	}
	if filepath.IsAbs(m.Entry) || strings.Contains(m.Entry, "..") {
		return fmt.Errorf("entry: must stay inside the plugin directory")
	}
	if _, err := os.Stat(filepath.Join(dir, m.Entry)); err != nil {
		return fmt.Errorf("entry: %w", err)
	}

	if len(m.Tools) == 0 {
		return fmt.Errorf("tools: at least one tool is required")
	}
	if len(m.Tools) > maxTools {
		return fmt.Errorf("tools: at most %d tools are allowed", maxTools)
	}

	seen := map[string]bool{}
	for i, t := range m.Tools {
		if !reTool.MatchString(t.Name) {
			return fmt.Errorf("tools[%d].name: expected lowercase latin, digits or underscore", i)
		}
		if jsReserved[strings.ToLower(t.Name)] {
			return fmt.Errorf("tools[%d].name: %q conflicts with a JS object property", i, t.Name)
		}
		if seen[t.Name] {
			return fmt.Errorf("tools[%d].name: duplicate %q", i, t.Name)
		}
		seen[t.Name] = true

		if len(t.Description) > maxDescLength {
			return fmt.Errorf("tools[%d].description: longer than %d characters", i, maxDescLength)
		}
		if t.InputSchema == nil {
			return fmt.Errorf("tools[%d].input_schema: required", i)
		}
		if err := validateSchema(i, t.InputSchema); err != nil {
			return err
		}
		if t.Permission != "" {
			if err := validatePermission(fmt.Sprintf("tools[%d].permission", i), t.Permission); err != nil {
				return err
			}
		}
	}

	if m.Permission != "" {
		if err := validatePermission("permission", m.Permission); err != nil {
			return err
		}
	}

	for i, cap := range m.Capabilities {
		if strings.HasPrefix(cap, "core.") {
			return fmt.Errorf("capabilities[%d]: core.* is not accessible to plugins", i)
		}
	}

	if err := validateTimeout("init_timeout_ms", m.InitTimeoutMS, 100, 60000); err != nil {
		return err
	}
	if err := validateTimeout("tool_timeout_ms", m.ToolTimeoutMS, 100, 600000); err != nil {
		return err
	}
	if err := validateTimeout("shutdown_timeout_ms", m.ShutdownTimeoutMS, 100, 30000); err != nil {
		return err
	}

	return nil
}

// validateSchema requires the schema root to be an object.
func validateSchema(index int, schema map[string]any) error {
	kind, _ := schema["type"].(string)
	if kind != "object" {
		return fmt.Errorf("tools[%d].input_schema.type: expected object, got %q", index, kind)
	}
	return nil
}

// validatePermission accepts the known permission levels.
func validatePermission(field, value string) error {
	switch value {
	case "read", "edit", "dangerous":
		return nil
	}
	return fmt.Errorf("%s: expected read|edit|dangerous, got %q", field, value)
}

// validateTimeout bounds a timeout when it is set.
func validateTimeout(field string, value, min, max int) error {
	if value == 0 {
		return nil
	}
	if value < min || value > max {
		return fmt.Errorf("%s: must be between %d and %d", field, min, max)
	}
	return nil
}

// ToolID builds the internal identifier of a plugin tool.
func (m *Manifest) ToolID(tool string) string {
	return "plugin." + m.Name + "." + tool
}

// LevelFor resolves the permission level of a tool.
func (m *Manifest) LevelFor(t *ToolSpec) string {
	if t.Permission != "" {
		return t.Permission
	}
	if m.Permission != "" {
		return m.Permission
	}
	return registry.LevelEdit
}

// HasCapability reports whether the manifest grants a topic.
//
// A trailing wildcard covers the whole group: mcp.github.* allows
// mcp.github.search.
func (m *Manifest) HasCapability(topic string) bool {
	for _, cap := range m.Capabilities {
		if cap == topic {
			return true
		}
		if strings.HasSuffix(cap, ".*") {
			prefix := strings.TrimSuffix(cap, "*")
			if strings.HasPrefix(topic, prefix) {
				return true
			}
		}
	}
	return false
}

// HasCapabilities reports the first topic the manifest does not grant.
func (m *Manifest) HasCapabilities(topics []string) (string, bool) {
	for _, topic := range topics {
		if !m.HasCapability(topic) {
			return topic, false
		}
	}
	return "", true
}

// SortedToolNames returns the tool names in alphabetical order.
func (m *Manifest) SortedToolNames() []string {
	out := make([]string, 0, len(m.Tools))
	for _, t := range m.Tools {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}
