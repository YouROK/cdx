//go:build live

package live

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yourok/cdx/internal/cli"
	"github.com/yourok/cdx/internal/daemon"
	"github.com/yourok/cdx/internal/paths"
)

// writeLiveAsyncPlugin creates a plugin with an async hook, an async tool, and
// an HTTP route next to them in the same VM.
func writeLiveAsyncPlugin(t *testing.T, root, slowURL, hookURL string) string {
	t.Helper()

	dir := filepath.Join(root, "live-async-probe")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	manifest := `{
		"id": "live-async-probe",
		"name": "Live Async Probe",
		"version": "1.0.0",
		"description": "Exercises async hooks, tools, and routes",
		"entry": "main.js"
	}`
	script := fmt.Sprintf(`
		function setup(cdx) {
			cdx.hooks.on("session:before_turn", 50, async function(payload) {
				await cdx.sys.fetch(%q, { method: "GET", timeout_ms: 5000 });
				payload.system_prompt = (payload.system_prompt || "") +
					"\nWhen you call the live_probe tool, pass exactly ASYNC_HOOK_OK as note." +
					"\nBegin your final answer with ASYNC_HOOK_OK.";
				return payload;
			});

			cdx.http.get("/api/plugins/live-async-probe/ping", function(ctx) {
				ctx.json(200, { pong: true });
			});

			cdx.tools.register({
				name: "live_probe",
				description: "Fetches a URL, then reads magic.txt from the session working directory. The probe URL is %s. Use it exactly once.",
				permission: "read",
				input_schema: {
					type: "object",
					properties: {
						url: { type: "string" },
						note: { type: "string" }
					},
					required: ["url"]
				}
			}, async function(args) {
				await cdx.sys.fetch(args.url, { method: "GET", timeout_ms: 120000 });
				var workdir = cdx.workdir();
				var marker = cdx.sys.readFile(workdir + "/magic.txt");
				var result = {
					note: String(args.note || ""),
					workdir: workdir,
					marker: marker
				};
				cdx.sys.writeFile(workdir + "/probe-result.json", JSON.stringify(result));
				return result;
			});
		}
	`, hookURL, slowURL)

	for name, content := range map[string]string{"manifest.json": manifest, "main.js": script} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// quoteJSON encodes a string as a JSON literal.
func quoteJSON(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

// TestLiveAsyncPluginArchitecture runs a real model through a plugin whose
// hook, tool, and route share one VM. The tool suspends on a URL that the test
// holds open, so the route must answer while the tool waits.
func TestLiveAsyncPluginArchitecture(t *testing.T) {
	apiKey := os.Getenv("CDX_LIVE_API_KEY")
	if apiKey == "" {
		t.Skip("CDX_LIVE_API_KEY is not set")
	}
	baseURL := envValue("CDX_LIVE_BASE_URL", "https://opencode.ai/zen/go/v1")
	model := envValue("CDX_LIVE_MODEL", "beam-free")

	// The slow endpoint blocks until the test has checked the plugin route.
	probeEntered := make(chan struct{})
	releaseProbe := make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseProbe) }) }
	var hookHits int
	var hookMu sync.Mutex
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hook":
			hookMu.Lock()
			hookHits++
			hookMu.Unlock()
			_, _ = w.Write([]byte("hook-ok"))
		case "/probe":
			enteredOnce.Do(func() { close(probeEntered) })
			<-releaseProbe
			_, _ = w.Write([]byte("probe-ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer func() {
		release()
		slow.Close()
	}()

	tmp := t.TempDir()
	pluginsDir := filepath.Join(tmp, "plugins")
	writeLiveAsyncPlugin(t, pluginsDir, slow.URL+"/probe", slow.URL+"/hook")

	layout := paths.ResolveWithHome(tmp, func(string) string { return "" })
	layout.PluginDirs = []string{pluginsDir, repoPluginsDir(t)}

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

	provider := `{"name":"live","protocol":"openai-chat","base_url":` + quoteJSON(baseURL) +
		`,"api_key":` + quoteJSON(apiKey) + `}`
	if status, body := apiPost(t, base+"/api/plugins/providers/providers", token, provider); status != http.StatusOK {
		t.Fatalf("failed to configure provider: status=%d body=%s", status, body)
	}
	modelBody := `{"provider":"live","name":"async-live","remote":` + quoteJSON(model) +
		`,"context_window":128000,"max_tokens":2048,"temperature":0.2,"default":true}`
	if status, body := apiPost(t, base+"/api/plugins/providers/models", token, modelBody); status != http.StatusOK {
		t.Fatalf("failed to configure model: status=%d body=%s", status, body)
	}

	// The plugin reads and writes files under the session workdir.
	permBody := `{"read_policy":"allow","edit_policy":"allow","dangerous_policy":"allow","outside_policy":"allow"}`
	if status, body := apiPost(t, base+"/api/plugins/permissions/config", token, permBody); status != http.StatusOK {
		t.Fatalf("failed to configure permissions: status=%d body=%s", status, body)
	}
	if status, body := apiPost(t, base+"/api/core/agent", token, `{"max_steps":8,"step_limit_mode":"stop"}`); status != http.StatusOK {
		t.Fatalf("failed to configure the step budget: status=%d body=%s", status, body)
	}

	workdir := filepath.Join(tmp, "work")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir workdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "magic.txt"), []byte("ASYNC_MARKER_OK"), 0o644); err != nil {
		t.Fatalf("write magic.txt: %v", err)
	}
	t.Chdir(workdir)

	var stdout, stderr bytes.Buffer
	app := &cli.App{
		Layout: layout,
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
	}
	prompt := "Call the live_probe tool exactly once with url " + slow.URL + "/probe and pass the note value that the system prompt requires. Then report the note, marker, and workdir from the tool result."
	done := make(chan error, 1)
	go func() { done <- app.Run([]string{prompt}) }()

	select {
	case <-probeEntered:
	case <-time.After(120 * time.Second):
		t.Fatalf("the model did not reach the async tool\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}

	// The tool is suspended at await: its VM must answer the plugin route.
	req, err := http.NewRequest(http.MethodGet, base+"/api/plugins/live-async-probe/ping", nil)
	if err != nil {
		t.Fatalf("build ping request: %v", err)
	}
	req.Header.Set("X-CDX-Token", token)
	client := &http.Client{Timeout: 3 * time.Second}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("the plugin route did not answer while the tool was suspended: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "pong") {
		t.Fatalf("unexpected ping response: %d %s", resp.StatusCode, string(body))
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the plugin route waited %s behind the suspended tool", elapsed)
	}

	hookMu.Lock()
	if hookHits == 0 {
		hookMu.Unlock()
		t.Fatal("the async before-turn hook did not run")
	}
	hookMu.Unlock()

	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("live turn failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
	case <-time.After(120 * time.Second):
		t.Fatal("the live turn did not finish")
	}
	t.Logf("live output:\n%s", stdout.String())

	resultPath := filepath.Join(workdir, "probe-result.json")
	rawResult, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("the async tool did not write its result: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	var result struct {
		Note    string `json:"note"`
		Workdir string `json:"workdir"`
		Marker  string `json:"marker"`
	}
	if err := json.Unmarshal(rawResult, &result); err != nil {
		t.Fatalf("probe result is not valid JSON: %v\n%s", err, string(rawResult))
	}
	if result.Note != "ASYNC_HOOK_OK" {
		t.Fatalf("the model did not pass the token injected by the async hook: %+v", result)
	}
	if result.Marker != "ASYNC_MARKER_OK" {
		t.Fatalf("the tool did not read the marker after await: %+v", result)
	}
	if result.Workdir != workdir {
		t.Fatalf("the tool resumed with workdir %q, want %q", result.Workdir, workdir)
	}
	if !strings.Contains(stdout.String(), "ASYNC_HOOK_OK") {
		t.Fatalf("the final answer does not follow the hook instruction: %q", stdout.String())
	}
}
