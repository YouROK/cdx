package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yourok/cdx/internal/ipc"
	"github.com/yourok/cdx/internal/paths"
	"github.com/yourok/cdx/internal/plugin"
	"github.com/yourok/cdx/internal/session"
)

func repoPluginsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "plugins"))
}

func TestDefaultPluginsAndWebUI(t *testing.T) {
	t.Setenv("CDX_ALLOW_REMOTE_HOST", "")
	tmp := t.TempDir()
	layout := paths.ResolveWithHome(tmp, func(string) string { return "" })
	layout.PluginDirs = []string{repoPluginsDir(t)}

	d, err := New(context.Background(), Options{
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

	baseURL := "http://" + d.HTTPAddr()

	// 1. Verify WebUI plugin serves index.html on "/".
	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "<title>cdx</title>") {
		t.Fatalf("unexpected GET / response: %d %s", resp.StatusCode, string(body))
	}

	// 2. The auth plugin guards the API with the token embedded into the page.
	token := tokenFromPage(t, string(body))
	if token == "" {
		t.Fatalf("the served page does not carry the API token: %s", string(body))
	}
	noToken, err := http.Get(baseURL + "/api/core/status")
	if err != nil {
		t.Fatalf("GET /api/core/status failed: %v", err)
	}
	noToken.Body.Close()
	if noToken.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a token, got %d", noToken.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/core/status", nil)
	req.Header.Set("X-CDX-Token", token)
	req.Host = "attacker.example"
	foreign, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/core/status with a foreign host failed: %v", err)
	}
	foreign.Body.Close()
	if foreign.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-local host, got %d", foreign.StatusCode)
	}

	// 3. Verify that every default plugin is loaded.
	var statusData struct {
		Plugins []map[string]any `json:"plugins"`
		Tools   []map[string]any `json:"tools"`
		UI      []map[string]any `json:"ui"`
	}
	statusResp := apiGet(t, baseURL+"/api/core/status", token)
	_ = json.NewDecoder(statusResp.Body).Decode(&statusData)
	statusResp.Body.Close()

	if len(statusData.Plugins) != 9 {
		t.Fatalf("expected 9 default plugins loaded, got %d: %+v", len(statusData.Plugins), statusData.Plugins)
	}
	if len(statusData.Tools) != 7 {
		t.Fatalf("expected 7 tools registered, got %d: %+v", len(statusData.Tools), statusData.Tools)
	}

	// 4. The step-limit settings round-trip through the core API.
	agentResp, err := apiPost(t, baseURL+"/api/core/agent", token, `{"max_steps":25,"step_limit_mode":"stop"}`)
	if err != nil {
		t.Fatalf("POST agent settings failed: %v", err)
	}
	var agentData struct {
		MaxSteps      int    `json:"max_steps"`
		StepLimitMode string `json:"step_limit_mode"`
	}
	_ = json.NewDecoder(agentResp.Body).Decode(&agentData)
	agentResp.Body.Close()
	if agentResp.StatusCode != http.StatusOK || agentData.MaxSteps != 25 || agentData.StepLimitMode != "stop" {
		t.Fatalf("unexpected agent settings: status=%d data=%+v", agentResp.StatusCode, agentData)
	}

	badAgentResp, err := apiPost(t, baseURL+"/api/core/agent", token, `{"step_limit_mode":"sometimes"}`)
	if err != nil {
		t.Fatalf("POST invalid agent settings failed: %v", err)
	}
	badAgentResp.Body.Close()
	if badAgentResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown step limit mode, got %d", badAgentResp.StatusCode)
	}

	// 5. Test self-contained Providers plugin: add a provider with an encrypted API key and add a model.
	provPayload := `{"name":"deepseek","protocol":"openai-chat","base_url":"https://api.deepseek.com/v1","api_key":"sk-test-secret-key"}`
	pResp, err := apiPost(t, baseURL+"/api/plugins/providers/providers", token, provPayload)
	if err != nil {
		t.Fatalf("POST provider failed: %v", err)
	}
	var pOut map[string]any
	_ = json.NewDecoder(pResp.Body).Decode(&pOut)
	pResp.Body.Close()
	if pOut["has_api_key"] != true {
		t.Fatalf("expected provider has_api_key=true, got %+v", pOut)
	}

	modelPayload := `{"provider":"deepseek","name":"ds-chat","remote":"deepseek-chat","context_window":64000,"default":true}`
	mResp, err := apiPost(t, baseURL+"/api/plugins/providers/models", token, modelPayload)
	if err != nil {
		t.Fatalf("POST model failed: %v", err)
	}
	mResp.Body.Close()
	if mResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 adding model, got %d", mResp.StatusCode)
	}

	// Verify the core now sees the model registered by the providers plugin.
	models, err := d.registry.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "deepseek/ds-chat" {
		t.Fatalf("expected core registry to list deepseek/ds-chat, got %+v (err=%v)", models, err)
	}
}

