// Package session manages directory-scoped temporary, persistent, and stateless sessions and runs the agent turn loop.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/yourok/cdx/internal/bus"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/plugin"
	"github.com/yourok/cdx/internal/store"
)

// Session kinds.
const (
	KindTemp       = "temp"
	KindPersistent = "persistent"
	KindNone       = "none"
)

// Turn modes passed from CLI or API.
const (
	ModeAuto = "auto" // Use the active session for workdir, or create a temporary one.
	ModeNone = "none" // Stateless execution without reading or saving history.
)

// DefaultTempTTL is the default lifetime of a temporary directory session since its last update.
const DefaultTempTTL = 24 * time.Hour

const activeSessionsStateKey = "active_sessions"

// Message represents a single conversation message in a session.
type Message struct {
	Role       string           `json:"role"` // "user", "assistant", "tool"
	Content    string           `json:"content"`
	Reasoning  string           `json:"reasoning,omitempty"`
	Model      string           `json:"model,omitempty"`
	Provider   string           `json:"provider,omitempty"`
	TokensIn   int              `json:"tokens_in,omitempty"`
	TokensOut  int              `json:"tokens_out,omitempty"`
	ToolCalls  []map[string]any `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
	CreatedAt  int64            `json:"created_at"`
}

// Session represents a conversation bound to a working directory.
type Session struct {
	ID            string `json:"id"`
	Workdir       string `json:"workdir"`
	Kind          string `json:"kind"` // "temp" or "persistent"
	Model         string `json:"model,omitempty"`
	Summary       string `json:"summary,omitempty"`
	StoppedReason string `json:"stopped_reason,omitempty"`
	MessageCount  int    `json:"message_count"`
	TokensIn      int    `json:"tokens_in"`
	TokensOut     int    `json:"tokens_out"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

// IsExpired reports whether a temporary session has exceeded the given TTL at time now.
func (s *Session) IsExpired(now time.Time, ttl time.Duration) bool {
	if s == nil || s.Kind != KindTemp || ttl <= 0 {
		return false
	}
	last := time.Unix(s.UpdatedAt, 0)
	return now.Sub(last) > ttl
}

// TurnRequest describes a single user prompt execution.
type TurnRequest struct {
	Workdir   string
	Mode      string // ModeAuto or ModeNone.
	SessionID string // Optional explicit session ID.
	Model     string // Optional model override.
	Preset    string // Optional preset override (--preset / -p).
	Prompt    string
	MaxSteps  int
}

// TurnEvent is emitted during a turn (text deltas, reasoning deltas, tool starts/results).
type TurnEvent struct {
	Type     string         `json:"type"` // "session", "text", "reasoning", "tool_start", "tool_end", "done"
	Delta    string         `json:"delta,omitempty"`
	Session  *Session       `json:"session,omitempty"`
	ToolName string         `json:"tool_name,omitempty"`
	ToolArgs map[string]any `json:"tool_args,omitempty"`
	ToolOut  any            `json:"tool_out,omitempty"`
	Usage    map[string]any `json:"usage,omitempty"`
}

// TurnResult contains the final reply and session metadata after a turn finishes.
type TurnResult struct {
	Session       *Session       `json:"session"`
	Content       string         `json:"content"`
	Reasoning     string         `json:"reasoning,omitempty"`
	Steps         int            `json:"steps"`
	StoppedReason string         `json:"stopped_reason,omitempty"`
	Usage         map[string]any `json:"usage,omitempty"`
}

// Orchestrator manages sessions in BoltDB and executes multi-step agent turns.
type Orchestrator struct {
	store    *store.Store
	bus      *bus.Bus
	registry *plugin.Registry
	log      *logx.Scope
	nowFn    func() time.Time

	activeMu sync.Mutex
}

// NewOrchestrator creates a session Orchestrator.
func NewOrchestrator(st *store.Store, eb *bus.Bus, reg *plugin.Registry, logger *logx.Logger) *Orchestrator {
	if logger == nil {
		logger = logx.New()
	}
	return &Orchestrator{
		store:    st,
		bus:      eb,
		registry: reg,
		log:      logger.Tagged("Session"),
		nowFn:    time.Now,
	}
}

// DefaultMaxSteps is the default number of model/tool iterations per turn.
const DefaultMaxSteps = 40

// Step-limit modes stored in the step_limit_mode setting.
const (
	StepLimitNone = "none" // The turn runs without a tool-step limit.
	StepLimitAsk  = "ask"  // The client is asked before extending the budget.
	StepLimitStop = "stop" // The turn stops at the budget.
)

// MaxSteps returns the configured tool-step budget or DefaultMaxSteps.
func (o *Orchestrator) MaxSteps() int {
	if o.store == nil {
		return DefaultMaxSteps
	}
	var steps int
	found, err := o.store.GetState("max_steps", &steps)
	if err == nil && found && steps >= 1 && steps <= 200 {
		return steps
	}
	return DefaultMaxSteps
}

// SetMaxSteps updates the per-turn tool-step budget.
func (o *Orchestrator) SetMaxSteps(steps int) error {
	if steps < 1 || steps > 200 {
		return fmt.Errorf("max steps must be between 1 and 200")
	}
	return o.store.PutState("max_steps", steps)
}

// StepLimitMode returns what happens when a turn exhausts its step budget.
func (o *Orchestrator) StepLimitMode() string {
	if o.store == nil {
		return StepLimitAsk
	}
	var mode string
	if found, err := o.store.GetState("step_limit_mode", &mode); err == nil && found {
		switch mode {
		case StepLimitNone, StepLimitAsk, StepLimitStop:
			return mode
		}
	}
	// Settings written before the mode existed: a zero budget meant no limit,
	// and ask_on_max_steps chose between asking and stopping.
	var steps int
	if found, err := o.store.GetState("max_steps", &steps); err == nil && found && steps == 0 {
		return StepLimitNone
	}
	var ask bool
	if found, err := o.store.GetState("ask_on_max_steps", &ask); err == nil && found && !ask {
		return StepLimitStop
	}
	return StepLimitAsk
}

// SetStepLimitMode updates what happens when a turn exhausts its step budget.
func (o *Orchestrator) SetStepLimitMode(mode string) error {
	switch mode {
	case StepLimitNone, StepLimitAsk, StepLimitStop:
		return o.store.PutState("step_limit_mode", mode)
	}
	return fmt.Errorf("step limit mode must be none, ask, or stop")
}

func (o *Orchestrator) askContinue(ctx context.Context, steps, budget, add int) bool {
	if o.bus == nil {
		return false
	}
	answer, err := o.bus.Request(ctx, "session", "client:prompt_user", map[string]any{
		"kind":      "max_steps",
		"steps":     steps,
		"max_steps": budget,
		"add_steps": add,
		"question":  fmt.Sprintf("Model made %d requests. Continue?", steps),
		"options":   []string{"continue", "stop"},
	})
	if err != nil {
		return false
	}
	m, _ := answer.(map[string]any)
	allow, _ := m["allow"].(bool)
	return allow
}

// TempTTL returns the configured temporary session TTL or DefaultTempTTL (24h).
func (o *Orchestrator) TempTTL() time.Duration {
	if o.store == nil {
		return DefaultTempTTL
	}
	var hours int
	found, err := o.store.GetState("temp_session_ttl_hours", &hours)
	if err == nil && found && hours > 0 {
		return time.Duration(hours) * time.Hour
	}
	return DefaultTempTTL
}

// SetTempTTLHours updates the temporary session lifetime in hours.
func (o *Orchestrator) SetTempTTLHours(hours int) error {
	if hours <= 0 {
		return fmt.Errorf("ttl hours must be positive")
	}
	return o.store.PutState("temp_session_ttl_hours", hours)
}

// ResolveForWorkdir finds the active session for a working directory or creates a temporary one.
func (o *Orchestrator) ResolveForWorkdir(workdir, mode string) (*Session, error) {
	workdir = normalizeWorkdir(workdir)
	now := o.nowFn()

	if normalizeMode(mode) == ModeNone {
		return &Session{
			ID:        "ephemeral",
			Workdir:   workdir,
			Kind:      KindNone,
			CreatedAt: now.Unix(),
			UpdatedAt: now.Unix(),
		}, nil
	}

	active, err := o.ActiveForWorkdir(workdir)
	if err != nil {
		return nil, err
	}
	if active != nil {
		return active, nil
	}
	return o.createSession(workdir, KindTemp, now)
}

// ActiveForWorkdir returns the nearest active session in workdir or its parents, or nil if none exists.
func (o *Orchestrator) ActiveForWorkdir(workdir string) (*Session, error) {
	workdir = normalizeWorkdir(workdir)
	active := o.snapshotActiveSessions()
	now := o.nowFn()
	ttl := o.TempTTL()

	dir := workdir
	for {
		if id := active[dir]; id != "" {
			s, found, err := o.GetSession(id)
			if err != nil {
				return nil, err
			}
			if found && s.Workdir == dir {
				if !s.IsExpired(now, ttl) {
					return s, nil
				}
				_ = o.DeleteSession(id)
			} else {
				_ = o.deleteActiveEntry(dir, id)
			}
		}

		best, err := o.bestSessionInDir(dir)
		if err != nil {
			return nil, err
		}
		if best != nil {
			_ = o.setActiveSession(best.Workdir, best.ID)
			return best, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil
		}
		dir = parent
	}
}

// NewSession creates a new persistent session in workdir and makes it active there.
func (o *Orchestrator) NewSession(workdir string) (*Session, error) {
	return o.createSession(normalizeWorkdir(workdir), KindPersistent, o.nowFn())
}

// SetActiveSession selects an existing session as active for its directory.
func (o *Orchestrator) SetActiveSession(sessionID string) (*Session, error) {
	s, found, err := o.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("session %q is not found", sessionID)
	}
	if err := o.setActiveSession(s.Workdir, s.ID); err != nil {
		return nil, err
	}
	return s, nil
}

