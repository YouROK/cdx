package plugin

import "context"

// Task states reported to a plugin.
const (
	TaskRunning    = "running"
	TaskNeedsInput = "needs_input"
	TaskFinished   = "finished"
	TaskFailed     = "failed"
	TaskCancelled  = "cancelled"
)

// TaskRequest starts a background turn owned by a plugin.
type TaskRequest struct {
	Prompt         string
	Workdir        string
	Preset         string
	Model          string
	MaxSteps       int
	TimeoutSeconds int
	AllowedTools   []string
	Owner          string
}

// TaskQuestion is a question a running task waits for.
type TaskQuestion struct {
	ID    string
	Kind  string
	Title string
	Body  string
}

// TaskInfo is the state of one background turn.
type TaskInfo struct {
	ID            string
	SessionID     string
	Status        string
	Result        string
	StoppedReason string
	Error         string
	Question      *TaskQuestion
}

// TaskRunner starts, watches, answers, and stops background turns.
type TaskRunner interface {
	StartTask(ctx context.Context, req TaskRequest) (TaskInfo, error)
	GetTask(ctx context.Context, id string) (TaskInfo, bool)
	ListTasks(ctx context.Context) []TaskInfo
	AnswerTask(ctx context.Context, id, questionID string, answer map[string]any) error
	CancelTask(ctx context.Context, id string) error
}
