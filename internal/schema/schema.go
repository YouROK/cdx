// Package schema validates objects against JSON Schema and splits plain and secret fields.
package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// FormatSecret marks a string property to be stored in the plugin's encrypted secrets namespace.
const FormatSecret = "secret"

// TypeName accepts the JSON Schema type keyword as a name or a union array.
type TypeName string

// UnmarshalJSON reads a type name or the first concrete entry of a union array.
func (t *TypeName) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		*t = ""
		return nil
	}
	switch trimmed[0] {
	case '"':
		var name string
		if err := json.Unmarshal(trimmed, &name); err != nil {
			return err
		}
		*t = TypeName(name)
		return nil
	case '[':
		var names []string
		if err := json.Unmarshal(trimmed, &names); err != nil {
			return err
		}
		for _, name := range names {
			if name != "null" {
				*t = TypeName(name)
				return nil
			}
		}
		*t = ""
		return nil
	}
	*t = ""
	return nil
}

// Schema represents a JSON Schema object definition.
type Schema struct {
	Type        TypeName             `json:"type"`
	Title       string               `json:"title,omitempty"`
	Description string               `json:"description,omitempty"`
	Properties  map[string]*Property `json:"properties,omitempty"`
	Required    []string             `json:"required,omitempty"`
}

// Property represents a single field inside a JSON Schema object.
type Property struct {
	Type        TypeName             `json:"type"`
	Title       string               `json:"title,omitempty"`
	Description string               `json:"description,omitempty"`
	Format      string               `json:"format,omitempty"`
	Default     any                  `json:"default,omitempty"`
	Enum        []any                `json:"enum,omitempty"`
	Minimum     *float64             `json:"minimum,omitempty"`
	Maximum     *float64             `json:"maximum,omitempty"`
	Items       *Property            `json:"items,omitempty"`
	Properties  map[string]*Property `json:"properties,omitempty"`
	Required    []string             `json:"required,omitempty"`
}

// Parse parses a raw map or JSON object into a validated Schema definition.
func Parse(raw map[string]any) (*Schema, error) {
	if raw == nil {
		return nil, fmt.Errorf("schema is required")
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode schema: %w", err)
	}
	var s Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse schema: %w", err)
	}
	if s.Type != "object" {
		return nil, fmt.Errorf("schema root type: expected \"object\", got %q", s.Type)
	}
	return &s, nil
}

// Validate checks input against the schema and returns a copy with defaults applied.
func (s *Schema) Validate(input map[string]any) (map[string]any, error) {
	if s == nil {
		return nil, fmt.Errorf("schema is nil")
	}
	out := make(map[string]any, len(input))
	for k, v := range input {
		out[k] = v
	}

	// Apply default values for missing fields.
	for name, prop := range s.Properties {
		if prop == nil {
			continue
		}
		if _, exists := out[name]; !exists && prop.Default != nil {
			out[name] = prop.Default
		}
	}

	// Check required fields.
	for _, req := range s.Required {
		val, exists := out[req]
		if !exists || val == nil {
			return nil, fmt.Errorf("field %q is required", req)
		}
		if str, ok := val.(string); ok && str == "" {
			return nil, fmt.Errorf("field %q is required", req)
		}
	}

	// Validate each provided property.
	for name, val := range out {
		prop, defined := s.Properties[name]
		if !defined || prop == nil {
			continue
		}
		if val == nil {
			continue
		}
		coerced, err := validateValue(name, prop, val)
		if err != nil {
			return nil, err
		}
		out[name] = coerced
	}

	return out, nil
}

// SplitSecrets separates validated values into plain storage fields and secret fields (format: "secret").
func (s *Schema) SplitSecrets(values map[string]any) (plain map[string]any, secrets map[string]string) {
	plain = make(map[string]any)
	secrets = make(map[string]string)

	for k, v := range values {
		prop := s.Properties[k]
		if prop != nil && prop.Format == FormatSecret {
			if str, ok := v.(string); ok {
				secrets[k] = str
			}
			continue
		}
		plain[k] = v
	}
	return plain, secrets
}

// SecretFields returns the sorted names of all properties with format: "secret".
func (s *Schema) SecretFields() []string {
	var names []string
	for name, prop := range s.Properties {
		if prop != nil && prop.Format == FormatSecret {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func validateValue(path string, prop *Property, val any) (any, error) {
	switch prop.Type {
	case "string":
		str, ok := val.(string)
		if !ok {
			return nil, fmt.Errorf("field %q: expected string, got %T", path, val)
		}
		if err := checkEnum(path, prop.Enum, str); err != nil {
			return nil, err
		}
		return str, nil

	case "boolean":
		b, ok := val.(bool)
		if !ok {
			return nil, fmt.Errorf("field %q: expected boolean, got %T", path, val)
		}
		return b, nil

	case "integer":
		num, ok := toFloat64(val)
		if !ok || math.Trunc(num) != num {
			return nil, fmt.Errorf("field %q: expected integer, got %v", path, val)
		}
		if err := checkBounds(path, prop, num); err != nil {
			return nil, err
		}
		if err := checkEnum(path, prop.Enum, num); err != nil {
			return nil, err
		}
		return int64(num), nil

	case "number":
		num, ok := toFloat64(val)
		if !ok {
			return nil, fmt.Errorf("field %q: expected number, got %v", path, val)
		}
		if err := checkBounds(path, prop, num); err != nil {
			return nil, err
		}
		if err := checkEnum(path, prop.Enum, num); err != nil {
			return nil, err
		}
		return num, nil

	case "array":
		slice, ok := val.([]any)
		if !ok {
			return nil, fmt.Errorf("field %q: expected array, got %T", path, val)
		}
		if prop.Items == nil {
			return slice, nil
		}
		out := make([]any, len(slice))
		for i, item := range slice {
			elem, err := validateValue(fmt.Sprintf("%s[%d]", path, i), prop.Items, item)
			if err != nil {
				return nil, err
			}
			out[i] = elem
		}
		return out, nil

	case "object":
		m, ok := val.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("field %q: expected object, got %T", path, val)
		}
		sub := &Schema{
			Type:       "object",
			Properties: prop.Properties,
			Required:   prop.Required,
		}
		return sub.Validate(m)

	case "":
		return val, nil

	default:
		return nil, fmt.Errorf("field %q: unsupported schema type %q", path, prop.Type)
	}
}

func checkBounds(path string, prop *Property, num float64) error {
	if prop.Minimum != nil && num < *prop.Minimum {
		return fmt.Errorf("field %q: must be >= %v, got %v", path, *prop.Minimum, num)
	}
	if prop.Maximum != nil && num > *prop.Maximum {
		return fmt.Errorf("field %q: must be <= %v, got %v", path, *prop.Maximum, num)
	}
	return nil
}

func checkEnum(path string, enum []any, val any) error {
	if len(enum) == 0 {
		return nil
	}
	for _, allowed := range enum {
		if f1, ok1 := toFloat64(allowed); ok1 {
			if f2, ok2 := toFloat64(val); ok2 && f1 == f2 {
				return nil
			}
		}
		if allowed == val {
			return nil
		}
	}
	return fmt.Errorf("field %q: value %v is not in allowed enum %v", path, val, enum)
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
