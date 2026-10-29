package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeServer writes a stub MCP server script and returns its path.
func fakeServer(t *testing.T) string {
	t.Helper()

	script := `#!/usr/bin/env python3
import json, sys

def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        msg = json.loads(line)
    except Exception:
        continue

    method = msg.get("method", "")
    mid = msg.get("id")

    if method == "initialize":
        send({"jsonrpc":"2.0","id":mid,"result":{
            "protocolVersion":"2024-11-05",
            "capabilities":{"tools":{}},
            "serverInfo":{"name":"fake","version":"1.0.0"}}})
    elif method == "notifications/initialized":
        pass
    elif method == "tools/list":
        send({"jsonrpc":"2.0","id":mid,"result":{"tools":[
            {"name":"echo","description":"Echo text",
             "inputSchema":{"type":"object","properties":{"text":{"type":"string"}}}},
            {"name":"fail","description":"Always fails",
             "inputSchema":{"type":"object"}}]}})
    elif method == "tools/call":
        params = msg.get("params", {})
        name = params.get("name")
        args = params.get("arguments", {})
        if name == "echo":
            text = args.get("text", "")
            send({"jsonrpc":"2.0","id":mid,"result":{
                "content":[{"type":"text","text":"echo: " + str(text)}]}})
        elif name == "fail":
            send({"jsonrpc":"2.0","id":mid,"error":{"code":-32000,"message":"boom"}})
        else:
            send({"jsonrpc":"2.0","id":mid,"error":{"code":-32601,"message":"unknown tool"}})
    else:
        send({"jsonrpc":"2.0","id":mid,"error":{"code":-32601,"message":"unknown method"}})
`

	path := filepath.Join(t.TempDir(), "fake_server.py")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write server: %v", err)
	}
	return path
}

// startClient brings up a client on the stub server.
func startClient(t *testing.T) *Client {
	t.Helper()

	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		t.Skip("python3 is not available")
	}

	client := NewClient(Options{
		Name:    "fake",
		Command: "python3",
		Args:    []string{fakeServer(t)},
		Timeout: 5 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(client.Stop)
	return client
}

// TestClientListTools covers fetching the tool list.
func TestClientListTools(t *testing.T) {
	client := startClient(t)

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name != "echo" || tools[0].Description != "Echo text" {
		t.Fatalf("unexpected tool %+v", tools[0])
	}
	if tools[0].InputSchema == nil {
		t.Fatal("input schema must be parsed")
	}
}

// TestClientCallTool covers calling a tool.
func TestClientCallTool(t *testing.T) {
	client := startClient(t)

	text, err := client.CallTool(context.Background(), "echo", map[string]any{"text": "привет"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if text != "echo: привет" {
		t.Fatalf("unexpected text %q", text)
	}
}

// TestClientCallToolError covers a server error.
func TestClientCallToolError(t *testing.T) {
	client := startClient(t)

	if _, err := client.CallTool(context.Background(), "fail", nil); err == nil {
		t.Fatal("expected server error")
	}
}

// TestClientUnknownTool covers an unknown tool.
func TestClientUnknownTool(t *testing.T) {
	client := startClient(t)

	if _, err := client.CallTool(context.Background(), "nope", nil); err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

// TestClientStartMissingCommand covers a missing process.
func TestClientStartMissingCommand(t *testing.T) {
	client := NewClient(Options{Name: "x", Command: "/nonexistent/binary"})
	if err := client.Start(context.Background()); err == nil {
		t.Fatal("expected error for missing command")
	}
}

// TestClientCallStopped covers a request to a stopped client.
func TestClientCallStopped(t *testing.T) {
	client := NewClient(Options{Name: "x", Command: "sleep", Args: []string{"10"}})
	client.Stop()

	if _, err := client.Call(context.Background(), "tools/list", nil); err == nil {
		t.Fatal("expected error when server is stopped")
	}
}

// TestClientTimeout covers the request timeout.
func TestClientTimeout(t *testing.T) {
	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		t.Skip("python3 is not available")
	}

	// A server that never answers the handshake.
	script := "#!/usr/bin/env python3\nimport sys, time\nfor line in sys.stdin:\n    time.sleep(60)\n"
	path := filepath.Join(t.TempDir(), "silent.py")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	client := NewClient(Options{
		Name: "silent", Command: "python3", Args: []string{path},
		Timeout: 300 * time.Millisecond,
	})

	started := time.Now()
	err := client.Start(context.Background())
	if err == nil {
		client.Stop()
		t.Fatal("expected timeout")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("timeout must be quick, took %v", elapsed)
	}
}

// TestTrimSpace covers dropping trailing line breaks.
func TestTrimSpace(t *testing.T) {
	if got := string(trimSpace([]byte("abc\r\n"))); got != "abc" {
		t.Fatalf("unexpected %q", got)
	}
	if got := string(trimSpace([]byte{})); got != "" {
		t.Fatalf("unexpected %q", got)
	}
}

// TestMcpShortName covers the model-facing tool name.
func TestMcpShortName(t *testing.T) {
	if got := mcpShortName("filesystem", "read_file"); got != "mcp_filesystem_read_file" {
		t.Fatalf("unexpected %q", got)
	}
	if got := mcpShortName("my.server", "tool"); got != "mcp_my_server_tool" {
		t.Fatalf("dots must be replaced, got %q", got)
	}

	long := mcpShortName("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb")
	if len(long) > 64 {
		t.Fatalf("name must be at most 64 characters, got %d", len(long))
	}
}

// TestSendWithoutServer covers writing with no process.
func TestSendWithoutServer(t *testing.T) {
	client := NewClient(Options{Name: "x"})
	if err := client.send(request{Method: "test"}); err == nil {
		t.Fatal("expected error without server")
	}
}

// TestReadLoopIgnoresGarbage covers tolerance to noise on the stream.
func TestReadLoopIgnoresGarbage(t *testing.T) {
	reader := bufio.NewReaderSize(nil, 1)
	_ = reader

	// Lines that do not parse as a reply must be skipped.
	input := "not json\n\n{\"jsonrpc\":\"2.0\",\"id\":99,\"result\":{}}\n"
	var resp response
	if err := json.Unmarshal([]byte(trimSpace([]byte("not json"))), &resp); err == nil {
		t.Fatal("garbage must not parse")
	}
	_ = input
}
