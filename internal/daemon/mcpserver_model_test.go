package daemon

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yourok/cdx/internal/session"
)

// TestMCPRealModelUsesTwoServers is a live check that a real model understands
// the two remote tool groups. It is skipped unless a provider is configured:
//
//	CDX_TEST_MODEL_BASE=https://host/v1 CDX_TEST_MODEL_KEY=... \
//	CDX_TEST_MODEL=<model id> go test ./internal/daemon -run TestMCPRealModel -v
//
// The provider must speak the OpenAI chat completions protocol and accept a
// bearer token, which is what the providers plugin sends.
func TestMCPRealModelUsesTwoServers(t *testing.T) {
	baseURL := strings.TrimRight(os.Getenv("CDX_TEST_MODEL_BASE"), "/")
	apiKey := os.Getenv("CDX_TEST_MODEL_KEY")
	model := os.Getenv("CDX_TEST_MODEL")
	if baseURL == "" || apiKey == "" || model == "" {
		t.Skip("set CDX_TEST_MODEL_BASE, CDX_TEST_MODEL_KEY, and CDX_TEST_MODEL to run against a real model")
	}

	_, baseA, apiA := startMCPServerTest(t)
	_, baseB, apiB := startMCPServerTest(t)
	client, baseC, apiC := startMCPServerTest(t)

	rootA := t.TempDir()
	rootB := t.TempDir()
	for name, root := range map[string]string{"alpha.txt": rootA, "beta.txt": rootB} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	enableMCPServer(t, baseA, apiA, "")
	enableMCPServer(t, baseB, apiB, "")
	allowLocalClass(t, baseA, apiA, `{"read_policy":"allow"}`)
	allowLocalClass(t, baseB, apiB, `{"read_policy":"allow"}`)
	// The turn runs without an interactive client, so its own permission layer
	// must not ask about the MCP calls.
	allowLocalClass(t, baseC, apiC, `{"read_policy":"allow","outside_policy":"allow"}`)
	tokenA := issueMCPClient(t, baseA, apiA, "model-a", `{"read":"allow"}`)
	tokenB := issueMCPClient(t, baseB, apiB, "model-b", `{"read":"allow"}`)

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

	provider := `{"name":"live","protocol":"openai-chat","base_url":` + quoteJSON(baseURL) +
		`,"api_key":` + quoteJSON(apiKey) + `}`
	resp, err = apiPost(t, baseC+"/api/plugins/providers/providers", apiC, provider)
	if err != nil {
		t.Fatalf("add the provider: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("adding the provider returned %d: %s", resp.StatusCode, body)
	}
	modelBody := `{"provider":"live","name":` + quoteJSON(model) + `,"remote":` + quoteJSON(model) +
		`,"context_window":32000,"default":true}`
	resp, err = apiPost(t, baseC+"/api/plugins/providers/models", apiC, modelBody)
	if err != nil {
		t.Fatalf("add the model: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("adding the model returned %d: %s", resp.StatusCode, body)
	}

	prompt := fmt.Sprintf(
		"Two remote servers are connected as MCP tools. Call mcp_a_cdx_list_dir for %s and mcp_b_cdx_list_dir for %s, then name the files each server returned.",
		rootA, rootB)
	result, err := client.sessions.RunTurn(context.Background(), session.TurnRequest{
		Workdir: t.TempDir(),
		Mode:    session.ModeAuto,
		Prompt:  prompt,
	}, nil)
	if err != nil {
		t.Fatalf("turn failed: %v", err)
	}

	messages, err := client.sessions.GetMessages(result.Session.ID)
	if err != nil {
		t.Fatalf("read the history: %v", err)
	}
	called := map[string]bool{}
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			name, _ := call["name"].(string)
			called[name] = true
		}
	}
	if !called["mcp_a_cdx_list_dir"] || !called["mcp_b_cdx_list_dir"] {
		t.Fatalf("the model did not use both servers (called: %v, answer: %q)", called, result.Content)
	}
	t.Logf("answer: %s", result.Content)
	if !strings.Contains(result.Content, "alpha.txt") && !strings.Contains(result.Content, "beta.txt") {
		t.Fatalf("the answer does not mention what the servers returned: %q", result.Content)
	}
}
