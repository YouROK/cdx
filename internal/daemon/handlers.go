package daemon

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/yourok/cdx/internal/agent"
	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/ctxb"
	"github.com/yourok/cdx/internal/ipc"
	"github.com/yourok/cdx/internal/llm"
	"github.com/yourok/cdx/internal/perm"
)

// Methods the daemon accepts.
const (
	MethodTurnRun        = "turn.run"
	MethodDaemonStatus   = "daemon.status"
	MethodDaemonStop     = "daemon.stop"
	MethodConfigGet      = "config.get"
	MethodPermissionsMe  = "permissions.list"
	MethodWebURL         = "web.url"
	MethodPluginsList    = "plugins.list"
	MethodMCPList        = "mcp.list"
	MethodMCPRestart     = "mcp.restart"
	MethodSessionCompact = "session.compact"
)

// TurnRequest holds turn parameters.
type TurnRequest struct {
	Prompt string    `json:"prompt"`
	CWD    string    `json:"cwd"`
	Model  string    `json:"model"`
	Output OutParams `json:"output"`
	Allow  []string  `json:"allow"`
	// Session selects the history to use: auto, none or an id.
	Session SessionMode `json:"session"`
	// Preset selects the agent preset for this call.
	Preset string `json:"preset"`
}

// SessionMode selects the session for a turn.
type SessionMode struct {
	Mode string `json:"mode"`
	ID   string `json:"id"`
}

// OutParams holds output parameters.
type OutParams struct {
	JSON     bool `json:"json"`
	Color    bool `json:"color"`
	Progress bool `json:"progress"`
}

// TurnResult is a finished turn as the client sees it.
type TurnResult struct {
	Result     string         `json:"result"`
	SessionID  string         `json:"session_id"`
	Error      *agent.Failure `json:"error"`
	Model      string         `json:"model"`
	DurationMS int64          `json:"duration_ms"`
	Tokens     agent.Usage    `json:"tokens"`
	ToolsUsed  []string       `json:"tools_used"`
	Progress   []progressLine `json:"progress,omitempty"`
}

type progressLine struct {
	Seq   int    `json:"seq"`
	Topic string `json:"topic"`
	Tool  string `json:"tool,omitempty"`
}

// handleRequest dispatches a method request.
func (d *Daemon) handleRequest(c *session, f *ipc.Frame) {
	switch f.Method {
	case MethodTurnRun:
		d.handleTurn(c, f)
	case MethodDaemonStatus:
		_ = c.conn.Write(ipc.Success(f.ID, map[string]any{
			"state":    d.State(),
			"version":  versionString(),
			"proto":    protoVersion(),
			"pid":      pidOf(),
			"uptime_s": d.Uptime(),
		}))
	case MethodDaemonStop:
		_ = c.conn.Write(ipc.Success(f.ID, map[string]any{"stopping": true}))
		go d.Stop()
	case MethodConfigGet:
		_ = c.conn.Write(ipc.Success(f.ID, d.cfg))
	case MethodSessionNew, MethodSessionEnd, MethodSessionList, MethodSessionShow,
		MethodSessionUse, MethodSessionDelete, MethodSessionWriter:
		d.handleSession(c, f)
	case MethodPluginsList:
		payload, err := d.handlePlugin(f.Method, nil)
		if err != nil {
			_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeInternal, err.Error())))
			return
		}
		_ = c.conn.Write(ipc.Success(f.ID, payload))
	case MethodMCPList:
		payload, err := d.handleMCP(f.Method, nil)
		if err != nil {
			_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeInternal, err.Error())))
			return
		}
		_ = c.conn.Write(ipc.Success(f.ID, payload))
	case MethodMCPRestart:
		var req struct {
			Name string `json:"name"`
		}
		_ = decodePayload(f, &req)
		payload, err := d.handleMCP(f.Method, req.Name)
		if err != nil {
			_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeInternal, err.Error())))
			return
		}
		_ = c.conn.Write(ipc.Success(f.ID, payload))
	case MethodWebURL:
		d.mu.Lock()
		url := d.webURL
		failure := d.webError
		d.mu.Unlock()

		if url == "" {
			msg := "web interface is not running"
			if failure != "" {
				msg = failure
			}
			_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeInternal, msg)))
			return
		}
		_ = c.conn.Write(ipc.Success(f.ID, map[string]any{"url": url}))
	case MethodSessionCompact:
		d.handleSessionCompact(c, f)
	case MethodPermissionsMe:
		_ = c.conn.Write(ipc.Success(f.ID, map[string]any{
			"read":            d.cfg.Perms.Read,
			"edit":            d.cfg.Perms.Edit,
			"dangerous":       d.cfg.Perms.Dangerous,
			"non_interactive": d.cfg.Perms.NonInteractive,
		}))
	default:
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeUnknownMethod,
			fmt.Sprintf("method %q is not supported", f.Method))))
	}
}

