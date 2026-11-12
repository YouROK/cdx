package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/yourok/cdx/internal/mcp"
	"github.com/yourok/cdx/internal/question"
)

// buildMCPObject exposes the MCP connections of the plugin.
func (rt *Runtime) buildMCPObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()

	_ = obj.Set("connect", func(raw map[string]any) goja.Value {
		manager := rt.deps.MCP
		if manager == nil {
			panic(vm.NewGoError(errors.New("mcp is not available")))
		}
		cfg, err := mcpConfigFromJS(raw)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		if cfg.Logger == nil {
			cfg.Logger = rt.deps.Logger
		}
		return rt.async(vm, rt.deps.Async, func(ctx context.Context) (any, error) {
			workdir := WorkdirFromContext(ctx)
			if cfg.Dir == "" {
				cfg.Dir = workdir
			}
			if cfg.Root == "" {
				cfg.Root = workdir
			}
			client, connectErr := manager.Connect(ctx, rt.owner, cfg)
			if connectErr != nil {
				return nil, connectErr
			}
			client.SetInputHandler(rt.answerMCPInput)
			tools, listErr := client.ListTools(ctx)
			if listErr != nil {
				return nil, listErr
			}
			return map[string]any{
				"id":        cfg.ID,
				"connected": true,
				"revision":  client.Revision(),
				"server":    mcpServerInfo(client.Info()),
				"tools":     tools,
			}, nil
		})
	})

	_ = obj.Set("call", func(serverID, tool string, args map[string]any) goja.Value {
		return rt.async(vm, rt.deps.Async, func(ctx context.Context) (any, error) {
			client, ok := rt.mcpClient(serverID)
			if !ok {
				return nil, fmt.Errorf("mcp: server %q is not connected", serverID)
			}
			res, err := client.CallTool(ctx, tool, args)
			if err != nil {
				return nil, err
			}
			data, err := json.Marshal(res)
			if err != nil {
				return nil, err
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return nil, err
			}
			return out, nil
		})
	})

	_ = obj.Set("listTools", func(serverID string) goja.Value {
		return rt.async(vm, rt.deps.Async, func(ctx context.Context) (any, error) {
			client, ok := rt.mcpClient(serverID)
			if !ok {
				return []mcp.Tool{}, nil
			}
			tools, err := client.ListTools(ctx)
			if err != nil {
				return nil, err
			}
			if tools == nil {
				return []mcp.Tool{}, nil
			}
			return tools, nil
		})
	})

	_ = obj.Set("status", func(serverID string) map[string]any {
		client, ok := rt.mcpClient(serverID)
		if !ok {
			return map[string]any{"id": serverID, "connected": false}
		}
		return map[string]any{
			"id":        serverID,
			"connected": true,
			"revision":  client.Revision(),
			"server":    mcpServerInfo(client.Info()),
		}
	})

	_ = obj.Set("listResources", func(serverID string) goja.Value {
		return rt.async(vm, rt.deps.Async, func(ctx context.Context) (any, error) {
			client, ok := rt.mcpClient(serverID)
			if !ok {
				return []mcp.Resource{}, nil
			}
			resources, err := client.ListResources(ctx)
			if err != nil {
				return nil, err
			}
			if resources == nil {
				return []mcp.Resource{}, nil
			}
			return resources, nil
		})
	})

	_ = obj.Set("readResource", func(serverID, uri string) goja.Value {
		return rt.async(vm, rt.deps.Async, func(ctx context.Context) (any, error) {
			client, ok := rt.mcpClient(serverID)
			if !ok {
				return nil, fmt.Errorf("mcp: server %q is not connected", serverID)
			}
			res, err := client.ReadResource(ctx, uri)
			if err != nil {
				return nil, err
			}
			data, err := json.Marshal(res)
			if err != nil {
				return nil, err
			}
			var out map[string]any
			if err := json.Unmarshal(data, &out); err != nil {
				return nil, err
			}
			return out, nil
		})
	})

	_ = obj.Set("servers", func() []string {
		if rt.deps.MCP == nil {
			return []string{}
		}
		ids := rt.deps.MCP.IDs(rt.owner)
		if ids == nil {
			return []string{}
		}
		return ids
	})

	rt.bindAsync(vm, obj, "close", rt.deps.Async, func(ctx context.Context, args []any) (any, error) {
		if rt.deps.MCP == nil || len(args) == 0 {
			return false, nil
		}
		serverID, _ := args[0].(string)
		return rt.deps.MCP.Close(rt.owner, serverID), nil
	})

	return obj
}

