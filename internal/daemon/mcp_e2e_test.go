package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yourok/cdx/internal/mcp"
	"github.com/yourok/cdx/internal/paths"
	"github.com/yourok/cdx/internal/plugin"
	"github.com/yourok/cdx/internal/session"
)

// TestMCPToolEndToEnd runs a full turn that calls a tool of an MCP server.
func TestMCPToolEndToEnd(t *testing.T) {
	var mu sync.Mutex
	var calls []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg map[string]any
		_ = json.Unmarshal(body, &msg)
		method, _ := msg["method"].(string)
		id := msg["id"]

		respond := func(result any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		}

		switch method {
		case "server/discover":
			respond(map[string]any{
				"resultType":        "complete",
				"supportedVersions": []string{mcp.RevisionModern},
				"capabilities":      map[string]any{"tools": map[string]any{}},
				"serverInfo":        map[string]any{"name": "e2e-mcp", "version": "1.0.0"},
			})
		case "tools/list":
			respond(map[string]any{
				"resultType": "complete",
				"tools": []any{map[string]any{
					"name":        "echo",
					"description": "Echo a value",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"value": map[string]any{"type": "string"}},
					},
					"annotations": map[string]any{"readOnlyHint": true},
				}},
			})
		case "tools/call":
			params, _ := msg["params"].(map[string]any)
			name, _ := params["name"].(string)
			args, _ := params["arguments"].(map[string]any)
			mu.Lock()
			calls = append(calls, name)
			mu.Unlock()
			respond(map[string]any{
				"resultType": "complete",
				"content":    []any{map[string]any{"type": "text", "text": "echo:" + toText(args["value"])}},
			})
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			})
		}
	}))
	defer server.Close()

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

	config := `{"servers":[{"id":"fake","name":"Fake","transport":"http","url":` + quoteJSON(server.URL) + `}]}`
	saveResp, err := apiPost(t, baseURL+"/api/plugins/mcp/servers", token, config)
	if err != nil {
		t.Fatalf("save MCP config: %v", err)
	}
	saveBody, _ := io.ReadAll(saveResp.Body)
	saveResp.Body.Close()
	if saveResp.StatusCode != http.StatusOK || !strings.Contains(string(saveBody), `"connected":true`) {
		t.Fatalf("unexpected save response: %d %s", saveResp.StatusCode, saveBody)
	}
	if _, ok := d.registry.GetTool("mcp_fake_echo"); !ok {
		t.Fatalf("the MCP tool is not registered: %+v", d.registry.ListTools())
	}

	step := 0
	d.registry.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock-e2e",
		ListModels: func(context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "mock-e2e/chat", Name: "mock-e2e/chat", Default: true}}, nil
		},
		Complete: func(_ context.Context, req plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			step++
			if step == 1 {
				return &plugin.CompleteResponse{
					Content: "calling the tool",
					ToolCalls: []map[string]any{{
						"id":        "call_1",
						"name":      "mcp_fake_echo",
						"arguments": map[string]any{"value": "hi"},
					}},
				}, nil
			}
			result := ""
			for _, message := range req.Messages {
				if message["role"] == "tool" {
					result = toText(message["content"])
				}
			}
			return &plugin.CompleteResponse{Content: "done: " + result}, nil
		},
	})

	workdir := filepath.Join(tmp, "work")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir workdir: %v", err)
	}
	turn, err := d.sessions.RunTurn(context.Background(), session.TurnRequest{
		Workdir: workdir,
		Mode:    session.ModeAuto,
		Prompt:  "call the echo tool",
	}, nil)
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if turn == nil || !strings.Contains(turn.Content, "echo:hi") {
		t.Fatalf("the tool result did not reach the model: %+v", turn)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || calls[0] != "echo" {
		t.Fatalf("unexpected MCP calls: %v", calls)
	}
}

// toText renders a value as a string.
func toText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

// quoteJSON encodes a string as a JSON literal.
func quoteJSON(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
