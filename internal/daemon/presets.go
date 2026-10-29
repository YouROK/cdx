package daemon

import (
	"fmt"
	"strings"

	"github.com/yourok/cdx/internal/agent"
	"github.com/yourok/cdx/internal/agent/plugindoc"
	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/web"
)

// presetPrompt returns the explaining prompt part for a preset.
//
// Order: the preset from the request, then the config one, then the
// built-in standard. An unknown id is an error: silently substituting
// another preset would hide a typo.
func (d *Daemon) presetPrompt(id string) (string, error) {
	preset, err := d.resolvePreset(id)
	if err != nil {
		return "", err
	}

	text := preset.Prompt
	// The plugin authoring preset gets the API reference built from code.
	if preset.ID == agent.PresetCreator {
		text = strings.TrimRight(text, "\n") + "\n\n" + plugindoc.Render()
	}
	return text, nil
}

// resolvePreset finds a preset by id.
func (d *Daemon) resolvePreset(id string) (agent.Preset, error) {
	if id == "" {
		id = d.cfg.AgentPreset.Active
	}
	if id == "" {
		id = agent.PresetStandard
	}

	if p, ok := agent.FindBuiltinPreset(id); ok {
		return p, nil
	}
	for _, c := range d.cfg.AgentPreset.Custom {
		if c.ID != id {
			continue
		}
		return agent.Preset{
			ID:          c.ID,
			Name:        c.Name,
			Description: c.Description,
			Prompt:      c.Prompt,
		}, nil
	}
	return agent.Preset{}, fmt.Errorf("preset %q is not configured", id)
}

// listPresets returns presets for the web interface.
func (d *Daemon) listPresets() []web.PresetView {
	builtin := agent.BuiltinPresets()
	out := make([]web.PresetView, 0, len(builtin)+len(d.cfg.AgentPreset.Custom))

	for _, p := range builtin {
		out = append(out, web.PresetView{
			ID:          p.ID,
			Name:        p.Name,
			Description: p.Description,
			Prompt:      p.Prompt,
			Builtin:     true,
		})
	}
	for _, c := range d.cfg.AgentPreset.Custom {
		out = append(out, web.PresetView{
			ID:          c.ID,
			Name:        c.Name,
			Description: c.Description,
			Prompt:      c.Prompt,
		})
	}
	return out
}

// savePreset creates or updates a custom preset.
//
// Built-in presets are never replaced: their text ships with the program,
// and overriding it would make that preset unpredictable.
func (d *Daemon) savePreset(edit web.PresetEdit) error {
	if edit.ID == "" {
		return fmt.Errorf("preset id is required")
	}
	if !validPresetID(edit.ID) {
		return fmt.Errorf("preset id %q: use letters, digits, dash or underscore", edit.ID)
	}
	if _, ok := agent.FindBuiltinPreset(edit.ID); ok {
		return fmt.Errorf("preset %q is built in and cannot be replaced", edit.ID)
	}
	if strings.TrimSpace(edit.Prompt) == "" {
		return fmt.Errorf("preset prompt must not be empty")
	}

	next := *d.cfg
	next.AgentPreset.Custom = make([]config.CustomPreset, 0, len(d.cfg.AgentPreset.Custom)+1)

	original := edit.Original
	if original == "" {
		original = edit.ID
	}
	found := false
	for _, c := range d.cfg.AgentPreset.Custom {
		if c.ID == original {
			next.AgentPreset.Custom = append(next.AgentPreset.Custom, config.CustomPreset{
				ID: edit.ID, Name: edit.Name, Description: edit.Description, Prompt: edit.Prompt,
			})
			found = true
			continue
		}
		next.AgentPreset.Custom = append(next.AgentPreset.Custom, c)
	}
	if !found {
		next.AgentPreset.Custom = append(next.AgentPreset.Custom, config.CustomPreset{
			ID: edit.ID, Name: edit.Name, Description: edit.Description, Prompt: edit.Prompt,
		})
	}

	return d.persistConfig(&next)
}

// deletePreset removes a custom preset.
func (d *Daemon) deletePreset(id string) error {
	if _, ok := agent.FindBuiltinPreset(id); ok {
		return fmt.Errorf("preset %q is built in and cannot be removed", id)
	}

	next := *d.cfg
	next.AgentPreset.Custom = make([]config.CustomPreset, 0, len(d.cfg.AgentPreset.Custom))
	removed := false
	for _, c := range d.cfg.AgentPreset.Custom {
		if c.ID == id {
			removed = true
			continue
		}
		next.AgentPreset.Custom = append(next.AgentPreset.Custom, c)
	}
	if !removed {
		return fmt.Errorf("preset %q is not configured", id)
	}
	// A removed preset must not stay active.
	if next.AgentPreset.Active == id {
		next.AgentPreset.Active = agent.PresetStandard
	}
	return d.persistConfig(&next)
}

// setActivePreset makes a preset active.
func (d *Daemon) setActivePreset(id string) error {
	if _, err := d.resolvePreset(id); err != nil {
		return err
	}
	next := *d.cfg
	next.AgentPreset.Active = id
	return d.persistConfig(&next)
}

// validPresetID reports whether an id works as a flag value.
func validPresetID(id string) bool {
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return id != ""
}

// listRunning collects running requests for the web interface.
func (d *Daemon) listRunning() []web.RunningView {
	list := d.turns.list()
	out := make([]web.RunningView, 0, len(list))
	for _, t := range list {
		prompt := t.Prompt
		if len([]rune(prompt)) > 200 {
			prompt = string([]rune(prompt)[:200]) + "..."
		}
		out = append(out, web.RunningView{
			ID: t.ID, Model: t.Model, Preset: t.Preset,
			Prompt: prompt, CWD: t.CWD, Started: t.Started,
		})
	}
	return out
}

// cancelRunning stops a running request.
func (d *Daemon) cancelRunning(id string) bool {
	return d.turns.cancelByID(id)
}