// SetSessionKind changes a session between temporary and persistent.
func (o *Orchestrator) SetSessionKind(sessionID, kind string) (*Session, error) {
	normalized, err := normalizeKind(kind)
	if err != nil {
		return nil, err
	}
	s, found, err := o.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("session %q is not found", sessionID)
	}
	s.Kind = normalized
	s.UpdatedAt = o.nowFn().Unix()
	if err := o.saveSession(s); err != nil {
		return nil, err
	}
	return s, nil
}

// SetActiveSessionKind changes the kind of the active session for workdir.
func (o *Orchestrator) SetActiveSessionKind(workdir, kind string) (*Session, error) {
	s, err := o.ActiveForWorkdir(workdir)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("no active session in %s", normalizeWorkdir(workdir))
	}
	return o.SetSessionKind(s.ID, kind)
}

// PromoteSession converts a temporary session into a persistent session.
func (o *Orchestrator) PromoteSession(sessionID string) (*Session, error) {
	return o.SetSessionKind(sessionID, KindPersistent)
}

func (o *Orchestrator) bestSessionInDir(dir string) (*Session, error) {
	all, err := o.ListSessions()
	if err != nil {
		return nil, err
	}
	var best *Session
	for i := range all {
		s := &all[i]
		if s.Workdir != dir {
			continue
		}
		if best == nil {
			best = s
			continue
		}
		if s.Kind == KindPersistent && best.Kind != KindPersistent {
			best = s
			continue
		}
		if s.Kind == best.Kind && s.UpdatedAt > best.UpdatedAt {
			best = s
		}
	}
	return best, nil
}

