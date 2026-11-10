package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// headerField is one tool parameter mirrored into an Mcp-Param-* HTTP header.
type headerField struct {
	Path []string
	Name string
	Type string
}

// validateToolHeaders checks the x-mcp-header annotations of one tool definition.
// Invalid annotations make the whole tool definition invalid, as the transport requires.
func validateToolHeaders(tool Tool) error {
	if tool.InputSchema == nil {
		return nil
	}
	fields, err := collectHeaderFields(tool.InputSchema)
	if err != nil {
		return err
	}
	if countHeaderAnnotations(tool.InputSchema) != len(fields) {
		return fmt.Errorf("x-mcp-header is only allowed on a chain of properties")
	}
	return nil
}

// collectHeaderFields walks the reachable properties chains of an input schema.
func collectHeaderFields(schema map[string]any) ([]headerField, error) {
	properties, _ := schema["properties"].(map[string]any)
	var out []headerField
	seen := map[string]bool{}
	if err := walkHeaderProps(properties, nil, &out, seen); err != nil {
		return nil, err
	}
	return out, nil
}

// walkHeaderProps collects annotations below a properties map.
func walkHeaderProps(properties map[string]any, path []string, out *[]headerField, seen map[string]bool) error {
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		prop, ok := properties[name].(map[string]any)
		if !ok {
			continue
		}
		fieldPath := append(append([]string{}, path...), name)
		if raw, ok := prop["x-mcp-header"]; ok {
			header, ok := raw.(string)
			if !ok || !validHeaderName(header) {
				return fmt.Errorf("invalid x-mcp-header %v on %q", raw, name)
			}
			if seen[strings.ToLower(header)] {
				return fmt.Errorf("duplicate x-mcp-header %q", header)
			}
			seen[strings.ToLower(header)] = true

			typ, _ := prop["type"].(string)
			switch typ {
			case "string", "integer", "boolean":
			default:
				return fmt.Errorf("x-mcp-header %q needs a primitive type, got %q", header, typ)
			}
			*out = append(*out, headerField{Path: fieldPath, Name: header, Type: typ})
		}
		if child, ok := prop["properties"].(map[string]any); ok {
			if err := walkHeaderProps(child, fieldPath, out, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

// countHeaderAnnotations counts every annotation in the schema, reachable or not.
func countHeaderAnnotations(value any) int {
	switch typed := value.(type) {
	case map[string]any:
		count := 0
		for key, item := range typed {
			if key == "x-mcp-header" {
				count++
				continue
			}
			count += countHeaderAnnotations(item)
		}
		return count
	case []any:
		count := 0
		for _, item := range typed {
			count += countHeaderAnnotations(item)
		}
		return count
	}
	return 0
}

// validHeaderName reports whether the name is an HTTP field-name token.
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)):
		default:
			return false
		}
	}
	return true
}

// toolHeaderValues builds the Mcp-Param-* headers for one call.
func toolHeaderValues(tool *Tool, args map[string]any) map[string]string {
	if tool == nil || tool.InputSchema == nil || len(args) == 0 {
		return nil
	}
	fields, err := collectHeaderFields(tool.InputSchema)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, field := range fields {
		value, ok := lookupPath(args, field.Path)
		if !ok || value == nil {
			continue
		}
		text, ok := formatHeaderValue(field.Type, value)
		if !ok {
			continue
		}
		out["Mcp-Param-"+field.Name] = headerValue(text)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// lookupPath reads a nested object value.
func lookupPath(args map[string]any, path []string) (any, bool) {
	var current any = args
	for _, name := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[name]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// formatHeaderValue renders a primitive value for a header.
func formatHeaderValue(typ string, value any) (string, bool) {
	switch typ {
	case "string":
		text, ok := value.(string)
		return text, ok
	case "boolean":
		flag, ok := value.(bool)
		if !ok {
			return "", false
		}
		return strconv.FormatBool(flag), true
	case "integer":
		switch typed := value.(type) {
		case float64:
			if typed != float64(int64(typed)) {
				return "", false
			}
			return strconv.FormatInt(int64(typed), 10), true
		case int:
			return strconv.Itoa(typed), true
		case int64:
			return strconv.FormatInt(typed, 10), true
		case json.Number:
			return typed.String(), true
		}
	}
	return "", false
}

// headerValue encodes a value that is not safe to send as plain ASCII.
func headerValue(value string) string {
	if isPlainHeaderValue(value) {
		return value
	}
	return "=?base64?" + base64.StdEncoding.EncodeToString([]byte(value)) + "?="
}

// isPlainHeaderValue reports whether the value needs no Base64 encoding.
func isPlainHeaderValue(value string) bool {
	if value == "" {
		return true
	}
	if strings.HasPrefix(value, "=?base64?") && strings.HasSuffix(value, "?=") {
		return false
	}
	if strings.TrimSpace(value) != value {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c < 0x20 || c > 0x7e {
			if c != '\t' {
				return false
			}
		}
	}
	return true
}

// deriveHeaders mirrors the request line and name into the Mcp-* headers.
func deriveHeaders(frame []byte) map[string]string {
	var probe struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
			URI  string `json:"uri"`
		} `json:"params"`
	}
	if err := json.Unmarshal(frame, &probe); err != nil || probe.Method == "" {
		return nil
	}
	headers := map[string]string{"Mcp-Method": probe.Method}
	switch probe.Method {
	case "tools/call", "prompts/get":
		if probe.Params.Name != "" {
			headers["Mcp-Name"] = headerValue(probe.Params.Name)
		}
	case "resources/read":
		if probe.Params.URI != "" {
			headers["Mcp-Name"] = headerValue(probe.Params.URI)
		}
	}
	return headers
}
