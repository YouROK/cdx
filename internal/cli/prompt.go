package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/yourok/cdx/internal/question"
)

const (
	promptIndent   = "  "
	promptMinWidth = 40
)

// appendPromptField adds one "Label: value" block, wrapping long values.
func appendPromptField(lines []string, label, value string, width int) []string {
	prefix := promptIndent
	if label != "" {
		prefix = promptIndent + label + ": "
	}
	avail := width - len([]rune(prefix))
	if avail < 20 {
		avail = 20
	}
	chunks := wrapPromptText(value, avail)
	pad := strings.Repeat(" ", len([]rune(prefix)))
	for i, chunk := range chunks {
		if i == 0 {
			lines = append(lines, prefix+styleAction(chunk))
			continue
		}
		lines = append(lines, pad+styleAction(chunk))
	}
	return lines
}

// wrapPromptText breaks text into lines no longer than width, keeping
// explicit line breaks and hard-breaking single long words.
func wrapPromptText(text string, width int) []string {
	if width < 10 {
		width = 10
	}
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t")
		if line == "" {
			out = append(out, "")
			continue
		}
		out = append(out, wrapLine(line, width)...)
	}
	return out
}

func wrapLine(line string, width int) []string {
	var out []string
	var current []rune
	flush := func() {
		if len(current) > 0 {
			out = append(out, string(current))
			current = current[:0]
		}
	}
	for _, word := range strings.Fields(line) {
		runes := []rune(word)
		if len(current) == 0 && len(runes) <= width {
			current = append(current, runes...)
			continue
		}
		if len(current) > 0 && len(current)+1+len(runes) <= width {
			current = append(current, ' ')
			current = append(current, runes...)
			continue
		}
		flush()
		for len(runes) > width {
			out = append(out, string(runes[:width]))
			runes = runes[width:]
		}
		current = append(current, runes...)
	}
	flush()
	if len(out) == 0 {
		out = append(out, "")
	}
	return out
}

// questionPrompt is a rendered generic client question.
type questionPrompt struct {
	lines     []string
	options   []question.Option
	def       string
	allowText bool
	prompt    string
}

// buildQuestionPrompt renders a question envelope for the terminal.
func buildQuestionPrompt(raw map[string]any, width int) questionPrompt {
	if width < promptMinWidth {
		width = promptMinWidth
	}

	kind := questionStringField(raw, "kind")
	title := questionStringField(raw, "title")
	body := questionStringField(raw, "body")
	p := questionPrompt{
		def:       questionStringField(raw, "default"),
		allowText: questionBoolField(raw, "text"),
	}

	switch kind {
	case question.KindModel:
		p.lines = append(p.lines, "The agent asks:")
	case question.KindPermission:
		p.lines = append(p.lines, "Permission required:")
	case question.KindLoop:
		p.lines = append(p.lines, "Possible loop:")
	}
	if title != "" {
		for _, line := range wrapPromptText(title, width) {
			p.lines = append(p.lines, line)
		}
	}
	if body != "" {
		for _, line := range wrapPromptText(body, width) {
			p.lines = append(p.lines, promptIndent+line)
		}
	}
	for _, item := range questionListField(raw, "details") {
		label := strings.TrimSpace(fmt.Sprint(item["label"]))
		value := strings.TrimSpace(fmt.Sprint(item["value"]))
		if value == "" || value == "<nil>" {
			continue
		}
		p.lines = appendPromptField(p.lines, label, value, width)
	}

	for _, item := range questionListField(raw, "options") {
		id := strings.TrimSpace(fmt.Sprint(item["id"]))
		label := strings.TrimSpace(fmt.Sprint(item["label"]))
		if id == "" || id == "<nil>" || label == "" || label == "<nil>" {
			continue
		}
		opt := question.Option{ID: id, Label: label}
		if description, ok := item["description"].(string); ok {
			opt.Description = strings.TrimSpace(description)
		}
		p.options = append(p.options, opt)
	}

	if p.def == "" && len(p.options) > 0 {
		p.def = p.options[0].ID
	}
	if len(p.options) > 0 {
		p.lines = append(p.lines, "")
		for i, opt := range p.options {
			line := fmt.Sprintf("%s[%d] %s", promptIndent, i+1, opt.Label)
			if opt.Description != "" {
				line += " - " + opt.Description
			}
			if opt.ID == p.def {
				line += " (default)"
			}
			p.lines = append(p.lines, line)
		}
	}

	switch {
	case len(p.options) > 0 && p.allowText:
		p.prompt = "Choose a number or type an answer: "
	case len(p.options) > 0:
		p.prompt = fmt.Sprintf("Choose [%d]: ", questionDefaultIndex(p.options, p.def)+1)
	default:
		p.prompt = "Answer: "
	}
	return p
}

// parseQuestionAnswer maps a typed line to a question answer.
// The second result is false when the line does not match an answer and the client should ask again.
func parseQuestionAnswer(answer string, p questionPrompt) (map[string]any, bool) {
	text := strings.TrimSpace(answer)
	if text == "" {
		if p.def != "" {
			return map[string]any{"choice": p.def}, true
		}
		return map[string]any{"cancelled": true, "reason": "no answer"}, true
	}

	lower := strings.ToLower(text)
	if lower == "y" || lower == "yes" {
		if id, ok := questionOptionID(p.options, "continue"); ok {
			return map[string]any{"choice": id}, true
		}
		if id, ok := questionOptionID(p.options, "once"); ok {
			return map[string]any{"choice": id}, true
		}
	}
	if lower == "n" || lower == "no" {
		if id, ok := questionOptionID(p.options, "stop"); ok {
			return map[string]any{"choice": id}, true
		}
		if id, ok := questionOptionID(p.options, "deny"); ok {
			return map[string]any{"choice": id}, true
		}
	}
	if n, err := strconv.Atoi(lower); err == nil && n >= 1 && n <= len(p.options) {
		return map[string]any{"choice": p.options[n-1].ID}, true
	}
	for _, opt := range p.options {
		if strings.EqualFold(opt.ID, text) || strings.EqualFold(opt.Label, text) {
			return map[string]any{"choice": opt.ID}, true
		}
	}
	if p.allowText {
		return map[string]any{"text": text}, true
	}
	return nil, false
}

func questionStringField(raw map[string]any, key string) string {
	value, _ := raw[key].(string)
	return strings.TrimSpace(value)
}

func questionBoolField(raw map[string]any, key string) bool {
	value, _ := raw[key].(bool)
	return value
}

func questionListField(raw map[string]any, key string) []map[string]any {
	list, _ := raw[key].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func questionOptionID(options []question.Option, want string) (string, bool) {
	for _, opt := range options {
		if opt.ID == want {
			return opt.ID, true
		}
	}
	return "", false
}

func questionDefaultIndex(options []question.Option, def string) int {
	if def != "" {
		for i, opt := range options {
			if opt.ID == def {
				return i
			}
		}
	}
	if len(options) > 0 {
		return 0
	}
	return -1
}
