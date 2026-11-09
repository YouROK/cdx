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

// permissionPrompt is a rendered permission question.
type permissionPrompt struct {
	lines   []string
	options []string
	def     string
	prompt  string
}

// buildPermissionPrompt renders a client:prompt_user question for the terminal.
//
// The action is printed in full and wrapped to the terminal width: the user
// must see what the agent is about to run before answering.
func buildPermissionPrompt(question map[string]any, width int) permissionPrompt {
	if width < promptMinWidth {
		width = promptMinWidth
	}

	options := promptOptions(question)
	if len(options) == 0 {
		// A permission question without an explicit list offers the standard choices.
		options = []string{"once", "session", "daemon", "always", "deny"}
	}
	p := permissionPrompt{options: options}
	p.def = defaultOption(options)

	level := promptString(question, "permission", "level")
	title := promptString(question, "title")
	if title == "" {
		title = promptString(question, "summary", "question")
	}
	if title == "" {
		if tool := promptString(question, "tool"); tool != "" {
			title = "use the tool " + tool
		}
	}
	if title == "" {
		title = "unknown action"
	}

	prefix := "Permission required: "
	if level != "" {
		prefix = "Permission required (" + level + "): "
	}
	titleLines := wrapPromptText(title, width-len([]rune(prefix)))
	for i, line := range titleLines {
		if i == 0 {
			p.lines = append(p.lines, promptHeader(level)+line)
			continue
		}
		p.lines = append(p.lines, promptIndent+line)
	}

	for _, field := range promptFields(question) {
		if field[0] == "" && field[1] == title {
			continue
		}
		p.lines = appendPromptField(p.lines, field[0], field[1], width)
	}

	switch {
	case isOnceOrDeny(options):
		p.lines = append(p.lines, "")
		p.prompt = "Allow once? [Y/n]: "
	case len(options) > 0:
		p.lines = append(p.lines, "")
		for i, opt := range options {
			line := fmt.Sprintf("%s[%d] %s", promptIndent, i+1, opt)
			if opt == p.def {
				line += " (default)"
			}
			p.lines = append(p.lines, line)
		}
		p.prompt = fmt.Sprintf("Choose [%d]: ", indexOfOption(options, p.def)+1)
	default:
		p.prompt = "Allow? [Y/n]: "
	}
	return p
}

// promptHeader styles the level while the layout width stays the plain one.
func promptHeader(level string) string {
	if level == "" {
		return "Permission required: "
	}
	return "Permission required " + styleLevel(level) + ": "
}

// parsePromptAnswer maps a typed answer to one of the offered options.
func parsePromptAnswer(answer string, p permissionPrompt) string {
	choice := strings.ToLower(strings.TrimSpace(answer))
	switch choice {
	case "":
		return p.def
	case "y", "yes":
		if hasOption(p.options, "once") {
			return "once"
		}
		return p.def
	case "n", "no":
		if hasOption(p.options, "deny") {
			return "deny"
		}
		return "deny"
	}
	if n, err := strconv.Atoi(choice); err == nil && n >= 1 && n <= len(p.options) {
		return p.options[n-1]
	}
	for _, opt := range p.options {
		if strings.ToLower(opt) == choice {
			return opt
		}
	}
	return "deny"
}

func promptOptions(question map[string]any) []string {
	raw, ok := question["options"].([]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if opt, ok := item.(string); ok && opt != "" {
			out = append(out, opt)
		}
	}
	return out
}

// promptFields lists the labelled actions: command, file, workspace, and so on.
func promptFields(question map[string]any) [][2]string {
	raw, ok := question["details"].([]any)
	if !ok {
		if summary := promptString(question, "summary", "question"); summary != "" {
			return [][2]string{{"", summary}}
		}
		return nil
	}
	out := make([][2]string, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		label := strings.TrimSpace(fmt.Sprint(m["label"]))
		value := strings.TrimSpace(fmt.Sprint(m["value"]))
		if value == "" || value == "<nil>" {
			continue
		}
		out = append(out, [2]string{label, value})
	}
	return out
}

func promptString(question map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := question[key].(string); ok {
			if text := strings.TrimSpace(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func defaultOption(options []string) string {
	if hasOption(options, "once") {
		return "once"
	}
	if len(options) > 0 {
		return options[0]
	}
	return "deny"
}

func hasOption(options []string, want string) bool {
	return indexOfOption(options, want) >= 0
}

func indexOfOption(options []string, want string) int {
	for i, opt := range options {
		if opt == want {
			return i
		}
	}
	return -1
}

func isOnceOrDeny(options []string) bool {
	return len(options) == 2 && hasOption(options, "once") && hasOption(options, "deny")
}

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
func parseQuestionAnswer(answer string, p questionPrompt) map[string]any {
	text := strings.TrimSpace(answer)
	if text == "" {
		if p.def != "" {
			return map[string]any{"choice": p.def}
		}
		if len(p.options) > 0 {
			return map[string]any{"choice": p.options[0].ID}
		}
		return map[string]any{"cancelled": true, "reason": "no answer"}
	}

	lower := strings.ToLower(text)
	switch lower {
	case "y", "yes":
		if id, ok := questionOptionID(p.options, "continue"); ok {
			return map[string]any{"choice": id}
		}
		if id, ok := questionOptionID(p.options, "once"); ok {
			return map[string]any{"choice": id}
		}
	case "n", "no":
		if id, ok := questionOptionID(p.options, "stop"); ok {
			return map[string]any{"choice": id}
		}
		if id, ok := questionOptionID(p.options, "deny"); ok {
			return map[string]any{"choice": id}
		}
	}
	if n, err := strconv.Atoi(lower); err == nil && n >= 1 && n <= len(p.options) {
		return map[string]any{"choice": p.options[n-1].ID}
	}
	for _, opt := range p.options {
		if strings.EqualFold(opt.ID, text) || strings.EqualFold(opt.Label, text) {
			return map[string]any{"choice": opt.ID}
		}
	}
	if p.allowText {
		return map[string]any{"text": text}
	}
	return map[string]any{"cancelled": true, "reason": "unrecognized answer"}
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
