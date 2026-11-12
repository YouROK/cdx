package session

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yourok/cdx/internal/plugin"
)

func TestPrepareToolCallAppliesHooks(t *testing.T) {
	orch, eb, reg, _ := setupTestOrchestrator(t)

	err := reg.RegisterTool(plugin.ToolSpec{
		PluginID:    "tools-local",
		Name:        "echo_tool",
		Description: "Echo text",
		Permission:  "read",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
			"required": []any{"text"},
		},
	})
	if err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}

	eb.OnHook("plugin:permissions", "tool:before_call", 10, func(_ context.Context, p map[string]any) (map[string]any, error) {
		args, _ := p["arguments"].(map[string]any)
		if args["text"] == "blocked" {
			return nil, errors.New("blocked by policy")
		}
		args["text"] = strings.ToUpper(args["text"].(string))
		p["arguments"] = args
		return p, nil
	})
	eb.OnHook("plugin:output", "tool:after_call", 10, func(_ context.Context, p map[string]any) (map[string]any, error) {
		output, _ := p["output"].(string)
		p["output"] = output + " (checked)"
		return p, nil
	})

	args, err := orch.PrepareToolCall(context.Background(), plugin.ToolCall{Name: "echo_tool", Arguments: map[string]any{"text": "hi"}})
	if err != nil {
		t.Fatalf("PrepareToolCall failed: %v", err)
	}
	if args["text"] != "HI" {
		t.Fatalf("the hook did not rewrite the arguments: %v", args)
	}

	if _, err := orch.PrepareToolCall(context.Background(), plugin.ToolCall{Name: "echo_tool", Arguments: map[string]any{"text": "blocked"}}); err == nil {
		t.Fatal("a denied call must return an error")
	}

	out := orch.FinishToolCall(context.Background(), plugin.ToolCall{Name: "echo_tool"}, args, "result")
	if out != "result (checked)" {
		t.Fatalf("the after hook did not run: %q", out)
	}
}

func TestRunToolReportsMissingTools(t *testing.T) {
	orch, _, _, _ := setupTestOrchestrator(t)

	out, err := orch.RunTool(context.Background(), plugin.ToolCall{Name: "no_such_tool", Arguments: map[string]any{}})
	if err == nil {
		t.Fatal("a missing tool must return an error")
	}
	if !strings.Contains(out, "not registered") {
		t.Fatalf("unexpected output: %q", out)
	}
}
