package cli

import (
	"strings"
	"testing"
)

// plain returns the output without control sequences.
func plain(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestRenderHeading checks headings.
func TestRenderHeading(t *testing.T) {
	got := plain(newRenderer(false).Render("# Заголовок\n\nобычный текст"))
	if strings.Contains(got, "#") {
		t.Fatalf("решётка заголовка должна исчезнуть: %q", got)
	}
	if !strings.Contains(got, "Заголовок") {
		t.Fatalf("текст заголовка потерян: %q", got)
	}
}

// TestRenderBoldAndCode checks inline highlighting.
func TestRenderBoldAndCode(t *testing.T) {
	got := plain(newRenderer(false).Render("Текст **жирный** и `код`."))
	if strings.Contains(got, "**") || strings.Contains(got, "`") {
		t.Fatalf("разметка должна быть снята: %q", got)
	}
	if !strings.Contains(got, "жирный") || !strings.Contains(got, "код") {
		t.Fatalf("текст потерян: %q", got)
	}
}

// TestRenderCodeBlockKeepsContent checks a code block.
//
// Markup is not parsed inside a block, else the code would be mangled.
func TestRenderCodeBlockKeepsContent(t *testing.T) {
	src := "```go\nfunc main() {\n\tx := **2**\n}\n```"
	got := plain(newRenderer(false).Render(src))
	for _, want := range []string{"func main()", "**2**", "}"} {
		if !strings.Contains(got, want) {
			t.Fatalf("блок кода испорчен, нет %q: %q", want, got)
		}
	}
	if strings.Contains(got, "```") {
		t.Fatalf("ограждение блока должно исчезнуть: %q", got)
	}
}

// TestRenderLists checks markers and nesting.
func TestRenderLists(t *testing.T) {
	got := plain(newRenderer(false).Render("- первый\n  - вложенный\n1. шаг"))
	if !strings.Contains(got, "• первый") {
		t.Fatalf("маркер списка потерян: %q", got)
	}
	if !strings.Contains(got, "• вложенный") {
		t.Fatalf("вложенный пункт потерян: %q", got)
	}
	if !strings.Contains(got, "1. шаг") {
		t.Fatalf("нумерация потеряна: %q", got)
	}
}

// TestRenderUnclosedCodeBlock checks an unclosed block.
//
// The model may cut an answer inside a block: the rest must stay code
// rather than turn into parsed text.
func TestRenderUnclosedCodeBlock(t *testing.T) {
	got := plain(newRenderer(false).Render("```\nкод **без** закрытия"))
	if !strings.Contains(got, "**без**") {
		t.Fatalf("незакрытый блок должен сохранить текст: %q", got)
	}
}

// TestRenderLinkKeepsURL checks a link.
func TestRenderLinkKeepsURL(t *testing.T) {
	got := plain(newRenderer(false).Render("см. [документацию](https://example.com/x)"))
	if !strings.Contains(got, "документацию") || !strings.Contains(got, "https://example.com/x") {
		t.Fatalf("ссылка потеряла адрес: %q", got)
	}
}

// TestRenderEntities checks expansion of HTML entities.
func TestRenderEntities(t *testing.T) {
	got := plain(newRenderer(false).Render("a &lt; b &amp; c &gt; d"))
	if !strings.Contains(got, "a < b & c > d") {
		t.Fatalf("сущности не раскрыты: %q", got)
	}
}

// TestRenderQuote checks a quote.
func TestRenderQuote(t *testing.T) {
	got := plain(newRenderer(false).Render("> цитата"))
	if !strings.Contains(got, "│ цитата") {
		t.Fatalf("цитата не размечена: %q", got)
	}
}

// TestRenderEmptyLines checks collapsing of blank lines.
func TestRenderEmptyLines(t *testing.T) {
	got := newRenderer(false).Render("первый\n\n\n\nвторой")
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("пустые строки не схлопнуты: %q", got)
	}
}

// TestRenderColorAddsEscapes checks that color adds control codes.
func TestRenderColorAddsEscapes(t *testing.T) {
	got := newRenderer(true).Render("**жирный**")
	if !strings.Contains(got, "\033[") {
		t.Fatalf("ожидались управляющие последовательности: %q", got)
	}
}

// TestRenderNoColorHasNoEscapes checks clean output without a terminal.
func TestRenderNoColorHasNoEscapes(t *testing.T) {
	got := newRenderer(false).Render("# заголовок\n\n**жирный** и `код`\n\n- пункт")
	if strings.Contains(got, "\033[") {
		t.Fatalf("без цвета управляющих кодов быть не должно: %q", got)
	}
}

// TestRenderHorizontalRule checks the separator.
func TestRenderHorizontalRule(t *testing.T) {
	got := plain(newRenderer(false).Render("текст\n\n---\n\nещё"))
	if !strings.Contains(got, "─") {
		t.Fatalf("разделитель не нарисован: %q", got)
	}
}

// TestRenderItalicLeavesBold checks that italic keeps bold intact.
func TestRenderItalicLeavesBold(t *testing.T) {
	got := plain(newRenderer(false).Render("**жирный** и *курсив*"))
	if !strings.Contains(got, "жирный") || !strings.Contains(got, "курсив") {
		t.Fatalf("текст потерян: %q", got)
	}
	if strings.Contains(got, "*") {
		t.Fatalf("разметка осталась: %q", got)
	}
}
