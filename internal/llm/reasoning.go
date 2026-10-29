package llm

import "strings"

// thinkTags mark reasoning inside the answer.
//
// Providers with open reasoning send it inside content: MiniMax, DeepSeek
// and others wrap the text in <think> tags. The user wants the finished
// answer, so reasoning is split off.
var thinkTags = [][2]string{
	{"<think>", "</think>"},
	{"<thinking>", "</thinking>"},
}

// SplitReasoning separates reasoning from the answer.
//
// It returns the reasoning and the cleaned answer. An unclosed block counts
// as reasoning entirely: no answer has been produced in it yet.
func SplitReasoning(content string) (reasoning, answer string) {
	answer = content

	for _, tags := range thinkTags {
		for {
			start := strings.Index(answer, tags[0])
			if start < 0 {
				break
			}
			rest := answer[start+len(tags[0]):]
			end := strings.Index(rest, tags[1])
			if end < 0 {
				// The block never closes, so everything is reasoning.
				reasoning += rest
				answer = answer[:start]
				break
			}
			reasoning += rest[:end]
			answer = answer[:start] + rest[end+len(tags[1]):]
		}
	}

	return strings.TrimSpace(reasoning), strings.TrimSpace(answer)
}

// artifacts are service blocks some models add to the answer.
//
// Some models mark tool calls this way: with no tools the block stays in
// the text as noise.
var artifacts = []string{
	"minimax:tool_call",
	"tool_call",
	"tool_response",
	"minimax:tool_response",
}

// StripArtifacts removes service markup from the answer.
func StripArtifacts(text string) string {
	for _, tag := range artifacts {
		for {
			start := strings.Index(text, "<"+tag+">")
			if start < 0 {
				break
			}
			rest := text[start+len(tag)+2:]
			if end := strings.Index(rest, "</"+tag+">"); end >= 0 {
				text = text[:start] + rest[end+len(tag)+3:]
				continue
			}
			text = text[:start]
		}
		text = strings.ReplaceAll(text, "</"+tag+">", "")
	}
	return strings.TrimSpace(text)
}

// MaxRepeatedTail is how many times the same tail may repeat.
//
// Some models loop on a phrase ("391 response391 response..."): the stream
// never closes and the request hangs until the timeout. The repeat is cut
// on the client side so such an answer does not eat time and money.
const MaxRepeatedTail = 40

// LooksLooping reports the answer degenerated into a repeat.
//
// The tail is compared: a looping model produces the same fragment over and
// over, so the end of the text is checked for repeats of one chunk.
func LooksLooping(text string) bool {
	const window = 2000
	if len(text) < window {
		return false
	}
	tail := text[len(text)-window:]

	// Look for the shortest block that repeats at the end.
	for size := 4; size <= len(tail)/4; size++ {
		block := tail[len(tail)-size:]
		repeats := 0
		for pos := len(tail) - size; pos >= size; pos -= size {
			if tail[pos-size:pos] != block {
				break
			}
			repeats++
			if repeats >= MaxRepeatedTail {
				return true
			}
		}
	}
	return false
}