// mcpClient returns a live connection owned by this plugin.
func (rt *Runtime) mcpClient(serverID string) (*mcp.Client, bool) {
	if rt.deps.MCP == nil {
		return nil, false
	}
	return rt.deps.MCP.Get(rt.owner, serverID)
}

// mcpServerInfo converts discovery data into a plain JS object.
func mcpServerInfo(info mcp.ServerInfo) map[string]any {
	return map[string]any{
		"name":             info.Name,
		"title":            info.Title,
		"version":          info.Version,
		"protocol_version": info.ProtocolVersion,
		"revision":         info.Revision,
		"capabilities":     info.Capabilities,
		"instructions":     info.Instructions,
	}
}

// mcpConfigFromJS reads a connection configuration passed from JavaScript.
func mcpConfigFromJS(raw map[string]any) (mcp.Config, error) {
	cfg := mcp.Config{
		ID:        strings.TrimSpace(jsString(raw["id"])),
		Name:      strings.TrimSpace(jsString(raw["name"])),
		Transport: strings.ToLower(strings.TrimSpace(jsString(raw["transport"]))),
		Command:   strings.TrimSpace(jsString(raw["command"])),
		URL:       strings.TrimSpace(jsString(raw["url"])),
		Protocol:  strings.TrimSpace(jsString(raw["protocol"])),
		Dir:       strings.TrimSpace(jsString(raw["cwd"])),
		Root:      strings.TrimSpace(jsString(raw["root"])),
		Args:      jsStringSlice(raw["args"]),
		Env:       jsStringMap(raw["env"]),
		Headers:   jsStringMap(raw["headers"]),
	}
	if cfg.ID == "" {
		return cfg, errors.New("mcp: server id is required")
	}
	cfg.Timeout = jsTimeout(raw["timeout_ms"])
	return cfg, nil
}

// jsString reads a string value.
func jsString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

// jsStringSlice reads an array of strings.
func jsStringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

// jsStringMap reads an object of string values.
func jsStringMap(value any) map[string]string {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(object))
	for name, item := range object {
		if text, ok := item.(string); ok && name != "" {
			out[name] = text
		}
	}
	return out
}

// jsTimeout reads a timeout in milliseconds.
func jsTimeout(value any) time.Duration {
	var ms int64
	switch typed := value.(type) {
	case int64:
		ms = typed
	case int:
		ms = int64(typed)
	case float64:
		ms = int64(typed)
	}
	if ms <= 0 {
		return 0
	}
	if ms > int64(30*time.Minute/time.Millisecond) {
		ms = int64(30 * time.Minute / time.Millisecond)
	}
	return time.Duration(ms) * time.Millisecond
}

