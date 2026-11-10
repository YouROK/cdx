package schema

import "testing"

func TestSchemaValidateAndSplitSecrets(t *testing.T) {
	raw := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"base_url": map[string]any{
				"type":    "string",
				"default": "https://api.openai.com/v1",
			},
			"api_key": map[string]any{
				"type":   "string",
				"format": "secret",
			},
			"timeout": map[string]any{
				"type":    "integer",
				"default": 30,
				"minimum": 5,
				"maximum": 300,
			},
			"mode": map[string]any{
				"type": "string",
				"enum": []any{"fast", "smart"},
			},
		},
		"required": []any{"api_key"},
	}

	s, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Missing required api_key should fail.
	if _, err := s.Validate(map[string]any{}); err == nil {
		t.Fatal("expected error for missing required field api_key")
	}

	// Valid input with defaults applied.
	validated, err := s.Validate(map[string]any{
		"api_key": "sk-secret-123",
		"mode":    "smart",
	})
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if validated["base_url"] != "https://api.openai.com/v1" {
		t.Fatalf("expected default base_url, got %v", validated["base_url"])
	}
	if validated["timeout"] != int64(30) {
		t.Fatalf("expected default timeout 30, got %v (%T)", validated["timeout"], validated["timeout"])
	}

	// Out-of-range integer should fail.
	if _, err := s.Validate(map[string]any{"api_key": "sk-1", "timeout": 1}); err == nil {
		t.Fatal("expected minimum bound error for timeout=1")
	}

	// Invalid enum value should fail.
	if _, err := s.Validate(map[string]any{"api_key": "sk-1", "mode": "unknown"}); err == nil {
		t.Fatal("expected enum error for mode=unknown")
	}

	plain, secrets := s.SplitSecrets(validated)
	if _, exists := plain["api_key"]; exists {
		t.Fatal("api_key must not be present in plain map")
	}
	if secrets["api_key"] != "sk-secret-123" {
		t.Fatalf("expected secret api_key, got %q", secrets["api_key"])
	}
	if plain["base_url"] != "https://api.openai.com/v1" {
		t.Fatalf("expected base_url in plain map, got %v", plain["base_url"])
	}
}

// TestParseAcceptsUnionTypes covers MCP schemas that use nullable unions and unknown keywords.
func TestParseAcceptsUnionTypes(t *testing.T) {
	raw := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":  map[string]any{"type": []any{"string", "null"}},
			"count": map[string]any{"type": []any{"null", "integer"}, "minimum": float64(1)},
			"data":  map[string]any{"anyOf": []any{map[string]any{"type": "string"}}},
			"items": map[string]any{"type": "array", "items": map[string]any{"type": []any{"string", "null"}}},
		},
		"$defs":                map[string]any{"unused": map[string]any{"type": "string"}},
		"additionalProperties": false,
	}
	s, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Properties["name"].Type != "string" {
		t.Fatalf("union type was not resolved: %q", s.Properties["name"].Type)
	}
	if s.Properties["count"].Type != "integer" {
		t.Fatalf("nullable integer was not resolved: %q", s.Properties["count"].Type)
	}
	if _, err := s.Validate(map[string]any{"name": "ok", "count": int64(2)}); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if _, err := s.Validate(map[string]any{"count": int64(0)}); err == nil {
		t.Fatal("expected the minimum bound to apply")
	}
}

// TestParseAcceptsRootTypeArray covers a root declared as a union.
func TestParseAcceptsRootTypeArray(t *testing.T) {
	if _, err := Parse(map[string]any{"type": []any{"object", "null"}}); err != nil {
		t.Fatalf("root union rejected: %v", err)
	}
	if _, err := Parse(map[string]any{"type": []any{"string"}}); err == nil {
		t.Fatal("a non-object root was accepted")
	}
}