func (o *Orchestrator) snapshotActiveSessions() map[string]string {
	o.activeMu.Lock()
	defer o.activeMu.Unlock()

	out := map[string]string{}
	if o.store != nil {
		_, _ = o.store.GetState(activeSessionsStateKey, &out)
	}
	if out == nil {
		out = map[string]string{}
	}
	return out
}

func (o *Orchestrator) updateActiveSessions(fn func(map[string]string) bool) error {
	o.activeMu.Lock()
	defer o.activeMu.Unlock()

	out := map[string]string{}
	if o.store != nil {
		_, _ = o.store.GetState(activeSessionsStateKey, &out)
	}
	if out == nil {
		out = map[string]string{}
	}
	if fn != nil && !fn(out) {
		return nil
	}
	if o.store == nil {
		return nil
	}
	return o.store.PutState(activeSessionsStateKey, out)
}

func (o *Orchestrator) setActiveSession(workdir, sessionID string) error {
	return o.updateActiveSessions(func(m map[string]string) bool {
		if m[workdir] == sessionID {
			return false
		}
		m[workdir] = sessionID
		return true
	})
}

func (o *Orchestrator) deleteActiveEntry(workdir, sessionID string) error {
	return o.updateActiveSessions(func(m map[string]string) bool {
		if m[workdir] != sessionID {
			return false
		}
		delete(m, workdir)
		return true
	})
}