// TestRemoteAccessEnvironment covers the API access switch for remote hosts.
func TestRemoteAccessEnvironment(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  int
	}{
		{"enabled", "1", http.StatusOK},
		{"disabled", "0", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CDX_ALLOW_REMOTE_HOST", tc.value)
			tmp := t.TempDir()
			layout := paths.ResolveWithHome(tmp, func(string) string { return "" })
			layout.PluginDirs = []string{repoPluginsDir(t)}

			d, err := New(context.Background(), Options{Layout: layout, HTTPAddr: "127.0.0.1:0"})
			if err != nil {
				t.Fatalf("daemon.New failed: %v", err)
			}
			if err := d.Start(); err != nil {
				t.Fatalf("daemon.Start failed: %v", err)
			}
			defer d.Stop()

			req, err := http.NewRequest(http.MethodGet, "http://"+d.HTTPAddr()+"/api/core/status", nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Host = "remote.example"
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET /api/core/status failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, tc.want, string(body))
			}
		})
	}
}

// tokenFromPage extracts the API token that the webui plugin embeds into the page.
func tokenFromPage(t *testing.T, page string) string {
	t.Helper()
	const marker = `window.__CDX_TOKEN__="`
	start := strings.Index(page, marker)
	if start == -1 {
		return ""
	}
	rest := page[start+len(marker):]
	end := strings.Index(rest, `"`)
	if end == -1 {
		return ""
	}
	return rest[:end]
}

func apiGet(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request failed: %v", err)
	}
	req.Header.Set("X-CDX-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s failed: %v", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s returned %d", url, resp.StatusCode)
	}
	return resp
}

func apiPost(t *testing.T, url, token, payload string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(payload))
	if err != nil {
		t.Fatalf("build request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CDX-Token", token)
	return http.DefaultClient.Do(req)
}

