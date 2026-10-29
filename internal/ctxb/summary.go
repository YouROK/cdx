package ctxb

import (
	"context"
	"fmt"
	"strings"

	"github.com/yourok/cdx/internal/llm"
)

// Summarizer compresses the older part of the history into one summary.
type Summarizer struct {
	Provider llm.Provider
	// MaxTokens caps the size of the summary.
	MaxTokens int
}

// promptTemplate is the summarization instruction.
const promptTemplate = `Summarize the earlier part of this session for your own later use.
Keep: the user's goal, decisions made, files changed, facts found, open questions.
Drop: greetings, repeated attempts, full file listings, tool output bodies.
Answer with the summary only, no preamble and no markdown fences.

Conversation:
%s`

// Summary builds a summary from the messages.
func (s *Summarizer) Summary(ctx context.Context, messages []llm.Message) (string, error) {
	if s.Provider == nil {
		return "", fmt.Errorf("summarizer has no provider")
	}
	if len(messages) == 0 {
		return "", nil
	}

	var sb strings.Builder
	for _, msg := range messages {
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		sb.WriteString(msg.Role)
		sb.WriteString(": ")
		sb.WriteString(msg.Content)
		sb.WriteString("\n\n")
	}

	maxTokens := s.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2000
	}

	resp, err := s.Provider.Chat(ctx, llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: fmt.Sprintf(promptTemplate, sb.String())},
		},
		MaxTokens: maxTokens,
	})
	if err != nil {
		return "", err
	}

	text := strings.TrimSpace(resp.Content)
	if text == "" {
		return "", fmt.Errorf("model returned an empty summary")
	}
	return text, nil
}

// Plan describes how to build the context from the history.
type Plan struct {
	// Head holds the messages to be compressed.
	Head []llm.Message
	// Tail holds the messages passed through in full.
	Tail []llm.Message
	// Overflow reports that the context does not fit even after compression.
	Overflow bool
}

// Options configures context building.
type Options struct {
	MaxTokens          int
	RecentMessages     int
	RecentMessagesMin  int
	SummarizeThreshold int
	Strategy           string
	// Fixed is the size of the service blocks: prompt, tools, files.
	Fixed int
}

// Split divides the history into the part to summarize and the tail.
//
// The tail shrinks until it fits the budget: first to RecentMessages, then
// to RecentMessagesMin. Everything older than the tail goes to Head.
func Split(messages []llm.Message, opts Options) Plan {
	if len(messages) == 0 {
		return Plan{}
	}

	recent := opts.RecentMessages
	if recent <= 0 {
		recent = 20
	}
	minimum := opts.RecentMessagesMin
	if minimum <= 0 {
		minimum = 4
	}

	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		return Plan{Tail: messages}
	}

	budget := maxTokens - opts.Fixed
	if budget <= 0 {
		return Plan{Overflow: true}
	}

	tailStart := len(messages) - recent
	if tailStart < 0 {
		tailStart = 0
	}

	// The tail shrinks, but not below the lower bound.
	for tailStart < len(messages)-minimum {
		if tailTokens(messages[tailStart:]) <= budget {
			break
		}
		tailStart++
	}

	plan := Plan{
		Head: messages[:tailStart],
		Tail: messages[tailStart:],
	}

	// The recent strategy does not summarize: old messages are dropped.
	if opts.Strategy == "recent" {
		plan.Head = nil
	}

	threshold := opts.SummarizeThreshold
	if threshold <= 0 {
		threshold = 50
	}
	// Below the threshold no summary is needed: old messages are dropped.
	if len(plan.Head) < threshold {
		plan.Head = nil
	}

	if tailTokens(plan.Tail) > budget {
		plan.Overflow = true
	}
	return plan
}

// tailTokens counts the size of the tail.
func tailTokens(messages []llm.Message) int {
	total := 0
	for _, msg := range messages {
		total += EstimateTokens(msg.Content) + 4
	}
	return total
}
