package plugin

import "context"

// AgentRequest describes a delegated child turn.
type AgentRequest struct {
	Prompt   string
	Preset   string
	Model    string
	MaxSteps int
}

// AgentResult is the child turn outcome returned to a plugin.
type AgentResult struct {
	SessionID     string
	Content       string
	Steps         int
	StoppedReason string
	Usage         map[string]any
}

// AgentRunner starts a delegated turn and waits for its result.
type AgentRunner interface {
	RunChild(ctx context.Context, req AgentRequest) (AgentResult, error)
}
