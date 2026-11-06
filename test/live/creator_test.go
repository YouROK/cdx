//go:build live

package live

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yourok/cdx/internal/bus"
	"github.com/yourok/cdx/internal/cli"
	"github.com/yourok/cdx/internal/daemon"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/paths"
	"github.com/yourok/cdx/internal/plugin"
	"github.com/yourok/cdx/internal/router"
	"github.com/yourok/cdx/internal/store"
)

// envValue returns the environment value or the fallback.
func envValue(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// TestLiveCreatorBuildsPanelPlugin asks the creator preset for a minimal plugin
// with one tool and one web panel, then loads the result and checks it.
func TestLiveCreatorBuildsPanelPlugin(t *testing.T) {
	apiKey := os.Getenv("CDX_LIVE_API_KEY")
	if apiKey == "" {
		t.Skip("CDX_LIVE_API_KEY is not set")
	}
	baseURL := envValue("CDX_LIVE_BASE_URL", "https://opencode.ai/zen/go/v1")
	model := envValue("CDX_LIVE_MODEL", "deepseek-v4.1-flash")
	provider := "opencode-go"

	tmp := t.TempDir()
	layout := paths.ResolveWithHome(tmp, func(string) string { return "" })
	layout.PluginDirs = []string{repoPluginsDir(t)}

	d, err := daemon.New(context.Background(), daemon.Options{
		Layout:   layout,
		HTTPAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatalf("daemon.New failed: %v", err)
	}
	if err := d.Start(); err != nil {
		t.Fatalf("daemon.Start failed: %v", err)
	}
	defer d.Stop()

	base := "http://" + d.HTTPAddr()
	token := pageToken(t, base)

	provBody := `{"name":"` + provider + `","protocol":"openai-chat","base_url":"` + baseURL + `","api_key":"` + apiKey + `"}`
	if status, body := apiPost(t, base+"/api/plugins/providers/providers", token, provBody); status != http.StatusOK {
		t.Fatalf("failed to configure provider: status=%d body=%s", status, body)
	}
	modelBody := `{"provider":"` + provider + `","name":"creator-test","remote":"` + model + `","context_window":128000,"max_tokens":1024,"temperature":0.2,"default":true}`
	if status, body := apiPost(t, base+"/api/plugins/providers/models", token, modelBody); status != http.StatusOK {
		t.Fatalf("failed to configure model: status=%d body=%s", status, body)
	}

	// The creator writes files outside the session directory: allow it and keep
	// the turn short so the test does not run for long.
	permBody := `{"read_policy":"allow","edit_policy":"allow","dangerous_policy":"allow","outside_policy":"allow"}`
	if status, body := apiPost(t, base+"/api/plugins/permissions/config", token, permBody); status != http.StatusOK {
		t.Fatalf("failed to configure permissions: status=%d body=%s", status, body)
	}
	agentBody := `{"max_steps":14,"step_limit_mode":"stop"}`
	if status, body := apiPost(t, base+"/api/core/agent", token, agentBody); status != http.StatusOK {
		t.Fatalf("failed to configure the step budget: status=%d body=%s", status, body)
	}

	target := filepath.Join(tmp, "demo-plugin")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	prompt := strings.Join([]string{
		"Create a cdx plugin in " + target + " with exactly these three files, then stop.",
		"",
		"1. manifest.json: id demo-plugin, name Demo Plugin, version 1.0.0, description Minimal plugin created by the live creator test, entry main.js.",
		"2. main.js: function setup(cdx) that",
		"   - registers a tool named demo_echo with permission read, description Echo the text, input_schema { type: object, properties: { text: { type: string } }, required: [text] }, returning \"echo: \" + args.text;",
		"   - serves the panel folder with cdx.http.static(\"/ui/demo-plugin\", \"./web\");",
		"   - registers the panel with cdx.ui.register({ title: \"Demo Plugin\", order: 50, entry: \"/ui/demo-plugin/panel.js\", ui_api: 1 }).",
		"3. web/panel.js: an ES module exporting mount(container, api) that renders a heading Demo, a labeled text input, and a button that shows the typed text with api.toast. Use the dashboard classes (.panel, .form-stack, label, .btn, .btn-primary). No JSX and no build step.",
		"",
		"Write the three files and report the paths. Do not create anything else.",
	}, "\n")

	var stdout, stderr bytes.Buffer
	app := &cli.App{
		Layout: layout,
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
	}
	if err := app.Run([]string{"-p", "creator", prompt}); err != nil {
		t.Fatalf("creator turn failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	t.Logf("creator output:\n%s", stdout.String())

	// The three files must exist.
	manifestPath := filepath.Join(target, "manifest.json")
	mainPath := filepath.Join(target, "main.js")
	panelPath := filepath.Join(target, "web", "panel.js")
	for _, path := range []string{manifestPath, mainPath, panelPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing %s: %v\nstdout:\n%s\nstderr:\n%s", path, err, stdout.String(), stderr.String())
		}
	}

	var manifest struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Version string `json:"version"`
		Entry   string `json:"entry"`
	}
	rawManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v\n%s", err, string(rawManifest))
	}
	if manifest.ID != "demo-plugin" || manifest.Entry != "main.js" {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}

	mainSrc, _ := os.ReadFile(mainPath)
	if !strings.Contains(string(mainSrc), "demo_echo") {
		t.Fatalf("main.js does not register the demo_echo tool:\n%s", mainSrc)
	}
	panelSrc, _ := os.ReadFile(panelPath)
	if !strings.Contains(string(panelSrc), "mount") {
		t.Fatalf("panel.js does not export mount:\n%s", panelSrc)
	}

	// The plugin must load and register its tool and panel.
	dbPath := filepath.Join(tmp, "plugin-check.db")
	st, err := store.Open(store.Options{DBPath: dbPath, MasterKeyPath: filepath.Join(tmp, "plugin-check.key")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	registry := plugin.NewRegistry()
	rt := router.New()
	mgr := plugin.NewManager(plugin.RuntimeDependencies{
		Store:    st,
		Bus:      bus.New(),
		Router:   rt,
		Registry: registry,
		Logger:   logx.New(),
	})
	defer mgr.CloseAll()

	if _, err := mgr.LoadPlugin(context.Background(), target); err != nil {
		t.Fatalf("the created plugin does not load: %v", err)
	}

	foundTool := false
	for _, spec := range registry.ListTools() {
		if spec.Name == "demo_echo" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Fatalf("the plugin did not register the demo_echo tool: %+v", registry.ListTools())
	}

	var entry string
	for _, ui := range rt.ListUI() {
		if ui.PluginID == "demo-plugin" {
			entry = ui.Entry
		}
	}
	if entry == "" {
		t.Fatalf("the plugin did not register a web panel: %+v", rt.ListUI())
	}

	// The panel file must be reachable through the registered static mount.
	srv := httptest.NewServer(rt)
	defer srv.Close()
	resp, err := http.Get(srv.URL + entry)
	if err != nil {
		t.Fatalf("GET %s failed: %v", entry, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "mount") {
		t.Fatalf("panel entry %s is not served: %d %s", entry, resp.StatusCode, string(body))
	}
}