// handleTurn runs one agent turn.
func (d *Daemon) handleTurn(c *session, f *ipc.Frame) {
	var req TurnRequest
	if err := decodePayload(f, &req); err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, err.Error())))
		return
	}
	if req.Prompt == "" {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, "prompt is required")))
		return
	}

	// Turns run one at a time: a second client waits for its slot.
	d.turnMu.Lock()
	defer d.turnMu.Unlock()

	provider, modelName, ms, contextWindow, err := d.provider(req.Model)
	if err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeInternal, err.Error())))
		return
	}

	// The turn is registered so the client can cancel it with a cancel
	// frame, and a dropped connection cancels all its turns.
	ctx, cancel := d.turns.register(c, f.ID)
	defer cancel()

	// The connection and the turn directory are remembered: the first
	// carries plugin permission asks, the second is the path root.
	cwd := req.CWD
	if cwd == "" {
		cwd = d.cwd
	}
	d.mu.Lock()
	d.activeSession = c
	d.turnCWD = cwd
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.activeSession = nil
		d.turnCWD = ""
		d.mu.Unlock()
	}()

	// Turn details are shown in the web interface and allow cancelling.
	info := turnInfo{
		ID:      f.ID,
		Model:   modelName,
		Preset:  req.Preset,
		Prompt:  req.Prompt,
		Started: time.Now().Unix(),
		CWD:     req.CWD,
	}
	if info.Preset == "" {
		info.Preset = d.cfg.AgentPreset.Active
	}
	d.turns.setInfo(c, info)
	defer d.turns.dropInfo(c, f.ID)

	// Permissions apply to this turn only and are cleared after it: a
	// past --allow edit would otherwise grant access forever.
	d.perms.SetAllow(req.Allow)
	defer d.perms.ClearAllow()

	// The session is resolved before the turn: its history is context.
	target, err := d.turnSession(req)
	if err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, err.Error())))
		return
	}

	var seq int64
	emit := func(ev agent.Event) {
		seq++
		data := map[string]any{}
		for k, v := range ev.Data {
			data[k] = v
		}
		data["seq"] = seq
		if req.Output.Progress || req.Output.JSON {
			_ = c.conn.Write(ipc.Event(f.ID, ev.Topic, data))
		}
	}

	ask := d.makeAsk(c)

	// The preset sets the explaining prompt part; the dynamic part is shared.
	prompt, err := d.presetPrompt(req.Preset)
	if err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, err.Error())))
		return
	}
	sessionRoot := ""
	if target != nil {
		sessionRoot = target.Root
	}

	// Block order follows the spec: prompt, files, environment.
	files := toContextFiles(d.contextFiles(req.CWD, sessionRoot))
	parts := agent.BuildParts(prompt, d.cfg, req.CWD, sessionRoot, files).
		WithTools(d.reg.LLMSpecs(), d.cfg, req.CWD, sessionRoot)
	sysPrompt := parts.Render()

	// The history budget excludes the fixed blocks and the request.
	// The window comes from the model: there is no global threshold.
	fixed := ctxb.EstimateTokens(sysPrompt) + ctxb.EstimateTokens(req.Prompt) + d.toolsTokens()
	history := d.loadHistory(target, fixed, contextWindow)
	promptHash := parts.Hash()

	res := agent.Run(ctx, agent.Options{
		Provider:        provider,
		Model:           modelName,
		MaxTokens:       ms.MaxTokens,
		Temperature:     ms.Temperature,
		ReasoningEffort: ms.Reasoning,
		MaxTokensField:  ms.MaxTokensField,
		SkipTemperature: ms.SkipTemperature,
		Registry:        d.reg,
		Perms:           d.perms,
		Prompt:          sysPrompt,
		CWD:             req.CWD,
		MaxIters:        d.cfg.Agent.MaxIterations,
		LLMTimeout:      config.MS(d.cfg.Agent.LLMTimeoutMS),
		ToolMaxByte:     d.cfg.Agent.ToolResultMaxBytes,
		Emit:            emit,
		Ask:             ask,
		History:         history,
		// Reasoning streams only for interactive output: in JSON and
		// quiet modes it would be noise.
		ShowThinking: req.Output.Progress,
	}, req.Prompt)

	// A turn is stored only when a session is active.
	if target != nil {
		d.recordTurn(target, req.Prompt, res, modelName, promptHash)
	}

	out := TurnResult{
		Result:     res.Result,
		Error:      res.Error,
		Model:      res.Model,
		DurationMS: res.DurationM,
		Tokens:     res.Tokens,
		ToolsUsed:  res.ToolsUsed,
	}
	if target != nil {
		out.SessionID = target.ID
	}
	_ = c.conn.Write(ipc.Success(f.ID, out))
}

