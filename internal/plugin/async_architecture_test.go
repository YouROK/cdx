package plugin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yourok/cdx/internal/bus"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/router"
	"github.com/yourok/cdx/internal/store"
)

// asyncToolRunner executes a nested tool call through the registry.
type asyncToolRunner func(context.Context, ToolCall) (string, error)

func (f asyncToolRunner) RunTool(ctx context.Context, call ToolCall) (string, error) {
	return f(ctx, call)
}

// asyncEnv is an isolated plugin manager with the subsystems the async tests use.
type asyncEnv struct {
	manager  *Manager
	registry *Registry
	router   *router.Router
	bus      *bus.Bus
	store    *store.Store
}

// startAsyncEnv loads one plugin into an isolated manager and returns its host.
func startAsyncEnv(t *testing.T, id, script string) (*asyncEnv, error) {
	t.Helper()

	tmp := t.TempDir()
	st, err := store.Open(store.Options{
		DBPath:        filepath.Join(tmp, "cdx.db"),
		MasterKeyPath: filepath.Join(tmp, "master.key"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	reg := NewRegistry()
	eb := bus.New()
	rtRouter := router.New()
	mgr := NewManager(RuntimeDependencies{
		Store:    st,
		Bus:      eb,
		Router:   rtRouter,
		Registry: reg,
		Logger:   logx.New(),
		Tools: asyncToolRunner(func(ctx context.Context, call ToolCall) (string, error) {
			out, runErr := reg.CallTool(ctx, call.Name, call.Arguments)
			if runErr != nil {
				return "", runErr
			}
			return fmt.Sprint(out), nil
		}),
		Async:     NewAsyncLimiter(16),
		AsyncExec: NewAsyncLimiter(4),
	})
	t.Cleanup(mgr.CloseAll)

	pluginsDir := filepath.Join(tmp, "plugins")
	manifest := fmt.Sprintf(`{
		"id": %q,
		"name": %q,
		"version": "1.0.0",
		"description": "Async architecture test",
		"entry": "main.js"
	}`, id, id)
	writePluginFiles(t, pluginsDir, id, manifest, script)
	_, err = mgr.LoadPlugin(context.Background(), filepath.Join(pluginsDir, id))
	if err != nil {
		return &asyncEnv{manager: mgr, registry: reg, router: rtRouter, bus: eb, store: st}, err
	}
	return &asyncEnv{manager: mgr, registry: reg, router: rtRouter, bus: eb, store: st}, nil
}

// newAsyncEnv is startAsyncEnv for a plugin that must load.
func newAsyncEnv(t *testing.T, id, script string) *asyncEnv {
	t.Helper()
	env, err := startAsyncEnv(t, id, script)
	if err != nil {
		t.Fatalf("load plugin: %v", err)
	}
	return env
}

// TestHTTPRouteServesWhileHandlerAwaits checks that one suspended handler does
// not stop the plugin VM from answering another route.
func TestHTTPRouteServesWhileHandlerAwaits(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	releaseSlow := func() { releaseOnce.Do(func() { close(release) }) }
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		enteredOnce.Do(func() { close(entered) })
		<-release
		_, _ = w.Write([]byte("slow-ok"))
	}))
	defer func() {
		releaseSlow()
		slow.Close()
	}()

	env := newAsyncEnv(t, "async-route", fmt.Sprintf(`
		function setup(cdx) {
			cdx.http.get("/api/test/slow", async function(ctx) {
				var res = await cdx.sys.fetch(%q, { method: "GET", timeout_ms: 10000 });
				ctx.json(200, { body: res.body });
			});
			cdx.http.get("/api/test/ping", function(ctx) {
				ctx.json(200, { pong: true });
			});
		}
	`, slow.URL))

	srv := httptest.NewServer(env.router)
	defer srv.Close()

	slowClient := &http.Client{Timeout: 10 * time.Second}
	slowDone := make(chan int, 1)
	go func() {
		resp, err := slowClient.Get(srv.URL + "/api/test/slow")
		if err != nil {
			slowDone <- 0
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		slowDone <- resp.StatusCode
	}()

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the slow handler did not reach the HTTP call")
	}

	start := time.Now()
	fastClient := &http.Client{Timeout: 2 * time.Second}
	resp, err := fastClient.Get(srv.URL + "/api/test/ping")
	if err != nil {
		t.Fatalf("the plugin VM did not answer while a handler was suspended: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "pong") {
		t.Fatalf("unexpected ping response: %d %s", resp.StatusCode, string(body))
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("the ping waited %s behind the suspended handler", elapsed)
	}

	releaseSlow()
	select {
	case code := <-slowDone:
		if code != http.StatusOK {
			t.Fatalf("the slow handler returned %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the slow handler did not finish")
	}
}

// TestConcurrentHandlersKeepTheirWorkdir checks that two suspended calls resume
// with the context of their own caller, not the last one that touched the VM.
func TestConcurrentHandlersKeepTheirWorkdir(t *testing.T) {
	type gate struct {
		entered     chan struct{}
		release     chan struct{}
		once        sync.Once
		releaseOnce sync.Once
	}
	gates := map[string]*gate{
		"a": {entered: make(chan struct{}), release: make(chan struct{})},
		"b": {entered: make(chan struct{}), release: make(chan struct{})},
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g := gates[r.URL.Query().Get("id")]
		if g == nil {
			http.Error(w, "unknown gate", http.StatusBadRequest)
			return
		}
		g.once.Do(func() { close(g.entered) })
		<-g.release
		_, _ = w.Write([]byte("ok"))
	}))
	releaseAll := func() {
		for _, g := range gates {
			g.releaseOnce.Do(func() { close(g.release) })
		}
	}
	defer func() {
		releaseAll()
		slow.Close()
	}()

	env := newAsyncEnv(t, "async-context", `
		function setup(cdx) {
			cdx.tools.register({
				name: "where_after_await",
				description: "Returns the workdir after a request",
				input_schema: {
					type: "object",
					properties: { url: { type: "string" } },
					required: ["url"]
				}
			}, async function(args) {
				await cdx.sys.fetch(args.url, { method: "GET", timeout_ms: 10000 });
				return cdx.workdir();
			});
		}
	`)

	workdirA := t.TempDir()
	workdirB := t.TempDir()

	type outcome struct {
		dir string
		err error
	}
	results := make(chan outcome, 2)
	call := func(id, workdir string) {
		ctx := WithWorkdir(context.Background(), workdir)
		res, err := env.registry.CallTool(ctx, "where_after_await", map[string]any{"url": slow.URL + "?id=" + id})
		results <- outcome{dir: fmt.Sprint(res), err: err}
	}
	go call("a", workdirA)
	go call("b", workdirB)

	for _, id := range []string{"a", "b"} {
		select {
		case <-gates[id].entered:
		case <-time.After(3 * time.Second):
			t.Fatalf("call %q did not reach the HTTP call", id)
		}
	}
	releaseAll()

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case out := <-results:
			if out.err != nil {
				t.Fatalf("tool call failed: %v", out.err)
			}
			seen[out.dir] = true
		case <-time.After(5 * time.Second):
			t.Fatal("a concurrent tool call did not finish")
		}
	}
	if !seen[workdirA] {
		t.Fatalf("the first call resumed with a foreign workdir: %v", seen)
	}
	if !seen[workdirB] {
		t.Fatalf("the second call resumed with a foreign workdir: %v", seen)
	}
}