func (o *Orchestrator) removeActiveSession(sessionID string) error {
	return o.updateActiveSessions(func(m map[string]string) bool {
		changed := false
		for dir, id := range m {
			if id == sessionID {
				delete(m, dir)
				changed = true
			}
		}
		return changed
	})
}

// GetSession retrieves a session by ID.
func (o *Orchestrator) GetSession(id string) (*Session, bool, error) {
	var s Session
	var found bool
	err := o.store.DB().View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(store.BucketSessions))
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(id))
		if raw == nil {
			return nil
		}
		found = true
		return json.Unmarshal(raw, &s)
	})
	if err != nil || !found {
		return nil, false, err
	}
	return &s, true, nil
}

// ListSessions returns all stored sessions sorted by UpdatedAt descending, pruning expired temp sessions first.
func (o *Orchestrator) ListSessions() ([]Session, error) {
	now := o.nowFn()
	ttl := o.TempTTL()

	var all []Session
	var expiredIDs []string

	err := o.store.DB().View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(store.BucketSessions))
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, v []byte) error {
			var s Session
			if err := json.Unmarshal(v, &s); err != nil {
				return nil
			}
			if s.IsExpired(now, ttl) {
				expiredIDs = append(expiredIDs, s.ID)
				return nil
			}
			all = append(all, s)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	for _, id := range expiredIDs {
		_ = o.DeleteSession(id)
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].UpdatedAt > all[j].UpdatedAt
	})
	return all, nil
}

// DeleteSession removes a session and its message history from BoltDB.
func (o *Orchestrator) DeleteSession(id string) error {
	err := o.store.DB().Update(func(tx *bolt.Tx) error {
		if b := tx.Bucket([]byte(store.BucketSessions)); b != nil {
			_ = b.Delete([]byte(id))
		}
		if b := tx.Bucket([]byte(store.BucketMessages)); b != nil {
			_ = b.Delete([]byte(id))
		}
		return nil
	})
	_ = o.removeActiveSession(id)
	return err
}

// GetMessages returns the stored message list for sessionID.
func (o *Orchestrator) GetMessages(sessionID string) ([]Message, error) {
	if sessionID == "" || sessionID == "ephemeral" {
		return nil, nil
	}
	var msgs []Message
	err := o.store.DB().View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(store.BucketMessages))
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(sessionID))
		if raw == nil {
			return nil
		}
		return json.Unmarshal(raw, &msgs)
	})
	return msgs, err
}

