package llm

import "testing"

// TestSplitReasoning checks reasoning is split from the answer.
func TestSplitReasoning(t *testing.T) {
	cases := []struct {
		name      string
		content   string
		reasoning string
		answer    string
	}{
		{
			name:      "think block before the answer",
			content:   "<think>считаю 17×23</think>\n\n391",
			reasoning: "считаю 17×23",
			answer:    "391",
		},
		{
			name:      "no reasoning",
			content:   "просто ответ",
			reasoning: "",
			answer:    "просто ответ",
		},
		{
			name:      "unclosed block counts as reasoning",
			content:   "<think>ещё думаю",
			reasoning: "ещё думаю",
			answer:    "",
		},
		{
			name:      "text after the block is kept",
			content:   "<think>кратко</think>ответ\nвторая строка",
			reasoning: "кратко",
			answer:    "ответ\nвторая строка",
		},
		{
			name:      "thinking tag variant",
			content:   "<thinking>размышление</thinking>итог",
			reasoning: "размышление",
			answer:    "итог",
		},
	}

	for _, c := range cases {
		reasoning, answer := SplitReasoning(c.content)
		if reasoning != c.reasoning {
			t.Fatalf("%s: reasoning = %q, want %q", c.name, reasoning, c.reasoning)
		}
		if answer != c.answer {
			t.Fatalf("%s: answer = %q, want %q", c.name, answer, c.answer)
		}
	}
}
