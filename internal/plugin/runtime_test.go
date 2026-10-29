package plugin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yourok/cdx/internal/bus"
	"github.com/yourok/cdx/internal/registry"
)

// newRuntime starts a plugin with the given code.
func newRuntime(t *testing.T, manifest, code string, opts RuntimeOptions) *Runtime {
	t.Helper()

	dir := t.TempDir()
	writePlugin(t, dir, manifest, code)

	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if opts.Bus == nil {
		opts.Bus = bus.New()
	}

	rt, err := Start(context.Background(), dir, m, opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { rt.Close(time.Second) })
	return rt
}

// TestRuntimeCallsTool covers a tool call.
func TestRuntimeCallsTool(t *testing.T) {
	rt := newRuntime(t, validManifest(),
		`cdx.exports = { search: function(args) { return { content: "найдено: " + args.query }; } };`,
		RuntimeOptions{})

	value, err := rt.bus.Request(context.Background(), "", "plugin.web-search.search",
		map[string]any{"query": "go"})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}

	obj, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected object result, got %T", value)
	}
	if obj["content"] != "найдено: go" {
		t.Fatalf("unexpected content %v", obj["content"])
	}
}

// TestRuntimeRegistersTools covers bus registration.
func TestRuntimeRegistersTools(t *testing.T) {
	rt := newRuntime(t, validManifest(),
		`cdx.exports = { search: function() { return "ok"; } };`, RuntimeOptions{})

	found := false
	for _, topic := range rt.bus.Topics() {
		if topic == "plugin.web-search.search" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tool must be registered, topics: %v", rt.bus.Topics())
	}
}

// TestRuntimeMissingExport covers a declared but unexported tool.
func TestRuntimeMissingExport(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, validManifest(), `cdx.exports = {};`)

	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if _, err := Start(context.Background(), dir, m, RuntimeOptions{Bus: bus.New()}); err == nil {
		t.Fatal("expected error for missing export")
	}
}

// TestRuntimeNoExports covers a script without cdx.exports.
func TestRuntimeNoExports(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, validManifest(), `var cdx = {};`)

	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if _, err := Start(context.Background(), dir, m, RuntimeOptions{Bus: bus.New()}); err == nil {
		t.Fatal("expected error when cdx.exports is missing")
	}
}

// TestRuntimeSyntaxError covers a syntax error.
func TestRuntimeSyntaxError(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, validManifest(), `cdx.exports = { search: function( { };`)

	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if _, err := Start(context.Background(), dir, m, RuntimeOptions{Bus: bus.New()}); err == nil {
		t.Fatal("expected syntax error")
	}
}

// TestRuntimeCapabilityDenied covers a call without a capability.
func TestRuntimeCapabilityDenied(t *testing.T) {
	rt := newRuntime(t, validManifest(),
		`cdx.exports = { search: function() {
			try { cdx.request("system.shell.exec", {}); return "allowed"; }
			catch (e) { return "denied: " + e; }
		} };`, RuntimeOptions{})

	value, err := rt.bus.Request(context.Background(), "", "plugin.web-search.search", nil)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if !strings.Contains(asText(value), "denied") {
		t.Fatalf("undeclared capability must be denied, got %v", value)
	}
}

// TestRuntimeCapabilityAllowed covers a call with a declared capability.
func TestRuntimeCapabilityAllowed(t *testing.T) {
	called := false
	rt := newRuntime(t, validManifest(),
		`cdx.exports = { search: function() { return cdx.request("system.http.get", {url: "http://x"}); } };`,
		RuntimeOptions{
			CallBridge: func(ctx context.Context, topic string, payload any) (any, error) {
				called = true
				if topic != "system.http.get" {
					t.Fatalf("unexpected topic %q", topic)
				}
				return "ответ", nil
			},
		})

	value, err := rt.bus.Request(context.Background(), "", "plugin.web-search.search", nil)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if !called {
		t.Fatal("bridge must be called for a declared capability")
	}
	if asText(value) != "ответ" {
		t.Fatalf("unexpected value %v", value)
	}
}

