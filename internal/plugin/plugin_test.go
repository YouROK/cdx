package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

// writePlugin puts a plugin on disk.
func writePlugin(t *testing.T, dir, manifest, code string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.js"), []byte(code), 0o644); err != nil {
		t.Fatalf("write plugin: %v", err)
	}
}

// validManifest returns a well-formed manifest.
func validManifest() string {
	return `{
		"name": "web-search",
		"version": "1.0.0",
		"entry": "plugin.js",
		"capabilities": ["system.http.get"],
		"tools": [{
			"name": "search",
			"description": "Search the web",
			"input_schema": {"type": "object", "properties": {"query": {"type": "string"}}}
		}]
	}`
}

// TestLoadValidManifest covers loading a well-formed manifest.
func TestLoadValidManifest(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, validManifest(), "cdx.exports = { search: function(a) { return 'ok'; } };")

	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Name != "web-search" || m.Version != "1.0.0" {
		t.Fatalf("unexpected manifest %+v", m)
	}
	if got := m.ToolID("search"); got != "plugin.web-search.search" {
		t.Fatalf("unexpected tool id %q", got)
	}
}

// TestManifestRejectsBadName covers the name limits.
func TestManifestRejectsBadName(t *testing.T) {
	cases := []string{"Web-Search", "system-thing", "core", "plugin-x", "-bad", ""}
	for _, name := range cases {
		dir := t.TempDir()
		manifest := `{"name":"` + name + `","version":"1.0.0","entry":"plugin.js","tools":[{"name":"t","input_schema":{"type":"object"}}]}`
		writePlugin(t, dir, manifest, "cdx.exports={t:function(){return 1;}};")

		if _, err := Load(dir); err == nil {
			t.Fatalf("name %q must be rejected", name)
		}
	}
}

// TestManifestRejectsBadVersion covers the version format.
func TestManifestRejectsBadVersion(t *testing.T) {
	for _, version := range []string{"1.0", "v1.0.0", "", "1.0.0-beta"} {
		dir := t.TempDir()
		manifest := `{"name":"p","version":"` + version + `","entry":"plugin.js","tools":[{"name":"t","input_schema":{"type":"object"}}]}`
		writePlugin(t, dir, manifest, "cdx.exports={t:function(){return 1;}};")

		if _, err := Load(dir); err == nil {
			t.Fatalf("version %q must be rejected", version)
		}
	}
}

// TestManifestRejectsEscape covers an entry escaping the directory.
func TestManifestRejectsEscape(t *testing.T) {
	for _, entry := range []string{"../evil.js", "/etc/passwd"} {
		dir := t.TempDir()
		manifest := `{"name":"p","version":"1.0.0","entry":"` + entry + `","tools":[{"name":"t","input_schema":{"type":"object"}}]}`
		writePlugin(t, dir, manifest, "x")

		if _, err := Load(dir); err == nil {
			t.Fatalf("entry %q must be rejected", entry)
		}
	}
}

// TestManifestRejectsMissingEntry covers a missing entry file.
func TestManifestRejectsMissingEntry(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"name":"p","version":"1.0.0","entry":"missing.js","tools":[{"name":"t","input_schema":{"type":"object"}}]}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("missing entry must be rejected")
	}
}

