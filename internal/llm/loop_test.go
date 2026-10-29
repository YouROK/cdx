package llm

import (
	"strings"
	"testing"
)

// TestLooksLooping checks a looping answer is recognized.
func TestLooksLooping(t *testing.T) {
	// A real deepseek answer: "391 response" repeated forever.
	var looped strings.Builder
	for i := 0; i < 400; i++ {
		looped.WriteString("391 response")
	}
	if !LooksLooping(looped.String()) {
		t.Fatal("repeating tail must be detected")
	}

	// A normal long answer is not treated as a loop.
	normal := strings.Repeat("Это обычное предложение с разными словами. ", 100)
	if LooksLooping(normal) {
		t.Fatal("normal text must not be treated as a loop")
	}

	// Short answers are not checked: a repeat there may be meaningful.
	if LooksLooping("391391391") {
		t.Fatal("short text must not be treated as a loop")
	}

	// Code with repeated lines is not a loop.
	var code strings.Builder
	code.WriteString("```go\n")
	for i := 0; i < 60; i++ {
		code.WriteString("\tx := compute(a, b)\n")
	}
	code.WriteString("\tx2 := compute(a, c)\n```")
	if LooksLooping(code.String()) {
		t.Fatal("repeating code lines must not be treated as a loop")
	}
}
