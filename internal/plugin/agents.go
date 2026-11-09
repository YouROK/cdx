package plugin

import "context"

// MaxAgentTasks is the maximum number of delegated turns in one batch.
const MaxAgentTasks = 8

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
	Error         string
}

// AgentRunner starts delegated turns and waits for their results.
type AgentRunner interface {
	RunChild(ctx context.Context, req AgentRequest) (AgentResult, error)
	RunChildren(ctx context.Context, reqs []AgentRequest) ([]AgentResult, error)
}
