package store

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Provider is a model provider: address, protocol and access key.
type Provider struct {
	Name      string `json:"name"`
	Protocol  string `json:"protocol"`
	BaseURL   string `json:"base_url"`
	CreatedAt int64  `json:"created_at"`
}

// Model is a model inside a provider.
type Model struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	// Remote is the model identifier on the provider side.
	Remote  string `json:"model"`
	Default bool   `json:"default"`

	// ContextWindow is the context size in tokens.
	ContextWindow int     `json:"context_window,omitempty"`
	MaxTokens     int     `json:"max_tokens,omitempty"`
	Temperature   float64 `json:"temperature,omitempty"`

	Reasoning Reasoning `json:"reasoning"`
	Compat    Compat    `json:"compat"`

	CreatedAt int64 `json:"created_at"`
}

// Key returns the record key of the model.
func (m *Model) Key() string {
	return ModelKey(m.Provider, m.Name)
}

// ModelKey joins a provider name and a model name into a record key.
func ModelKey(provider, name string) string {
	return provider + "/" + name
}

// Reasoning describes the reasoning levels of a model.
//
// Levels are the labels shown to the user; each maps to the value sent to the
// provider. An empty map means the model has no reasoning.
type Reasoning struct {
	Levels  map[string]string `json:"levels,omitempty"`
	Default string            `json:"default,omitempty"`
}

// Enabled reports whether the model supports reasoning.
func (r Reasoning) Enabled() bool {
	return len(r.Levels) > 0
}

// LevelNames returns the levels ordered from shallow to deep.
func (r Reasoning) LevelNames() []string {
	if len(r.Levels) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.Levels))
	for name := range r.Levels {
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool {
		return reasoningRank(out[i]) < reasoningRank(out[j])
	})
	return out
}

// WireValue returns the value to send for a level name.
//
// The default level is used when the request names none. A false result means
// the parameter must be left out.
func (r Reasoning) WireValue(level string) (string, bool) {
	if len(r.Levels) == 0 {
		return "", false
	}
	if level == "" {
		level = r.Default
	}
	if level == "" {
		return "", false
	}
	wire, ok := r.Levels[level]
	if !ok {
		return "", false
	}
	return wire, wire != ""
}

// reasoningRank orders levels from weakest to strongest.
func reasoningRank(level string) int {
	switch strings.ToLower(level) {
	case "off":
		return 0
	case "minimal":
		return 1
	case "low":
		return 2
	case "medium":
		return 3
	case "high":
		return 4
	case "xhigh":
		return 5
	case "max":
		return 6
	}
	return 7
}

// Compat holds protocol switches for provider quirks.
type Compat struct {
	SupportsReasoningEffort bool `json:"supports_reasoning_effort,omitempty"`
	// MaxTokensField names the limit field: max_tokens or max_completion_tokens.
	MaxTokensField string `json:"max_tokens_field,omitempty"`
	// SupportsTemperature is false for providers that reject temperature.
	SupportsTemperature bool `json:"supports_temperature,omitempty"`
}

// Protocols lists the supported protocols.
var Protocols = []string{"openai-chat", "openai-responses", "anthropic"}

// DefaultReasoningLevels is the usual level set for a reasoning model.
var DefaultReasoningLevels = map[string]string{
	"low":    "low",
	"medium": "medium",
	"high":   "high",
	"max":    "max",
}

// PutProvider stores a provider.
func (s *Store) PutProvider(p *Provider) error {
	if p.Name == "" {
		return fmt.Errorf("provider name is required")
	}
	if p.Protocol == "" {
		p.Protocol = "openai-chat"
	}
	if p.CreatedAt == 0 {
		p.CreatedAt = time.Now().Unix()
	}
	return s.PutJSON(BucketProviders, p.Name, p)
}

// GetProvider reads a provider by name.
func (s *Store) GetProvider(name string) (*Provider, bool, error) {
	var p Provider
	found, err := s.GetJSON(BucketProviders, name, &p)
	if err != nil || !found {
		return nil, found, err
	}
	return &p, true, nil
}

