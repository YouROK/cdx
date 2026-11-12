package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/plugin"
	"github.com/yourok/cdx/internal/question"
	"github.com/yourok/cdx/internal/session"
)

// DefaultTaskTimeout bounds a background turn when the caller sets none.
const DefaultTaskTimeout = 15 * time.Minute

// MaxTaskTimeout caps the lifetime requested by a caller.
const MaxTaskTimeout = 2 * time.Hour

// taskManager runs background turns owned by a plugin, such as an MCP task.
// A turn keeps running when its caller disconnects and stops only on its own
// timeout, an explicit cancel, or a daemon shutdown.
type taskManager struct {
	sessions *session.Orchestrator
	registry *plugin.Registry
	log      *logx.Scope

	mu    sync.Mutex
	tasks map[string]*task
	seq   uint64
}

// task is one background turn and the question it may be waiting for.
type task struct {
	id        string
	sessionID string
	owner     string
	cancel    context.CancelFunc
	createdAt time.Time

	mu       sync.Mutex
	status   string
	result   string
	stopped  string
	errText  string
	question *taskQuestion
}

// taskQuestion is a question the turn is blocked on until the caller answers.
type taskQuestion struct {
	id    string
	kind  string
	title string
	body  string
	slots chan question.Answer
}

// newTaskManager creates an empty manager for background turns.
func newTaskManager(sessions *session.Orchestrator, registry *plugin.Registry, log *logx.Scope) *taskManager {
	return &taskManager{sessions: sessions, registry: registry, log: log, tasks: map[string]*task{}}
}

// StartTask stores a new session and runs the turn in the background.
func (m *taskManager) StartTask(ctx context.Context, req plugin.TaskRequest) (plugin.TaskInfo, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return plugin.TaskInfo{}, errors.New("a task prompt is required")
	}
	workdir := strings.TrimSpace(req.Workdir)
	if workdir == "" {
		return plugin.TaskInfo{}, errors.New("a task working directory is required")
	}
	if info, err := os.Stat(workdir); err != nil || !info.IsDir() {
		return plugin.TaskInfo{}, fmt.Errorf("the task working directory %q is not usable", workdir)
	}

	if m.registry != nil {
		models, err := m.registry.ListModels(context.Background())
		if err == nil && len(models) == 0 {
			return plugin.TaskInfo{}, errors.New("no model is configured on this daemon")
		}
	}

	timeout := DefaultTaskTimeout
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}
	if timeout > MaxTaskTimeout {
		timeout = MaxTaskTimeout
	}

	sess, err := m.sessions.CreateRootSession(workdir, session.KindTask, taskTitle(req.Prompt), strings.TrimSpace(req.Owner))
	if err != nil {
		return plugin.TaskInfo{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t := &task{
		id:        m.nextID(),
		sessionID: sess.ID,
		owner:     req.Owner,
		cancel:    cancel,
		createdAt: time.Now(),
		status:    plugin.TaskRunning,
	}
	ctx = question.WithAsker(ctx, taskAsker{t: t})

	m.mu.Lock()
	m.tasks[t.id] = t
	m.mu.Unlock()

	if m.log != nil {
		m.log.Info("Task %s started for %s", t.id, t.owner)
	}

	go func() {
		defer cancel()
		result, runErr := m.sessions.RunTurn(ctx, session.TurnRequest{
			SessionID:     sess.ID,
			Workdir:       workdir,
			Mode:          session.ModeNone,
			Prompt:        req.Prompt,
			Preset:        req.Preset,
			Model:         req.Model,
			MaxSteps:      req.MaxSteps,
			AllowedTools:  req.AllowedTools,
			StepLimitMode: session.StepLimitStop,
		}, nil)
		t.finish(result, runErr, ctx.Err())
		if m.log != nil {
			m.log.Info("Task %s finished with %s", t.id, t.statusOf())
		}
	}()

	return t.info(), nil
}

// GetTask returns one task snapshot.
func (m *taskManager) GetTask(_ context.Context, id string) (plugin.TaskInfo, bool) {
	m.mu.Lock()
	t := m.tasks[id]
	m.mu.Unlock()
	if t == nil {
		return plugin.TaskInfo{}, false
	}
	return t.info(), true
}

// ListTasks returns every task the daemon still knows.
func (m *taskManager) ListTasks(context.Context) []plugin.TaskInfo {
	m.mu.Lock()
	tasks := make([]*task, 0, len(m.tasks))
	for _, t := range m.tasks {
		tasks = append(tasks, t)
	}
	m.mu.Unlock()

	out := make([]plugin.TaskInfo, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.info())
	}
	return out
}

// AnswerTask delivers an answer to the question the turn is waiting for.
func (m *taskManager) AnswerTask(_ context.Context, id, questionID string, answer map[string]any) error {
	m.mu.Lock()
	t := m.tasks[id]
	m.mu.Unlock()
	if t == nil {
		return fmt.Errorf("task %q is not found", id)
	}
	return t.answer(questionID, question.AnswerFromMap(answer))
}