// SaveMessages replaces the stored message list for a session and updates its metadata.
func (o *Orchestrator) SaveMessages(s *Session, msgs []Message) error {
	if s == nil || s.Kind == KindNone || s.ID == "ephemeral" {
		return nil
	}
	s.MessageCount = len(msgs)
	s.UpdatedAt = o.nowFn().Unix()

	return o.store.DB().Update(func(tx *bolt.Tx) error {
		sb := tx.Bucket([]byte(store.BucketSessions))
		mb := tx.Bucket([]byte(store.BucketMessages))
		if sb == nil || mb == nil {
			return fmt.Errorf("session buckets are not initialized")
		}
		sBytes, err := json.Marshal(s)
		if err != nil {
			return err
		}
		mBytes, err := json.Marshal(msgs)
		if err != nil {
			return err
		}
		if err := sb.Put([]byte(s.ID), sBytes); err != nil {
			return err
		}
		return mb.Put([]byte(s.ID), mBytes)
	})
}

// RunTurn executes a full user turn, including hooks, provider streaming, and tool call iterations.
func (o *Orchestrator) RunTurn(ctx context.Context, req TurnRequest, onEvent func(TurnEvent)) (*TurnResult, error) {
	var sess *Session
	var err error
	if req.SessionID != "" {
		var found bool
		sess, found, err = o.GetSession(req.SessionID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("session %q is not found", req.SessionID)
		}
	} else {
		sess, err = o.ResolveForWorkdir(req.Workdir, req.Mode)
		if err != nil {
			return nil, err
		}
	}
	ctx = plugin.WithWorkdir(ctx, sess.Workdir)

	if onEvent != nil {
		onEvent(TurnEvent{Type: "session", Session: sess})
	}

	history, err := o.GetMessages(sess.ID)
	if err != nil {
		return nil, err
	}
	history = repairToolCalls(history)

	now := o.nowFn().Unix()
	history = append(history, Message{
		Role:      "user",
		Content:   req.Prompt,
		CreatedAt: now,
	})

	// Persist partial progress, so an interrupted turn keeps what it did.
	save := func() {
		if err := o.SaveMessages(sess, history); err != nil {
			o.log.Warn("Failed to save session messages: %v", err)
		}
	}
	completed := false
	defer func() {
		if completed {
			return
		}
		sess.StoppedReason = "error"
		if ctx.Err() != nil {
			sess.StoppedReason = "interrupted"
		}
		save()
	}()
	save()

	modelName := req.Model
	if modelName == "" {
		modelName = sess.Model
	}

	var finalContent, finalReasoning string
	var lastUsage map[string]any
	steps := 0
	finished := false
	budget := req.MaxSteps
	if budget <= 0 {
		budget = o.MaxSteps()
	}
	unlimited := o.StepLimitMode() == StepLimitNone
	if unlimited {
		budget = 0
	}

	for {
		for unlimited || steps < budget {
			steps++
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			// Build raw message maps and tool specs for hooks and provider.
			wireMsgs := messagesToMaps(history)
			toolSpecs := o.buildToolDefs()
			systemPrompt := ""

			// 1. Run session:before_turn hooks (context plugin can set system_prompt, summary, or compact messages).
			if o.bus != nil {
				hookPayload := map[string]any{
					"session_id":    sess.ID,
					"workdir":       sess.Workdir,
					"session_kind":  sess.Kind,
					"model":         modelName,
					"preset":        req.Preset,
					"max_steps":     budget,
					"system_prompt": systemPrompt,
					"summary":       sess.Summary,
					"messages":      wireMsgs,
					"tools":         toolSpecs,
				}
				hookOut, hookErr := o.bus.RunHook(ctx, "session:before_turn", hookPayload)
				if hookErr != nil {
					return nil, hookErr
				}
				if sp, ok := hookOut["system_prompt"].(string); ok {
					systemPrompt = sp
				}
				if sum, ok := hookOut["summary"].(string); ok {
					sess.Summary = sum
				}
				if mList, ok := toMapSlice(hookOut["messages"]); ok {
					wireMsgs = mList
				}
			}

			// 2. Call the provider plugin.
			wireMsgs = sanitizeWireMessages(wireMsgs)
			compResp, err := o.registry.Complete(ctx, plugin.CompleteRequest{
				Model:        modelName,
				SystemPrompt: systemPrompt,
				Messages:     wireMsgs,
				Tools:        toolSpecs,
				Options:      map[string]any{"session_id": sess.ID},
			}, func(se plugin.StreamEvent) {
				if onEvent == nil {
					return
				}
				switch se.Type {
				case "text":
					onEvent(TurnEvent{Type: "text", Delta: se.Delta})
				case "reasoning":
					onEvent(TurnEvent{Type: "reasoning", Delta: se.Delta})
				}
			})
			if err != nil {
				return nil, err
			}

			finalContent = compResp.Content
			finalReasoning = compResp.Reasoning
			tokIn, tokOut := extractUsageTokens(compResp.Usage, wireMsgs, compResp.Content)
			sess.TokensIn += tokIn
			sess.TokensOut += tokOut
			if compResp.Model != "" {
				sess.Model = compResp.Model
			}
			if compResp.Usage != nil {
				lastUsage = compResp.Usage
			}

			for idx := range compResp.ToolCalls {
				if id, _ := compResp.ToolCalls[idx]["id"].(string); id == "" {
					compResp.ToolCalls[idx]["id"] = fmt.Sprintf("call_%d_%d", steps, idx)
				}
			}

			history = append(history, Message{
				Role:      "assistant",
				Content:   compResp.Content,
				Reasoning: compResp.Reasoning,
				Model:     compResp.Model,
				Provider:  compResp.Provider,
				TokensIn:  tokIn,
				TokensOut: tokOut,
				ToolCalls: compResp.ToolCalls,
				CreatedAt: o.nowFn().Unix(),
			})
			save()

			// If the assistant made no tool calls, the turn is complete.
			if len(compResp.ToolCalls) == 0 {
				finished = true
				break
			}

			// 3. Execute requested tool calls.
			for idx, tc := range compResp.ToolCalls {
				toolName, _ := tc["name"].(string)
				callID, _ := tc["id"].(string)
				if callID == "" {
					callID = fmt.Sprintf("call_%d_%d", steps, idx)
				}
				args, _ := tc["arguments"].(map[string]any)
				if args == nil {
					args = map[string]any{}
				}

				// Run tool:before_call hook (permissions plugin can allow, modify, or return error to deny).
				if o.bus != nil {
					spec, _ := o.registry.GetTool(toolName)
					hookOut, hookErr := o.bus.RunHook(ctx, "tool:before_call", map[string]any{
						"session_id": sess.ID,
						"workdir":    sess.Workdir,
						"tool":       toolName,
						"permission": spec.Permission,
						"arguments":  args,
					})
					if hookErr != nil {
						toolErrStr := fmt.Sprintf("error: %v", hookErr)
						history = append(history, Message{
							Role:       "tool",
							Name:       toolName,
							ToolCallID: callID,
							Content:    toolErrStr,
							CreatedAt:  o.nowFn().Unix(),
						})
						continue
					}
					if modArgs, ok := hookOut["arguments"].(map[string]any); ok {
						args = modArgs
					}
				}

				if onEvent != nil {
					onEvent(TurnEvent{Type: "tool_start", ToolName: toolName, ToolArgs: args})
				}

				toolOut, callErr := o.registry.CallTool(ctx, toolName, args)
				var contentStr string
				if callErr != nil {
					contentStr = fmt.Sprintf("error: %v", callErr)
				} else {
					contentStr = formatToolOutput(toolOut)
				}

				// Run tool:after_call hook.
				if o.bus != nil {
					hookOut, hookErr := o.bus.RunHook(ctx, "tool:after_call", map[string]any{
						"session_id": sess.ID,
						"workdir":    sess.Workdir,
						"tool":       toolName,
						"arguments":  args,
						"output":     contentStr,
					})
					if hookErr == nil {
						if modOut, ok := hookOut["output"].(string); ok {
							contentStr = modOut
						}
					}
				}

				if onEvent != nil {
					onEvent(TurnEvent{Type: "tool_end", ToolName: toolName, ToolArgs: args, ToolOut: contentStr})
				}

				history = append(history, Message{
					Role:       "tool",
					Name:       toolName,
					ToolCallID: callID,
					Content:    contentStr,
					CreatedAt:  o.nowFn().Unix(),
				})
				save()
			}
		}

		if finished {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if unlimited {
			break
		}
		// Re-read the settings so a change made during the turn takes effect now.
		mode := o.StepLimitMode()
		if mode == StepLimitNone {
			unlimited = true
			budget = 0
			continue
		}
		if mode != StepLimitAsk {
			break
		}
		next := o.MaxSteps()
		if !o.askContinue(ctx, steps, budget, next) {
			break
		}
		budget += next
	}

	stoppedReason := ""
	if !finished {
		stoppedReason = "max_steps"
	} else if strings.TrimSpace(finalContent) == "" {
		stoppedReason = "empty_answer"
	}
	sess.StoppedReason = stoppedReason
	if err := o.SaveMessages(sess, history); err != nil {
		return nil, err
	}
	completed = true

	if onEvent != nil {
		onEvent(TurnEvent{Type: "done", Session: sess, Usage: lastUsage})
	}

	return &TurnResult{
		Session:       sess,
		Content:       finalContent,
		Reasoning:     finalReasoning,
		Steps:         steps,
		StoppedReason: stoppedReason,
		Usage:         lastUsage,
	}, nil
}

func (o *Orchestrator) createSession(workdir, kind string, now time.Time) (*Session, error) {
	s := &Session{
		ID:        newSessionID(),
		Workdir:   workdir,
		Kind:      kind,
		CreatedAt: now.Unix(),
		UpdatedAt: now.Unix(),
	}
	if err := o.saveSession(s); err != nil {
		return nil, err
	}
	if err := o.setActiveSession(s.Workdir, s.ID); err != nil {
		return nil, err
	}
	return s, nil
}

func (o *Orchestrator) saveSession(s *Session) error {
	return o.store.DB().Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(store.BucketSessions))
		if b == nil {
			return fmt.Errorf("sessions bucket is not initialized")
		}
		data, err := json.Marshal(s)
		if err != nil {
			return err
		}
		return b.Put([]byte(s.ID), data)
	})
}

