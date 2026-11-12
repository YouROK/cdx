package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yourok/cdx/internal/plugin"
	"github.com/yourok/cdx/internal/session"
)

// enableMCPServer turns the MCP endpoint on with the given extra settings.
func enableMCPServer(t *testing.T, baseURL, apiToken, extra string) {
	t.Helper()
	body := `{"enabled":true`
	if extra != "" {
		body += "," + extra
	}
	body += "}"
	resp, err := apiPost(t, baseURL+"/api/core/plugins/mcp-server/settings", apiToken, body)
	if err != nil {
		t.Fatalf("enable MCP server: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enabling the MCP server returned %d", resp.StatusCode)
	}
}

// issueMCPClient creates a client token with the given permissions object.
func issueMCPClient(t *testing.T, baseURL, apiToken, name, permissions string) string {
	t.Helper()
	body := `{"name":` + quoteJSON(name)
	if permissions != "" {
		body += `,"permissions":` + permissions
	}
	body += "}"
	resp, err := apiPost(t, baseURL+"/api/plugins/mcp-server/clients", apiToken, body)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	created, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var client struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(created, &client); err != nil || client.Token == "" {
		t.Fatalf("no token in the create response: %s", created)
	}
	return client.Token
}

// allowLocalClass lifts the daemon's own permission class, so the MCP layer is
// the only one that can ask or deny.
func allowLocalClass(t *testing.T, baseURL, apiToken, body string) {
	t.Helper()
	resp, err := apiPost(t, baseURL+"/api/plugins/permissions/config", apiToken, body)
	if err != nil {
		t.Fatalf("set the local policy: %v", err)
	}
	resp.Body.Close()
}

// toolsOn returns the registry tool names of one server.
func toolsOn(d *Daemon, prefix string) []string {
	names := []string{}
	for _, spec := range d.registry.ListTools() {
		if strings.HasPrefix(spec.Name, prefix) {
			names = append(names, spec.Name)
		}
	}
	return names
}

