package daemon

import (
	"fmt"
	"os"

	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/store"
	"github.com/yourok/cdx/internal/version"
)

// llmModel is a model together with its provider connection.
type llmModel struct {
	// Name is the model name, also used in history and logs.
	Name     string
	Provider string
	Protocol string
	BaseURL  string
	Remote   string
	Model    *store.Model
}

// settings turns model settings into request parameters.
//
// Every value comes from the model alone: there are no global defaults.
// A zero value means unset, and such a parameter is not sent.
func (m *llmModel) settings() requestSettings {
	// The default applies only when the model record is missing: a real
	// model always has a concrete temperature, and zero is valid there.
	out := requestSettings{Temperature: 1}
	if m.Model == nil {
		return out
	}

	if wire, ok := m.Model.Reasoning.WireValue(""); ok && m.Model.Compat.SupportsReasoningEffort {
		out.Reasoning = wire
	}
	if m.Model.MaxTokens > 0 {
		out.MaxTokens = m.Model.MaxTokens
	}
	// Zero is valid: a model may require a strict answer.
	out.Temperature = m.Model.Temperature
	out.MaxTokensField = m.Model.Compat.MaxTokensField
	out.SkipTemperature = m.Model.Compat.SupportsTemperature
	return out
}

// contextWindow returns the model context window.
//
// Zero means the window is unknown: history is not compacted in advance
// and the program runs until the provider reports an overflow.
func (m *llmModel) contextWindow() int {
	if m.Model == nil {
		return 0
	}
	return m.Model.ContextWindow
}

// defaultModel returns the default model.
func (d *Daemon) defaultModel() (*llmModel, error) {
	if name := d.cfg.Agent.Model; name != "" {
		return d.modelByName(name)
	}
	m, err := d.store.DefaultModel()
	if err != nil {
		return nil, fmt.Errorf("no model configured: %w", err)
	}
	return d.fromStore(m)
}

// modelByName finds a model by name across providers.
//
// The web interface keeps model names unique, so the search covers every
// provider instead of guessing the owner.
func (d *Daemon) modelByName(name string) (*llmModel, error) {
	list, err := d.store.ListModels("")
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Name == name {
			return d.fromStore(&list[i])
		}
	}
	return nil, fmt.Errorf("model %q is not configured", name)
}

// fromStore attaches the provider connection to a model.
func (d *Daemon) fromStore(m *store.Model) (*llmModel, error) {
	provider, found, err := d.store.GetProvider(m.Provider)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("provider %q of model %q is not configured", m.Provider, m.Name)
	}

	protocol := provider.Protocol
	if protocol == "" {
		protocol = "openai-chat"
	}
	return &llmModel{
		Name:     m.Name,
		Provider: provider.Name,
		Protocol: protocol,
		BaseURL:  provider.BaseURL,
		Remote:   m.Remote,
		Model:    m,
	}, nil
}

// providerKey returns the provider access key.
func (d *Daemon) providerKey(name string) string {
	key, _ := d.keys.Get(name)
	return key
}

// migrateLegacyModels moves old-format models into providers.
//
// An old record kept the address and protocol inside the model, with the
// key stored under the model name. Such a model becomes a provider of the
// same name: the connection moves there and the key keeps its name.
func (d *Daemon) migrateLegacyModels() {
	keys, err := d.store.List(store.BucketModels)
	if err != nil {
		logx.Warn("[Daemon] Failed to list models for migration: %v", err)
		return
	}

	for _, key := range keys {
		legacy, err := d.store.ReadLegacyModel(key)
		if err != nil {
			logx.Warn("[Daemon] Failed to read legacy model %s: %v", key, err)
			continue
		}
		if legacy == nil {
			// Already a new-format record.
			continue
		}

		provider, model := legacy.ToProviderAndModel()
		if err := d.store.PutProvider(provider); err != nil {
			logx.Warn("[Daemon] Failed to migrate provider %s: %v", provider.Name, err)
			continue
		}
		// The old key goes before the new one is written, otherwise a
		// second copy of the model would stay under the old name.
		if err := d.store.Delete(store.BucketModels, key); err != nil {
			logx.Warn("[Daemon] Failed to drop legacy model %s: %v", key, err)
			continue
		}
		if err := d.store.PutModel(model); err != nil {
			logx.Warn("[Daemon] Failed to migrate model %s: %v", key, err)
			continue
		}
		logx.Info("[Daemon] Migrated model %s into provider %s", model.Name, provider.Name)
	}
}

// modelNameExists reports whether a model name is taken.
func (d *Daemon) modelNameExists(name, exceptProvider, exceptName string) (bool, error) {
	list, err := d.store.ListModels("")
	if err != nil {
		return false, err
	}
	for i := range list {
		if list[i].Name != name {
			continue
		}
		if list[i].Provider == exceptProvider && list[i].Name == exceptName {
			continue
		}
		return true, nil
	}
	return false, nil
}

// versionString returns the program version.
func versionString() string { return version.Version }

// protoVersion returns the protocol version.
func protoVersion() int { return version.Protocol }

// pidOf returns the process pid.
func pidOf() int { return os.Getpid() }
