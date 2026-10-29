package daemon

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/yourok/cdx/internal/agent"
	"github.com/yourok/cdx/internal/web"
)

// TestPresetPrompts checks every built-in preset yields its own prompt.
func TestPresetPrompts(t *testing.T) {
	d := newTestDaemon(t)

	for _, id := range []string{agent.PresetStandard, agent.PresetMini, agent.PresetCreator} {
		text, err := d.presetPrompt(id)
		if err != nil {
			t.Fatalf("presetPrompt(%q): %v", id, err)
		}
		if strings.TrimSpace(text) == "" {
			t.Fatalf("preset %q produced an empty prompt", id)
		}
	}

	// The presets differ, otherwise the choice would be pointless.
	standard, _ := d.presetPrompt(agent.PresetStandard)
	mini, _ := d.presetPrompt(agent.PresetMini)
	if standard == mini {
		t.Fatal("minimal mode must differ from the standard one")
	}
}

// TestCreatorPresetHasAPIDoc checks the API reference in the creator preset.
//
// The reference is built from code, so the preset cannot lack it.
func TestCreatorPresetHasAPIDoc(t *testing.T) {
	d := newTestDaemon(t)

	text, err := d.presetPrompt(agent.PresetCreator)
	if err != nil {
		t.Fatalf("presetPrompt: %v", err)
	}
	for _, want := range []string{
		"Plugin API reference",
		"cdx.request",
		"cdx.publish",
		"manifest.json",
		"input_schema",
		"capabilities",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("creator prompt must mention %q", want)
		}
	}
}

// TestMiniPresetForbidsFiller checks the preset demands a short answer.
func TestMiniPresetForbidsFiller(t *testing.T) {
	d := newTestDaemon(t)

	text, _ := d.presetPrompt(agent.PresetMini)
	// Meaning is checked, not wording: the text gets edited, but the short
	// answer requirement and a correct example must stay.
	for _, want := range []string{"nothing else", "short sentence", "code block"} {
		if !strings.Contains(text, want) {
			t.Fatalf("minimal prompt must state %q", want)
		}
	}
	// Answer examples show the shape; without them the model adds filler.
	if !strings.Contains(text, "you print:") {
		t.Fatal("minimal prompt must show the shape of the answer")
	}
}

// TestUnknownPresetRejected checks an unknown preset is refused.
func TestUnknownPresetRejected(t *testing.T) {
	d := newTestDaemon(t)

	if _, err := d.presetPrompt("nope"); err == nil {
		t.Fatal("expected an error for an unknown preset")
	}
}

// TestDefaultPresetIsStandard checks the default without an explicit preset.
func TestDefaultPresetIsStandard(t *testing.T) {
	d := newTestDaemon(t)

	text, err := d.presetPrompt("")
	if err != nil {
		t.Fatalf("presetPrompt: %v", err)
	}
	standard, _ := d.presetPrompt(agent.PresetStandard)
	if text != standard {
		t.Fatal("an empty preset must fall back to the standard one")
	}
}

// TestSaveCustomPreset checks creating a custom preset.
func TestSaveCustomPreset(t *testing.T) {
	d := newPresetDaemon(t)

	err := d.savePreset(web.PresetEdit{
		ID: "review", Name: "Review", Description: "Code review",
		Prompt: "You review code.",
	})
	if err != nil {
		t.Fatalf("savePreset: %v", err)
	}

	text, err := d.presetPrompt("review")
	if err != nil {
		t.Fatalf("presetPrompt: %v", err)
	}
	if text != "You review code." {
		t.Fatalf("unexpected prompt %q", text)
	}
}

// TestBuiltinPresetIsProtected checks built-in presets cannot be replaced.
func TestBuiltinPresetIsProtected(t *testing.T) {
	d := newTestDaemon(t)

	if err := d.savePreset(web.PresetEdit{
		ID: agent.PresetStandard, Prompt: "заменённый текст",
	}); err == nil {
		t.Fatal("built-in preset must not be replaced")
	}
	if err := d.deletePreset(agent.PresetStandard); err == nil {
		t.Fatal("built-in preset must not be removed")
	}
}

// TestDeleteActivePresetResets checks the active flag resets on delete.
func TestDeleteActivePresetResets(t *testing.T) {
	d := newPresetDaemon(t)

	if err := d.savePreset(web.PresetEdit{ID: "mine", Prompt: "текст"}); err != nil {
		t.Fatalf("savePreset: %v", err)
	}
	if err := d.setActivePreset("mine"); err != nil {
		t.Fatalf("setActivePreset: %v", err)
	}
	if err := d.deletePreset("mine"); err != nil {
		t.Fatalf("deletePreset: %v", err)
	}
	if d.cfg.AgentPreset.Active != agent.PresetStandard {
		t.Fatalf("active preset must fall back to standard, got %q", d.cfg.AgentPreset.Active)
	}
}

// newPresetDaemon builds a daemon with a writable config.
//
// Presets live in the config, so saving needs a temporary file.
func newPresetDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newTestDaemon(t)
	d.cfgPath = filepath.Join(t.TempDir(), "config.yaml")
	return d
}