func (o *Orchestrator) buildToolDefs() []map[string]any {
	if o.registry == nil {
		return nil
	}
	specs := o.registry.ListTools()
	out := make([]map[string]any, 0, len(specs))
	for _, s := range specs {
		out = append(out, map[string]any{
			"name":         s.Name,
			"description":  s.Description,
			"input_schema": s.InputSchema,
		})
	}
	return out
}

// repairToolCalls drops tool calls and results that an interrupted turn left unanswered.
func repairToolCalls(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for i := 0; i < len(msgs); {
		m := msgs[i]
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			out = append(out, m)
			i++
			continue
		}
		j := i + 1
		answered := map[string]bool{}
		for ; j < len(msgs) && msgs[j].Role == "tool"; j++ {
			if id := msgs[j].ToolCallID; id != "" {
				answered[id] = true
			}
		}
		calls := make([]map[string]any, 0, len(m.ToolCalls))
		ids := map[string]bool{}
		for _, tc := range m.ToolCalls {
			id, _ := tc["id"].(string)
			if id != "" && answered[id] {
				calls = append(calls, tc)
				ids[id] = true
			}
		}
		if len(calls) == 0 {
			if strings.TrimSpace(m.Content) == "" && strings.TrimSpace(m.Reasoning) == "" {
				i = j
				continue
			}
			m.ToolCalls = nil
			out = append(out, m)
			i = j
			continue
		}
		m.ToolCalls = calls
		out = append(out, m)
		for k := i + 1; k < j; k++ {
			if ids[msgs[k].ToolCallID] {
				out = append(out, msgs[k])
			}
		}
		i = j
	}
	return out
}