// ListProviders returns all providers ordered by name.
func (s *Store) ListProviders() ([]Provider, error) {
	names, err := s.List(BucketProviders)
	if err != nil {
		return nil, err
	}
	out := make([]Provider, 0, len(names))
	for _, name := range names {
		p, found, err := s.GetProvider(name)
		if err != nil {
			return nil, err
		}
		if found {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// DeleteProvider removes a provider with its models.
func (s *Store) DeleteProvider(name string) error {
	models, err := s.ListModels(name)
	if err != nil {
		return err
	}
	for i := range models {
		if err := s.DeleteModel(models[i].Provider, models[i].Name); err != nil {
			return err
		}
	}
	return s.Delete(BucketProviders, name)
}

// PutModel stores a model.
func (s *Store) PutModel(m *Model) error {
	if m.Provider == "" {
		return fmt.Errorf("provider is required")
	}
	if m.Name == "" {
		return fmt.Errorf("model name is required")
	}
	if m.Remote == "" {
		m.Remote = m.Name
	}
	if m.MaxTokens < 0 {
		return fmt.Errorf("max_tokens must not be negative")
	}
	if m.ContextWindow < 0 {
		return fmt.Errorf("context_window must not be negative")
	}
	if m.Reasoning.Default != "" {
		if _, ok := m.Reasoning.Levels[m.Reasoning.Default]; !ok {
			return fmt.Errorf("reasoning default %q is not among declared levels", m.Reasoning.Default)
		}
	}
	if m.CreatedAt == 0 {
		m.CreatedAt = time.Now().Unix()
	}
	if m.Default {
		if err := s.clearDefaultModel(); err != nil {
			return err
		}
	}
	return s.PutJSON(BucketModels, m.Key(), m)
}

// GetModel reads a model by provider and name.
func (s *Store) GetModel(provider, name string) (*Model, bool, error) {
	var m Model
	found, err := s.GetJSON(BucketModels, ModelKey(provider, name), &m)
	if err != nil || !found {
		return nil, found, err
	}
	return &m, true, nil
}

// GetModelByKey reads a model by record key.
func (s *Store) GetModelByKey(key string) (*Model, bool, error) {
	var m Model
	found, err := s.GetJSON(BucketModels, key, &m)
	if err != nil || !found {
		return nil, found, err
	}
	return &m, true, nil
}

// ListModels returns the models of all providers, or of one when the provider
// name is not empty.
func (s *Store) ListModels(provider string) ([]Model, error) {
	var keys []string
	var err error
	if provider == "" {
		keys, err = s.List(BucketModels)
	} else {
		keys, err = s.ListPrefix(BucketModels, provider+"/")
	}
	if err != nil {
		return nil, err
	}

	out := make([]Model, 0, len(keys))
	for _, key := range keys {
		m, found, err := s.GetModelByKey(key)
		if err != nil {
			return nil, err
		}
		if found {
			out = append(out, *m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// DeleteModel removes a model.
func (s *Store) DeleteModel(provider, name string) error {
	return s.Delete(BucketModels, ModelKey(provider, name))
}

// DefaultModel returns the default model.
//
// A single model counts as default even without the flag, so the first setup
// does not need an extra step.
func (s *Store) DefaultModel() (*Model, error) {
	list, err := s.ListModels("")
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Default {
			return &list[i], nil
		}
	}
	if len(list) == 1 {
		return &list[0], nil
	}
	return nil, fmt.Errorf("no default model configured")
}

// clearDefaultModel clears the flag on every model.
func (s *Store) clearDefaultModel() error {
	list, err := s.ListModels("")
	if err != nil {
		return err
	}
	for i := range list {
		if !list[i].Default {
			continue
		}
		list[i].Default = false
		if err := s.PutJSON(BucketModels, list[i].Key(), &list[i]); err != nil {
			return err
		}
	}
	return nil
}

// GetPrompt returns the user system prompt.
func (s *Store) GetPrompt() (string, bool, error) {
	return s.GetString(BucketState, "prompt.system")
}

// PutPrompt stores the system prompt.
func (s *Store) PutPrompt(text string) error {
	return s.PutString(BucketState, "prompt.system", text)
}

// DeletePrompt resets the prompt to the built-in one.
func (s *Store) DeletePrompt() error {
	return s.Delete(BucketState, "prompt.system")
}

// ValidName reports whether a provider or model name can be used as a key.
func ValidName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