func TestMCPTwoServersOneClient(t *testing.T) {
	serverA, baseA, apiA := startMCPServerTest(t)
	serverB, baseB, apiB := startMCPServerTest(t)
	client, baseC, apiC := startMCPServerTest(t)

	rootA := t.TempDir()
	rootB := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootA, "file-a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write project file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootB, "file-b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write project file: %v", err)
	}

	enableMCPServer(t, baseA, apiA, `"remote_root":`+quoteJSON(rootA))
	enableMCPServer(t, baseB, apiB, `"remote_root":`+quoteJSON(rootB))
	// The daemon of each server must allow the classes this test calls, so the
	// approval layer is exercised only where the test expects it.
	allowLocalClass(t, baseA, apiA, `{"read_policy":"allow","edit_policy":"allow","dangerous_policy":"allow"}`)
	allowLocalClass(t, baseB, apiB, `{"read_policy":"allow","edit_policy":"allow","dangerous_policy":"allow"}`)

	tokenA := issueMCPClient(t, baseA, apiA, "agent-a", `{"read":"allow","edit":"allow","dangerous":"allow","task":"allow"}`)
	tokenB := issueMCPClient(t, baseB, apiB, "agent-b", `{"read":"allow","edit":"allow","dangerous":"allow","task":"deny"}`)

	config := `{"servers":[` +
		`{"id":"a","name":"Server A","transport":"http","url":` + quoteJSON(baseA+"/mcp") +
		`,"headers":{"Authorization":` + quoteJSON("Bearer "+tokenA) + `},"timeout_ms":5000},` +
		`{"id":"b","name":"Server B","transport":"http","url":` + quoteJSON(baseB+"/mcp") +
		`,"headers":{"Authorization":` + quoteJSON("Bearer "+tokenB) + `},"timeout_ms":1000}` +
		`]}`
	resp, err := apiPost(t, baseC+"/api/plugins/mcp/servers", apiC, config)
	if err != nil {
		t.Fatalf("configure the client: %v", err)
	}
	saved, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("configuring the client returned %d: %s", resp.StatusCode, saved)
	}

	// Both catalogs are namespaced by server, so nothing collides.
	for _, name := range []string{"mcp_a_cdx_list_dir", "mcp_b_cdx_list_dir", "mcp_a_cdx_read_file", "mcp_b_cdx_read_file"} {
		if _, ok := client.registry.GetTool(name); !ok {
			t.Fatalf("%s is missing: %v", name, toolsOn(client, "mcp_"))
		}
	}

	// The same call on two servers runs in parallel and each result stays in
	// its own server.
	var wg sync.WaitGroup
	results := make([]string, 2)
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		out, err := client.registry.CallTool(context.Background(), "mcp_a_cdx_list_dir", map[string]any{"path": rootA})
		results[0], errs[0] = fmt.Sprint(out), err
	}()
	go func() {
		defer wg.Done()
		out, err := client.registry.CallTool(context.Background(), "mcp_b_cdx_list_dir", map[string]any{"path": rootB})
		results[1], errs[1] = fmt.Sprint(out), err
	}()
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("parallel calls failed: %v %v", errs[0], errs[1])
	}
	if !strings.Contains(results[0], "file-a.txt") || strings.Contains(results[0], "file-b.txt") {
		t.Fatalf("server A returned the wrong listing: %s", results[0])
	}
	if !strings.Contains(results[1], "file-b.txt") || strings.Contains(results[1], "file-a.txt") {
		t.Fatalf("server B returned the wrong listing: %s", results[1])
	}

	// A slow call on B hits the client timeout of that server only.
	start := time.Now()
	if _, err := client.registry.CallTool(context.Background(), "mcp_b_cdx_shell", map[string]any{"command": "sleep 3", "timeout_ms": 5000}); err == nil {
		t.Fatal("the slow call did not time out")
	}
	if elapsed := time.Since(start); elapsed > 2500*time.Millisecond {
		t.Fatalf("the timeout took %s", elapsed)
	}
	outA, err := client.registry.CallTool(context.Background(), "mcp_a_cdx_list_dir", map[string]any{"path": rootA})
	if err != nil || !strings.Contains(fmt.Sprint(outA), "file-a.txt") {
		t.Fatalf("server A was affected by the timeout on B: %v %v", outA, err)
	}
	if _, err := client.registry.CallTool(context.Background(), "mcp_b_cdx_list_dir", map[string]any{"path": rootB}); err != nil {
		t.Fatalf("server B stopped answering after its own timeout: %v", err)
	}

	// A task is allowed on A and refused on B, per client permission.
	step := 0
	serverA.registry.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock-model",
		ListModels: func(context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "mock-model/chat", Name: "mock-model/chat", Default: true}}, nil
		},
		Complete: func(_ context.Context, req plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			step++
			return &plugin.CompleteResponse{Content: fmt.Sprintf("answer %d", step)}, nil
		},
	})
	out, err := client.registry.CallTool(context.Background(), "mcp_a_cdx_task_start", map[string]any{"prompt": "say hello", "timeout_s": 30})
	if err != nil {
		t.Fatalf("starting a task on A failed: %v", err)
	}
	text := fmt.Sprint(out)
	if !strings.Contains(text, "started") {
		t.Fatalf("unexpected task start result: %s", text)
	}
	// Server B refuses the task class, so it does not advertise the tools at all.
	if _, ok := client.registry.GetTool("mcp_b_cdx_task_start"); ok {
		t.Fatalf("server B advertised a task tool it must refuse: %v", toolsOn(client, "mcp_b_"))
	}
	if _, ok := client.registry.GetTool("mcp_a_cdx_task_start"); !ok {
		t.Fatalf("server A did not advertise its task tool: %v", toolsOn(client, "mcp_a_"))
	}

	// One server going away does not disturb the other.
	serverB.Stop()
	if _, err := client.registry.CallTool(context.Background(), "mcp_b_cdx_list_dir", map[string]any{"path": rootB}); err == nil {
		t.Fatal("a call to the stopped server succeeded")
	}
	if _, err := client.registry.CallTool(context.Background(), "mcp_a_cdx_list_dir", map[string]any{"path": rootA}); err != nil {
		t.Fatalf("server A broke when B stopped: %v", err)
	}
}

// TestMCPTurnUsesTwoRemoteServers runs a full turn where the model asks for one
// tool on each server in the same step, and checks that both remote calls land
// and that their results reach the model.
func TestMCPTurnUsesTwoRemoteServers(t *testing.T) {
	_, baseA, apiA := startMCPServerTest(t)
	_, baseB, apiB := startMCPServerTest(t)
	client, baseC, apiC := startMCPServerTest(t)

	rootA := t.TempDir()
	rootB := t.TempDir()

	enableMCPServer(t, baseA, apiA, `"remote_root":`+quoteJSON(rootA))
	enableMCPServer(t, baseB, apiB, `"remote_root":`+quoteJSON(rootB))
	allowLocalClass(t, baseA, apiA, `{"read_policy":"allow"}`)
	allowLocalClass(t, baseB, apiB, `{"read_policy":"allow"}`)
	tokenA := issueMCPClient(t, baseA, apiA, "turn-a", `{"read":"allow"}`)
	tokenB := issueMCPClient(t, baseB, apiB, "turn-b", `{"read":"allow"}`)

	config := `{"servers":[` +
		`{"id":"a","name":"Server A","transport":"http","url":` + quoteJSON(baseA+"/mcp") +
		`,"headers":{"Authorization":` + quoteJSON("Bearer "+tokenA) + `}},` +
		`{"id":"b","name":"Server B","transport":"http","url":` + quoteJSON(baseB+"/mcp") +
		`,"headers":{"Authorization":` + quoteJSON("Bearer "+tokenB) + `}}` +
		`]}`
	resp, err := apiPost(t, baseC+"/api/plugins/mcp/servers", apiC, config)
	if err != nil {
		t.Fatalf("configure the client: %v", err)
	}
	resp.Body.Close()

	step := 0
	client.registry.RegisterProvider(plugin.ProviderEntry{
		PluginID: "scripted",
		ListModels: func(context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "scripted/chat", Name: "scripted/chat", Default: true}}, nil
		},
		Complete: func(_ context.Context, req plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			step++
			if step == 1 {
				return &plugin.CompleteResponse{
					Content: "checking both servers",
					ToolCalls: []map[string]any{
						{"id": "call_a", "name": "mcp_a_cdx_list_dir", "arguments": map[string]any{"path": rootA}},
						{"id": "call_b", "name": "mcp_b_cdx_list_dir", "arguments": map[string]any{"path": rootB}},
					},
				}, nil
			}
			seen := 0
			for _, message := range req.Messages {
				if message["role"] == "tool" {
					seen++
				}
			}
			return &plugin.CompleteResponse{Content: fmt.Sprintf("both ok: %d results", seen)}, nil
		},
	})

	workdir := t.TempDir()
	result, err := client.sessions.RunTurn(context.Background(), session.TurnRequest{
		Workdir: workdir,
		Mode:    session.ModeAuto,
		Prompt:  "list both directories",
	}, nil)
	if err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	if !strings.Contains(result.Content, "both ok: 2 results") {
		t.Fatalf("the model did not receive both results: %q", result.Content)
	}

	messages, err := client.sessions.GetMessages(result.Session.ID)
	if err != nil {
		t.Fatalf("read the turn history: %v", err)
	}
	calls := map[string]bool{}
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			name, _ := call["name"].(string)
			calls[name] = true
		}
	}
	if !calls["mcp_a_cdx_list_dir"] || !calls["mcp_b_cdx_list_dir"] {
		t.Fatalf("the turn did not call both servers: %v", calls)
	}
}