// CancelTask stops a running turn and releases its question.
func (m *taskManager) CancelTask(_ context.Context, id string) error {
	m.mu.Lock()
	t := m.tasks[id]
	m.mu.Unlock()
	if t == nil {
		return fmt.Errorf("task %q is not found", id)
	}
	t.mu.Lock()
	if t.status == plugin.TaskFinished || t.status == plugin.TaskFailed || t.status == plugin.TaskCancelled {
		t.mu.Unlock()
		return nil
	}
	t.status = plugin.TaskCancelled
	pending := t.question
	t.question = nil
	t.mu.Unlock()
	if pending != nil {
		select {
		case pending.slots <- question.Answer{Cancelled: true, Reason: "the task was cancelled"}:
		default:
		}
	}
	t.cancel()
	return nil
}

// CancelAll stops every task, which the daemon does before it closes the store.
func (m *taskManager) CancelAll() {
	m.mu.Lock()
	tasks := make([]*task, 0, len(m.tasks))
	for _, t := range m.tasks {
		tasks = append(tasks, t)
	}
	m.mu.Unlock()
	for _, t := range tasks {
		t.mu.Lock()
		pending := t.question
		t.question = nil
		if t.status == plugin.TaskRunning || t.status == plugin.TaskNeedsInput {
			t.status = plugin.TaskCancelled
		}
		t.mu.Unlock()
		if pending != nil {
			select {
			case pending.slots <- question.Answer{Cancelled: true, Reason: "the daemon is stopping"}:
			default:
			}
		}
		t.cancel()
	}
}

// nextID returns a short identifier that is unique inside the daemon.
func (m *taskManager) nextID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	return fmt.Sprintf("task_%d", m.seq)
}

// finish records the outcome of the turn.
func (t *task) finish(result *session.TurnResult, runErr, ctxErr error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.status == plugin.TaskCancelled {
		return
	}
	switch {
	case runErr == nil:
		t.status = plugin.TaskFinished
		if result != nil {
			t.result = result.Content
			t.stopped = result.StoppedReason
		}
	case errors.Is(ctxErr, context.DeadlineExceeded):
		t.status = plugin.TaskCancelled
		t.errText = "the task hit its time limit"
	case errors.Is(ctxErr, context.Canceled):
		t.status = plugin.TaskCancelled
		t.errText = "the task was cancelled"
	default:
		t.status = plugin.TaskFailed
		t.errText = runErr.Error()
	}
}

// statusOf returns the current status for logging.
func (t *task) statusOf() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

// info snapshots the task for a caller.
func (t *task) info() plugin.TaskInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := plugin.TaskInfo{
		ID:            t.id,
		SessionID:     t.sessionID,
		Status:        t.status,
		Result:        t.result,
		StoppedReason: t.stopped,
		Error:         t.errText,
	}
	if t.question != nil {
		out.Question = &plugin.TaskQuestion{
			ID:    t.question.id,
			Kind:  t.question.kind,
			Title: t.question.title,
			Body:  t.question.body,
		}
	}
	return out
}

// answer hands one answer to the waiting question.
func (t *task) answer(questionID string, answer question.Answer) error {
	t.mu.Lock()
	pending := t.question
	if pending == nil {
		t.mu.Unlock()
		return errors.New("the task is not waiting for an answer")
	}
	if questionID != "" && questionID != pending.id {
		t.mu.Unlock()
		return fmt.Errorf("task question %q is not the pending one", questionID)
	}
	// A remote answer never creates a persistent grant: the session and daemon
	// choices are downgraded to a single call.
	if answer.Choice == "always" || answer.Choice == "daemon" || answer.Choice == "session" {
		answer.Choice = "once"
	}
	t.question = nil
	if t.status == plugin.TaskNeedsInput {
		t.status = plugin.TaskRunning
	}
	t.mu.Unlock()
	select {
	case pending.slots <- answer:
		return nil
	default:
		return errors.New("the answer was already delivered")
	}
}

// taskAsker routes a turn question to the task and waits for the caller.
type taskAsker struct {
	t *task
}

func (a taskAsker) Ask(ctx context.Context, q question.Question) (question.Answer, error) {
	pending := &taskQuestion{
		id:    fmt.Sprintf("%s_q%d", a.t.id, time.Now().UnixNano()),
		kind:  q.Kind,
		title: q.Title,
		body:  q.Body,
		slots: make(chan question.Answer, 1),
	}

	a.t.mu.Lock()
	if a.t.question != nil {
		a.t.mu.Unlock()
		return question.Answer{Cancelled: true, Reason: "another question is already waiting"}, nil
	}
	a.t.question = pending
	a.t.status = plugin.TaskNeedsInput
	a.t.mu.Unlock()

	defer func() {
		a.t.mu.Lock()
		if a.t.question == pending {
			a.t.question = nil
			if a.t.status == plugin.TaskNeedsInput {
				a.t.status = plugin.TaskRunning
			}
		}
		a.t.mu.Unlock()
	}()

	select {
	case answer := <-pending.slots:
		return answer, nil
	case <-ctx.Done():
		return question.Answer{Cancelled: true, Reason: "the task stopped while waiting for an answer"}, nil
	}
}

// taskTitle derives a short session title from the prompt.
func taskTitle(prompt string) string {
	text := strings.Join(strings.Fields(prompt), " ")
	if len(text) > 60 {
		text = text[:60] + "..."
	}
	return text
}
