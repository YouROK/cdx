package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yourok/cdx/internal/bus"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/router"
	"github.com/yourok/cdx/internal/store"
)

func writePluginFiles(t *testing.T, root, id, manifestJSON, scriptJS string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte(scriptJS), 0o644); err != nil {
		t.Fatalf("write main.js: %v", err)
	}
	return dir
}

func TestPluginFullCapabilities(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(store.Options{
		DBPath:        filepath.Join(tmp, "cdx.db"),
		MasterKeyPath: filepath.Join(tmp, "master.key"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	eb := bus.New()
	rtRouter := router.New()
	reg := NewRegistry()

	mgr := NewManager(RuntimeDependencies{
		Store:    st,
		Bus:      eb,
		Router:   rtRouter,
		Registry: reg,
		Logger:   logx.New(),
	})
	defer mgr.CloseAll()

	pluginsDir := filepath.Join(tmp, "plugins")

	// 1. Create a mock provider & HTTP & storage/secrets plugin.
	writePluginFiles(t, pluginsDir, "mock-provider", `{
		"id": "mock-provider",
		"name": "Mock Provider",
		"version": "1.0.0",
		"description": "Tests storage, secrets, HTTP routes, middleware, and LLM provider",
		"entry": "main.js"
	}`, `
		function setup(cdx) {
			cdx.storage.set("endpoint", "https://mock.local/v1");
			cdx.secrets.set("api_key", "sk-encrypted-999");

			cdx.http.use("/api/mock", function(ctx) {
				ctx.set("tag", "from-mw");
				ctx.next();
			});

			var grp = cdx.http.group("/api/mock");
			grp.get("/info", function(ctx) {
				ctx.json(200, {
					endpoint: cdx.storage.get("endpoint"),
					has_key: cdx.secrets.has("api_key"),
					tag: ctx.get("tag")
				});
			});

			cdx.ui.register({
				title: "Mock Provider UI",
				order: 5
			});

			cdx.providers.register({
				listModels: function() {
					return [{
						id: "mock-gpt",
						name: "Mock GPT",
						provider: "mock",
						default: true,
						context_window: 32000
					}];
				},
				complete: function(req, emit) {
					emit({ type: "text", delta: "Hello " });
					emit({ type: "text", delta: "world!" });
					return {
						content: "Hello world!",
						usage: { total_tokens: 12 }
					};
				}
			});
		}
	`)

	// 2. Create a mock tool & hook plugin.
	writePluginFiles(t, pluginsDir, "mock-tools", `{
		"id": "mock-tools",
		"name": "Mock Tools",
		"version": "1.0.0",
		"description": "Tests tools and hooks",
		"entry": "main.js"
	}`, `
		function setup(cdx) {
			cdx.hooks.on("tool:before_call", 10, function(payload) {
				payload.hooked = true;
				return payload;
			});

			cdx.tools.register({
				name: "echo_text",
				description: "Echoes input text",
				input_schema: {
					type: "object",
					properties: {
						text: { type: "string" }
					},
					required: ["text"]
				}
			}, function(args) {
				return { echoed: args.text.toUpperCase() };
			});
		}
	`)

	if err := mgr.LoadDirs(context.Background(), []string{pluginsDir}); err != nil {
		t.Fatalf("LoadDirs failed: %v", err)
	}

	if got := len(mgr.List()); got != 2 {
		t.Fatalf("expected 2 loaded plugins, got %d", got)
	}

	// Test HTTP route + middleware registered by mock-provider.
	w := httptest.NewRecorder()
	rtRouter.ServeHTTP(w, httptest.NewRequest("GET", "/api/mock/info", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from plugin HTTP route, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"has_key":true`) || !strings.Contains(w.Body.String(), `"tag":"from-mw"`) {
		t.Fatalf("unexpected HTTP body: %s", w.Body.String())
	}

	// Test UI registration.
	uis := rtRouter.ListUI()
	if len(uis) != 1 || uis[0].Title != "Mock Provider UI" {
		t.Fatalf("unexpected UI descriptors: %+v", uis)
	}

	// Test Provider registration & streaming completion.
	var chunks []string
	resp, err := reg.Complete(context.Background(), CompleteRequest{
		Messages: []map[string]any{{"role": "user", "content": "Hi"}},
	}, func(ev StreamEvent) {
		if ev.Type == "text" {
			chunks = append(chunks, ev.Delta)
		}
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if resp.Content != "Hello world!" || strings.Join(chunks, "") != "Hello world!" {
		t.Fatalf("unexpected provider completion: resp=%+v chunks=%v", resp, chunks)
	}

	// Test Tool registration & schema validation.
	toolRes, err := reg.CallTool(context.Background(), "echo_text", map[string]any{"text": "ping"})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	resMap, _ := toolRes.(map[string]any)
	if resMap["echoed"] != "PING" {
		t.Fatalf("unexpected tool output: %v", toolRes)
	}

	// Test Hook registered by mock-tools.
	hookOut, err := eb.RunHook(context.Background(), "tool:before_call", map[string]any{"tool": "echo_text"})
	if err != nil || hookOut["hooked"] != true {
		t.Fatalf("unexpected hook result: %v (err=%v)", hookOut, err)
	}
}

func TestProvidersPluginDeletesModelWithSlashInName(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(store.Options{
		DBPath:        filepath.Join(tmp, "cdx.db"),
		MasterKeyPath: filepath.Join(tmp, "master.key"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	rtRouter := router.New()
	mgr := NewManager(RuntimeDependencies{
		Store:    st,
		Bus:      bus.New(),
		Router:   rtRouter,
		Registry: NewRegistry(),
		Logger:   logx.New(),
	})
	defer mgr.CloseAll()

	pluginDir := filepath.Join("..", "..", "plugins", "providers")
	if _, err := mgr.LoadPlugin(context.Background(), pluginDir); err != nil {
		t.Fatalf("load providers plugin: %v", err)
	}

	addBody := `{"provider":"dahl","name":"dahl/minimax","remote":"MiniMaxAI/MiniMax-M2.7"}`
	w := httptest.NewRecorder()
	rtRouter.ServeHTTP(w, httptest.NewRequest("POST", "/api/plugins/providers/models", strings.NewReader(addBody)))
	if w.Code != http.StatusOK {
		t.Fatalf("add model failed: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	rtRouter.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/plugins/providers/models?provider=dahl&name=dahl%2Fminimax", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("delete model failed: %d %s", w.Code, w.Body.String())
	}
	var deleteOut struct {
		Deleted string `json:"deleted"`
		Removed int    `json:"removed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &deleteOut); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if deleteOut.Deleted != "dahl/dahl/minimax" || deleteOut.Removed != 1 {
		t.Fatalf("unexpected delete response: %+v", deleteOut)
	}

	w = httptest.NewRecorder()
	rtRouter.ServeHTTP(w, httptest.NewRequest("GET", "/api/plugins/providers/state", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("state failed: %d %s", w.Code, w.Body.String())
	}
	var state struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	for _, m := range state.Models {
		if m.Name == "dahl/minimax" {
			t.Fatalf("model with slash in name was not deleted: %+v", state.Models)
		}
	}
}

func TestProvidersPluginStateResponsiveDuringCompletion(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(store.Options{
		DBPath:        filepath.Join(tmp, "cdx.db"),
		MasterKeyPath: filepath.Join(tmp, "master.key"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	rtRouter := router.New()
	reg := NewRegistry()
	mgr := NewManager(RuntimeDependencies{
		Store:    st,
		Bus:      bus.New(),
		Router:   rtRouter,
		Registry: reg,
		Logger:   logx.New(),
	})
	defer mgr.CloseAll()

	pluginDir := filepath.Join("..", "..", "plugins", "providers")
	if _, err := mgr.LoadPlugin(context.Background(), pluginDir); err != nil {
		t.Fatalf("load providers plugin: %v", err)
	}

	started := make(chan struct{}, 1)
	release := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
		case <-time.After(3 * time.Second):
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	post := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		rtRouter.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(body)))
		if w.Code != http.StatusOK {
			t.Fatalf("POST %s failed: %d %s", path, w.Code, w.Body.String())
		}
		return w
	}
	post("/api/plugins/providers/providers", fmt.Sprintf(`{"name":"dahl","protocol":"openai-chat","base_url":%q}`, srv.URL))
	post("/api/plugins/providers/models", `{"provider":"dahl","name":"minimax","remote":"MiniMaxAI/MiniMax-M2.7"}`)

	type completeResult struct {
		resp *CompleteResponse
		err  error
	}
	done := make(chan completeResult, 1)
	go func() {
		resp, err := reg.Complete(context.Background(), CompleteRequest{
			Messages: []map[string]any{{"role": "user", "content": "hi"}},
		}, nil)
		done <- completeResult{resp: resp, err: err}
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider completion did not start")
	}

	stateDone := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		rtRouter.ServeHTTP(w, httptest.NewRequest("GET", "/api/plugins/providers/state", nil))
		stateDone <- w.Code
	}()
	select {
	case code := <-stateDone:
		if code != http.StatusOK {
			t.Fatalf("state returned %d while completion runs", code)
		}
	case <-time.After(time.Second):
		t.Fatal("providers state endpoint blocked while completion runs")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	models, err := reg.ListModels(ctx)
	cancel()
	if err != nil || len(models) != 1 || models[0].Name != "minimax" {
		t.Fatalf("models were not available during completion: %+v (err=%v)", models, err)
	}

	release <- struct{}{}
	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("Complete failed: %v", out.err)
		}
		if out.resp.Content != "hello" {
			t.Fatalf("unexpected completion content: %q", out.resp.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider completion did not finish")
	}
}

func TestToolsUseSessionWorkdir(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(store.Options{
		DBPath:        filepath.Join(tmp, "cdx.db"),
		MasterKeyPath: filepath.Join(tmp, "master.key"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	reg := NewRegistry()
	mgr := NewManager(RuntimeDependencies{
		Store:    st,
		Bus:      bus.New(),
		Router:   router.New(),
		Registry: reg,
		Logger:   logx.New(),
	})
	defer mgr.CloseAll()

	if _, err := mgr.LoadPlugin(context.Background(), filepath.Join("..", "..", "plugins", "tools-local")); err != nil {
		t.Fatalf("load tools-local plugin: %v", err)
	}

	workdir := filepath.Join(tmp, "project")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir workdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "note.txt"), []byte("session workdir"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}

	ctx := WithWorkdir(context.Background(), workdir)
	readRes, err := reg.CallTool(ctx, "read_file", map[string]any{"path": "note.txt"})
	if err != nil {
		t.Fatalf("read_file failed: %v", err)
	}
	if readRes != "session workdir" {
		t.Fatalf("unexpected read_file result: %v", readRes)
	}

	shellRes, err := reg.CallTool(ctx, "shell", map[string]any{"command": "pwd"})
	if err != nil {
		t.Fatalf("shell failed: %v", err)
	}
	out, _ := shellRes.(map[string]any)
	stdout, _ := out["stdout"].(string)
	if strings.TrimSpace(stdout) != workdir {
		t.Fatalf("shell ran in %q, expected %q", strings.TrimSpace(stdout), workdir)
	}
}

func TestUnmarkedProviderAndToolStayInControlVM(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(store.Options{
		DBPath:        filepath.Join(tmp, "cdx.db"),
		MasterKeyPath: filepath.Join(tmp, "master.key"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	reg := NewRegistry()
	mgr := NewManager(RuntimeDependencies{
		Store:    st,
		Bus:      bus.New(),
		Router:   router.New(),
		Registry: reg,
		Logger:   logx.New(),
	})
	defer mgr.CloseAll()

	pluginsDir := filepath.Join(tmp, "plugins")
	writePluginFiles(t, pluginsDir, "mock-control", `{
		"id": "mock-control",
		"name": "Mock Control",
		"version": "1.0.0",
		"description": "Keeps unmarked functions in the control VM",
		"entry": "main.js",
		"workers": 2
	}`, `
		function setup(cdx) {
			cdx.providers.register({
				listModels: function() {
					return [{ id: "control-model", name: "Control Model", default: true }];
				},
				complete: function(req, emit) {
					return { content: "control-complete" };
				}
			});

			cdx.tools.register({
				name: "control_tool",
				description: "Runs in the control VM",
				input_schema: { type: "object", properties: {} }
			}, function(args) {
				return "control-tool";
			});
		}
	`)
	if err := mgr.LoadDirs(context.Background(), []string{pluginsDir}); err != nil {
		t.Fatalf("LoadDirs failed: %v", err)
	}

	resp, err := reg.Complete(context.Background(), CompleteRequest{
		Messages: []map[string]any{{"role": "user", "content": "hi"}},
	}, nil)
	if err != nil || resp.Content != "control-complete" {
		t.Fatalf("unexpected complete: %+v (err=%v)", resp, err)
	}
	toolRes, err := reg.CallTool(context.Background(), "control_tool", map[string]any{})
	if err != nil || toolRes != "control-tool" {
		t.Fatalf("unexpected tool result: %v (err=%v)", toolRes, err)
	}

	mgr.mu.RLock()
	rt := mgr.plugins["mock-control"]
	mgr.mu.RUnlock()
	if rt == nil {
		t.Fatal("runtime not found")
	}
	rt.workerMu.Lock()
	workers := len(rt.workers)
	rt.workerMu.Unlock()
	if workers != 0 {
		t.Fatalf("unmarked functions started %d worker VMs", workers)
	}
}

func TestPermissionsAskOutsideWorkspace(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(store.Options{
		DBPath:        filepath.Join(tmp, "cdx.db"),
		MasterKeyPath: filepath.Join(tmp, "master.key"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	eb := bus.New()
	reg := NewRegistry()
	mgr := NewManager(RuntimeDependencies{
		Store:    st,
		Bus:      eb,
		Router:   router.New(),
		Registry: reg,
		Logger:   logx.New(),
	})
	defer mgr.CloseAll()

	if _, err := mgr.LoadPlugin(context.Background(), filepath.Join("..", "..", "plugins", "permissions")); err != nil {
		t.Fatalf("load permissions plugin: %v", err)
	}
	ns, err := st.PluginStorage("permissions")
	if err != nil {
		t.Fatalf("permissions storage: %v", err)
	}
	if err := ns.Set("config", map[string]any{
		"read_policy":      "allow",
		"edit_policy":      "allow",
		"dangerous_policy": "allow",
		"tool_rules":       map[string]any{},
	}); err != nil {
		t.Fatalf("set permissions config: %v", err)
	}

	prompts := 0
	var lastOptions []string
	var lastSummary string
	eb.Handle("test", "client:prompt_user", func(_ context.Context, payload any) (any, error) {
		prompts++
		m, _ := payload.(map[string]any)
		lastSummary, _ = m["summary"].(string)
		lastOptions = nil
		switch opts := m["options"].(type) {
		case []string:
			lastOptions = append(lastOptions, opts...)
		case []any:
			for _, raw := range opts {
				if opt, ok := raw.(string); ok {
					lastOptions = append(lastOptions, opt)
				}
			}
		}
		return map[string]any{"choice": "deny"}, nil
	})

	runHook := func(payload map[string]any) error {
		_, err := eb.RunHook(context.Background(), "tool:before_call", payload)
		return err
	}

	err = runHook(map[string]any{
		"tool":       "read_file",
		"permission": "read",
		"workdir":    "/work",
		"arguments":  map[string]any{"path": "/etc/passwd"},
	})
	if err == nil {
		t.Fatal("outside read must be denied when the user denies the prompt")
	}
	if prompts != 1 || !strings.Contains(lastSummary, "outside workspace") {
		t.Fatalf("unexpected outside prompt: prompts=%d summary=%q", prompts, lastSummary)
	}
	if strings.Join(lastOptions, ",") != "once,deny" {
		t.Fatalf("outside prompt must not offer remembered grants: %v", lastOptions)
	}

	if err := runHook(map[string]any{
		"tool":       "read_file",
		"permission": "read",
		"workdir":    "/work",
		"arguments":  map[string]any{"path": "/work/note.txt"},
	}); err != nil {
		t.Fatalf("inside read must be allowed: %v", err)
	}
	if prompts != 1 {
		t.Fatalf("inside read must not prompt, prompts=%d", prompts)
	}

	if err := runHook(map[string]any{
		"tool":       "shell",
		"permission": "dangerous",
		"workdir":    "/work",
		"arguments":  map[string]any{"command": "ls -la 2>/dev/null"},
	}); err != nil {
		t.Fatalf("inside shell must be allowed: %v", err)
	}
	if prompts != 1 {
		t.Fatalf("inside shell must not prompt, prompts=%d", prompts)
	}

	err = runHook(map[string]any{
		"tool":       "shell",
		"permission": "dangerous",
		"workdir":    "/work",
		"arguments":  map[string]any{"command": "rm -rf /"},
	})
	if err == nil {
		t.Fatal("outside shell must be denied when the user denies the prompt")
	}
	if prompts != 2 || !strings.Contains(lastSummary, "outside workspace") {
		t.Fatalf("unexpected shell prompt: prompts=%d summary=%q", prompts, lastSummary)
	}

	err = runHook(map[string]any{
		"tool":       "shell",
		"permission": "dangerous",
		"workdir":    "/work",
		"arguments":  map[string]any{"command": "cd /tmp && ls"},
	})
	if err == nil {
		t.Fatal("cd outside workspace must be denied when the user denies the prompt")
	}
	if prompts != 3 {
		t.Fatalf("cd outside workspace must prompt, prompts=%d", prompts)
	}
}

func TestPluginWorkerVMsKeepControlPlaneResponsive(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(store.Options{
		DBPath:        filepath.Join(tmp, "cdx.db"),
		MasterKeyPath: filepath.Join(tmp, "master.key"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	eb := bus.New()
	rtRouter := router.New()
	reg := NewRegistry()
	mgr := NewManager(RuntimeDependencies{
		Store:    st,
		Bus:      eb,
		Router:   rtRouter,
		Registry: reg,
		Logger:   logx.New(),
	})
	defer mgr.CloseAll()

	started := make(chan struct{}, 4)
	release := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
		case <-time.After(3 * time.Second):
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	pluginsDir := filepath.Join(tmp, "plugins")
	writePluginFiles(t, pluginsDir, "mock-worker", `{
		"id": "mock-worker",
		"name": "Mock Worker",
		"version": "1.0.0",
		"description": "Tests isolated worker VMs",
		"entry": "main.js",
		"workers": 2
	}`, fmt.Sprintf(`
		function setup(cdx) {
			cdx.http.get("/api/mock-worker/ping", function(ctx) {
				ctx.json(200, { ok: true, worker: cdx.worker });
			});

			cdx.providers.register({
				listModels: function() {
					return [{ id: "worker-model", name: "Worker Model", default: true }];
				},
				complete: cdx.inWorker(function(req, emit) {
					var resp = cdx.sys.fetch(%q, { method: "GET", timeout_ms: 5000 });
					return { content: "completed:" + resp.status };
				})
			});

			cdx.tools.register({
				name: "slow_tool",
				description: "Blocks until released",
				input_schema: { type: "object", properties: {} }
			}, cdx.inWorker(function(args) {
				var resp = cdx.sys.fetch(%q, { method: "GET", timeout_ms: 5000 });
				return "tool:" + resp.status;
			}));
		}
	`, srv.URL+"/block", srv.URL+"/block"))

	if err := mgr.LoadDirs(context.Background(), []string{pluginsDir}); err != nil {
		t.Fatalf("LoadDirs failed: %v", err)
	}

	waitSignal := func(ch <-chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %s", what)
		}
	}

	// Completion runs in a worker VM; the control VM must answer status and HTTP routes meanwhile.
	type completeResult struct {
		resp *CompleteResponse
		err  error
	}
	completeDone := make(chan completeResult, 1)
	go func() {
		resp, err := reg.Complete(context.Background(), CompleteRequest{
			Messages: []map[string]any{{"role": "user", "content": "hi"}},
		}, nil)
		completeDone <- completeResult{resp: resp, err: err}
	}()
	waitSignal(started, "provider completion")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	models, err := reg.ListModels(ctx)
	cancel()
	if err != nil {
		t.Fatalf("ListModels while completion runs failed: %v", err)
	}
	if len(models) != 1 || models[0].ID != "worker-model" {
		t.Fatalf("unexpected models while completion runs: %+v", models)
	}

	httpDone := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		rtRouter.ServeHTTP(w, httptest.NewRequest("GET", "/api/mock-worker/ping", nil))
		httpDone <- w.Code
	}()
	select {
	case code := <-httpDone:
		if code != http.StatusOK {
			t.Fatalf("unexpected HTTP status while completion runs: %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("plugin HTTP route blocked while provider completion runs")
	}

	release <- struct{}{}
	select {
	case out := <-completeDone:
		if out.err != nil {
			t.Fatalf("Complete failed: %v", out.err)
		}
		if out.resp.Content != "completed:200" {
			t.Fatalf("unexpected completion response: %+v", out.resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider completion did not finish")
	}

	// Tool handlers also run in workers, so the control plane stays responsive.
	toolDone := make(chan struct {
		res any
		err error
	}, 1)
	go func() {
		res, err := reg.CallTool(context.Background(), "slow_tool", map[string]any{})
		toolDone <- struct {
			res any
			err error
		}{res: res, err: err}
	}()
	waitSignal(started, "tool execution")

	httpDone = make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		rtRouter.ServeHTTP(w, httptest.NewRequest("GET", "/api/mock-worker/ping", nil))
		httpDone <- w.Code
	}()
	select {
	case code := <-httpDone:
		if code != http.StatusOK {
			t.Fatalf("unexpected HTTP status while tool runs: %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("plugin HTTP route blocked while tool handler runs")
	}

	release <- struct{}{}
	select {
	case out := <-toolDone:
		if out.err != nil {
			t.Fatalf("CallTool failed: %v", out.err)
		}
		if out.res != "tool:200" {
			t.Fatalf("unexpected tool output: %v", out.res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tool handler did not finish")
	}
}
