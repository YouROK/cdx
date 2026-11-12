package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/yourok/cdx/internal/paths"
	"github.com/yourok/cdx/internal/plugin"
)

// startMCPServerTest starts a daemon with the default plugins and returns its
// base URL plus the dashboard token.
func startMCPServerTest(t *testing.T) (*Daemon, string, string) {
	t.Helper()
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
	t.Cleanup(d.Stop)

	baseURL := "http://" + d.HTTPAddr()
	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	token := tokenFromPage(t, string(body))
	if token == "" {
		t.Fatal("the served page does not carry the API token")
	}
	return d, baseURL, token
}

// mcpCall posts one JSON-RPC message to the MCP endpoint.
func mcpCall(t *testing.T, baseURL, token, body string) (int, http.Header, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("build request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp failed: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("POST /mcp returned invalid JSON: %s", raw)
		}
	}
	return resp.StatusCode, resp.Header, out
}

func mcpToolNames(result map[string]any) []string {
	tools, _ := result["tools"].([]any)
	names := make([]string, 0, len(tools))
	for _, item := range tools {
		tool, _ := item.(map[string]any)
		name, _ := tool["name"].(string)
		names = append(names, name)
	}
	return names
}

func TestMCPServerRequiresTokenAndEnable(t *testing.T) {
	_, baseURL, apiToken := startMCPServerTest(t)

	// Disabled by default: the endpoint must look absent.
	status, _, _ := mcpCall(t, baseURL, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if status != http.StatusNotFound {
		t.Fatalf("a disabled MCP server returned %d, want 404", status)
	}

	resp, err := apiPost(t, baseURL+"/api/core/plugins/mcp-server/settings", apiToken, `{"enabled":true}`)
	if err != nil {
		t.Fatalf("enable MCP server: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enabling the MCP server returned %d", resp.StatusCode)
	}

	status, header, _ := mcpCall(t, baseURL, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if status != http.StatusUnauthorized {
		t.Fatalf("an unauthenticated call returned %d, want 401", status)
	}
	if !strings.HasPrefix(header.Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("missing WWW-Authenticate header: %q", header.Get("WWW-Authenticate"))
	}

	status, _, _ = mcpCall(t, baseURL, "cdx_mcp_unknown_secret", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if status != http.StatusUnauthorized {
		t.Fatalf("an unknown token returned %d, want 401", status)
	}

	// The dashboard token must not open the MCP endpoint.
	status, _, _ = mcpCall(t, baseURL, apiToken, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if status != http.StatusUnauthorized {
		t.Fatalf("the dashboard token returned %d, want 401", status)
	}

	// The panel bundle is served next to the endpoint, so the settings page
	// renders the client list instead of an empty form.
	panel := apiGet(t, baseURL+"/ui/mcp-server/dist/panel.js", apiToken)
	panel.Body.Close()
}

func TestMCPServerHandshakeAndTools(t *testing.T) {
	_, baseURL, apiToken := startMCPServerTest(t)

	resp, err := apiPost(t, baseURL+"/api/core/plugins/mcp-server/settings", apiToken, `{"enabled":true}`)
	if err != nil {
		t.Fatalf("enable MCP server: %v", err)
	}
	resp.Body.Close()

	resp, err = apiPost(t, baseURL+"/api/plugins/mcp-server/clients", apiToken, `{"name":"test agent"}`)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	created, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("creating a client returned %d: %s", resp.StatusCode, created)
	}
	var client struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(created, &client); err != nil || client.Token == "" {
		t.Fatalf("no token in the create response: %s", created)
	}

	status, header, init := mcpCall(t, baseURL, client.Token,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if status != http.StatusOK {
		t.Fatalf("initialize returned %d", status)
	}
	result, _ := init["result"].(map[string]any)
	if result["protocolVersion"] != "2025-06-18" {
		t.Fatalf("unexpected protocol version: %v", result)
	}
	if header.Get("Mcp-Session-Id") == "" {
		t.Fatal("initialize did not return a session id")
	}
	serverInfo, _ := result["serverInfo"].(map[string]any)
	if serverInfo["name"] != "cdx" {
		t.Fatalf("unexpected server info: %v", serverInfo)
	}

	status, _, list := mcpCall(t, baseURL, client.Token, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if status != http.StatusOK {
		t.Fatalf("tools/list returned %d", status)
	}
	listResult, _ := list["result"].(map[string]any)
	names := mcpToolNames(listResult)
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "cdx_list_dir") || !strings.Contains(joined, "cdx_read_file") {
		t.Fatalf("read tools are missing: %v", names)
	}
	for _, hidden := range []string{"cdx_shell", "cdx_subagent", "cdx_ask_user", "mcp__"} {
		if strings.Contains(joined, hidden) {
			t.Fatalf("hidden tool %q is exposed: %v", hidden, names)
		}
	}
	if !strings.Contains(joined, "cdx_write_file") {
		t.Fatalf("a tool behind an approval must still be listed: %v", names)
	}
	// A client maps the annotations to its own permission classes; without
	// readOnlyHint a read tool would look like an edit and ask every time.
	readTool := findTool(listResult, "cdx_list_dir")
	if readTool == nil {
		t.Fatalf("cdx_list_dir is missing: %v", names)
	}
	annotations, _ := readTool["annotations"].(map[string]any)
	if readonly, _ := annotations["readOnlyHint"].(bool); !readonly {
		t.Fatalf("a read tool has no readOnlyHint: %v", readTool)
	}
	editTool := findTool(listResult, "cdx_write_file")
	annotations, _ = editTool["annotations"].(map[string]any)
	if readonly, _ := annotations["readOnlyHint"].(bool); readonly {
		t.Fatalf("an edit tool claims to be read-only: %v", editTool)
	}

	tmp := t.TempDir()
	body := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"cdx_list_dir","arguments":{"path":` + quoteJSON(tmp) + `}}}`
	status, _, call := mcpCall(t, baseURL, client.Token, body)
	if status != http.StatusOK {
		t.Fatalf("tools/call returned %d", status)
	}
	callResult, _ := call["result"].(map[string]any)
	if isErr, _ := callResult["isError"].(bool); isErr {
		t.Fatalf("list_dir failed: %v", callResult)
	}
	content, _ := callResult["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("tools/call returned no content: %v", callResult)
	}

	// An unknown tool is reported as a tool error, not as a transport error.
	status, _, unknown := mcpCall(t, baseURL, client.Token,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"cdx_missing","arguments":{}}}`)
	if status != http.StatusOK {
		t.Fatalf("an unknown tool returned %d", status)
	}
	unknownResult, _ := unknown["result"].(map[string]any)
	if isErr, _ := unknownResult["isError"].(bool); !isErr {
		t.Fatalf("an unknown tool is not marked as an error: %v", unknownResult)
	}

	// The daemon's own MCP client must connect to this server, which also
	// exercises the fallback from the modern discovery call to the handshake.
	clientConfig := `{"server":{"id":"self","name":"Self","transport":"http","url":` + quoteJSON(baseURL+"/mcp") +
		`,"headers":{"Authorization":"Bearer ` + client.Token + `"}}}`
	probeResp, err := apiPost(t, baseURL+"/api/plugins/mcp/servers/test", apiToken, clientConfig)
	if err != nil {
		t.Fatalf("probe the MCP server: %v", err)
	}
	probeBody, _ := io.ReadAll(probeResp.Body)
	probeResp.Body.Close()
	var probe struct {
		OK       bool   `json:"ok"`
		Tools    []any  `json:"tools"`
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(probeBody, &probe); err != nil {
		t.Fatalf("invalid probe response: %s", probeBody)
	}
	if !probe.OK || len(probe.Tools) == 0 {
		t.Fatalf("the MCP client could not use the server: %s", probeBody)
	}
	if probe.Revision != "2026-07-28" {
		t.Fatalf("the client did not negotiate the modern revision: %s", probeBody)
	}

	// Revoking the token closes the door.
	req, _ := http.NewRequest(http.MethodDelete, baseURL+"/api/plugins/mcp-server/clients/"+clientID(t, created), nil)
	req.Header.Set("X-CDX-Token", apiToken)
	revoked, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("revoke client: %v", err)
	}
	revoked.Body.Close()
	if revoked.StatusCode != http.StatusNoContent {
		t.Fatalf("revoking the client returned %d", revoked.StatusCode)
	}
	status, _, _ = mcpCall(t, baseURL, client.Token, `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`)
	if status != http.StatusUnauthorized {
		t.Fatalf("a revoked token returned %d, want 401", status)
	}
}

// clientID reads the id of a created client from the raw create response.
func clientID(t *testing.T, created []byte) string {
	t.Helper()
	var payload struct {
		Client struct {
			ID string `json:"id"`
		} `json:"client"`
	}
	if err := json.Unmarshal(created, &payload); err != nil || payload.Client.ID == "" {
		t.Fatalf("no client id in the create response: %s", created)
	}
	return payload.Client.ID
}

func TestMCPServerPermissionsAndApprovals(t *testing.T) {
	_, baseURL, apiToken := startMCPServerTest(t)

	resp, err := apiPost(t, baseURL+"/api/core/plugins/mcp-server/settings", apiToken, `{"enabled":true}`)
	if err != nil {
		t.Fatalf("enable MCP server: %v", err)
	}
	resp.Body.Close()

	resp, err = apiPost(t, baseURL+"/api/plugins/mcp-server/clients", apiToken, `{"name":"builder"}`)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	created, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var client struct {
		Token  string `json:"token"`
		Client struct {
			ID string `json:"id"`
		} `json:"client"`
	}
	if err := json.Unmarshal(created, &client); err != nil || client.Token == "" {
		t.Fatalf("no token in the create response: %s", created)
	}

	shellBody := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"cdx_shell","arguments":{"command":"echo remote-ok"}}}`

	// The default policy denies dangerous tools before the tool is even offered.
	status, _, denied := mcpCall(t, baseURL, client.Token, shellBody)
	if status != http.StatusOK {
		t.Fatalf("a denied call returned %d", status)
	}
	deniedResult, _ := denied["result"].(map[string]any)
	if isErr, _ := deniedResult["isError"].(bool); !isErr {
		t.Fatalf("a denied class is not reported as an error: %v", deniedResult)
	}

	// Let the daemon's own permission policy allow shell, then require an
	// approval in the MCP panel for the client.
	resp, err = apiPost(t, baseURL+"/api/plugins/permissions/config", apiToken, `{"dangerous_policy":"allow"}`)
	if err != nil {
		t.Fatalf("allow dangerous tools locally: %v", err)
	}
	resp.Body.Close()
	resp, err = apiPost(t, baseURL+"/api/plugins/mcp-server/clients/"+client.Client.ID+"/permissions", apiToken, `{"dangerous":"ask"}`)
	if err != nil {
		t.Fatalf("set client permissions: %v", err)
	}
	resp.Body.Close()

	status, _, asked := mcpCall(t, baseURL, client.Token, shellBody)
	if status != http.StatusOK {
		t.Fatalf("an approval request returned %d", status)
	}
	askedResult, _ := asked["result"].(map[string]any)
	askedText := firstText(askedResult)
	if isErr, _ := askedResult["isError"].(bool); !isErr || !strings.Contains(askedText, "approval required") {
		t.Fatalf("the call did not ask for an approval: %v", askedResult)
	}

	approvalID := pendingApprovalID(t, baseURL, apiToken)
	resp, err = apiPost(t, baseURL+"/api/plugins/mcp-server/approvals/"+approvalID+"/approve", apiToken, `{}`)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	resp.Body.Close()

	status, _, allowed := mcpCall(t, baseURL, client.Token, shellBody)
	if status != http.StatusOK {
		t.Fatalf("an approved call returned %d", status)
	}
	allowedResult, _ := allowed["result"].(map[string]any)
	if isErr, _ := allowedResult["isError"].(bool); isErr {
		t.Fatalf("the approved call failed: %v", allowedResult)
	}
	if !strings.Contains(firstText(allowedResult), "remote-ok") {
		t.Fatalf("unexpected shell output: %v", allowedResult)
	}

	// The approval is one shot: the same call asks again.
	_, _, again := mcpCall(t, baseURL, client.Token, shellBody)
	againResult, _ := again["result"].(map[string]any)
	if isErr, _ := againResult["isError"].(bool); !isErr {
		t.Fatalf("an approval was reused: %v", againResult)
	}

	// A dangerous class on allow is listed to the client.
	resp, err = apiPost(t, baseURL+"/api/plugins/mcp-server/clients/"+client.Client.ID+"/permissions", apiToken, `{"dangerous":"allow"}`)
	if err != nil {
		t.Fatalf("allow dangerous tools for the client: %v", err)
	}
	resp.Body.Close()
	_, _, list := mcpCall(t, baseURL, client.Token, `{"jsonrpc":"2.0","id":8,"method":"tools/list"}`)
	listResult, _ := list["result"].(map[string]any)
	if !strings.Contains(strings.Join(mcpToolNames(listResult), ","), "cdx_shell") {
		t.Fatalf("an allowed dangerous tool is missing: %v", mcpToolNames(listResult))
	}
}

// firstText reads the first text content block of a tool result.
func firstText(result map[string]any) string {
	content, _ := result["content"].([]any)
	for _, item := range content {
		block, _ := item.(map[string]any)
		if text, ok := block["text"].(string); ok {
			return text
		}
	}
	return ""
}

// pendingApprovalID returns the id of the only approval waiting for a decision.
func pendingApprovalID(t *testing.T, baseURL, apiToken string) string {
	t.Helper()
	resp := apiGet(t, baseURL+"/api/plugins/mcp-server/approvals", apiToken)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var payload struct {
		Approvals []struct {
			ID string `json:"id"`
		} `json:"approvals"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("invalid approvals response: %s", body)
	}
	if len(payload.Approvals) != 1 {
		t.Fatalf("expected one pending approval, got %s", body)
	}
	return payload.Approvals[0].ID
}

