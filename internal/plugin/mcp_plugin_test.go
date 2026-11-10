package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yourok/cdx/internal/bus"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/mcp"
	"github.com/yourok/cdx/internal/router"
	"github.com/yourok/cdx/internal/store"
)

// fakeMCP is a minimal Streamable HTTP server used by the plugin test.
type fakeMCP struct {
	mu        sync.Mutex
	tools     []map[string]any
	resources []map[string]any
	contents  map[string]string
	calls     []string
}

// handle answers the MCP requests the client sends.
func (f *fakeMCP) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var msg map[string]any
	_ = json.Unmarshal(body, &msg)
	method, _ := msg["method"].(string)
	id := msg["id"]

	write := func(result any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}

	switch method {
	case "server/discover":
		write(map[string]any{
			"resultType":        "complete",
			"supportedVersions": []string{mcp.RevisionModern},
			"capabilities":      map[string]any{"tools": map[string]any{}, "resources": map[string]any{}},
			"serverInfo":        map[string]any{"name": "fake-mcp", "version": "1.0.0"},
		})
	case "tools/list":
		f.mu.Lock()
		tools := append([]map[string]any{}, f.tools...)
		f.mu.Unlock()
		write(map[string]any{"resultType": "complete", "tools": tools})
	case "resources/list":
		f.mu.Lock()
		resources := append([]map[string]any{}, f.resources...)
		f.mu.Unlock()
		write(map[string]any{"resultType": "complete", "resources": resources})
	case "resources/read":
		params, _ := msg["params"].(map[string]any)
		uri, _ := params["uri"].(string)
		f.mu.Lock()
		text := f.contents[uri]
		f.mu.Unlock()
		write(map[string]any{
			"resultType": "complete",
			"contents":   []any{map[string]any{"uri": uri, "mimeType": "text/plain", "text": text}},
		})
	case "tools/call":
		params, _ := msg["params"].(map[string]any)
		name, _ := params["name"].(string)
		f.mu.Lock()
		f.calls = append(f.calls, name)
		f.mu.Unlock()
		write(map[string]any{
			"resultType": "complete",
			"content":    []any{map[string]any{"type": "text", "text": "called " + name}},
		})
	default:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"error":   map[string]any{"code": -32601, "message": "method not found"},
		})
	}
}

// setTools replaces the advertised catalog.
func (f *fakeMCP) setTools(tools ...map[string]any) {
	f.mu.Lock()
	f.tools = tools
	f.mu.Unlock()
}

// toolNames returns the called tool names.
func (f *fakeMCP) toolNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}

// mcpTestSetup builds a manager with the real mcp plugin loaded.
func mcpTestSetup(t *testing.T) (*Manager, *Registry, *router.Router, *store.Store) {
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

	rtRouter := router.New()
	reg := NewRegistry()
	mgr := NewManager(RuntimeDependencies{
		Store:    st,
		Bus:      bus.New(),
		Router:   rtRouter,
		Registry: reg,
		Logger:   logx.New(),
		MCP:      mcp.NewManager(),
	})
	t.Cleanup(mgr.CloseAll)
	return mgr, reg, rtRouter, st
}

// loadMCPPlugin loads the plugin from the repository.
func loadMCPPlugin(t *testing.T, mgr *Manager) {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "plugins", "mcp"))
	if err != nil {
		t.Fatalf("resolve plugin dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "main.js")); err != nil {
		t.Skipf("mcp plugin is not present: %v", err)
	}
	if _, err := mgr.LoadPlugin(context.Background(), dir); err != nil {
		t.Fatalf("load mcp plugin: %v", err)
	}
}

// postJSON sends a JSON request through the core router.
func postJSON(t *testing.T, rtRouter *router.Router, path, body string) (int, []byte) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	rtRouter.ServeHTTP(recorder, request)
	data, _ := io.ReadAll(recorder.Result().Body)
	return recorder.Code, data
}

// findTool looks a tool up in the registry.
func findTool(reg *Registry, name string) (ToolSpec, bool) {
	for _, spec := range reg.ListTools() {
		if spec.Name == name {
			return spec, true
		}
	}
	return ToolSpec{}, false
}