// TestAsyncHookWaitsAndModifiesPayload checks that a promise returned by a hook
// is awaited before the core applies the payload.
func TestAsyncHookWaitsAndModifiesPayload(t *testing.T) {
	env := newAsyncEnv(t, "async-hook", `
		function setup(cdx) {
			cdx.hooks.on("tool:before_call", 10, async function(payload) {
				await cdx.sleep(10);
				payload.arguments = payload.arguments || {};
				payload.arguments.text = "hooked";
				payload.hooked = true;
				return payload;
			});
		}
	`)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := env.bus.RunHook(ctx, "tool:before_call", map[string]any{
		"tool":      "echo",
		"arguments": map[string]any{"text": "raw"},
	})
	if err != nil {
		t.Fatalf("RunHook failed: %v", err)
	}
	args, _ := out["arguments"].(map[string]any)
	if args["text"] != "hooked" || out["hooked"] != true {
		t.Fatalf("the async hook result was not applied: %+v", out)
	}
}

// TestAsyncProviderStreamsAndListsModels checks the promise-returning provider
// callbacks that the built-in providers plugin uses.
func TestAsyncProviderStreamsAndListsModels(t *testing.T) {
	env := newAsyncEnv(t, "async-provider", `
		function setup(cdx) {
			cdx.providers.register({
				listModels: async function() {
					await cdx.sleep(5);
					return [{ id: "async-model", name: "async-model", provider: "async-test" }];
				},
				complete: async function(req, emit) {
					await cdx.sleep(5);
					emit({ type: "text", delta: "hel" });
					emit({ type: "text", delta: "lo" });
					return { content: "hello", usage: { total_tokens: 3 } };
				}
			});
		}
	`)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	models, err := env.registry.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) != 1 || models[0].ID != "async-model" {
		t.Fatalf("unexpected models: %+v", models)
	}

	var streamed strings.Builder
	resp, err := env.registry.Complete(ctx, CompleteRequest{Model: "async-model"}, func(ev StreamEvent) {
		if ev.Type == "text" {
			streamed.WriteString(ev.Delta)
		}
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if streamed.String() != "hello" || resp.Content != "hello" {
		t.Fatalf("unexpected stream=%q content=%q", streamed.String(), resp.Content)
	}
}

// TestAsyncToolCallsAnotherTool checks that a handler waiting on cdx.tools.call
// does not deadlock the VM that must run the nested handler.
func TestAsyncToolCallsAnotherTool(t *testing.T) {
	env := newAsyncEnv(t, "async-nested", `
		function setup(cdx) {
			cdx.tools.register({
				name: "echo_text",
				description: "Echoes text",
				input_schema: {
					type: "object",
					properties: { text: { type: "string" } },
					required: ["text"]
				}
			}, function(args) {
				return "echo:" + args.text;
			});
			cdx.tools.register({
				name: "outer",
				description: "Calls echo_text",
				input_schema: { type: "object", properties: {} }
			}, async function(args) {
				var res = await cdx.tools.call("echo_text", { text: "nested" });
				return res.output;
			});
		}
	`)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := env.registry.CallTool(ctx, "outer", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if fmt.Sprint(out) != "echo:nested" {
		t.Fatalf("unexpected nested result: %v", out)
	}
}

// TestAsyncBusHandlerResolvesRequest checks that a promise-returning service
// handler answers a bus request.
func TestAsyncBusHandlerResolvesRequest(t *testing.T) {
	env := newAsyncEnv(t, "async-bus", `
		function setup(cdx) {
			cdx.bus.handle("async:double", async function(payload) {
				await cdx.sleep(5);
				return { value: payload.value * 2 };
			});
		}
	`)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := env.bus.Request(ctx, "test", "async:double", map[string]any{"value": 21})
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	out, _ := res.(map[string]any)
	if fmt.Sprint(out["value"]) != "42" {
		t.Fatalf("unexpected service result: %+v", res)
	}
}

// TestAsyncSetupMustBeSynchronous checks that a plugin cannot suspend setup.
func TestAsyncSetupMustBeSynchronous(t *testing.T) {
	_, err := startAsyncEnv(t, "async-setup", `
		async function setup(cdx) {
			await cdx.sleep(1);
		}
	`)
	if err == nil || !strings.Contains(err.Error(), "must be synchronous") {
		t.Fatalf("expected a synchronous setup error, got %v", err)
	}
}

// TestSetupBackgroundWorkRuns checks that async work started by setup keeps
// running after setup returns.
func TestSetupBackgroundWorkRuns(t *testing.T) {
	env := newAsyncEnv(t, "async-background", `
		function setup(cdx) {
			(async function() {
				try {
					await cdx.sleep(20);
					cdx.storage.set("ready", true);
				} catch (e) {
					cdx.log.error("background failed: " + e);
				}
			})();
		}
	`)

	namespace, err := env.store.PluginStorage("async-background")
	if err != nil {
		t.Fatalf("open plugin storage: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		value, found, getErr := namespace.GetAny("ready")
		if getErr != nil {
			t.Fatalf("read plugin storage: %v", getErr)
		}
		if found && value == true {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the background work started by setup did not finish")
}

// TestAsyncAfterCallbackRuns checks that cdx.after accepts an async callback.
func TestAsyncAfterCallbackRuns(t *testing.T) {
	env := newAsyncEnv(t, "async-after", `
		function setup(cdx) {
			cdx.tools.register({
				name: "schedule",
				description: "Schedules async work after a delay",
				input_schema: { type: "object", properties: {} }
			}, function(args) {
				cdx.after(10, async function() {
					await cdx.sleep(10);
					cdx.storage.set("after_done", true);
				});
				return "scheduled";
			});
		}
	`)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := env.registry.CallTool(ctx, "schedule", map[string]any{}); err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	namespace, err := env.store.PluginStorage("async-after")
	if err != nil {
		t.Fatalf("open plugin storage: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		value, found, getErr := namespace.GetAny("after_done")
		if getErr != nil {
			t.Fatalf("read plugin storage: %v", getErr)
		}
		if found && value == true {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the async cdx.after callback did not run")
}

// TestUnhandledRejectionKeepsVMRunning checks that a rejected fire-and-forget
// promise is logged without breaking later calls.
func TestUnhandledRejectionKeepsVMRunning(t *testing.T) {
	env := newAsyncEnv(t, "async-rejection", `
		function setup(cdx) {
			cdx.tools.register({
				name: "still_alive",
				description: "Reports that the VM is usable",
				input_schema: { type: "object", properties: {} }
			}, function(args) {
				return "alive";
			});
			(async function() {
				await cdx.sleep(10);
				throw new Error("background boom");
			})();
		}
	`)

	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := env.registry.CallTool(ctx, "still_alive", map[string]any{})
	if err != nil {
		t.Fatalf("the VM stopped after an unhandled rejection: %v", err)
	}
	if fmt.Sprint(res) != "alive" {
		t.Fatalf("unexpected result: %v", res)
	}
}

// TestCloseWhileHandlerAwaits checks that stopping a plugin releases a caller
// waiting on a promise instead of blocking until the work finishes.
func TestCloseWhileHandlerAwaits(t *testing.T) {
	env := newAsyncEnv(t, "async-close", `
		function setup(cdx) {
			cdx.tools.register({
				name: "wait_long",
				description: "Waits for a minute",
				input_schema: { type: "object", properties: {} }
			}, async function(args) {
				cdx.storage.set("started", true);
				await cdx.sleep(60000);
				return "late";
			});
		}
	`)

	namespace, err := env.store.PluginStorage("async-close")
	if err != nil {
		t.Fatalf("open plugin storage: %v", err)
	}

	type outcome struct {
		val any
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		val, callErr := env.registry.CallTool(context.Background(), "wait_long", map[string]any{})
		done <- outcome{val: val, err: callErr}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		value, found, getErr := namespace.GetAny("started")
		if getErr != nil {
			t.Fatalf("read plugin storage: %v", getErr)
		}
		if found && value == true {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	start := time.Now()
	env.manager.CloseAll()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("closing the plugin waited %s for the suspended handler", elapsed)
	}

	select {
	case out := <-done:
		if out.err == nil {
			t.Fatalf("the suspended call returned %v instead of an error", out.val)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the suspended call was not released by close")
	}
}

// TestAsyncExecReturnsOutput checks that the promise-returning shell call runs
// in the workdir of the calling context.
func TestAsyncExecReturnsOutput(t *testing.T) {
	env := newAsyncEnv(t, "async-exec", `
		function setup(cdx) {
			cdx.tools.register({
				name: "run_shell",
				description: "Runs a shell command in the workdir",
				input_schema: {
					type: "object",
					properties: { command: { type: "string" } },
					required: ["command"]
				}
			}, async function(args) {
				var res = await cdx.sys.exec(args.command, "", 5000);
				return String(res.stdout || "").trim();
			});
		}
	`)

	ctx := WithWorkdir(context.Background(), t.TempDir())
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := env.registry.CallTool(ctx, "run_shell", map[string]any{"command": "printf ASYNC_EXEC_OK"})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if fmt.Sprint(out) != "ASYNC_EXEC_OK" {
		t.Fatalf("unexpected exec output: %q", fmt.Sprint(out))
	}
}
