package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeRPCResult answers one request with a JSON result.
func writeRPCResult(w http.ResponseWriter, id any, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

// writeRPCError answers one request with an HTTP status and a JSON-RPC error.
func writeRPCError(w http.ResponseWriter, status int, id any, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
}

// decodeRPC reads one JSON-RPC frame from a request body.
func decodeRPC(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("read body: %v", err)
		return map[string]any{}
	}
	var msg map[string]any
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Errorf("decode body: %v", err)
		return map[string]any{}
	}
	return msg
}

// modernTools returns the catalog used by the HTTP tests.
func modernTools() []any {
	return []any{
		map[string]any{
			"name":        "echo",
			"description": "Echo a value",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"region": map[string]any{"type": "string", "x-mcp-header": "Region"},
					"value":  map[string]any{"type": "string"},
				},
			},
			"annotations": map[string]any{"readOnlyHint": true},
		},
		map[string]any{
			"name":        "bad",
			"description": "Invalid header annotation",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"items": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string", "x-mcp-header": "Item"},
					},
				},
			},
		},
	}
}

// TestHTTPModernJSON covers discovery, headers, tool listing, and calls over JSON.
func TestHTTPModernJSON(t *testing.T) {
	var mu sync.Mutex
	var seen []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("MCP-Protocol-Version") != RevisionModern {
			t.Errorf("missing protocol version header: %v", r.Header)
		}
		msg := decodeRPC(t, r)
		method, _ := msg["method"].(string)
		id := msg["id"]

		mu.Lock()
		seen = append(seen, method)
		mu.Unlock()

		if r.Header.Get("Mcp-Method") != method {
			t.Errorf("Mcp-Method %q does not match %q", r.Header.Get("Mcp-Method"), method)
		}

		switch method {
		case "server/discover":
			writeRPCResult(w, id, map[string]any{
				"resultType":        "complete",
				"supportedVersions": []string{RevisionModern},
				"capabilities":      map[string]any{"tools": map[string]any{}},
				"serverInfo":        map[string]any{"name": "http-modern", "version": "1.0.0"},
			})
		case "tools/list":
			writeRPCResult(w, id, map[string]any{"resultType": "complete", "tools": modernTools()})
		case "tools/call":
			if r.Header.Get("Mcp-Name") != "echo" {
				t.Errorf("Mcp-Name header: %q", r.Header.Get("Mcp-Name"))
			}
			if value := r.Header.Get("Mcp-Param-Region"); value != "us-west1" {
				t.Errorf("Mcp-Param-Region header: %q", value)
			}
			writeRPCResult(w, id, map[string]any{
				"resultType": "complete",
				"content":    []any{map[string]any{"type": "text", "text": "ok"}},
			})
		default:
			writeRPCError(w, http.StatusNotFound, id, -32601, "Method not found")
		}
	}))
	defer srv.Close()

	client, err := Dial(context.Background(), Config{
		ID:        "http",
		Transport: TransportHTTP,
		URL:       srv.URL,
		Timeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	if client.Revision() != RevisionModern || client.Info().Name != "http-modern" {
		t.Fatalf("unexpected discovery: %+v", client.Info())
	}

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("the invalid tool was not excluded: %+v", tools)
	}

	res, err := client.CallTool(context.Background(), "echo", map[string]any{"region": "us-west1", "value": "hi"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if len(res.Content) != 1 || res.Content[0].Text != "ok" {
		t.Fatalf("unexpected content: %+v", res.Content)
	}

	mu.Lock()
	defer mu.Unlock()
	if strings.Join(seen, ",") != "server/discover,tools/list,tools/call" {
		t.Fatalf("unexpected request order: %v", seen)
	}
}

// TestHTTPSSE covers an event stream response with a progress notification.
func TestHTTPSSE(t *testing.T) {
	var mu sync.Mutex
	calls := 0

	done := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msg := decodeRPC(t, r)
		method, _ := msg["method"].(string)
		id := msg["id"]

		switch method {
		case "server/discover":
			writeRPCResult(w, id, map[string]any{
				"resultType":        "complete",
				"supportedVersions": []string{RevisionModern},
				"capabilities":      map[string]any{"tools": map[string]any{}},
				"serverInfo":        map[string]any{"name": "http-sse", "version": "1.0.0"},
			})
		case "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher, _ := w.(http.Flusher)
			progress, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"method":  "notifications/progress",
				"params":  map[string]any{"progress": 1, "total": 2},
			})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", progress)
			if flusher != nil {
				flusher.Flush()
			}
			result, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"resultType": "complete",
					"content":    []any{map[string]any{"type": "text", "text": "streamed"}},
				},
			})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", result)
			if flusher != nil {
				flusher.Flush()
			}
			mu.Lock()
			calls++
			mu.Unlock()
			done <- struct{}{}
		default:
			writeRPCError(w, http.StatusNotFound, id, -32601, "Method not found")
		}
	}))
	defer srv.Close()

	client, err := Dial(context.Background(), Config{
		ID:        "http",
		Transport: TransportHTTP,
		URL:       srv.URL,
		Timeout:   3 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	notified := make(chan string, 1)
	client.SetNotificationHandler(func(method string, _ json.RawMessage) {
		notified <- method
	})

	res, err := client.CallTool(context.Background(), "echo", map[string]any{"value": "hi"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if len(res.Content) != 1 || res.Content[0].Text != "streamed" {
		t.Fatalf("unexpected content: %+v", res.Content)
	}
	select {
	case method := <-notified:
		if method != "notifications/progress" {
			t.Fatalf("unexpected notification: %q", method)
		}
	case <-time.After(time.Second):
		t.Fatal("the progress notification was not delivered")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the tool call did not finish")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("unexpected call count: %d", calls)
	}
}

// TestHTTPLegacySession covers the initialize handshake and the session header.
func TestHTTPLegacySession(t *testing.T) {
	const session = "session-42"
	var mu sync.Mutex
	initialized := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusOK)
			return
		}
		msg := decodeRPC(t, r)
		method, _ := msg["method"].(string)
		id := msg["id"]

		switch method {
		case "server/discover":
			writeRPCError(w, http.StatusNotFound, id, -32601, "Method not found")
		case "initialize":
			w.Header().Set("Mcp-Session-Id", session)
			writeRPCResult(w, id, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "http-legacy", "version": "1.0.0"},
			})
			mu.Lock()
			initialized = true
			mu.Unlock()
		case "notifications/initialized":
			if r.Header.Get("Mcp-Session-Id") != session {
				t.Errorf("initialized without the session header: %v", r.Header)
			}
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if got := r.Header.Get("Mcp-Session-Id"); got != session {
				t.Errorf("tools/list session header: %q", got)
			}
			writeRPCResult(w, id, map[string]any{"tools": []any{
				map[string]any{"name": "legacy", "inputSchema": map[string]any{"type": "object"}},
			}})
		default:
			writeRPCError(w, http.StatusNotFound, id, -32601, "Method not found")
		}
	}))
	defer srv.Close()

	client, err := Dial(context.Background(), Config{
		ID:        "http",
		Transport: TransportHTTP,
		URL:       srv.URL,
		Timeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	mu.Lock()
	ok := initialized
	mu.Unlock()
	if !ok || client.Revision() != RevisionLegacy {
		t.Fatalf("unexpected discovery: %+v", client.Info())
	}
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "legacy" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

// TestHTTPLegacySSETransport covers the deprecated 2024-11-05 transport.
func TestHTTPLegacySSETransport(t *testing.T) {
	var (
		mu       sync.Mutex
		stream   io.Writer
		flusher  http.Flusher
		streamCh = make(chan struct{})
		closed   = make(chan struct{})
	)
	var closeOne sync.Once

	send := func(value any) {
		data, err := json.Marshal(value)
		if err != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if stream == nil {
			return
		}
		_, _ = fmt.Fprintf(stream, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			mu.Lock()
			flusher, _ = w.(http.Flusher)
			stream = w
			_, _ = fmt.Fprint(w, "event: endpoint\ndata: /message\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			mu.Unlock()
			close(streamCh)
			<-closed
		case r.Method == http.MethodPost && r.URL.Path == "/sse":
			w.WriteHeader(http.StatusMethodNotAllowed)
		case r.Method == http.MethodPost && r.URL.Path == "/message":
			msg := decodeRPC(t, r)
			method, _ := msg["method"].(string)
			id := msg["id"]
			if id == nil {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			switch method {
			case "initialize":
				send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "http-old", "version": "0.1.0"},
				}})
			case "tools/list":
				send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"tools": []any{
					map[string]any{"name": "old", "inputSchema": map[string]any{"type": "object"}},
				}}})
			case "tools/call":
				send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
					"content": []any{map[string]any{"type": "text", "text": "old-ok"}},
				}})
			default:
				send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32601, "message": "Method not found"}})
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer func() {
		closeOne.Do(func() { close(closed) })
		srv.Close()
	}()

	client, err := Dial(context.Background(), Config{
		ID:        "http",
		Transport: TransportHTTP,
		URL:       srv.URL + "/sse",
		Timeout:   3 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	if client.Info().Name != "http-old" {
		t.Fatalf("unexpected discovery: %+v", client.Info())
	}
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "old" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
	res, err := client.CallTool(context.Background(), "old", nil)
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if len(res.Content) != 1 || res.Content[0].Text != "old-ok" {
		t.Fatalf("unexpected content: %+v", res.Content)
	}

	select {
	case <-streamCh:
	default:
		t.Fatal("the event stream was not opened")
	}
}

// TestHTTPInputRequired covers the multi round-trip retry with request state.
func TestHTTPInputRequired(t *testing.T) {
	state := ""
	var rounds int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msg := decodeRPC(t, r)
		method, _ := msg["method"].(string)
		id := msg["id"]
		params, _ := msg["params"].(map[string]any)

		switch method {
		case "server/discover":
			writeRPCResult(w, id, map[string]any{
				"resultType":        "complete",
				"supportedVersions": []string{RevisionModern},
				"capabilities":      map[string]any{"tools": map[string]any{}},
				"serverInfo":        map[string]any{"name": "mrtr", "version": "1.0.0"},
			})
		case "tools/list":
			writeRPCResult(w, id, map[string]any{"resultType": "complete", "tools": []any{
				map[string]any{"name": "login", "inputSchema": map[string]any{"type": "object"}},
			}})
		case "tools/call":
			if _, ok := params["inputResponses"]; !ok {
				state = "state-1"
				writeRPCResult(w, id, map[string]any{
					"resultType":   "input_required",
					"requestState": state,
					"inputRequests": map[string]any{
						"login": map[string]any{
							"method": "elicitation/create",
							"params": map[string]any{
								"mode":    "form",
								"message": "GitHub user name",
							},
						},
					},
				})
				return
			}
			rounds++
			if params["requestState"] != state {
				t.Errorf("request state was not echoed: %v", params["requestState"])
			}
			responses, _ := params["inputResponses"].(map[string]any)
			answer, _ := responses["login"].(map[string]any)
			if answer["action"] != "accept" {
				t.Errorf("unexpected response: %v", answer)
			}
			writeRPCResult(w, id, map[string]any{
				"resultType": "complete",
				"content":    []any{map[string]any{"type": "text", "text": "welcome"}},
			})
		default:
			writeRPCError(w, http.StatusNotFound, id, -32601, "Method not found")
		}
	}))
	defer srv.Close()

	client, err := Dial(context.Background(), Config{
		ID:        "http",
		Transport: TransportHTTP,
		URL:       srv.URL,
		Timeout:   3 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	client.SetInputHandler(func(ctx context.Context, requests map[string]InputRequest) (map[string]any, error) {
		if _, ok := requests["login"]; !ok {
			return nil, fmt.Errorf("login request is missing")
		}
		return map[string]any{"login": map[string]any{"action": "accept", "content": map[string]any{"name": "octocat"}}}, nil
	})

	res, err := client.CallTool(context.Background(), "login", nil)
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if len(res.Content) != 1 || res.Content[0].Text != "welcome" {
		t.Fatalf("unexpected content: %+v", res.Content)
	}
	if rounds != 1 {
		t.Fatalf("unexpected retry count: %d", rounds)
	}
}

// TestHTTPUnsupportedVersionFallsBack checks fallback when discovery is rejected.
func TestHTTPUnsupportedVersionFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msg := decodeRPC(t, r)
		method, _ := msg["method"].(string)
		id := msg["id"]
		switch method {
		case "server/discover":
			writeRPCError(w, http.StatusBadRequest, id, -32000, "Unsupported protocol version")
		case "initialize":
			writeRPCResult(w, id, map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fallback", "version": "1.0.0"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			writeRPCError(w, http.StatusNotFound, id, -32601, "Method not found")
		}
	}))
	defer srv.Close()

	client, err := Dial(context.Background(), Config{
		ID:        "http",
		Transport: TransportHTTP,
		URL:       srv.URL,
		Timeout:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()
	if client.Info().Name != "fallback" || client.Revision() != RevisionLegacy {
		t.Fatalf("unexpected discovery: %+v", client.Info())
	}
}

// TestHTTPSubscribe covers the modern subscriptions/listen stream.
func TestHTTPSubscribe(t *testing.T) {
	var mu sync.Mutex
	requested := map[string]any{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msg := decodeRPC(t, r)
		method, _ := msg["method"].(string)
		id := msg["id"]

		switch method {
		case "server/discover":
			writeRPCResult(w, id, map[string]any{
				"resultType":        "complete",
				"supportedVersions": []string{RevisionModern},
				"capabilities":      map[string]any{"tools": map[string]any{}},
				"serverInfo":        map[string]any{"name": "listen", "version": "1.0.0"},
			})
		case "subscriptions/listen":
			params, _ := msg["params"].(map[string]any)
			notifications, _ := params["notifications"].(map[string]any)
			mu.Lock()
			requested = notifications
			mu.Unlock()

			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher, _ := w.(http.Flusher)
			ack, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"method":  "notifications/subscriptions/acknowledged",
				"params":  map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/subscriptionId": id}},
			})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", ack)
			changed, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"method":  "notifications/tools/list_changed",
				"params":  map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/subscriptionId": id}},
			})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", changed)
			if flusher != nil {
				flusher.Flush()
			}
			<-r.Context().Done()
		default:
			writeRPCError(w, http.StatusNotFound, id, -32601, "Method not found")
		}
	}))
	defer srv.Close()

	client, err := Dial(context.Background(), Config{
		ID:        "http",
		Transport: TransportHTTP,
		URL:       srv.URL,
		Timeout:   3 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	notified := make(chan string, 2)
	client.SetNotificationHandler(func(method string, _ json.RawMessage) { notified <- method })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.Subscribe(ctx, map[string]any{"toolsListChanged": true}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case method := <-notified:
			seen[method] = true
		case <-time.After(time.Second):
			t.Fatalf("notification %d was not delivered", i+1)
		}
	}
	if !seen["notifications/subscriptions/acknowledged"] || !seen["notifications/tools/list_changed"] {
		t.Fatalf("unexpected notifications: %v", seen)
	}

	mu.Lock()
	defer mu.Unlock()
	if requested["toolsListChanged"] != true {
		t.Fatalf("the filter was not sent: %v", requested)
	}
}