func TestDeclarativeSettingsEndpoints(t *testing.T) {
	tmp := t.TempDir()
	pluginsDir := filepath.Join(tmp, "plugins", "settings-plugin")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	manifest := `{"id":"settings-plugin","name":"Settings","version":"1.0.0","description":"Settings test","entry":"main.js"}`
	script := `function setup(cdx) {
		cdx.ui.register({
			title: "Settings",
			schema: {
				type: "object",
				properties: {
					name: { type: "string", default: "cdx" },
					enabled: { type: "boolean", default: true },
					token: { type: "string", format: "secret" }
				}
			}
		});
	}`
	if err := os.WriteFile(filepath.Join(pluginsDir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "main.js"), []byte(script), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	layout := paths.ResolveWithHome(tmp, func(string) string { return "" })
	layout.PluginDirs = []string{filepath.Join(tmp, "plugins")}
	d, err := New(context.Background(), Options{Layout: layout, HTTPAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("daemon.New failed: %v", err)
	}
	if err := d.Start(); err != nil {
		t.Fatalf("daemon.Start failed: %v", err)
	}
	defer d.Stop()

	baseURL := "http://" + d.HTTPAddr()
	resp, err := http.Get(baseURL + "/api/core/plugins/settings-plugin/settings")
	if err != nil {
		t.Fatalf("GET settings failed: %v", err)
	}
	var snapshot struct {
		Schema  map[string]any  `json:"schema"`
		Values  map[string]any  `json:"values"`
		Secrets map[string]bool `json:"secrets"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&snapshot)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || snapshot.Schema == nil || snapshot.Values["name"] != "cdx" || snapshot.Values["enabled"] != true {
		t.Fatalf("unexpected initial settings: status=%d snapshot=%+v", resp.StatusCode, snapshot)
	}

	body := bytes.NewBufferString(`{"name":"custom","enabled":false,"token":"secret-value"}`)
	resp, err = http.Post(baseURL+"/api/core/plugins/settings-plugin/settings", "application/json", body)
	if err != nil {
		t.Fatalf("POST settings failed: %v", err)
	}
	var saved struct {
		Values  map[string]any  `json:"values"`
		Secrets map[string]bool `json:"secrets"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&saved)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || saved.Values["name"] != "custom" || saved.Values["enabled"] != false || !saved.Secrets["token"] {
		t.Fatalf("unexpected saved settings: status=%d saved=%+v", resp.StatusCode, saved)
	}
}

// TestLiveSessionEndpoint checks that the dashboard can follow a running turn.
func TestLiveSessionEndpoint(t *testing.T) {
	tmp := t.TempDir()
	layout := paths.ResolveWithHome(tmp, func(string) string { return "" })
	layout.PluginDirs = []string{repoPluginsDir(t)}

	d, err := New(context.Background(), Options{Layout: layout, HTTPAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("daemon.New failed: %v", err)
	}
	if err := d.Start(); err != nil {
		t.Fatalf("daemon.Start failed: %v", err)
	}
	defer d.Stop()

	baseURL := "http://" + d.HTTPAddr()
	page, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	token := tokenFromPage(t, string(body))
	if token == "" {
		t.Fatal("the served page does not carry the API token")
	}

	started := make(chan struct{})
	release := make(chan struct{})
	d.registry.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "mock-live", Name: "mock-live", Default: true}}, nil
		},
		Complete: func(_ context.Context, _ plugin.CompleteRequest, onStream func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			onStream(plugin.StreamEvent{Type: "text", Delta: "partial"})
			close(started)
			<-release
			return &plugin.CompleteResponse{Content: "partial done"}, nil
		},
	})

	workdir := filepath.Join(tmp, "work")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir workdir: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = d.sessions.RunTurn(context.Background(), session.TurnRequest{
			Workdir: workdir,
			Mode:    session.ModeAuto,
			Prompt:  "watch me",
		}, nil)
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the mock provider was not called")
	}

	var list struct {
		Sessions []session.Session `json:"sessions"`
		Live     []string          `json:"live"`
	}
	listResp := apiGet(t, baseURL+"/api/core/sessions", token)
	_ = json.NewDecoder(listResp.Body).Decode(&list)
	listResp.Body.Close()
	if len(list.Sessions) != 1 || len(list.Live) != 1 || list.Live[0] != list.Sessions[0].ID {
		t.Fatalf("unexpected session list: sessions=%+v live=%v", list.Sessions, list.Live)
	}
	id := list.Sessions[0].ID

	var live session.LiveTurn
	liveResp := apiGet(t, baseURL+"/api/core/sessions/"+id+"/live", token)
	_ = json.NewDecoder(liveResp.Body).Decode(&live)
	liveResp.Body.Close()
	if !live.Running || live.Prompt != "watch me" || live.HistoryCount != 0 {
		t.Fatalf("unexpected live state: %+v", live)
	}
	if len(live.Steps) != 1 || live.Steps[0].Text != "partial" {
		t.Fatalf("unexpected live steps: %+v", live.Steps)
	}

	close(release)
	<-done

	liveResp = apiGet(t, baseURL+"/api/core/sessions/"+id+"/live", token)
	_ = json.NewDecoder(liveResp.Body).Decode(&live)
	liveResp.Body.Close()
	if live.Running {
		t.Fatalf("expected the finished turn, got %+v", live)
	}
	if len(live.Steps) != 1 || live.Steps[0].Text != "partial" {
		t.Fatalf("unexpected finished live steps: %+v", live.Steps)
	}

	// An unknown session still answers with an idle snapshot instead of a 404.
	var idle session.LiveTurn
	idleResp := apiGet(t, baseURL+"/api/core/sessions/ses_unknown/live", token)
	_ = json.NewDecoder(idleResp.Body).Decode(&idle)
	idleResp.Body.Close()
	if idle.Running || idle.SessionID != "ses_unknown" {
		t.Fatalf("unexpected idle snapshot: %+v", idle)
	}
}

