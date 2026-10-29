package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yourok/cdx/internal/agent"
	"github.com/yourok/cdx/internal/ctxb"
	"github.com/yourok/cdx/internal/llm"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/sessions"
)

// turnSession picks the session for a turn from the request mode.
//
// Modes: auto looks up the active session through parent directories, none
// runs without history, and a non-empty id addresses one session.
func (d *Daemon) turnSession(req TurnRequest) (*sessions.Session, error) {
	if !d.cfg.Sessions.Enabled {
		return nil, nil
	}

	switch req.Session.Mode {
	case "none":
		return nil, nil
	case "id":
		if req.Session.ID == "" {
			return nil, errNoSessionID
		}
		s, found, err := d.sess.Get(req.Session.ID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, errSessionNotFound
		}
		return s, nil
	}

	// Auto mode: a session exists only when one was created explicitly.
	return d.sess.Active(req.CWD, d.cfg.Sessions.InheritFromParents)
}

// loadHistory builds session history for the model context.
//
// A summary replaces the messages it covers: the context gets either the
// summary or the original messages, never both.
func (d *Daemon) loadHistory(s *sessions.Session, fixedTokens, contextWindow int) []llm.Message {
	if s == nil {
		return nil
	}

	all, err := d.sess.Messages(s.ID)
	if err != nil || len(all) == 0 {
		return nil
	}

	// Coverage boundary: messages up to it are already compacted.
	covered := 0
	var summary *sessions.Message
	if last := lastSummary(all); last != nil {
		summary = last
		covered = last.CoversTo
	}

	var pending []sessions.Message
	for _, msg := range all {
		if msg.Kind == sessions.KindSummary || msg.Seq <= covered {
			continue
		}
		pending = append(pending, msg)
	}

	converted := make([]llm.Message, 0, len(pending))
	source := make([]sessions.Message, 0, len(pending))
	for _, msg := range pending {
		msgs, ok := toLLM(msg, d.cfg.Context.KeepToolResults)
		if !ok {
			continue
		}
		converted = append(converted, msgs...)
		source = append(source, msg)
	}

	plan := ctxb.Split(converted, ctxb.Options{
		MaxTokens:          contextWindow,
		RecentMessages:     d.cfg.Context.RecentMessages,
		RecentMessagesMin:  d.cfg.Context.RecentMessagesMin,
		SummarizeThreshold: d.cfg.Context.SummarizeThreshold,
		Strategy:           d.cfg.Context.Strategy,
		Fixed:              fixedTokens,
	})

	out := make([]llm.Message, 0, len(plan.Tail)+2)

	// The existing summary goes first.
	if summary != nil {
		out = append(out, llm.Message{
			Role:    llm.RoleSystem,
			Content: "Summary of the earlier part of this session:\n" + summary.Content,
		})
	}

	// Once the threshold is reached a new summary is made and stored.
	if len(plan.Head) > 0 {
		if text, ok := d.summarize(s, plan.Head, source); ok {
			out = append(out, llm.Message{
				Role:    llm.RoleSystem,
				Content: "Summary of the earlier part of this session:\n" + text,
			})
		}
	}

	out = append(out, plan.Tail...)
	return out
}

// summarize builds and stores a summary of the history head.
//
// It returns false when summarization is unavailable or failed: the turn
// is not interrupted, it just runs without a summary.
func (d *Daemon) summarize(s *sessions.Session, head []llm.Message, source []sessions.Message) (string, bool) {
	if len(head) == 0 {
		return "", false
	}

	d.mu.Lock()
	summarizer := d.summarizer
	d.mu.Unlock()
	if summarizer == nil {
		return "", false
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(d.cfg.Agent.LLMTimeoutMS)*time.Millisecond)
	defer cancel()

	text, err := summarizer.Summary(ctx, head)
	if err != nil {
		logx.Warn("[Daemon] Failed to summarize session history: %v", err)
		return "", false
	}

	// The coverage range uses the original message sequence numbers.
	from, to := 0, 0
	if len(source) > 0 {
		from = source[0].Seq
		to = source[len(source)-1].Seq
	}

	if _, err := d.sess.Append(s.ID, sessions.Message{
		Kind:       sessions.KindSummary,
		Content:    text,
		CoversFrom: from,
		CoversTo:   to,
	}); err != nil {
		logx.Warn("[Daemon] Failed to store summary: %v", err)
	}
	return text, true
}

// lastSummary returns the latest summary record.
func lastSummary(all []sessions.Message) *sessions.Message {
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Kind == sessions.KindSummary {
			return &all[i]
		}
	}
	return nil
}

// toLLM converts a history record into model messages.
func toLLM(msg sessions.Message, keepToolResults bool) ([]llm.Message, bool) {
	switch msg.Kind {
	case sessions.KindUser:
		return []llm.Message{{Role: llm.RoleUser, Content: msg.Content}}, true
	case sessions.KindAssistant:
		return []llm.Message{{Role: llm.RoleAssistant, Content: msg.Content}}, true
	case sessions.KindToolCall:
		// Call and result are stored apart; the model needs the result.
		return nil, false
	case sessions.KindToolResult:
		if !keepToolResults {
			return nil, false
		}
		return []llm.Message{{
			Role:       llm.RoleTool,
			Content:    msg.Content,
			ToolCallID: msg.ToolCallID,
			Name:       msg.ToolName,
		}}, true
	case sessions.KindError:
		// Errors stay out of the context: they are not part of the talk.
		return nil, false
	}
	return nil, false
}