func TestMCPServerTaskLifecycle(t *testing.T) {
	d, baseURL, apiToken := startMCPServerTest(t)
	root := t.TempDir()

	settings := `{"enabled":true,"remote_root":` + quoteJSON(root) + `,"task_presets":"subagent"}`
	resp, err := apiPost(t, baseURL+"/api/core/plugins/mcp-server/settings", apiToken, settings)
	if err != nil {
		t.Fatalf("enable MCP server: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("saving settings returned %d", resp.StatusCode)
	}

	resp, err = apiPost(t, baseURL+"/api/plugins/mcp-server/clients", apiToken,
		`{"name":"tasker","permissions":{"read":"allow","edit":"allow","dangerous":"allow","task":"allow"}}`)
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

	// The local permission policy asks for dangerous tools, so the task parks
	// on a question instead of running the command.
	resp, err = apiPost(t, baseURL+"/api/plugins/permissions/config", apiToken, `{"dangerous_policy":"ask"}`)
	if err != nil {
		t.Fatalf("set the local policy: %v", err)
	}
	resp.Body.Close()

	step := 0
	d.registry.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock-task",
		ListModels: func(context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "mock-task/chat", Name: "mock-task/chat", Default: true}}, nil
		},
		Complete: func(_ context.Context, req plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			step++
			if step == 1 {
				return &plugin.CompleteResponse{
					Content: "running the command",
					ToolCalls: []map[string]any{{
						"id":        "call_1",
						"name":      "shell",
						"arguments": map[string]any{"command": "echo task-ok"},
					}},
				}, nil
			}
			last := ""
			if len(req.Messages) > 0 {
				if content, ok := req.Messages[len(req.Messages)-1]["content"].(string); ok {
					last = content
				}
			}
			return &plugin.CompleteResponse{Content: "done: " + last}, nil
		},
	})

	status, _, started := mcpCall(t, baseURL, client.Token,
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"cdx_task_start","arguments":{"prompt":"run the command","max_steps":4,"timeout_s":30}}}`)
	if status != http.StatusOK {
		t.Fatalf("task start returned %d", status)
	}
	startedResult, _ := started["result"].(map[string]any)
	startText := firstText(startedResult)
	// The prompt may be rejected before the turn starts.
	if isErr, _ := startedResult["isError"].(bool); isErr {
		t.Fatalf("task start failed: %s", startText)
	}
	taskID := regexp.MustCompile(`task (\S+) started`).FindStringSubmatch(startText)
	if len(taskID) != 2 {
		t.Fatalf("no task id in %q", startText)
	}

	report := waitTaskStatus(t, baseURL, client.Token, taskID[1], "needs_input")
	questionID := regexp.MustCompile(`question (\S+) \(`).FindStringSubmatch(report)
	if len(questionID) != 2 {
		t.Fatalf("no question id in %q", report)
	}

	// The waiting task keeps the slot: a second start must be refused.
	status, _, busy := mcpCall(t, baseURL, client.Token,
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"cdx_task_start","arguments":{"prompt":"second"}}}`)
	if status != http.StatusOK {
		t.Fatalf("the second start returned %d", status)
	}
	busyResult, _ := busy["result"].(map[string]any)
	if isErr, _ := busyResult["isError"].(bool); !isErr || !strings.Contains(firstText(busyResult), "task_slot_busy") {
		t.Fatalf("a second task was not refused: %v", busyResult)
	}

	answer := `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"cdx_task_answer","arguments":{"task_id":"` +
		taskID[1] + `","question_id":"` + questionID[1] + `","choice":"once"}}}`
	status, _, answered := mcpCall(t, baseURL, client.Token, answer)
	if status != http.StatusOK {
		t.Fatalf("task answer returned %d", status)
	}
	if isErr, _ := answered["result"].(map[string]any)["isError"].(bool); isErr {
		t.Fatalf("the answer was refused: %v", answered)
	}

	done := waitTaskStatus(t, baseURL, client.Token, taskID[1], "finished")
	if !strings.Contains(done, "task-ok") {
		t.Fatalf("the task result is missing the command output: %q", done)
	}
}