// TestRuntimeBridgeError covers an error coming back from the bridge.
func TestRuntimeBridgeError(t *testing.T) {
	rt := newRuntime(t, validManifest(),
		`cdx.exports = { search: function() {
			try { cdx.request("system.http.get", {}); return "ok"; }
			catch (e) { return "error: " + e; }
		} };`,
		RuntimeOptions{
			CallBridge: func(context.Context, string, any) (any, error) {
				return nil, errors.New("сеть недоступна")
			},
		})

	value, err := rt.bus.Request(context.Background(), "", "plugin.web-search.search", nil)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if !strings.Contains(asText(value), "сеть недоступна") {
		t.Fatalf("bridge error must reach the plugin, got %v", value)
	}
}

// TestRuntimePublish covers publishing an event.
func TestRuntimePublish(t *testing.T) {
	b := bus.New()

	received := make(chan any, 1)
	if _, err := b.Subscribe("test", "plugin.web-search.custom", 8, bus.PolicyDropOldest,
		func(ctx context.Context, payload any) {
			received <- payload
		}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	published := false
	newRuntime(t, `{
		"name": "web-search", "version": "1.0.0", "entry": "plugin.js",
		"capabilities": ["plugin.web-search.custom"],
		"tools": [{"name": "search", "input_schema": {"type": "object"}}]
	}`,
		`cdx.exports = { search: function() { cdx.publish("plugin.web-search.custom", "привет"); return "ok"; } };`,
		RuntimeOptions{
			Bus: b,
			PublishBridge: func(topic string, payload any) error {
				published = true
				b.Publish(context.Background(), topic, payload)
				return nil
			},
		})

	if _, err := b.Request(context.Background(), "", "plugin.web-search.search", nil); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if !published {
		t.Fatal("publish bridge must be called")
	}

	select {
	case got := <-received:
		if got != "привет" {
			t.Fatalf("unexpected payload %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("published event must reach the subscriber")
	}
}

// TestRuntimeToolError covers an error raised inside a tool.
func TestRuntimeToolError(t *testing.T) {
	rt := newRuntime(t, validManifest(),
		`cdx.exports = { search: function() { throw new Error("внутренняя ошибка"); } };`,
		RuntimeOptions{})

	if _, err := rt.bus.Request(context.Background(), "", "plugin.web-search.search", nil); err == nil {
		t.Fatal("expected tool error to be returned")
	}
}

// TestRuntimeTimeout covers interrupting a stuck script.
func TestRuntimeTimeout(t *testing.T) {
	rt := newRuntime(t, `{
		"name": "web-search", "version": "1.0.0", "entry": "plugin.js",
		"tool_timeout_ms": 200,
		"tools": [{"name": "search", "input_schema": {"type": "object"}}]
	}`,
		`cdx.exports = { search: function() { while (true) {} } };`,
		RuntimeOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := rt.bus.Request(ctx, "", "plugin.web-search.search", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("timeout must interrupt the VM quickly, took %v", elapsed)
	}
}

// TestRuntimeSelfRequest covers the recursive call ban.
func TestRuntimeSelfRequest(t *testing.T) {
	rt := newRuntime(t, validManifest(),
		`cdx.exports = { search: function() { return "ok"; } };`, RuntimeOptions{})

	go func() {
		// A call from the same owner: the VM is busy, so the handler must refuse.
		_, _ = rt.bus.Request(context.Background(), "plugin:web-search",
			"plugin.web-search.search", nil)
	}()
	time.Sleep(50 * time.Millisecond)

	if _, err := rt.bus.Request(context.Background(), "", "plugin.web-search.search", nil); err != nil {
		t.Fatalf("external call must work: %v", err)
	}
}

// TestRuntimeLogger covers plugin output reaching the log.
func TestRuntimeLogger(t *testing.T) {
	var messages []string
	newRuntime(t, validManifest(),
		`console.log("из плагина"); cdx.exports = { search: function() { return "ok"; } };`,
		RuntimeOptions{
			Log: func(level, message string) {
				messages = append(messages, level+":"+message)
			},
		})

	if len(messages) == 0 {
		t.Fatal("plugin log must reach the host")
	}
	if messages[0] != "info:из плагина" {
		t.Fatalf("unexpected log %q", messages[0])
	}
}

// TestRuntimeUnregisterOnClose covers removing tools on unload.
func TestRuntimeUnregisterOnClose(t *testing.T) {
	b := bus.New()
	dir := t.TempDir()
	writePlugin(t, dir, validManifest(), `cdx.exports = { search: function() { return "ok"; } };`)

	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	rt, err := Start(context.Background(), dir, m, RuntimeOptions{Bus: b})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	rt.Close(time.Second)

	if _, err := b.Request(context.Background(), "", "plugin.web-search.search", nil); err == nil {
		t.Fatal("tool must be unregistered after close")
	}
}

// TestManagerLoadUnload covers the manager end to end.
func TestManagerLoadUnload(t *testing.T) {
	root := t.TempDir()
	pluginDir := root + "/web-search"
	writePlugin(t, pluginDir, validManifest(), `cdx.exports = { search: function() { return "ok"; } };`)

	b := bus.New()
	reg := registry.NewRegistry()
	mgr := NewManager(Options{Dir: root, Bus: b, Registry: reg})

	if err := mgr.LoadAll(context.Background(), nil); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	names := mgr.Names()
	if len(names) != 1 || names[0] != "web-search" {
		t.Fatalf("unexpected names %v", names)
	}

	if _, ok := reg.GetByLLMName("p_web_search_search"); !ok {
		t.Fatalf("tool must be registered in the agent registry, got %v", reg.Names())
	}

	tool, _ := reg.GetByLLMName("p_web_search_search")
	res, err := tool.Run(context.Background(), map[string]any{"query": "go"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected success, got %q", res.Content)
	}

	if err := mgr.Unload("web-search"); err != nil {
		t.Fatalf("Unload: %v", err)
	}
	if len(mgr.Names()) != 0 {
		t.Fatal("plugin must be removed from the manager")
	}
	if err := mgr.Unload("web-search"); err == nil {
		t.Fatal("expected error for unknown plugin")
	}
}

// TestManagerSkipsDisabled covers the config filter.
func TestManagerSkipsDisabled(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root+"/web-search", validManifest(),
		`cdx.exports = { search: function() { return "ok"; } };`)

	mgr := NewManager(Options{Dir: root, Bus: bus.New(), Registry: registry.NewRegistry()})
	if err := mgr.LoadAll(context.Background(), map[string]bool{"web-search": false}); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(mgr.Names()) != 0 {
		t.Fatalf("disabled plugin must be skipped, got %v", mgr.Names())
	}
}

// TestManagerMissingDir covers a missing plugins directory.
func TestManagerMissingDir(t *testing.T) {
	mgr := NewManager(Options{Dir: t.TempDir() + "/nope", Bus: bus.New()})
	if err := mgr.LoadAll(context.Background(), nil); err != nil {
		t.Fatalf("missing dir must not fail: %v", err)
	}
}

// TestManagerBadPluginDoesNotStopOthers covers error isolation.
func TestManagerBadPluginDoesNotStopOthers(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root+"/web-search", validManifest(),
		`cdx.exports = { search: function() { return "ok"; } };`)
	writePlugin(t, root+"/broken", `{"name":"broken","version":"bad","entry":"plugin.js"}`, "x")

	mgr := NewManager(Options{Dir: root, Bus: bus.New(), Registry: registry.NewRegistry()})
	if err := mgr.LoadAll(context.Background(), nil); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	if len(mgr.Names()) != 1 || mgr.Names()[0] != "web-search" {
		t.Fatalf("only the valid plugin must load, got %v", mgr.Names())
	}
}