// recordTurn stores the request and the answer in the history.
//
// promptHash is kept with the answer, so the history shows which prompt
// produced it.
func (d *Daemon) recordTurn(s *sessions.Session, prompt string, res *agent.Result, model, promptHash string) {
	// A pure failure is not recorded: the model would see its own error
	// as an answer and start commenting on it. Only a real answer is
	// written to the session log.
	if res.Error != nil && res.Result == "" {
		return
	}

	msgs := []sessions.Message{
		{Kind: sessions.KindUser, Role: "user", Content: prompt},
	}

	content := res.Result
	msgs = append(msgs, sessions.Message{
		Kind:       sessions.KindAssistant,
		Role:       "assistant",
		Content:    content,
		Model:      model,
		TokensIn:   res.Tokens.Input,
		TokensOut:  res.Tokens.Output,
		PromptHash: promptHash,
	})

	for _, call := range res.ToolCalls {
		msgs = append(msgs, sessions.Message{
			Kind:       sessions.KindToolCall,
			ToolName:   call.Name,
			ToolCallID: call.ID,
			Content:    call.Arguments,
		})
		msgs = append(msgs, sessions.Message{
			Kind:       sessions.KindToolResult,
			ToolName:   call.Name,
			ToolCallID: call.ID,
			Content:    call.Output,
			OK:         call.OK,
		})
	}

	d.appendHistory(s, msgs)
	if model != "" {
		_ = d.sess.SetModel(s.ID, model)
	}
}

// contextFiles reads agent.md and skills.md from two directories.
//
// The pairs come back in order: the session root, then the current
// directory. Missing files are not an error.
func (d *Daemon) contextFiles(cwd, sessionRoot string) []contextFile {
	if len(d.cfg.Files.ContextFiles) == 0 {
		return nil
	}

	dirs := []string{cwd}
	if sessionRoot != "" && sessions.NormalizePath(sessionRoot) != sessions.NormalizePath(cwd) {
		dirs = append([]string{sessionRoot}, cwd)
	}

	var out []contextFile
	for _, dir := range dirs {
		for _, name := range d.cfg.Files.ContextFiles {
			content, ok := readTextFile(dir, name, d.cfg.Files.MaxInlineSizeKB)
			if !ok {
				continue
			}
			out = append(out, contextFile{Dir: dir, Name: name, Content: content})
		}
	}
	return out
}

// toContextFiles converts files to the prompt format.
func toContextFiles(files []contextFile) []agent.ContextFile {
	if len(files) == 0 {
		return nil
	}
	out := make([]agent.ContextFile, 0, len(files))
	for _, f := range files {
		out = append(out, agent.ContextFile{Dir: f.Dir, Name: f.Name, Content: f.Content})
	}
	return out
}

// toolsTokens estimates the size of tool descriptions in the context.
func (d *Daemon) toolsTokens() int {
	total := 0
	for _, spec := range d.reg.LLMSpecs() {
		raw, err := json.Marshal(spec)
		if err != nil {
			continue
		}
		total += ctxb.EstimateTokens(string(raw))
	}
	return total
}

// compactSession forces compaction of a session history.
//
// The summary covers every message except the last few: those would go
// into the next request anyway. It returns how many messages were
// covered by the summary.
func (d *Daemon) compactSession(id string) (int, error) {
	s, found, err := d.sess.Get(id)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, fmt.Errorf("session %q not found", id)
	}

	// The summarizer is prepared by a turn; an explicit compaction has
	// to build it again, or there is nothing to summarize with.
	d.prepareSummarizer(s.Model)

	all, err := d.sess.Messages(id)
	if err != nil {
		return 0, err
	}

	// What a summary already covers is not summarized again.
	covered := 0
	if last := lastSummary(all); last != nil {
		covered = last.CoversTo
	}

	var pending []sessions.Message
	for _, msg := range all {
		if msg.Kind == sessions.KindSummary || msg.Seq <= covered {
			continue
		}
		pending = append(pending, msg)
	}

	// An explicit compaction is user-driven, usually because of an
	// overflow, so only the bare minimum is kept: the usual tail
	// threshold is too large here.
	keep := d.cfg.Context.RecentMessagesMin
	if keep <= 0 {
		keep = 4
	}
	// The last messages are not worth summarizing, and at least half must
	// go into the summary, otherwise the command does nothing.
	if half := len(pending) / 2; keep > half {
		keep = half
	}
	if keep < 1 {
		return 0, fmt.Errorf("history is too short to compact (%d messages)", len(pending))
	}
	headEnd := len(pending) - keep

	head := pending[:headEnd]
	converted := make([]llm.Message, 0, len(head))
	source := make([]sessions.Message, 0, len(head))
	for _, msg := range head {
		msgs, ok := toLLM(msg, d.cfg.Context.KeepToolResults)
		if !ok {
			continue
		}
		converted = append(converted, msgs...)
		source = append(source, msg)
	}
	if len(converted) == 0 {
		return 0, fmt.Errorf("nothing to compact: history has no text messages")
	}

	if _, ok := d.summarize(s, converted, source); !ok {
		return 0, fmt.Errorf("failed to build a summary")
	}
	return len(source), nil
}

// prepareSummarizer prepares the summary model from the session model.
func (d *Daemon) prepareSummarizer(modelName string) {
	p, _, _, _, err := d.provider(modelName)
	if err != nil {
		logx.Warn("[Daemon] Failed to prepare summarizer: %v", err)
		return
	}
	d.setSummarizer(p)
}