// makeAsk builds the permission asker for a client.
func (d *Daemon) makeAsk(c *session) agent.AskFunc {
	var counter atomic.Int64
	return func(ctx context.Context, level, tool, summary string, options []string) (string, error) {
		id := fmt.Sprintf("ask-%d", counter.Add(1))
		payload := map[string]any{
			"level":      level,
			"tool":       tool,
			"summary":    summary,
			"options":    options,
			"timeout_ms": 120000,
		}
		if err := c.conn.Write(&ipc.Frame{Type: ipc.TypeReq, ID: id, Method: "permission.ask", Payload: payload}); err != nil {
			return perm.ChoiceDeny, err
		}

		// No answer in time counts as a refusal.
		ch := c.await(id)
		defer c.forget(id)

		select {
		case <-ctx.Done():
			return perm.ChoiceDeny, ctx.Err()
		case <-time.After(120 * time.Second):
			return perm.ChoiceDeny, fmt.Errorf("permission request timed out")
		case frame := <-ch:
			var body struct {
				Choice string `json:"choice"`
			}
			if err := decodePayload(frame, &body); err != nil {
				return perm.ChoiceDeny, err
			}
			if body.Choice == "" {
				return perm.ChoiceDeny, nil
			}
			return body.Choice, nil
		}
	}
}

// provider resolves a provider by model name.
func (d *Daemon) provider(name string) (llm.Provider, string, requestSettings, int, error) {
	var m *llmModel
	var err error
	if name == "" {
		m, err = d.defaultModel()
	} else {
		m, err = d.modelByName(name)
	}
	if err != nil {
		return nil, "", requestSettings{}, 0, err
	}

	// The key belongs to the provider: one setting for all its models.
	p := llm.NewOpenAIChat(m.BaseURL, d.providerKey(m.Provider), m.Remote)

	// The summarizer uses the turn model unless its own is set.
	d.setSummarizer(p)

	return p, m.Name, m.settings(), m.contextWindow(), nil
}

// setSummarizer prepares history compaction on the chosen model.
func (d *Daemon) setSummarizer(p llm.Provider) {
	if d.cfg.Context.Strategy == "recent" {
		d.summarizer = nil
		return
	}
	d.mu.Lock()
	d.summarizer = &ctxb.Summarizer{Provider: p, MaxTokens: d.cfg.Context.SummaryMaxTokens}
	d.mu.Unlock()
}

// requestSettings are request parameters taken from model settings.
type requestSettings struct {
	MaxTokens       int
	Temperature     float64
	Reasoning       string
	MaxTokensField  string
	SkipTemperature bool
}

// handleSessionCompact forces history compaction for a session.
func (d *Daemon) handleSessionCompact(c *session, f *ipc.Frame) {
	var req struct {
		Session SessionMode `json:"session"`
		CWD     string      `json:"cwd"`
	}
	_ = decodePayload(f, &req)

	// Without an explicit id the directory's active session is used:
	// a manual compaction targets the one in use.
	var id string
	if req.Session.ID != "" {
		id = req.Session.ID
	} else {
		cwd := req.CWD
		if cwd == "" {
			cwd = d.cwd
		}
		active, err := d.sess.Active(cwd, d.cfg.Sessions.InheritFromParents)
		if err != nil || active == nil {
			_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams,
				"no active session in this directory")))
			return
		}
		id = active.ID
	}

	compacted, err := d.compactSession(id)
	if err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, err.Error())))
		return
	}
	_ = c.conn.Write(ipc.Success(f.ID, map[string]any{
		"session_id": id,
		"compacted":  compacted,
	}))
}