// TestManifestRejectsCoreCapability covers the core.* ban.
func TestManifestRejectsCoreCapability(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"name":"p","version":"1.0.0","entry":"plugin.js","capabilities":["core.secret"],"tools":[{"name":"t","input_schema":{"type":"object"}}]}`
	writePlugin(t, dir, manifest, "cdx.exports={t:function(){return 1;}};")

	if _, err := Load(dir); err == nil {
		t.Fatal("core capability must be rejected")
	}
}

// TestManifestRejectsBadTool covers the tool limits.
func TestManifestRejectsBadTool(t *testing.T) {
	cases := []struct {
		name  string
		tools string
	}{
		{"пусто", `[]`},
		{"плохое имя", `[{"name":"Bad-Name","input_schema":{"type":"object"}}]`},
		{"конфликт JS", `[{"name":"__proto__","input_schema":{"type":"object"}}]`},
		{"дубликат", `[{"name":"t","input_schema":{"type":"object"}},{"name":"t","input_schema":{"type":"object"}}]`},
		{"схема не объект", `[{"name":"t","input_schema":{"type":"string"}}]`},
		{"нет схемы", `[{"name":"t"}]`},
		{"плохие права", `[{"name":"t","permission":"admin","input_schema":{"type":"object"}}]`},
	}

	for _, tc := range cases {
		dir := t.TempDir()
		manifest := `{"name":"p","version":"1.0.0","entry":"plugin.js","tools":` + tc.tools + `}`
		writePlugin(t, dir, manifest, "cdx.exports={t:function(){return 1;}};")

		if _, err := Load(dir); err == nil {
			t.Fatalf("case %q must be rejected", tc.name)
		}
	}
}

// TestManifestRejectsBadTimeout covers the timeout bounds.
func TestManifestRejectsBadTimeout(t *testing.T) {
	for _, timeout := range []string{"10", "999999"} {
		dir := t.TempDir()
		manifest := `{"name":"p","version":"1.0.0","entry":"plugin.js","tool_timeout_ms":` + timeout + `,"tools":[{"name":"t","input_schema":{"type":"object"}}]}`
		writePlugin(t, dir, manifest, "cdx.exports={t:function(){return 1;}};")

		if _, err := Load(dir); err == nil {
			t.Fatalf("timeout %s must be rejected", timeout)
		}
	}
}

// TestHasCapability covers capability matching.
func TestHasCapability(t *testing.T) {
	m := &Manifest{Capabilities: []string{"system.http.get", "mcp.github.*"}}

	if !m.HasCapability("system.http.get") {
		t.Fatal("exact capability must match")
	}
	if !m.HasCapability("mcp.github.search_repo") {
		t.Fatal("wildcard must cover the group")
	}
	if m.HasCapability("system.shell.exec") {
		t.Fatal("undeclared capability must not match")
	}
	if m.HasCapability("mcp.filesystem.read_file") {
		t.Fatal("other group must not match")
	}
}

// TestLevelFor covers the permission level of a tool.
func TestLevelFor(t *testing.T) {
	m := &Manifest{Permission: "read"}

	if got := m.LevelFor(&ToolSpec{Name: "a"}); got != "read" {
		t.Fatalf("manifest level must apply, got %q", got)
	}
	if got := m.LevelFor(&ToolSpec{Name: "a", Permission: "dangerous"}); got != "dangerous" {
		t.Fatalf("tool level must win, got %q", got)
	}

	empty := &Manifest{}
	if got := empty.LevelFor(&ToolSpec{Name: "a"}); got != "edit" {
		t.Fatalf("default must be edit, got %q", got)
	}
}

// TestShortName covers the model-facing tool name.
func TestShortName(t *testing.T) {
	cases := map[string]string{
		"web-search": "p_web_search_search",
		"my.plugin":  "p_my_plugin_tool",
	}
	for input, want := range cases {
		tool := "search"
		if input == "my.plugin" {
			tool = "tool"
		}
		if got := shortName(input, tool); got != want {
			t.Fatalf("shortName(%q) = %q, want %q", input, got, want)
		}
	}

	// The length is capped by the model API.
	long := shortName("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbb")
	if len(long) > 64 {
		t.Fatalf("name must be at most 64 characters, got %d", len(long))
	}
}

// TestAsText covers converting a plugin reply.
func TestAsText(t *testing.T) {
	if got := asText("строка"); got != "строка" {
		t.Fatalf("unexpected %q", got)
	}
	if got := asText(map[string]any{"content": "текст", "extra": 1}); got != "текст" {
		t.Fatalf("content must be extracted, got %q", got)
	}
	if got := asText(42); got != "42" {
		t.Fatalf("unexpected %q", got)
	}
	if got := asText(nil); got != "" {
		t.Fatalf("nil must give empty string, got %q", got)
	}
}
