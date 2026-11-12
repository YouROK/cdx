package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yourok/cdx/internal/bus"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/question"
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
				order: 5,
				entry: "/ui/mock/panel.js",
				ui_api: 1,
				config_url: "/api/mock/config",
				styles: ["/ui/mock/panel.css"]
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
	if uis[0].Entry != "/ui/mock/panel.js" || uis[0].UIApi != 1 ||
		uis[0].ConfigURL != "/api/mock/config" || len(uis[0].Styles) != 1 {
		t.Fatalf("unexpected UI descriptor fields: %+v", uis[0])
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

func TestDeclarativePluginSettings(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(store.Options{
		DBPath:        filepath.Join(tmp, "cdx.db"),
		MasterKeyPath: filepath.Join(tmp, "master.key"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	mgr := NewManager(RuntimeDependencies{
		Store:    st,
		Bus:      bus.New(),
		Router:   router.New(),
		Registry: NewRegistry(),
		Logger:   logx.New(),
	})
	defer mgr.CloseAll()

	pluginsDir := filepath.Join(tmp, "plugins")
	writePluginFiles(t, pluginsDir, "settings-plugin", `{
		"id": "settings-plugin",
		"name": "Settings Plugin",
		"version": "1.0.0",
		"description": "Tests declarative settings",
		"entry": "main.js"
	}`, `
		function setup(cdx) {
			cdx.ui.register({
				title: "Settings Plugin",
				schema: {
					type: "object",
					properties: {
						name: { type: "string", default: "cdx" },
						enabled: { type: "boolean", default: true },
						token: { type: "string", format: "secret" }
					}
				}
			});
		}
	`)

	if err := mgr.LoadDirs(context.Background(), []string{pluginsDir}); err != nil {
		t.Fatalf("LoadDirs failed: %v", err)
	}

	raw, values, secrets, ok := mgr.SettingsSnapshot("settings-plugin")
	if !ok || raw == nil {
		t.Fatalf("expected settings snapshot, got ok=%v schema=%v", ok, raw)
	}
	if values["name"] != "cdx" || values["enabled"] != true {
		t.Fatalf("unexpected settings defaults: %+v", values)
	}
	if len(secrets) != 0 {
		t.Fatalf("unexpected secrets: %+v", secrets)
	}

	stored, err := mgr.SaveSettings("settings-plugin", map[string]any{
		"name":    "custom",
		"enabled": false,
		"token":   "secret-value",
	})
	if err != nil {
		t.Fatalf("SaveSettings failed: %v", err)
	}
	if stored["name"] != "custom" || stored["enabled"] != false {
		t.Fatalf("unexpected stored values: %+v", stored)
	}

	_, values, secrets, _ = mgr.SettingsSnapshot("settings-plugin")
	if values["name"] != "custom" || values["enabled"] != false || !secrets["token"] {
		t.Fatalf("unexpected settings after save: values=%+v secrets=%+v", values, secrets)
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

func TestProviderAndToolHandlersRun(t *testing.T) {
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
		"description": "Runs the provider and tool handlers",
		"entry": "main.js"
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

}

// questionOptionIDs lists the option ids of a permission question.
func questionOptionIDs(ask map[string]any) []string {
	raw, _ := ask["options"].([]any)
	ids := make([]string, 0, len(raw))
	for _, item := range raw {
		m, _ := item.(map[string]any)
		if id, _ := m["id"].(string); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// questionDetails renders the labelled details of a question for assertions.
func questionDetails(ask map[string]any) []string {
	raw, _ := ask["details"].([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		m, _ := item.(map[string]any)
		label, _ := m["label"].(string)
		value, _ := m["value"].(string)
		out = append(out, label+"="+value)
	}
	return out
}

func TestPermissionsReturnQuestions(t *testing.T) {
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

	storeConfig := func(overrides map[string]any) {
		t.Helper()
		cfg := map[string]any{
			"read_policy":      "allow",
			"edit_policy":      "allow",
			"dangerous_policy": "allow",
			"outside_policy":   "ask",
			"tool_rules":       map[string]any{},
		}
		for key, value := range overrides {
			cfg[key] = value
		}
		if err := ns.Set("config", cfg); err != nil {
			t.Fatalf("set permissions config: %v", err)
		}
	}
	storeConfig(nil)

	runHook := func(payload map[string]any) (map[string]any, error) {
		return eb.RunHook(context.Background(), "tool:before_call", payload)
	}
	decide := func(payload map[string]any, choice string) map[string]any {
		t.Helper()
		payload["choice"] = choice
		payload["cancelled"] = false
		payload["tag"] = "permissions"
		out, err := eb.RunHook(context.Background(), "tool:decided", payload)
		if err != nil {
			t.Fatalf("tool:decided failed: %v", err)
		}
		return out
	}
	askOf := func(out map[string]any) map[string]any {
		t.Helper()
		ask, _ := out["ask"].(map[string]any)
		if ask == nil {
			t.Fatalf("expected an ask question, got %+v", out)
		}
		return ask
	}

	// 1. An outside read returns a question instead of blocking the hook.
	base := map[string]any{
		"tool":       "read_file",
		"permission": "read",
		"workdir":    "/work",
		"arguments":  map[string]any{"path": "/etc/passwd"},
	}
	out, err := runHook(base)
	if err != nil {
		t.Fatalf("outside read must ask, got error %v", err)
	}
	ask := askOf(out)
	if ask["kind"] != "permission" {
		t.Fatalf("unexpected question kind: %v", ask["kind"])
	}
	if title, _ := ask["title"].(string); title != "Read a file outside the working folder" {
		t.Fatalf("unexpected outside title: %q", title)
	}
	if got := strings.Join(questionOptionIDs(ask), ","); got != "once,deny" {
		t.Fatalf("outside options = %q", got)
	}
	joined := strings.Join(questionDetails(ask), " ")
	if !strings.Contains(joined, "File=/etc/passwd") || !strings.Contains(joined, "Outside=/etc/passwd") {
		t.Fatalf("the question must show the file and the outside path: %v", joined)
	}
	if !strings.Contains(joined, "Working folder=/work") {
		t.Fatalf("the question must show the working folder: %v", joined)
	}
	if decided := decide(map[string]any{
		"tool":       "read_file",
		"permission": "read",
		"workdir":    "/work",
		"arguments":  map[string]any{"path": "/etc/passwd"},
	}, "deny"); decided["allowed"] != false {
		t.Fatalf("deny must not allow the tool: %+v", decided)
	}

	// 2. An inside read needs no question.
	out, err = runHook(map[string]any{
		"tool":       "read_file",
		"permission": "read",
		"workdir":    "/work",
		"arguments":  map[string]any{"path": "/work/note.txt"},
	})
	if err != nil {
		t.Fatalf("inside read must be allowed: %v", err)
	}
	if _, ok := out["ask"]; ok {
		t.Fatalf("inside read must not ask: %+v", out)
	}

	// 3. An inside shell is allowed by the dangerous policy.
	out, err = runHook(map[string]any{
		"tool":       "shell",
		"permission": "dangerous",
		"workdir":    "/work",
		"arguments":  map[string]any{"command": "ls -la 2>/dev/null"},
	})
	if err != nil {
		t.Fatalf("inside shell must be allowed: %v", err)
	}
	if _, ok := out["ask"]; ok {
		t.Fatalf("inside shell must not ask: %+v", out)
	}

	// 4. An outside shell asks and shows the full command.
	out, err = runHook(map[string]any{
		"tool":       "shell",
		"permission": "dangerous",
		"workdir":    "/work",
		"arguments":  map[string]any{"command": "rm -rf /"},
	})
	if err != nil {
		t.Fatalf("outside shell must ask: %v", err)
	}
	ask = askOf(out)
	if !strings.Contains(strings.Join(questionDetails(ask), " "), "Command=rm -rf /") {
		t.Fatalf("the shell question must show the full command: %v", questionDetails(ask))
	}

	// 5. A cd outside the working folder asks.
	if _, err := runHook(map[string]any{
		"tool":       "shell",
		"permission": "dangerous",
		"workdir":    "/work",
		"arguments":  map[string]any{"command": "cd /tmp && ls"},
	}); err != nil {
		t.Fatalf("cd outside workspace must ask: %v", err)
	}

	// 6. The dangerous policy asks with the full option list.
	storeConfig(map[string]any{"dangerous_policy": "ask"})
	out, err = runHook(map[string]any{
		"tool":       "shell",
		"permission": "dangerous",
		"workdir":    "/work",
		"arguments":  map[string]any{"command": "make test"},
	})
	if err != nil {
		t.Fatalf("the dangerous policy must ask: %v", err)
	}
	ask = askOf(out)
	if title, _ := ask["title"].(string); title != "Run a shell command" {
		t.Fatalf("unexpected policy title: %q", title)
	}
	if got := strings.Join(questionOptionIDs(ask), ","); got != "once,session,daemon,always,deny" {
		t.Fatalf("policy options = %q", got)
	}
	if !strings.Contains(strings.Join(questionDetails(ask), " "), "Command=make test") {
		t.Fatalf("the policy question must show the command: %v", questionDetails(ask))
	}

	// 7. "Always allow" persists the rule, and the next call skips the question.
	if decided := decide(map[string]any{
		"session_id": "ses_1",
		"tool":       "shell",
		"permission": "dangerous",
		"workdir":    "/work",
		"arguments":  map[string]any{"command": "make test"},
	}, "always"); decided["allowed"] != true {
		t.Fatalf("always must allow the tool: %+v", decided)
	}
	var cfg map[string]any
	if ok, err := ns.Get("config", &cfg); err != nil || !ok {
		t.Fatalf("read permissions config: ok=%v err=%v", ok, err)
	}
	rules, _ := cfg["tool_rules"].(map[string]any)
	if rules["shell"] != "allow" {
		t.Fatalf("always must persist the rule, got %+v", cfg)
	}
	out, err = runHook(map[string]any{
		"tool":       "shell",
		"permission": "dangerous",
		"workdir":    "/work",
		"arguments":  map[string]any{"command": "make test"},
	})
	if err != nil {
		t.Fatalf("the saved rule must allow the tool: %v", err)
	}
	if _, ok := out["ask"]; ok {
		t.Fatalf("the saved rule must skip the question: %+v", out)
	}

	// 8. outside_policy=allow skips the outside question; deny blocks without asking.
	storeConfig(map[string]any{"outside_policy": "allow", "read_policy": "allow"})
	out, err = runHook(map[string]any{
		"tool":       "read_file",
		"permission": "read",
		"workdir":    "/work",
		"arguments":  map[string]any{"path": "/etc/passwd"},
	})
	if err != nil {
		t.Fatalf("outside read must pass when outside_policy is allow: %v", err)
	}
	if _, ok := out["ask"]; ok {
		t.Fatalf("outside_policy=allow must not ask: %+v", out)
	}
	storeConfig(map[string]any{"outside_policy": "deny"})
	if _, err := runHook(map[string]any{
		"tool":       "read_file",
		"permission": "read",
		"workdir":    "/work",
		"arguments":  map[string]any{"path": "/etc/passwd"},
	}); err == nil || !strings.Contains(err.Error(), "outside the working folder") {
		t.Fatalf("outside_policy=deny must block, got %v", err)
	}
}

func TestAsyncHandlersKeepControlPlaneResponsive(t *testing.T) {
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
	writePluginFiles(t, pluginsDir, "mock-async", `{
		"id": "mock-async",
		"name": "Mock Async",
		"version": "1.0.0",
		"description": "Tests async handlers",
		"entry": "main.js"
	}`, fmt.Sprintf(`
		function setup(cdx) {
			cdx.http.get("/api/mock-async/ping", function(ctx) {
				ctx.json(200, { ok: true });
			});

			cdx.providers.register({
				listModels: function() {
					return [{ id: "async-model", name: "Async Model", default: true }];
				},
				complete: async function(req, emit) {
					var resp = await cdx.sys.fetch(%q, { method: "GET", timeout_ms: 5000 });
					return { content: "completed:" + resp.status };
				}
			});

			cdx.tools.register({
				name: "slow_tool",
				description: "Blocks until released",
				input_schema: { type: "object", properties: {} }
			}, async function(args) {
				var resp = await cdx.sys.fetch(%q, { method: "GET", timeout_ms: 5000 });
				return "tool:" + resp.status;
			});
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

	// Completion waits in Go while the VM keeps serving status and HTTP routes.
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
	if len(models) != 1 || models[0].ID != "async-model" {
		t.Fatalf("unexpected models while completion runs: %+v", models)
	}

	httpDone := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		rtRouter.ServeHTTP(w, httptest.NewRequest("GET", "/api/mock-async/ping", nil))
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

	// Tool handlers also yield the VM while they wait.
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
		rtRouter.ServeHTTP(w, httptest.NewRequest("GET", "/api/mock-async/ping", nil))
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

func TestBusyPluginAnswersServiceUnavailable(t *testing.T) {
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
	dir := writePluginFiles(t, pluginsDir, "slow-plugin", `{
		"id": "slow-plugin",
		"name": "Slow Plugin",
		"version": "1.0.0",
		"description": "Blocks its VM for a while",
		"entry": "main.js"
	}`, `
		function setup(cdx) {
			cdx.http.get("/api/slow/ping", function(ctx) {
				ctx.json(200, { ok: true });
			});

			cdx.http.get("/api/slow/work", function(ctx) {
				var end = Date.now() + 400;
				while (Date.now() < end) {}
				ctx.json(200, { done: true });
			});
		}
	`)
	if _, err := mgr.LoadPlugin(context.Background(), dir); err != nil {
		t.Fatalf("load plugin: %v", err)
	}

	oldTimeout := vmBusyTimeout
	vmBusyTimeout = 50 * time.Millisecond
	defer func() { vmBusyTimeout = oldTimeout }()

	srv := httptest.NewServer(rtRouter)
	defer srv.Close()

	// Occupy the VM, then ask it for something else.
	workDone := make(chan struct{})
	go func() {
		_, _ = http.Get(srv.URL + "/api/slow/work")
		close(workDone)
	}()
	time.Sleep(30 * time.Millisecond)

	resp, err := http.Get(srv.URL + "/api/slow/ping")
	if err != nil {
		t.Fatalf("ping failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 while the VM is busy, got %d %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "is busy") {
		t.Fatalf("unexpected busy body: %s", string(body))
	}

	<-workDone
	time.Sleep(20 * time.Millisecond)

	resp, err = http.Get(srv.URL + "/api/slow/ping")
	if err != nil {
		t.Fatalf("ping after work failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after the VM is free, got %d", resp.StatusCode)
	}
}

func TestContextHookCapsTheSummary(t *testing.T) {
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
	mgr := NewManager(RuntimeDependencies{
		Store:    st,
		Bus:      eb,
		Router:   router.New(),
		Registry: NewRegistry(),
		Logger:   logx.New(),
	})
	defer mgr.CloseAll()

	if _, err := mgr.LoadPlugin(context.Background(), filepath.Join("..", "..", "plugins", "context")); err != nil {
		t.Fatalf("load context plugin: %v", err)
	}

	messages := make([]map[string]any, 0, 60)
	for i := 0; i < 60; i++ {
		messages = append(messages, map[string]any{
			"role":    "user",
			"content": fmt.Sprintf("message %d", i),
		})
	}
	payload := map[string]any{
		"workdir":  tmp,
		"summary":  strings.Repeat("summarized line\n", 200000),
		"messages": messages,
		"tools":    []any{},
	}

	out, err := eb.RunHook(context.Background(), "session:before_turn", payload)
	if err != nil {
		t.Fatalf("hook failed: %v", err)
	}

	prompt, _ := out["system_prompt"].(string)
	if len(prompt) > 20000 {
		t.Fatalf("the summary must be clipped in the system prompt, got %d characters", len(prompt))
	}
	summary, _ := out["summary"].(string)
	if len(summary) > 9000 {
		t.Fatalf("the stored summary must be capped, got %d characters", len(summary))
	}
	if !strings.Contains(summary, "older summary trimmed") {
		t.Fatalf("the clipped summary must carry a marker: %q", summary[:120])
	}
	kept, _ := out["messages"].([]any)
	if len(kept) != 40 {
		t.Fatalf("expected 40 kept messages, got %d", len(kept))
	}
}

func TestContextHookSummarizesDroppedMessagesOnce(t *testing.T) {
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
	mgr := NewManager(RuntimeDependencies{
		Store:    st,
		Bus:      eb,
		Router:   router.New(),
		Registry: NewRegistry(),
		Logger:   logx.New(),
	})
	defer mgr.CloseAll()

	if _, err := mgr.LoadPlugin(context.Background(), filepath.Join("..", "..", "plugins", "context")); err != nil {
		t.Fatalf("load context plugin: %v", err)
	}

	build := func(count int) map[string]any {
		messages := make([]map[string]any, 0, count)
		for i := 0; i < count; i++ {
			messages = append(messages, map[string]any{
				"role":    "user",
				"content": fmt.Sprintf("message %d\n", i),
			})
		}
		return map[string]any{
			"session_id": "ses_test",
			"workdir":    tmp,
			"summary":    "",
			"messages":   messages,
			"tools":      []any{},
		}
	}

	run := func(payload map[string]any) map[string]any {
		out, err := eb.RunHook(context.Background(), "session:before_turn", payload)
		if err != nil {
			t.Fatalf("hook failed: %v", err)
		}
		return out
	}

	payload := build(60)
	first := run(payload)
	summary1, _ := first["summary"].(string)
	if !strings.Contains(summary1, "message 0") {
		t.Fatalf("the first compaction must summarize the dropped messages: %q", summary1)
	}

	// The same history must not be summarized again on the next turn; the
	// session passes its stored summary back.
	payload = build(60)
	payload["summary"] = summary1
	second := run(payload)
	summary2, _ := second["summary"].(string)
	if summary2 != summary1 {
		t.Fatalf("the same history was summarized twice:\nfirst:  %q\nsecond: %q", summary1, summary2)
	}

	// A longer history summarizes only the new messages.
	payload = build(80)
	payload["summary"] = summary2
	third := run(payload)
	summary3, _ := third["summary"].(string)
	if !strings.Contains(summary3, "message 25") {
		t.Fatalf("new dropped messages must be summarized: %q", summary3)
	}
	if count := strings.Count(summary3, "message 0\n"); count != 1 {
		t.Fatalf("message 0 was summarized %d times: %q", count, summary3)
	}
}

type recordingQuestionAsker struct {
	questions []question.Question
	answer    question.Answer
}

func (a *recordingQuestionAsker) Ask(_ context.Context, q question.Question) (question.Answer, error) {
	a.questions = append(a.questions, q)
	return a.answer, nil
}

func TestQuestionsAskFromAsyncHandler(t *testing.T) {
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
	writePluginFiles(t, pluginsDir, "ask-plugin", `{
		"id": "ask-plugin",
		"name": "Ask Plugin",
		"version": "1.0.0",
		"description": "Asks the client from an async handler",
		"entry": "main.js"
	}`, `
		function setup(cdx) {
			cdx.tools.register({
				name: "ask_user",
				description: "Ask the user",
				permission: "read",
				input_schema: {
					type: "object",
					properties: { question: { type: "string" } },
					required: ["question"]
				}
			}, async function(args) {
				return await cdx.questions.ask({
					source: "core",
					kind: "model",
					title: args.question,
					body: "Choose one",
					options: [
						{ id: "a", label: "Option A" },
						{ id: "b", label: "Option B" }
					],
					default: "a",
					text: true
				});
			});
		}
	`)
	if _, err := mgr.LoadPlugin(context.Background(), filepath.Join(pluginsDir, "ask-plugin")); err != nil {
		t.Fatalf("load plugin: %v", err)
	}

	asker := &recordingQuestionAsker{answer: question.Answer{Choice: "b"}}
	ctx := question.WithAsker(context.Background(), asker)
	res, err := reg.CallTool(ctx, "ask_user", map[string]any{"question": "Which one?"})
	if err != nil {
		t.Fatalf("ask_user failed: %v", err)
	}
	out, _ := res.(map[string]any)
	if out["choice"] != "b" {
		t.Fatalf("unexpected answer: %+v", out)
	}
	if len(asker.questions) != 1 {
		t.Fatalf("expected one question, got %d", len(asker.questions))
	}
	q := asker.questions[0]
	if q.Source != "plugin:ask-plugin" {
		t.Fatalf("source = %q, want plugin:ask-plugin", q.Source)
	}
	if q.Kind != question.KindModel || q.Title != "Which one?" || q.Default != "a" || !q.Text {
		t.Fatalf("unexpected question envelope: %+v", q)
	}
	if len(q.Options) != 2 || q.Options[1].ID != "b" {
		t.Fatalf("unexpected options: %+v", q.Options)
	}
}

func TestAskPluginReturnsTheAnswer(t *testing.T) {
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

	if _, err := mgr.LoadPlugin(context.Background(), filepath.Join("..", "..", "plugins", "ask")); err != nil {
		t.Fatalf("load ask plugin: %v", err)
	}

	asker := &recordingQuestionAsker{answer: question.Answer{Choice: "o2"}}
	ctx := question.WithAsker(context.Background(), asker)
	res, err := reg.CallTool(ctx, "ask_user", map[string]any{
		"question":   "Which one?",
		"options":    []any{"Option A", "Option B"},
		"allow_text": true,
	})
	if err != nil {
		t.Fatalf("ask_user failed: %v", err)
	}
	out, _ := res.(map[string]any)
	if out["answer"] != "Option B" {
		t.Fatalf("unexpected answer: %+v", out)
	}
	if len(asker.questions) != 1 {
		t.Fatalf("expected one question, got %d", len(asker.questions))
	}
	q := asker.questions[0]
	if q.Kind != question.KindModel || q.Title != "Which one?" || q.Source != "plugin:ask" || q.Tag != "ask" {
		t.Fatalf("unexpected question envelope: %+v", q)
	}
	if len(q.Options) != 2 || q.Options[1].Label != "Option B" || !q.Text {
		t.Fatalf("unexpected options: %+v", q.Options)
	}
	if q.Default != "o1" {
		t.Fatalf("default = %q, want the first option", q.Default)
	}

	asker.answer = question.Answer{Text: "custom"}
	res, err = reg.CallTool(ctx, "ask_user", map[string]any{
		"question":   "Name it",
		"options":    []any{"Option A", "Option B"},
		"default":    "Option B",
		"allow_text": true,
	})
	if err != nil {
		t.Fatalf("ask_user with text failed: %v", err)
	}
	out, _ = res.(map[string]any)
	if out["answer"] != "custom" {
		t.Fatalf("unexpected text answer: %+v", out)
	}
	if len(asker.questions) != 2 || asker.questions[1].Default != "o2" {
		t.Fatalf("the recommended option must become the default: %+v", asker.questions)
	}

	asker.answer = question.Answer{Cancelled: true, Reason: "no interactive client"}
	res, err = reg.CallTool(ctx, "ask_user", map[string]any{"question": "Anything?"})
	if err != nil {
		t.Fatalf("ask_user without options failed: %v", err)
	}
	out, _ = res.(map[string]any)
	if out["cancelled"] != true {
		t.Fatalf("unexpected cancelled answer: %+v", out)
	}
}

type fakeAgentRunner struct {
	requests []AgentRequest
	result   AgentResult
	err      error
}

func (r *fakeAgentRunner) RunChild(_ context.Context, req AgentRequest) (AgentResult, error) {
	r.requests = append(r.requests, req)
	return r.result, r.err
}

func (r *fakeAgentRunner) RunChildren(_ context.Context, reqs []AgentRequest) ([]AgentResult, error) {
	r.requests = append(r.requests, reqs...)
	out := make([]AgentResult, len(reqs))
	for i := range reqs {
		out[i] = r.result
	}
	return out, nil
}

func TestSubagentPluginDelegates(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(store.Options{
		DBPath:        filepath.Join(tmp, "cdx.db"),
		MasterKeyPath: filepath.Join(tmp, "master.key"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	runner := &fakeAgentRunner{result: AgentResult{SessionID: "ses_child", Content: "done", Steps: 3}}
	reg := NewRegistry()
	mgr := NewManager(RuntimeDependencies{
		Store:    st,
		Bus:      bus.New(),
		Router:   router.New(),
		Registry: reg,
		Logger:   logx.New(),
		Agents:   runner,
	})
	defer mgr.CloseAll()

	if _, err := mgr.LoadPlugin(context.Background(), filepath.Join("..", "..", "plugins", "subagent")); err != nil {
		t.Fatalf("load subagent plugin: %v", err)
	}

	res, err := reg.CallTool(context.Background(), "subagent", map[string]any{"prompt": "do the thing"})
	if err != nil {
		t.Fatalf("subagent failed: %v", err)
	}
	out, _ := res.(map[string]any)
	if out["content"] != "done" || out["session_id"] != "ses_child" {
		t.Fatalf("unexpected result: %+v", out)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("expected one delegated turn, got %d", len(runner.requests))
	}
	req := runner.requests[0]
	if req.Prompt != "do the thing" || req.Preset != "subagent" {
		t.Fatalf("unexpected request: %+v", req)
	}

	res, err = reg.CallTool(context.Background(), "subagent", map[string]any{"prompt": "   "})
	if err != nil {
		t.Fatalf("empty prompt must not fail the tool: %v", err)
	}
	out, _ = res.(map[string]any)
	if out["error"] != "prompt or tasks are required" {
		t.Fatalf("unexpected empty-prompt result: %+v", out)
	}

	runner.err = errors.New("subagent depth limit 1 is reached")
	if _, err := reg.CallTool(context.Background(), "subagent", map[string]any{"prompt": "again"}); err == nil {
		t.Fatal("a runner error must fail the tool call")
	}

	runner.err = nil
	runner.requests = nil
	res, err = reg.CallTool(context.Background(), "subagent", map[string]any{
		"tasks": []any{"first task", "second task", "  "},
	})
	if err != nil {
		t.Fatalf("subagent batch failed: %v", err)
	}
	out, _ = res.(map[string]any)
	batchJSON, err := json.Marshal(out["results"])
	if err != nil {
		t.Fatalf("encode batch results: %v", err)
	}
	var results []map[string]any
	if err := json.Unmarshal(batchJSON, &results); err != nil {
		t.Fatalf("decode batch results: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected two child results, got %+v", out)
	}
	if len(runner.requests) != 2 || runner.requests[0].Prompt != "first task" || runner.requests[1].Prompt != "second task" {
		t.Fatalf("unexpected batch requests: %+v", runner.requests)
	}
	for _, req := range runner.requests {
		if req.Preset != "subagent" {
			t.Fatalf("batch preset = %q, want subagent", req.Preset)
		}
	}
}

func TestAsyncLimiterQueues(t *testing.T) {
	limiter := NewAsyncLimiter(1)
	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatalf("acquire first slot: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := limiter.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a busy limiter must wait for the context, got %v", err)
	}
	limiter.Release()
	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatalf("released slot was not reusable: %v", err)
	}
	limiter.Release()
}