// TestMCPToolPayloadForModel captures the exact tool payload a provider
// receives when two MCP servers are connected, which is what the model sees.
func TestMCPToolPayloadForModel(t *testing.T) {
	_, baseA, apiA := startMCPServerTest(t)
	_, baseB, apiB := startMCPServerTest(t)
	client, baseC, apiC := startMCPServerTest(t)

	enableMCPServer(t, baseA, apiA, "")
	enableMCPServer(t, baseB, apiB, "")
	tokenA := issueMCPClient(t, baseA, apiA, "payload-a", `{"read":"allow","edit":"deny","task":"deny"}`)
	tokenB := issueMCPClient(t, baseB, apiB, "payload-b", `{"read":"allow","edit":"deny","task":"deny"}`)

	config := `{"servers":[` +
		`{"id":"a","name":"Server A","transport":"http","url":` + quoteJSON(baseA+"/mcp") +
		`,"headers":{"Authorization":` + quoteJSON("Bearer "+tokenA) + `}},` +
		`{"id":"b","name":"Server B","transport":"http","url":` + quoteJSON(baseB+"/mcp") +
		`,"headers":{"Authorization":` + quoteJSON("Bearer "+tokenB) + `}}` +
		`]}`
	resp, err := apiPost(t, baseC+"/api/plugins/mcp/servers", apiC, config)
	if err != nil {
		t.Fatalf("configure the client: %v", err)
	}
	resp.Body.Close()

	var captured []map[string]any
	client.registry.RegisterProvider(plugin.ProviderEntry{
		PluginID: "capture",
		ListModels: func(context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "capture/chat", Name: "capture/chat", Default: true}}, nil
		},
		Complete: func(_ context.Context, req plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			if captured == nil {
				captured = req.Tools
			}
			return &plugin.CompleteResponse{Content: "done"}, nil
		},
	})

	workdir := t.TempDir()
	if _, err := client.sessions.RunTurn(context.Background(), session.TurnRequest{
		Workdir: workdir,
		Mode:    session.ModeAuto,
		Prompt:  "hello",
	}, nil); err != nil {
		t.Fatalf("turn failed: %v", err)
	}

	remote := map[string]map[string]any{}
	for _, tool := range captured {
		name, _ := tool["name"].(string)
		if strings.HasPrefix(name, "mcp_a_") || strings.HasPrefix(name, "mcp_b_") {
			remote[name] = tool
		}
	}
	want := []string{"mcp_a_cdx_list_dir", "mcp_a_cdx_read_file", "mcp_b_cdx_list_dir", "mcp_b_cdx_read_file"}
	if len(remote) != len(want) {
		t.Fatalf("unexpected remote tools for the model: %v", sortedKeys(remote))
	}
	for _, name := range want {
		tool, ok := remote[name]
		if !ok {
			t.Fatalf("%s is missing from the payload: %v", name, sortedKeys(remote))
		}
		if description, _ := tool["description"].(string); strings.TrimSpace(description) == "" {
			t.Fatalf("%s has no description for the model", name)
		}
		spec, _ := tool["input_schema"].(map[string]any)
		properties, _ := spec["properties"].(map[string]any)
		if len(properties) == 0 {
			t.Fatalf("%s has no input properties: %v", name, spec)
		}
	}
	payload, _ := json.MarshalIndent(remote, "", "  ")
	t.Logf("tool payload for the model:\n%s", payload)
}

// sortedKeys returns the map keys in a stable order for test output.
func sortedKeys(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
