package session

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
	"unicode/utf8"
)

// liveTurnTTL is how long a finished turn stays readable after it ends.
const liveTurnTTL = time.Hour

// liveToolOutputLimit caps the tool output kept for the live view.
const liveToolOutputLimit = 4000

// LiveTool is one tool call of a running turn.
type LiveTool struct {
	Name       string         `json:"name"`
	Args       map[string]any `json:"args,omitempty"`
	Output     string         `json:"output,omitempty"`
	Running    bool           `json:"running"`
	StartedAt  int64          `json:"started_at"`
	FinishedAt int64          `json:"finished_at,omitempty"`
}

// LiveStep is one model request of a running turn with its streamed answer and tools.
type LiveStep struct {
	Index     int        `json:"index"`
	Text      string     `json:"text"`
	Reasoning string     `json:"reasoning"`
	Tools     []LiveTool `json:"tools,omitempty"`
}

// LiveTurn is the in-memory state of a turn, kept briefly after it ends.
type LiveTurn struct {
	SessionID     string     `json:"session_id"`
	Running       bool       `json:"running"`
	StartedAt     int64      `json:"started_at"`
	UpdatedAt     int64      `json:"updated_at"`
	FinishedAt    int64      `json:"finished_at,omitempty"`
	Prompt        string     `json:"prompt,omitempty"`
	Model         string     `json:"model,omitempty"`
	HistoryCount  int        `json:"history_count"`
	Steps         []LiveStep `json:"steps"`
	StoppedReason string     `json:"stopped_reason,omitempty"`
	Error         string     `json:"error,omitempty"`
}

// beginLive registers a new live turn for the session and returns its handle.
func (o *Orchestrator) beginLive(sess *Session, prompt, model string, historyCount int) *LiveTurn {
	if o == nil || sess == nil || sess.ID == "" || sess.ID == "ephemeral" || sess.Kind == KindNone {
		return nil
	}
	now := o.nowFn().Unix()
	turn := &LiveTurn{
		SessionID:    sess.ID,
		Running:      true,
		StartedAt:    now,
		UpdatedAt:    now,
		Prompt:       prompt,
		Model:        model,
		HistoryCount: historyCount,
		Steps:        []LiveStep{},
	}

	o.liveMu.Lock()
	defer o.liveMu.Unlock()
	if o.live == nil {
		o.live = make(map[string]*LiveTurn)
	}
	o.pruneLiveLocked(now)
	o.live[sess.ID] = turn
	return turn
}

// applyLive folds a turn event into the live state of the given turn.
func (o *Orchestrator) applyLive(turn *LiveTurn, ev TurnEvent) {
	o.liveMu.Lock()
	defer o.liveMu.Unlock()
	if o.live[turn.SessionID] != turn {
		return
	}
	now := o.nowFn().Unix()
	turn.UpdatedAt = now

	switch ev.Type {
	case "text":
		step := liveStep(turn, ev.Step)
		step.Text += ev.Delta
	case "reasoning":
		step := liveStep(turn, ev.Step)
		step.Reasoning += ev.Delta
	case "tool_start":
		step := liveStep(turn, ev.Step)
		step.Tools = append(step.Tools, LiveTool{
			Name:      ev.ToolName,
			Args:      ev.ToolArgs,
			Running:   true,
			StartedAt: now,
		})
	case "tool_end":
		step := liveStep(turn, ev.Step)
		for i := len(step.Tools) - 1; i >= 0; i-- {
			if step.Tools[i].Running && step.Tools[i].Name == ev.ToolName {
				step.Tools[i].Running = false
				step.Tools[i].Output = liveToolOutput(ev.ToolOut)
				step.Tools[i].FinishedAt = now
				break
			}
		}
	}
}

// finishLive marks the live turn as finished with the final reason and error.
func (o *Orchestrator) finishLive(turn *LiveTurn, reason string, turnErr error) {
	o.liveMu.Lock()
	defer o.liveMu.Unlock()
	if o.live[turn.SessionID] != turn {
		return
	}
	turn.Running = false
	turn.FinishedAt = o.nowFn().Unix()
	turn.UpdatedAt = turn.FinishedAt
	turn.StoppedReason = reason
	if turnErr != nil {
		turn.Error = turnErr.Error()
	}
}

// GetLiveTurn returns a copy of the live turn for the session, if one exists.
func (o *Orchestrator) GetLiveTurn(sessionID string) (*LiveTurn, bool) {
	if o == nil || sessionID == "" {
		return nil, false
	}
	o.liveMu.Lock()
	defer o.liveMu.Unlock()
	o.pruneLiveLocked(o.nowFn().Unix())
	turn, ok := o.live[sessionID]
	if !ok {
		return nil, false
	}
	return turn.clone(), true
}

// LiveSessionIDs returns the IDs of sessions with a running turn.
func (o *Orchestrator) LiveSessionIDs() []string {
	if o == nil {
		return []string{}
	}
	o.liveMu.Lock()
	defer o.liveMu.Unlock()
	o.pruneLiveLocked(o.nowFn().Unix())
	ids := make([]string, 0, len(o.live))
	for id, turn := range o.live {
		if turn.Running {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// pruneLiveLocked drops finished turns that are older than the live view TTL.
func (o *Orchestrator) pruneLiveLocked(now int64) {
	cutoff := now - int64(liveTurnTTL/time.Second)
	for id, turn := range o.live {
		if !turn.Running && turn.FinishedAt > 0 && turn.FinishedAt < cutoff {
			delete(o.live, id)
		}
	}
}

func (t *LiveTurn) clone() *LiveTurn {
	out := *t
	out.Steps = make([]LiveStep, len(t.Steps))
	for i, step := range t.Steps {
		out.Steps[i] = step
		if step.Tools != nil {
			out.Steps[i].Tools = append([]LiveTool(nil), step.Tools...)
		}
	}
	return &out
}

// liveStep returns the step with the given 1-based index, growing the list as needed.
func liveStep(turn *LiveTurn, index int) *LiveStep {
	if index <= 0 {
		index = 1
	}
	for len(turn.Steps) < index {
		turn.Steps = append(turn.Steps, LiveStep{Index: len(turn.Steps) + 1})
	}
	return &turn.Steps[index-1]
}

// liveToolOutput renders a tool result for the live view and trims it to a readable size.
func liveToolOutput(value any) string {
	var out string
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		out = v
	default:
		data, err := json.Marshal(v)
		if err != nil {
			out = fmt.Sprint(v)
		} else {
			out = string(data)
		}
	}
	if len(out) <= liveToolOutputLimit {
		return out
	}
	cut := out[:liveToolOutputLimit]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "\n... (truncated)"
}
