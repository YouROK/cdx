package cli

// Terminal rendering of the model's answer.
//
// The markup is parsed line by line, without a node tree: an agent reply
// consists of paragraphs, lists, quotes and code blocks, while complex
// constructs such as tables are rare. Line parsing stays readable and
// pulls no dependencies.

import (
	"html"
	"regexp"
	"strings"
)

// ANSI style sequences.
const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiItalic = "\033[3m"
	ansiCode   = "\033[36m" // cyan, matching the interface accent
	ansiQuote  = "\033[2m"  // quotes are dimmed
)

// markdownRenderer draws markdown for the terminal.
type markdownRenderer struct {
	color bool
}

// newRenderer creates a renderer.
//
// color=false is used for redirected output, where control sequences only
// get in the way.
func newRenderer(color bool) *markdownRenderer {
	return &markdownRenderer{color: color}
}

// Render turns markdown into terminal text.
func (r *markdownRenderer) Render(src string) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(src, "\n")

	var out strings.Builder
	// inCode marks the inside of a code block, where markup is not parsed.
	inCode := false
	var fence string

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Closing and opening fences look the same, so only the start of
		// the line is checked.
		if trimmed := strings.TrimLeft(line, " "); strings.HasPrefix(trimmed, "```") ||
			strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if !inCode {
				inCode = true
				fence = marker
				continue
			}
			if marker == fence {
				inCode = false
				fence = ""
				continue
			}
		}
		if inCode {
			// Code is printed as is but indented, which sets it apart from
			// the surrounding text even without highlighting.
			out.WriteString("    ")
			out.WriteString(line)
			out.WriteString("\n")
			continue
		}

		text := r.renderLine(line)
		if text == "" {
			// Blank lines separate blocks; consecutive ones are collapsed.
			if strings.HasSuffix(out.String(), "\n\n") {
				continue
			}
			out.WriteString("\n")
			continue
		}
		out.WriteString(text)
		out.WriteString("\n")
	}

	return strings.TrimRight(out.String(), "\n") + "\n"
}

// renderLine draws a single line outside a code block.
func (r *markdownRenderer) renderLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}

	if isHorizontalRule(trimmed) {
		return r.style(ansiDim, strings.Repeat("─", 40))
	}

	// Headings lose their hashes and keep the bold face.
	if _, text := headingOf(trimmed); text != "" {
		return r.style(ansiBold, r.inline(text))
	}

	// Quotes get a bar on the left and dimmed text.
	if strings.HasPrefix(trimmed, ">") {
		body := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
		return r.style(ansiQuote, "│ "+r.inline(body))
	}

	// List items keep their marker and their indentation.
	if marker, text, indent, ok := listItemOf(line); ok {
		return strings.Repeat(" ", indent) + marker + " " + r.inline(text)
	}

	return r.inline(trimmed)
}

// inline parses markup inside a line.
func (r *markdownRenderer) inline(text string) string {
	text = r.codeSpans(text)
	text = r.bold(text)
	text = r.italic(text)
	text = r.links(text)
	// HTML entities are expanded: the model sends them instead of symbols.
	return html.UnescapeString(text)
}

var codeSpanRe = regexp.MustCompile("`([^`]+)`")

// codeSpans marks up code inside a line.
func (r *markdownRenderer) codeSpans(text string) string {
	if !r.color {
		return codeSpanRe.ReplaceAllString(text, "$1")
	}
	return codeSpanRe.ReplaceAllStringFunc(text, func(m string) string {
		inner := codeSpanRe.FindStringSubmatch(m)[1]
		return ansiCode + inner + ansiReset
	})
}

var boldRe = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)

// bold applies the bold face.
func (r *markdownRenderer) bold(text string) string {
	if !r.color {
		return boldRe.ReplaceAllStringFunc(text, func(m string) string {
			sub := boldRe.FindStringSubmatch(m)
			if sub[1] != "" {
				return sub[1]
			}
			return sub[2]
		})
	}
	return boldRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := boldRe.FindStringSubmatch(m)
		inner := sub[1]
		if inner == "" {
			inner = sub[2]
		}
		return ansiBold + inner + ansiReset
	})
}

// italicRe matches single asterisks while leaving bold markup alone.
var italicRe = regexp.MustCompile(`(^|[^*])\*([^*\n]+)\*`)

// italic applies the italic face.
func (r *markdownRenderer) italic(text string) string {
	if !r.color {
		return italicRe.ReplaceAllString(text, "$1$2")
	}
	return italicRe.ReplaceAllString(text, "$1"+ansiItalic+"$2"+ansiReset)
}

var linkRe = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

// links keeps both the label and the address of a link.
//
// The address is kept because a terminal link cannot be clicked, and
// without it there is no telling where the label leads.
func (r *markdownRenderer) links(text string) string {
	return linkRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := linkRe.FindStringSubmatch(m)
		label, url := sub[1], sub[2]
		if label == url {
			return label
		}
		if !r.color {
			return label + " (" + url + ")"
		}
		return label + " " + ansiDim + "(" + url + ")" + ansiReset
	})
}

// style wraps text in a style sequence when color is on.
func (r *markdownRenderer) style(seq, text string) string {
	if !r.color || text == "" {
		return text
	}
	return seq + text + ansiReset
}

// headingOf parses a heading and returns its level.
func headingOf(line string) (int, string) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0, ""
	}
	rest := strings.TrimPrefix(line[level:], " ")
	return level, strings.TrimSpace(rest)
}

// isHorizontalRule reports whether the line is a separator.
func isHorizontalRule(line string) bool {
	if len(line) < 3 {
		return false
	}
	first := line[0]
	if first != '-' && first != '*' && first != '_' {
		return false
	}
	for i := 0; i < len(line); i++ {
		if line[i] != first {
			if line[i] == ' ' {
				continue
			}
			return false
		}
	}
	return true
}

// listItemOf parses a list item.
//
// It returns the marker, the text and the indent: nested lists are told
// apart by leading spaces, so the indent has to be preserved.
func listItemOf(line string) (marker, text string, indent int, ok bool) {
	trimmed := strings.TrimLeft(line, " ")
	indent = len(line) - len(trimmed)

	for _, m := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(trimmed, m) {
			return "•", strings.TrimSpace(trimmed[len(m):]), indent, true
		}
	}
	// Numbered items look like "1. text" or "1) text".
	digits := 0
	for digits < len(trimmed) && trimmed[digits] >= '0' && trimmed[digits] <= '9' {
		digits++
	}
	if digits > 0 && digits+1 < len(trimmed) {
		sep := trimmed[digits]
		if (sep == '.' || sep == ')') && trimmed[digits+1] == ' ' {
			return trimmed[:digits+1], strings.TrimSpace(trimmed[digits+1:]), indent, true
		}
	}
	return "", "", 0, false
}