// waitTaskStatus polls cdx_task_get until the task reaches the wanted status.
func waitTaskStatus(t *testing.T, baseURL, token, taskID, want string) string {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"cdx_task_get","arguments":{"task_id":"` + taskID + `"}}}`
	for i := 0; i < 100; i++ {
		_, _, out := mcpCall(t, baseURL, token, body)
		result, _ := out["result"].(map[string]any)
		text := firstText(result)
		if strings.Contains(text, "task "+taskID+": "+want) {
			return text
		}
		if strings.Contains(text, ": failed") || strings.Contains(text, ": cancelled") {
			t.Fatalf("the task ended early: %q", text)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the task did not reach %q", want)
	return ""
}

func TestMCPServerModernRevision(t *testing.T) {
	_, baseURL, apiToken := startMCPServerTest(t)
	workdir := t.TempDir()

	resp, err := apiPost(t, baseURL+"/api/core/plugins/mcp-server/settings", apiToken, `{"enabled":true}`)
	if err != nil {
		t.Fatalf("enable MCP server: %v", err)
	}
	resp.Body.Close()

	resp, err = apiPost(t, baseURL+"/api/plugins/mcp-server/clients", apiToken,
		`{"name":"modern","permissions":{"read":"allow","edit":"ask"}}`)
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
	// The MCP layer approves the call, so the local policy must not ask again.
	resp, err = apiPost(t, baseURL+"/api/plugins/permissions/config", apiToken, `{"edit_policy":"allow"}`)
	if err != nil {
		t.Fatalf("allow edit tools locally: %v", err)
	}
	resp.Body.Close()

	// The server advertises the modern revision and reports its result kinds.
	status, _, discover := mcpCall(t, baseURL, client.Token,
		`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	if status != http.StatusOK {
		t.Fatalf("server/discover returned %d", status)
	}
	discoverResult, _ := discover["result"].(map[string]any)
	if discoverResult["resultType"] != "complete" {
		t.Fatalf("server/discover has no result kind: %v", discoverResult)
	}
	versions, _ := discoverResult["supportedVersions"].([]any)
	if !containsAny(versions, "2026-07-28") {
		t.Fatalf("the modern revision is not advertised: %v", versions)
	}

	status, _, list := mcpCall(t, baseURL, client.Token,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	if status != http.StatusOK {
		t.Fatalf("tools/list returned %d", status)
	}
	listResult, _ := list["result"].(map[string]any)
	if listResult["resultType"] != "complete" {
		t.Fatalf("tools/list has no result kind: %v", listResult)
	}

	// An edit tool asks in band instead of the panel.
	target := filepath.Join(workdir, "modern.txt")
	callBody := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"cdx_write_file","arguments":{"path":` +
		quoteJSON(target) + `,"content":"modern"},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`
	status, _, asked := mcpCall(t, baseURL, client.Token, callBody)
	if status != http.StatusOK {
		t.Fatalf("tools/call returned %d", status)
	}
	askedResult, _ := asked["result"].(map[string]any)
	if askedResult["resultType"] != "input_required" {
		t.Fatalf("the call did not ask for input: %v", askedResult)
	}
	state, _ := askedResult["requestState"].(string)
	if state == "" {
		t.Fatalf("no request state: %v", askedResult)
	}
	requests, _ := askedResult["inputRequests"].(map[string]any)
	if len(requests) != 1 {
		t.Fatalf("expected one input request, got %v", requests)
	}
	for key, raw := range requests {
		request, _ := raw.(map[string]any)
		if key != state || request["method"] != "elicitation/create" {
			t.Fatalf("unexpected input request: %v", requests)
		}
	}

	// The retry carries the answer and the call runs.
	retry := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"cdx_write_file","arguments":{"path":` +
		quoteJSON(target) + `,"content":"modern"},"requestState":` + quoteJSON(state) +
		`,"inputResponses":{` + quoteJSON(state) + `:{"action":"accept","content":{"approve":true}}},` +
		`"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`
	resp = nil
	status, _, allowed := mcpCall(t, baseURL, client.Token, retry)
	if status != http.StatusOK {
		t.Fatalf("the retry returned %d", status)
	}
	allowedResult, _ := allowed["result"].(map[string]any)
	if allowedResult["resultType"] != "complete" {
		t.Fatalf("the retry did not complete: %v", allowedResult)
	}
	if isErr, _ := allowedResult["isError"].(bool); isErr {
		t.Fatalf("the approved call failed: %v", allowedResult)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("the approved write did not happen: %v", err)
	}
}

// containsAny reports whether the list holds the wanted string.
func containsAny(list []any, want string) bool {
	for _, item := range list {
		if text, ok := item.(string); ok && text == want {
			return true
		}
	}
	return false
}

// findTool returns one tool entry of a tools/list result.
func findTool(result map[string]any, name string) map[string]any {
	tools, _ := result["tools"].([]any)
	for _, item := range tools {
		tool, _ := item.(map[string]any)
		if tool["name"] == name {
			return tool
		}
	}
	return nil
}