// answerMCPInput fulfils the input requests of a multi round-trip call.
func (rt *Runtime) answerMCPInput(ctx context.Context, requests map[string]mcp.InputRequest) (map[string]any, error) {
	names := make([]string, 0, len(requests))
	for name := range requests {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make(map[string]any, len(requests))
	for _, name := range names {
		request := requests[name]
		switch request.Method {
		case "roots/list":
			out[name] = map[string]any{"roots": mcpRoots(WorkdirFromContext(ctx))}
		case "elicitation/create":
			result, err := elicitMCP(ctx, request.Params)
			if err != nil {
				return nil, err
			}
			out[name] = result
		default:
			return nil, fmt.Errorf("mcp: %s is not supported", request.Method)
		}
	}
	return out, nil
}

// mcpRoots advertises the working directory as a file root.
func mcpRoots(workdir string) []map[string]string {
	root := strings.TrimSpace(workdir)
	if root == "" {
		return []map[string]string{}
	}
	return []map[string]string{{
		"uri":  (&url.URL{Scheme: "file", Path: root}).String(),
		"name": filepath.Base(root),
	}}
}

// elicitMCP turns an elicitation request into client questions.
func elicitMCP(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var params struct {
		Mode            string `json:"mode"`
		Message         string `json:"message"`
		RequestedSchema struct {
			Properties map[string]map[string]any `json:"properties"`
		} `json:"requestedSchema"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("mcp: parse elicitation: %w", err)
	}
	if params.Mode == "url" {
		return map[string]any{"action": "decline"}, nil
	}

	names := make([]string, 0, len(params.RequestedSchema.Properties))
	for name := range params.RequestedSchema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	content := make(map[string]any, len(names))
	for index, name := range names {
		var enumLabels map[string]string
		property := params.RequestedSchema.Properties[name]
		typ, _ := property["type"].(string)
		title := strings.TrimSpace(params.Message)
		if index > 0 || title == "" {
			if label, _ := property["title"].(string); strings.TrimSpace(label) != "" {
				title = strings.TrimSpace(label)
			} else if index > 0 {
				title = name
			}
		}
		if title == "" {
			title = name
		}

		ask := question.Question{
			Kind:  "mcp",
			Title: clipRunes(title, 200),
			Body:  stringValue(property, "description"),
		}
		switch typ {
		case "boolean":
			ask.Options = []question.Option{{ID: "no", Label: "No"}, {ID: "yes", Label: "Yes"}}
			ask.Default = "no"
		case "string":
			var labels map[string]string
			ask.Options, labels = enumOptions(property["enum"])
			if len(ask.Options) == 0 {
				ask.Text = true
			}
			enumLabels = labels
		default:
			ask.Text = true
		}

		answer, err := question.Ask(ctx, ask)
		if err != nil {
			return nil, err
		}
		if answer.Cancelled {
			return map[string]any{"action": "cancel"}, nil
		}
		content[name] = elicitValue(typ, answer, enumLabels)
	}
	return map[string]any{"action": "accept", "content": content}, nil
}

// enumOptions converts a schema enum into question options and an id-to-label map.
func enumOptions(raw any) ([]question.Option, map[string]string) {
	values, ok := raw.([]any)
	if !ok {
		return nil, nil
	}
	options := make([]question.Option, 0, len(values))
	labels := make(map[string]string, len(values))
	for index, value := range values {
		label := fmt.Sprint(value)
		if label == "" {
			continue
		}
		id := "o" + strconv.Itoa(index+1)
		options = append(options, question.Option{ID: id, Label: label})
		labels[id] = label
	}
	return options, labels
}

// elicitValue converts an answer into the value the requested schema expects.
func elicitValue(typ string, answer question.Answer, labels map[string]string) any {
	switch typ {
	case "boolean":
		return answer.Choice == "yes"
	case "integer":
		if value, err := strconv.ParseInt(strings.TrimSpace(answer.Text), 10, 64); err == nil {
			return value
		}
		return nil
	case "number":
		if value, err := strconv.ParseFloat(strings.TrimSpace(answer.Text), 64); err == nil {
			return value
		}
		return nil
	case "string":
		if answer.Choice != "" && answer.Text == "" {
			if label, ok := labels[answer.Choice]; ok {
				return label
			}
			return answer.Choice
		}
		return answer.Text
	}
	if answer.Text != "" {
		return answer.Text
	}
	return answer.Choice
}

// stringValue reads a string field of a schema property.
func stringValue(object map[string]any, name string) string {
	if text, ok := object[name].(string); ok {
		return text
	}
	return ""
}

// clipRunes truncates text to at most limit runes.
func clipRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