func TestConcurrentTurnsOverIPCWaitForTheRunningSession(t *testing.T) {
	tmp := t.TempDir()
	layout := paths.ResolveWithHome(tmp, func(string) string { return "" })
	layout.PluginDirs = nil

	d, err := New(context.Background(), Options{Layout: layout})
	if err != nil {
		t.Fatalf("daemon.New failed: %v", err)
	}
	if err := d.Start(); err != nil {
		t.Fatalf("daemon.Start failed: %v", err)
	}
	defer d.Stop()

	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	d.registry.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "mock-live", Name: "mock-live", Default: true}}, nil
		},
		Complete: func(ctx context.Context, _ plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			if calls.Add(1) == 1 {
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return &plugin.CompleteResponse{Content: "done"}, nil
		},
	})

	workdir := filepath.Join(tmp, "work")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir workdir: %v", err)
	}

	type outcome struct {
		data any
		err  error
	}
	first := make(chan outcome, 1)
	go func() {
		data, err := ipc.Call(context.Background(), layout.SocketPath, "turn.run", map[string]any{
			"workdir": workdir,
			"mode":    "auto",
			"prompt":  "first",
		}, ipc.ClientCallbacks{})
		first <- outcome{data: data, err: err}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first turn did not reach the provider")
	}

	waited := make(chan struct{}, 1)
	second := make(chan outcome, 1)
	go func() {
		data, err := ipc.Call(context.Background(), layout.SocketPath, "turn.run", map[string]any{
			"workdir": workdir,
			"mode":    "auto",
			"prompt":  "second",
		}, ipc.ClientCallbacks{
			OnEvent: func(data any) {
				ev, _ := data.(map[string]any)
				if ev["type"] == "waiting" {
					select {
					case waited <- struct{}{}:
					default:
					}
				}
			},
		})
		second <- outcome{data: data, err: err}
	}()

	select {
	case <-waited:
	case out := <-second:
		t.Fatalf("the second turn finished before the first: %+v", out)
	case <-time.After(5 * time.Second):
		t.Fatal("the second turn did not report waiting")
	}

	close(release)

	if out := <-first; out.err != nil {
		t.Fatalf("the first turn failed: %v", out.err)
	}
	if out := <-second; out.err != nil {
		t.Fatalf("the second turn failed: %v", out.err)
	}

	sessions, err := d.sessions.ListSessions()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected one session, got %d: %+v", len(sessions), sessions)
	}
	if sessions[0].MessageCount != 4 {
		t.Fatalf("expected 4 messages in the shared session, got %d", sessions[0].MessageCount)
	}
}

func TestSubagentToolRunsAChildTurn(t *testing.T) {
	tmp := t.TempDir()
	layout := paths.ResolveWithHome(tmp, func(string) string { return "" })
	layout.PluginDirs = []string{repoPluginsDir(t)}

	d, err := New(context.Background(), Options{Layout: layout})
	if err != nil {
		t.Fatalf("daemon.New failed: %v", err)
	}
	if err := d.Start(); err != nil {
		t.Fatalf("daemon.Start failed: %v", err)
	}
	defer d.Stop()

	d.registry.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "mock-child", Name: "mock-child", Default: true}}, nil
		},
		Complete: func(context.Context, plugin.CompleteRequest, func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			return &plugin.CompleteResponse{Content: "child answer"}, nil
		},
	})

	parent, err := d.sessions.ResolveForWorkdir("/work", session.ModeAuto)
	if err != nil {
		t.Fatalf("ResolveForWorkdir: %v", err)
	}
	ctx := session.WithTurn(context.Background(), session.TurnInfo{
		SessionID: parent.ID,
		RootID:    parent.ID,
		Depth:     0,
		Workdir:   parent.Workdir,
	})

	res, err := d.registry.CallTool(ctx, "subagent", map[string]any{"prompt": "do the child task"})
	if err != nil {
		t.Fatalf("subagent failed: %v", err)
	}
	out, _ := res.(map[string]any)
	if out["content"] != "child answer" {
		t.Fatalf("unexpected subagent result: %+v", out)
	}

	sessions, err := d.sessions.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	var child *session.Session
	for i := range sessions {
		if sessions[i].ParentID == parent.ID {
			child = &sessions[i]
			break
		}
	}
	if child == nil || child.Depth != 1 || child.RootID != parent.ID {
		t.Fatalf("unexpected child sessions: %+v", sessions)
	}
}
