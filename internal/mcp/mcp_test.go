package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestFakeServerProcess serves as the MCP server subprocess for the tests.
func TestFakeServerProcess(t *testing.T) {
	if os.Getenv("CDX_MCP_FAKE") != "1" {
		t.Skip("helper process for the mcp tests")
	}
	serveFakeServer(os.Getenv("CDX_MCP_FAKE_MODE"))
	os.Exit(0)
}

// serveFakeServer answers JSON-RPC requests for the legacy and modern revisions.
func serveFakeServer(mode string) {
	if mode == "exit" {
		fmt.Fprintln(os.Stderr, "Error: None of the specified directories are accessible")
		os.Exit(1)
	}
	var writeMu sync.Mutex
	send := func(value any) {
		data, err := json.Marshal(value)
		if err != nil {
			return
		}
		writeMu.Lock()
		_, _ = os.Stdout.Write(append(data, '\n'))
		writeMu.Unlock()
	}
	result := func(id any, payload any) {
		send(map[string]any{"jsonrpc": "2.0", "id": id, "result": payload})
	}
	rpcErr := func(id any, code int, message string) {
		send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for scanner.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		method, _ := msg["method"].(string)
		id, hasID := msg["id"]
		if !hasID {
			continue
		}
		go func(id any, method string) {
			params, _ := msg["params"].(map[string]any)
			switch method {
			case "initialize":
				result(id, map[string]any{
					"protocolVersion": "2025-06-18",
					"capabilities": map[string]any{
						"tools": map[string]any{"listChanged": true},
						"roots": map[string]any{},
					},
					"serverInfo": map[string]any{"name": "fake-legacy", "version": "1.0.0"},
				})
			case "server/discover":
				if mode != "modern" {
					rpcErr(id, -32601, "Method not found")
					return
				}
				meta, _ := params["_meta"].(map[string]any)
				if meta["io.modelcontextprotocol/protocolVersion"] != RevisionModern {
					rpcErr(id, -32600, "missing protocol metadata")
					return
				}
				result(id, map[string]any{
					"resultType":        "complete",
					"supportedVersions": []string{RevisionModern},
					"capabilities":      map[string]any{"tools": map[string]any{}},
					"serverInfo":        map[string]any{"name": "fake-modern", "version": "2.0.0"},
				})
			case "tools/list":
				if mode == "modern" {
					meta, _ := params["_meta"].(map[string]any)
					if meta["io.modelcontextprotocol/protocolVersion"] != RevisionModern {
						rpcErr(id, -32600, "missing protocol metadata")
						return
					}
				}
				cursor, _ := params["cursor"].(string)
				payload := map[string]any{"resultType": "complete"}
				if cursor == "" {
					payload["tools"] = []any{tool("echo"), tool("slow"), tool("fail"), tool("crash")}
					payload["nextCursor"] = "page2"
				} else {
					payload["tools"] = []any{tool("second-page")}
				}
				result(id, payload)
				if cursor == "" {
					send(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"})
				}
			case "tools/call":
				name, _ := params["name"].(string)
				args, _ := params["arguments"].(map[string]any)
				switch name {
				case "echo":
					result(id, map[string]any{
						"resultType":        "complete",
						"content":           []any{map[string]any{"type": "text", "text": "echo:" + fmt.Sprint(args["value"])}},
						"structuredContent": map[string]any{"value": args["value"]},
					})
				case "fail":
					result(id, map[string]any{
						"resultType": "complete",
						"content":    []any{map[string]any{"type": "text", "text": "boom"}},
						"isError":    true,
					})
				case "slow":
					time.Sleep(time.Second)
					result(id, map[string]any{
						"resultType": "complete",
						"content":    []any{map[string]any{"type": "text", "text": "slow"}},
					})
				case "crash":
					os.Exit(3)
				default:
					rpcErr(id, -32602, "unknown tool "+name)
				}
			default:
				rpcErr(id, -32601, "Method not found")
			}
		}(id, method)
	}
}

// tool builds a tool description for the fake server.
func tool(name string) map[string]any {
	return map[string]any{
		"name":        name,
		"description": "Fake tool " + name,
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

// fakeConfig points a connection at the test binary running as a fake server.
func fakeConfig(t *testing.T, mode string) Config {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	return Config{
		ID:        "fake",
		Name:      "Fake",
		Transport: TransportStdio,
		Command:   executable,
		Args:      []string{"-test.run=^TestFakeServerProcess$"},
		Env:       map[string]string{"CDX_MCP_FAKE": "1", "CDX_MCP_FAKE_MODE": mode},
		Dir:       t.TempDir(),
		Root:      t.TempDir(),
		Timeout:   3 * time.Second,
	}
}

// dialFake opens a client against the fake server and closes it after the test.
func dialFake(t *testing.T, mode string) *Client {
	t.Helper()
	client, err := Dial(context.Background(), fakeConfig(t, mode))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestLegacyDiscovery covers the handshake, pagination, notifications, and tool calls.
func TestLegacyDiscovery(t *testing.T) {
	client := dialFake(t, "legacy")
	ctx := context.Background()

	info := client.Info()
	if info.Name != "fake-legacy" || info.Version != "1.0.0" {
		t.Fatalf("unexpected server info: %+v", info)
	}
	if client.Revision() != RevisionLegacy {
		t.Fatalf("unexpected revision: %q", client.Revision())
	}
	if !client.HasCapability("tools") {
		t.Fatalf("tools capability is missing: %+v", client.Capabilities())
	}

	notified := make(chan string, 1)
	client.SetNotificationHandler(func(method string, _ json.RawMessage) { notified <- method })

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 5 {
		t.Fatalf("expected 5 tools across two pages, got %d", len(tools))
	}

	select {
	case method := <-notified:
		if method != "notifications/tools/list_changed" {
			t.Fatalf("unexpected notification: %q", method)
		}
	case <-time.After(time.Second):
		t.Fatal("the list_changed notification was not delivered")
	}

	res, err := client.CallTool(ctx, "echo", map[string]any{"value": "hi"})
	if err != nil {
		t.Fatalf("call echo: %v", err)
	}
	if len(res.Content) != 1 || res.Content[0].Text != "echo:hi" {
		t.Fatalf("unexpected echo content: %+v", res.Content)
	}
	if res.StructuredContent["value"] != "hi" {
		t.Fatalf("unexpected structured content: %+v", res.StructuredContent)
	}

	res, err = client.CallTool(ctx, "fail", nil)
	if err != nil {
		t.Fatalf("call fail: %v", err)
	}
	if !res.IsError || len(res.Content) != 1 || res.Content[0].Text != "boom" {
		t.Fatalf("unexpected failure result: %+v", res)
	}
}

// TestModernDiscovery covers per-request metadata and the modern revision.
func TestModernDiscovery(t *testing.T) {
	client := dialFake(t, "modern")
	ctx := context.Background()

	info := client.Info()
	if info.Name != "fake-modern" || info.Version != "2.0.0" {
		t.Fatalf("unexpected server info: %+v", info)
	}
	if client.Revision() != RevisionModern {
		t.Fatalf("unexpected revision: %q", client.Revision())
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 5 {
		t.Fatalf("expected 5 tools, got %d", len(tools))
	}

	res, err := client.CallTool(ctx, "echo", map[string]any{"value": "modern"})
	if err != nil {
		t.Fatalf("call echo: %v", err)
	}
	if res.Content[0].Text != "echo:modern" {
		t.Fatalf("unexpected content: %+v", res.Content)
	}
}

// TestForcedRevision skips detection when the configuration names a revision.
func TestForcedRevision(t *testing.T) {
	cfg := fakeConfig(t, "legacy")
	cfg.Protocol = RevisionLegacy
	client, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()
	if client.Info().Name != "fake-legacy" {
		t.Fatalf("unexpected server info: %+v", client.Info())
	}
}

// TestConcurrentCalls checks that replies reach the right caller.
func TestConcurrentCalls(t *testing.T) {
	client := dialFake(t, "legacy")
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(value int) {
			defer wg.Done()
			res, err := client.CallTool(ctx, "echo", map[string]any{"value": value})
			if err != nil {
				errs <- err
				return
			}
			want := fmt.Sprintf("echo:%d", value)
			if len(res.Content) != 1 || res.Content[0].Text != want {
				errs <- fmt.Errorf("value %d got %q", value, res.Content[0].Text)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// TestRequestTimeout checks the per-request deadline.
func TestRequestTimeout(t *testing.T) {
	cfg := fakeConfig(t, "legacy")
	cfg.Timeout = 150 * time.Millisecond
	client, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	start := time.Now()
	if _, err := client.CallTool(context.Background(), "slow", nil); err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
}

// TestCallCancellation checks that a cancelled turn aborts the request.
func TestCallCancellation(t *testing.T) {
	client := dialFake(t, "legacy")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if _, err := client.CallTool(ctx, "slow", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected a deadline error, got %v", err)
	}
}

// TestServerCrash checks that a dead server is reported instead of hanging.
func TestServerCrash(t *testing.T) {
	client := dialFake(t, "legacy")
	ctx := context.Background()

	if _, err := client.CallTool(ctx, "crash", nil); err == nil {
		t.Fatal("expected an error from the crashed server")
	}
	for i := 0; i < 100 && !client.IsClosed(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if !client.IsClosed() {
		t.Fatal("the client did not notice the server exit")
	}
	if _, err := client.ListTools(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected a closed connection, got %v", err)
	}
}

// TestManagerLifecycle checks connection ownership.
func TestManagerLifecycle(t *testing.T) {
	manager := NewManager()
	defer manager.CloseAll()

	client, err := manager.Connect(context.Background(), "plugin:mcp", fakeConfig(t, "legacy"))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if client.IsClosed() {
		t.Fatal("the connection is closed")
	}
	if _, ok := manager.Get("plugin:mcp", "fake"); !ok {
		t.Fatal("the connection is not registered")
	}
	if _, ok := manager.Get("plugin:other", "fake"); ok {
		t.Fatal("a connection leaked to another plugin")
	}
	if ids := manager.IDs("plugin:mcp"); len(ids) != 1 || ids[0] != "fake" {
		t.Fatalf("unexpected ids: %v", ids)
	}
	if manager.CloseOwner("plugin:mcp") != 1 {
		t.Fatal("expected exactly one closed connection")
	}
	if _, ok := manager.Get("plugin:mcp", "fake"); ok {
		t.Fatal("the connection is still registered")
	}
}

// TestMergedEnv checks environment inheritance and overrides.
func TestMergedEnv(t *testing.T) {
	env := mergedEnv(map[string]string{"CDX_MCP_TEST": "yes", "PATH": "/custom"}, "/tmp")
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "CDX_MCP_TEST=yes") {
		t.Fatal("the configured value is missing")
	}
	if !strings.Contains(joined, "PATH=/custom") {
		t.Fatal("the override is missing")
	}
	if !strings.Contains(joined, "PWD=/tmp") {
		t.Fatal("the working directory is missing")
	}
}

// TestServerExitsBeforeInitialize covers the diagnostic of a server that exits at startup.
func TestServerExitsBeforeInitialize(t *testing.T) {
	_, err := Dial(context.Background(), fakeConfig(t, "exit"))
	if err == nil {
		t.Fatal("expected an error from the exiting server")
	}
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("expected a closed connection, got %v", err)
	}
	if !strings.Contains(err.Error(), "None of the specified directories are accessible") {
		t.Fatalf("the server stderr was not reported: %v", err)
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("the exit status was not reported: %v", err)
	}
}

// TestRootsResponse covers the roots reply, which must be an array.
func TestRootsResponse(t *testing.T) {
	empty := &Client{cfg: Config{}}
	if roots := empty.roots(); roots == nil || len(roots) != 0 {
		t.Fatalf("empty roots must be an empty slice: %#v", roots)
	}

	client := &Client{cfg: Config{Root: "/home/user/project"}}
	roots := client.roots()
	if len(roots) != 1 || roots[0]["uri"] != "file:///home/user/project" || roots[0]["name"] != "project" {
		t.Fatalf("unexpected roots: %+v", roots)
	}
}
