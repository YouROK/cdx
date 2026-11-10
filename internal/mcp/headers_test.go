package mcp

import (
	"encoding/base64"
	"testing"
)

// TestHeaderValueEncoding covers the Base64 sentinel rules of the transport.
func TestHeaderValueEncoding(t *testing.T) {
	cases := map[string]string{
		"us-west1":           "us-west1",
		"Hello, world":       "Hello, world",
		"":                   "",
		" padded ":           "=?base64?IHBhZGRlZCA=?=",
		"Hello, 世界":          "=?base64?" + base64.StdEncoding.EncodeToString([]byte("Hello, 世界")) + "?=",
		"line1\nline2":       "=?base64?" + base64.StdEncoding.EncodeToString([]byte("line1\nline2")) + "?=",
		"=?base64?literal?=": "=?base64?" + base64.StdEncoding.EncodeToString([]byte("=?base64?literal?=")) + "?=",
	}
	for input, want := range cases {
		if got := headerValue(input); got != want {
			t.Errorf("headerValue(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestToolHeaderValues covers nested paths and primitive formatting.
func TestToolHeaderValues(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"region": map[string]any{"type": "string", "x-mcp-header": "Region"},
			"count":  map[string]any{"type": "integer", "x-mcp-header": "Count"},
			"dry":    map[string]any{"type": "boolean", "x-mcp-header": "Dry"},
			"tenant": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "x-mcp-header": "Tenant"},
				},
			},
		},
	}
	tool := Tool{Name: "run", InputSchema: schema}
	args := map[string]any{
		"region": "us-west1",
		"count":  float64(42),
		"dry":    true,
		"tenant": map[string]any{"id": "acme"},
	}
	headers := toolHeaderValues(&tool, args)
	want := map[string]string{
		"Mcp-Param-Region": "us-west1",
		"Mcp-Param-Count":  "42",
		"Mcp-Param-Dry":    "true",
		"Mcp-Param-Tenant": "acme",
	}
	for name, value := range want {
		if headers[name] != value {
			t.Errorf("%s = %q, want %q", name, headers[name], value)
		}
	}
	if len(headers) != len(want) {
		t.Errorf("unexpected headers: %v", headers)
	}

	partial := toolHeaderValues(&tool, map[string]any{"region": nil})
	if len(partial) != 0 {
		t.Errorf("a null value produced a header: %v", partial)
	}
}

// TestValidateToolHeaders covers the annotation constraints.
func TestValidateToolHeaders(t *testing.T) {
	valid := Tool{Name: "ok", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"region": map[string]any{"type": "string", "x-mcp-header": "Region"},
			"nested": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"zone": map[string]any{"type": "string", "x-mcp-header": "Zone"},
				},
			},
		},
	}}
	if err := validateToolHeaders(valid); err != nil {
		t.Fatalf("valid tool rejected: %v", err)
	}

	duplicate := Tool{Name: "dup", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a": map[string]any{"type": "string", "x-mcp-header": "Region"},
			"b": map[string]any{"type": "string", "x-mcp-header": "region"},
		},
	}}
	if err := validateToolHeaders(duplicate); err == nil {
		t.Fatal("a case-insensitive duplicate was accepted")
	}

	unreachable := Tool{Name: "deep", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string", "x-mcp-header": "Item"},
			},
		},
	}}
	if err := validateToolHeaders(unreachable); err == nil {
		t.Fatal("an unreachable annotation was accepted")
	}

	number := Tool{Name: "num", InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ratio": map[string]any{"type": "number", "x-mcp-header": "Ratio"},
		},
	}}
	if err := validateToolHeaders(number); err == nil {
		t.Fatal("a number annotation was accepted")
	}
}

// TestDeriveHeaders covers the request line and name mirrors.
func TestDeriveHeaders(t *testing.T) {
	frame := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"my tool","arguments":{}}}`)
	headers := deriveHeaders(frame)
	if headers["Mcp-Method"] != "tools/call" {
		t.Fatalf("unexpected method header: %v", headers)
	}
	if headers["Mcp-Name"] != headerValue("my tool") {
		t.Fatalf("unexpected name header: %v", headers)
	}

	listing := deriveHeaders([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`))
	if _, ok := listing["Mcp-Name"]; ok {
		t.Fatalf("tools/list carries a name header: %v", listing)
	}
}