func sanitizeWireMessages(msgs []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	allowTools := false
	allowedIDs := map[string]bool{}

	for _, m := range msgs {
		role, _ := m["role"].(string)
		switch role {
		case "assistant":
			allowTools = false
			allowedIDs = map[string]bool{}
			switch calls := m["tool_calls"].(type) {
			case []map[string]any:
				allowTools = len(calls) > 0
				for _, tc := range calls {
					if id, _ := tc["id"].(string); id != "" {
						allowedIDs[id] = true
					}
				}
			case []any:
				allowTools = len(calls) > 0
				for _, raw := range calls {
					tc, _ := raw.(map[string]any)
					if id, _ := tc["id"].(string); id != "" {
						allowedIDs[id] = true
					}
				}
			}
			out = append(out, m)

		case "tool":
			if !allowTools {
				continue
			}
			id, _ := m["tool_call_id"].(string)
			if len(allowedIDs) > 0 && id != "" && !allowedIDs[id] {
				continue
			}
			out = append(out, m)

		default:
			allowTools = false
			allowedIDs = map[string]bool{}
			out = append(out, m)
		}
	}
	return out
}

func messagesToMaps(msgs []Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		item := map[string]any{
			"role":    m.Role,
			"content": m.Content,
		}
		if m.Reasoning != "" {
			item["reasoning"] = m.Reasoning
		}
		if len(m.ToolCalls) > 0 {
			item["tool_calls"] = m.ToolCalls
		}
		if m.ToolCallID != "" {
			item["tool_call_id"] = m.ToolCallID
		}
		if m.Name != "" {
			item["name"] = m.Name
		}
		out = append(out, item)
	}
	return out
}

