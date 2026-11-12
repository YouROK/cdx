package plugin

import "context"

// ToolCall describes one tool invocation requested by a plugin.
type ToolCall struct {
	SessionID string
	RootID    string
	Depth     int
	Workdir   string
	Name      string
	Arguments map[string]any
}

// ToolRunner runs a tool through the core pipeline: the before hook, the
// approval question, the handler, and the after hook.
type ToolRunner interface {
	RunTool(ctx context.Context, call ToolCall) (string, error)
}