// TestHTTPResources covers resources/list and resources/read.
func TestHTTPResources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msg := decodeRPC(t, r)
		method, _ := msg["method"].(string)
		id := msg["id"]
		params, _ := msg["params"].(map[string]any)

		switch method {
		case "server/discover":
			writeRPCResult(w, id, map[string]any{
				"resultType":        "complete",
				"supportedVersions": []string{RevisionModern},
				"capabilities":      map[string]any{"resources": map[string]any{}},
				"serverInfo":        map[string]any{"name": "resources", "version": "1.0.0"},
			})
		case "resources/list":
			writeRPCResult(w, id, map[string]any{
				"resultType": "complete",
				"resources": []any{
					map[string]any{"uri": "file:///doc.txt", "name": "doc", "mimeType": "text/plain"},
				},
			})
		case "resources/read":
			uri, _ := params["uri"].(string)
			writeRPCResult(w, id, map[string]any{
				"resultType": "complete",
				"contents":   []any{map[string]any{"uri": uri, "mimeType": "text/plain", "text": "hello"}},
			})
		default:
			writeRPCError(w, http.StatusNotFound, id, -32601, "Method not found")
		}
	}))
	defer srv.Close()

	client, err := Dial(context.Background(), Config{
		ID:        "http",
		Transport: TransportHTTP,
		URL:       srv.URL,
		Timeout:   3 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	resources, err := client.ListResources(context.Background())
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if len(resources) != 1 || resources[0].URI != "file:///doc.txt" {
		t.Fatalf("unexpected resources: %+v", resources)
	}

	result, err := client.ReadResource(context.Background(), "file:///doc.txt")
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(result.Contents) != 1 || result.Contents[0].Text != "hello" {
		t.Fatalf("unexpected contents: %+v", result.Contents)
	}
}