func toMapSlice(v any) ([]map[string]any, bool) {
	if s, ok := v.([]map[string]any); ok {
		return s, true
	}
	rawSlice, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]map[string]any, 0, len(rawSlice))
	for _, elem := range rawSlice {
		if m, ok := elem.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, true
}

func formatToolOutput(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}

func normalizeWorkdir(w string) string {
	w = strings.TrimSpace(w)
	if w == "" {
		return "/"
	}
	return filepath.Clean(w)
}

func normalizeMode(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "no", "none", "stateless", "off":
		return ModeNone
	default:
		return ModeAuto
	}
}

func normalizeKind(k string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case KindTemp, "temporary":
		return KindTemp, nil
	case KindPersistent, "permanent", "save":
		return KindPersistent, nil
	default:
		return "", fmt.Errorf("unknown session mode %q (expected: temp, persistent)", k)
	}
}

func newSessionID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return "ses_" + hex.EncodeToString(buf)
}

func extractUsageTokens(usage map[string]any, msgs []map[string]any, reply string) (in int, out int) {
	if usage != nil {
		if v, ok := numToInt(usage["prompt_tokens"]); ok && v > 0 {
			in = v
		} else if v, ok := numToInt(usage["input_tokens"]); ok && v > 0 {
			in = v
		}
		if v, ok := numToInt(usage["completion_tokens"]); ok && v > 0 {
			out = v
		} else if v, ok := numToInt(usage["output_tokens"]); ok && v > 0 {
			out = v
		}
	}
	if in == 0 {
		chars := 0
		for _, m := range msgs {
			if c, ok := m["content"].(string); ok {
				chars += len(c)
			}
		}
		in = (chars + 3) / 4
	}
	if out == 0 && len(reply) > 0 {
		out = (len(reply) + 3) / 4
	}
	return in, out
}

func numToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}