// TestMCPPluginTools covers configuration, discovery, registration, and calls.
func TestMCPPluginTools(t *testing.T) {
	mgr, reg, rtRouter, st := mcpTestSetup(t)
	loadMCPPlugin(t, mgr)

	fake := &fakeMCP{}
	fake.setTools(
		map[string]any{
			"name":        "echo",
			"description": "Echo a value",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"value": map[string]any{"type": []any{"string", "null"}}},
			},
			"annotations": map[string]any{"readOnlyHint": true},
		},
		map[string]any{
			"name":        "remove",
			"description": "Delete something",
			"inputSchema": map[string]any{"type": "object"},
			"annotations": map[string]any{"destructiveHint": true},
		},
	)
	server := httptest.NewServer(http.HandlerFunc(fake.handle))
	defer server.Close()

	body := fmt.Sprintf(`{"servers":[{"id":"fake","name":"Fake","transport":"http","url":%q,"enabled":true}]}`, server.URL)
	status, payload := postJSON(t, rtRouter, "/api/plugins/mcp/servers", body)
	if status != http.StatusOK {
		t.Fatalf("save config: %d %s", status, payload)
	}
	if !strings.Contains(string(payload), `"connected":true`) {
		t.Fatalf("the server is not connected: %s", payload)
	}

	echo, ok := findTool(reg, "mcp_fake_echo")
	if !ok {
		t.Fatalf("echo tool is missing: %+v", reg.ListTools())
	}
	if echo.Permission != "read" {
		t.Fatalf("unexpected permission: %q", echo.Permission)
	}
	if echo.InputSchema["properties"] == nil {
		t.Fatalf("the input schema was lost: %+v", echo.InputSchema)
	}

	remove, ok := findTool(reg, "mcp_fake_remove")
	if !ok {
		t.Fatal("remove tool is missing")
	}
	if remove.Permission != "dangerous" {
		t.Fatalf("unexpected permission: %q", remove.Permission)
	}

	out, err := reg.CallTool(context.Background(), "mcp_fake_echo", map[string]any{"value": "hi"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	text, _ := out.(string)
	if text != "called echo" {
		t.Fatalf("unexpected output: %v", out)
	}
	if names := fake.toolNames(); len(names) != 1 || names[0] != "echo" {
		t.Fatalf("unexpected server calls: %v", names)
	}

	recorder := httptest.NewRecorder()
	rtRouter.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/plugins/mcp/tools?server=fake", nil))
	catalog, _ := io.ReadAll(recorder.Result().Body)
	if recorder.Code != http.StatusOK || !strings.Contains(string(catalog), "echo") {
		t.Fatalf("unexpected catalog response: %d %s", recorder.Code, catalog)
	}

	probeBody := fmt.Sprintf(
		`{"server":{"id":"probe","transport":"http","url":%q,"header_secrets":{"Authorization":"Bearer probe"}}}`,
		server.URL,
	)
	probeStatus, probePayload := postJSON(t, rtRouter, "/api/plugins/mcp/servers/test", probeBody)
	if probeStatus != http.StatusOK || !strings.Contains(string(probePayload), `"ok":true`) {
		t.Fatalf("test connection: %d %s", probeStatus, probePayload)
	}
	listRecorder := httptest.NewRecorder()
	rtRouter.ServeHTTP(listRecorder, httptest.NewRequest(http.MethodGet, "/api/plugins/mcp/servers", nil))
	if strings.Contains(listRecorder.Body.String(), `"id":"probe"`) {
		t.Fatal("the test connection saved the server")
	}
	probeSecrets, err := st.PluginSecrets("mcp")
	if err != nil {
		t.Fatalf("plugin secrets: %v", err)
	}
	if _, found, _ := probeSecrets.Get("server:probe:header:Authorization"); found {
		t.Fatal("the test connection stored a secret")
	}

	foundUI := false
	for _, descriptor := range rtRouter.ListUI() {
		if descriptor.PluginID != "mcp" {
			continue
		}
		foundUI = true
		if descriptor.Entry != "/ui/mcp/dist/panel.js" || descriptor.UIApi != 1 {
			t.Fatalf("unexpected UI descriptor: %+v", descriptor)
		}
	}
	if !foundUI {
		t.Fatal("the MCP settings panel is not registered")
	}
}

// TestMCPPluginRefreshDropsTools covers catalog replacement after a refresh.
func TestMCPPluginRefreshDropsTools(t *testing.T) {
	mgr, reg, rtRouter, _ := mcpTestSetup(t)
	loadMCPPlugin(t, mgr)

	fake := &fakeMCP{}
	fake.setTools(map[string]any{
		"name":        "echo",
		"description": "Echo",
		"inputSchema": map[string]any{"type": "object"},
	})
	server := httptest.NewServer(http.HandlerFunc(fake.handle))
	defer server.Close()

	body := fmt.Sprintf(`{"servers":[{"id":"fake","transport":"http","url":%q}]}`, server.URL)
	if status, payload := postJSON(t, rtRouter, "/api/plugins/mcp/servers", body); status != http.StatusOK {
		t.Fatalf("save config: %d %s", status, payload)
	}
	if _, ok := findTool(reg, "mcp_fake_echo"); !ok {
		t.Fatal("echo tool is missing after the first refresh")
	}

	fake.setTools(map[string]any{
		"name":        "other",
		"description": "Other",
		"inputSchema": map[string]any{"type": "object"},
	})
	if status, payload := postJSON(t, rtRouter, "/api/plugins/mcp/refresh", "{}"); status != http.StatusOK {
		t.Fatalf("refresh: %d %s", status, payload)
	}
	if _, ok := findTool(reg, "mcp_fake_echo"); ok {
		t.Fatal("the removed tool is still registered")
	}
	if _, ok := findTool(reg, "mcp_fake_other"); !ok {
		t.Fatal("the new tool was not registered")
	}
}

// TestMCPPluginGateway covers the gateway exposure mode.
func TestMCPPluginGateway(t *testing.T) {
	mgr, reg, rtRouter, _ := mcpTestSetup(t)
	loadMCPPlugin(t, mgr)

	fake := &fakeMCP{}
	fake.setTools(map[string]any{
		"name":        "echo",
		"description": "Echo",
		"inputSchema": map[string]any{"type": "object"},
	})
	server := httptest.NewServer(http.HandlerFunc(fake.handle))
	defer server.Close()

	body := fmt.Sprintf(`{"servers":[{"id":"fake","transport":"http","url":%q,"expose":"gateway"}]}`, server.URL)
	if status, payload := postJSON(t, rtRouter, "/api/plugins/mcp/servers", body); status != http.StatusOK {
		t.Fatalf("save config: %d %s", status, payload)
	}
	if _, ok := findTool(reg, "mcp_fake_echo"); ok {
		t.Fatal("a direct tool was registered in gateway mode")
	}
	if _, ok := findTool(reg, "mcp_call"); !ok {
		t.Fatalf("the gateway tool is missing: %+v", reg.ListTools())
	}

	out, err := reg.CallTool(context.Background(), "mcp_call", map[string]any{
		"server":    "fake",
		"tool":      "echo",
		"arguments": map[string]any{"value": "x"},
	})
	if err != nil {
		t.Fatalf("gateway call: %v", err)
	}
	if text, _ := out.(string); text != "called echo" {
		t.Fatalf("unexpected output: %v", out)
	}
}

// TestMCPPluginImport covers the mcp.json import and secret masking.
func TestMCPPluginImport(t *testing.T) {
	mgr, _, rtRouter, st := mcpTestSetup(t)
	loadMCPPlugin(t, mgr)

	fake := &fakeMCP{}
	fake.setTools()
	server := httptest.NewServer(http.HandlerFunc(fake.handle))
	defer server.Close()

	importBody := fmt.Sprintf(`{"json":%q}`, fmt.Sprintf(`{"mcpServers":{"docs":{"type":"http","url":%q,"headers":{"Authorization":"Bearer top-secret"}}}}`, server.URL))
	status, payload := postJSON(t, rtRouter, "/api/plugins/mcp/import", importBody)
	if status != http.StatusOK {
		t.Fatalf("import: %d %s", status, payload)
	}
	if strings.Contains(string(payload), "top-secret") {
		t.Fatalf("the secret leaked into the API response: %s", payload)
	}

	storageNS, err := st.PluginStorage("mcp")
	if err != nil {
		t.Fatalf("plugin storage: %v", err)
	}
	stored, found, err := storageNS.GetAny("servers")
	if err != nil || !found {
		t.Fatalf("stored config is missing: %v %v", err, found)
	}
	raw, _ := json.Marshal(stored)
	if !strings.Contains(string(raw), "header_secrets") {
		t.Fatalf("unexpected stored config: %v", stored)
	}

	secrets, err := st.PluginSecrets("mcp")
	if err != nil {
		t.Fatalf("plugin secrets: %v", err)
	}
	value, found, err := secrets.Get("server:docs:header:Authorization")
	if err != nil || !found {
		t.Fatalf("the imported header is not in secrets: %v %v", err, found)
	}
	if value != "Bearer top-secret" {
		t.Fatalf("unexpected secret value: %q", value)
	}
}

// TestPluginAfterCallback covers cdx.after.
func TestPluginAfterCallback(t *testing.T) {
	mgr, _, _, st := mcpTestSetup(t)

	dir := writePluginFiles(t, t.TempDir(), "delayed", `{
		"id": "delayed",
		"name": "Delayed",
		"version": "1.0.0",
		"description": "Runs a deferred callback",
		"entry": "main.js"
	}`, `
		function setup(cdx) {
			cdx.storage.set("started", true);
			cdx.after(10, function() {
				cdx.storage.set("deferred", "done");
			});
		}
	`)
	if _, err := mgr.LoadPlugin(context.Background(), dir); err != nil {
		t.Fatalf("load plugin: %v", err)
	}

	storageNS, err := st.PluginStorage("delayed")
	if err != nil {
		t.Fatalf("plugin storage: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		value, found, err := storageNS.GetAny("deferred")
		if err == nil && found && value == "done" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the deferred callback did not run")
}

// TestMCPPluginSecretsRoundTrip covers masking, keeping, and deleting secrets.
func TestMCPPluginSecretsRoundTrip(t *testing.T) {
	mgr, _, rtRouter, st := mcpTestSetup(t)
	loadMCPPlugin(t, mgr)

	fake := &fakeMCP{}
	fake.setTools()
	server := httptest.NewServer(http.HandlerFunc(fake.handle))
	defer server.Close()

	secrets, err := st.PluginSecrets("mcp")
	if err != nil {
		t.Fatalf("plugin secrets: %v", err)
	}

	save := func(headerSecrets string) []byte {
		t.Helper()
		body := fmt.Sprintf(
			`{"servers":[{"id":"fake","transport":"http","url":%q,"header_secrets":%s}]}`,
			server.URL, headerSecrets,
		)
		status, payload := postJSON(t, rtRouter, "/api/plugins/mcp/servers", body)
		if status != http.StatusOK {
			t.Fatalf("save: %d %s", status, payload)
		}
		return payload
	}

	payload := save(`{"Authorization":"Bearer one"}`)
	if strings.Contains(string(payload), "Bearer one") {
		t.Fatalf("the secret leaked into the response: %s", payload)
	}
	value, found, err := secrets.Get("server:fake:header:Authorization")
	if err != nil || !found || value != "Bearer one" {
		t.Fatalf("the secret was not stored: %v %v %q", err, found, value)
	}

	// A blank value keeps the stored secret, an omitted name deletes it.
	save(`{"Authorization":""}`)
	value, found, _ = secrets.Get("server:fake:header:Authorization")
	if !found || value != "Bearer one" {
		t.Fatalf("the blank value dropped the secret: %v %q", found, value)
	}

	save(`{}`)
	if _, found, _ = secrets.Get("server:fake:header:Authorization"); found {
		t.Fatal("the deleted secret is still stored")
	}
}

// TestMCPPluginResources covers the resource tools.
func TestMCPPluginResources(t *testing.T) {
	mgr, reg, rtRouter, _ := mcpTestSetup(t)
	loadMCPPlugin(t, mgr)

	fake := &fakeMCP{
		resources: []map[string]any{{"uri": "file:///doc.txt", "name": "doc"}},
		contents:  map[string]string{"file:///doc.txt": "hello resource"},
	}
	server := httptest.NewServer(http.HandlerFunc(fake.handle))
	defer server.Close()

	body := fmt.Sprintf(`{"servers":[{"id":"fake","transport":"http","url":%q}]}`, server.URL)
	if status, payload := postJSON(t, rtRouter, "/api/plugins/mcp/servers", body); status != http.StatusOK {
		t.Fatalf("save config: %d %s", status, payload)
	}
	if _, ok := findTool(reg, "mcp_list_resources"); !ok {
		t.Fatalf("the resource listing tool is missing: %+v", reg.ListTools())
	}
	if _, ok := findTool(reg, "mcp_read_resource"); !ok {
		t.Fatal("the resource reading tool is missing")
	}

	out, err := reg.CallTool(context.Background(), "mcp_list_resources", map[string]any{"server": "fake"})
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	encoded, _ := json.Marshal(out)
	if !strings.Contains(string(encoded), "file:///doc.txt") {
		t.Fatalf("unexpected resource list: %s", encoded)
	}

	out, err = reg.CallTool(context.Background(), "mcp_read_resource", map[string]any{
		"server": "fake",
		"uri":    "file:///doc.txt",
	})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if text, _ := out.(string); text != "hello resource" {
		t.Fatalf("unexpected resource text: %v", out)
	}
}

// TestMCPPluginAutoGateway covers the tool-count threshold of automatic exposure.
func TestMCPPluginAutoGateway(t *testing.T) {
	mgr, reg, rtRouter, _ := mcpTestSetup(t)
	loadMCPPlugin(t, mgr)

	fake := &fakeMCP{}
	tools := make([]map[string]any, 0, 21)
	for i := 0; i < 21; i++ {
		tools = append(tools, map[string]any{
			"name":        "tool" + strconv.Itoa(i),
			"description": "Generated tool",
			"inputSchema": map[string]any{"type": "object"},
		})
	}
	fake.setTools(tools...)
	server := httptest.NewServer(http.HandlerFunc(fake.handle))
	defer server.Close()

	body := fmt.Sprintf(`{"servers":[{"id":"fake","transport":"http","url":%q}]}`, server.URL)
	if status, payload := postJSON(t, rtRouter, "/api/plugins/mcp/servers", body); status != http.StatusOK {
		t.Fatalf("save config: %d %s", status, payload)
	}
	if _, ok := findTool(reg, "mcp_call"); !ok {
		t.Fatalf("the gateway tool is missing: %+v", reg.ListTools())
	}
	if _, ok := findTool(reg, "mcp_fake_tool0"); ok {
		t.Fatal("a direct tool was registered above the threshold")
	}
}

// TestMCPPluginToolBudget covers the direct tool cap across servers.
func TestMCPPluginToolBudget(t *testing.T) {
	mgr, reg, rtRouter, _ := mcpTestSetup(t)
	loadMCPPlugin(t, mgr)

	servers := make([]string, 0, 3)
	for index := 0; index < 3; index++ {
		fake := &fakeMCP{}
		tools := make([]map[string]any, 0, 20)
		for toolIndex := 0; toolIndex < 20; toolIndex++ {
			tools = append(tools, map[string]any{
				"name":        "tool" + strconv.Itoa(toolIndex),
				"description": "Generated",
				"inputSchema": map[string]any{"type": "object"},
			})
		}
		fake.setTools(tools...)
		server := httptest.NewServer(http.HandlerFunc(fake.handle))
		t.Cleanup(server.Close)
		servers = append(servers, fmt.Sprintf(`{"id":%q,"transport":"http","url":%q}`, string(rune('a'+index)), server.URL))
	}

	body := `{"servers":[` + strings.Join(servers, ",") + `]}`
	if status, payload := postJSON(t, rtRouter, "/api/plugins/mcp/servers", body); status != http.StatusOK {
		t.Fatalf("save config: %d %s", status, payload)
	}
	if _, ok := findTool(reg, "mcp_a_tool0"); !ok {
		t.Fatal("the first server did not expose direct tools")
	}
	if _, ok := findTool(reg, "mcp_b_tool0"); !ok {
		t.Fatal("the second server did not expose direct tools")
	}
	if _, ok := findTool(reg, "mcp_c_tool0"); ok {
		t.Fatal("the third server exceeded the direct tool budget")
	}
	if _, ok := findTool(reg, "mcp_call"); !ok {
		t.Fatal("the gateway tool is missing")
	}
}
