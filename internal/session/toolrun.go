package session

import (
	"context"
	"fmt"

	"github.com/yourok/cdx/internal/plugin"
)

// ToolDeniedError reports a tool call that the approval path refused.
type ToolDeniedError struct {
	Reason string
}

func (e *ToolDeniedError) Error() string {
	if e.Reason == "" {
		return "permission denied"
	}
	return e.Reason
}

// PrepareToolCall runs the before hook and the approval question and returns
// the arguments to execute.
func (o *Orchestrator) PrepareToolCall(ctx context.Context, call plugin.ToolCall) (map[string]any, error) {
	args := call.Arguments
	if args == nil {
		args = map[string]any{}
	}
	if o.bus == nil {
		return args, nil
	}
	spec, _ := o.registry.GetTool(call.Name)
	rootID := call.RootID
	if rootID == "" {
		rootID = call.SessionID
	}
	hookOut, err := o.bus.RunHook(ctx, "tool:before_call", map[string]any{
		"session_id":      call.SessionID,
		"root_session_id": rootID,
		"depth":           call.Depth,
		"workdir":         call.Workdir,
		"tool":            call.Name,
		"permission":      spec.Permission,
		"arguments":       args,
	})
	if err != nil {
		return nil, err
	}
	if modArgs, ok := hookOut["arguments"].(map[string]any); ok {
		args = modArgs
	}
	if askRaw, ok := hookOut["ask"].(map[string]any); ok && len(askRaw) > 0 {
		allowed, reason, askErr := o.askToolPermission(ctx, spec, call.SessionID, call.Workdir, call.Name, args, askRaw)
		if askErr != nil {
			return nil, askErr
		}
		if !allowed {
			return nil, &ToolDeniedError{Reason: reason}
		}
	}
	return args, nil
}

// FinishToolCall runs the after hook and returns the output to record.
func (o *Orchestrator) FinishToolCall(ctx context.Context, call plugin.ToolCall, args map[string]any, output string) string {
	if o.bus == nil {
		return output
	}
	hookOut, err := o.bus.RunHook(ctx, "tool:after_call", map[string]any{
		"session_id": call.SessionID,
		"workdir":    call.Workdir,
		"tool":       call.Name,
		"arguments":  args,
		"output":     output,
	})
	if err != nil {
		return output
	}
	if modOut, ok := hookOut["output"].(string); ok {
		return modOut
	}
	return output
}

// RunTool executes one tool call through the hooks, the approval question, and
// the handler. A failed handler returns its error text together with the error.
func (o *Orchestrator) RunTool(ctx context.Context, call plugin.ToolCall) (string, error) {
	args, err := o.PrepareToolCall(ctx, call)
	if err != nil {
		return "", err
	}
	call.Arguments = args
	output, callErr := o.registry.CallTool(ctx, call.Name, args)
	var content string
	if callErr != nil {
		content = fmt.Sprintf("error: %v", callErr)
	} else {
		content = formatToolOutput(output)
	}
	content = o.FinishToolCall(ctx, call, args, content)
	return content, callErr
}
