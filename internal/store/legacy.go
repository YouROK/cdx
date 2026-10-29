package store

import (
	"encoding/json"
	"fmt"
)

// legacyModel is a model record in the old format.
//
// Connection details used to live in the model itself rather than in a
// provider. The type only reads old records during the move to providers.
type legacyModel struct {
	Name      string `json:"name"`
	Protocol  string `json:"protocol"`
	BaseURL   string `json:"base_url"`
	Remote    string `json:"model"`
	Provider  string `json:"provider"`
	Default   bool   `json:"default"`
	CreatedAt int64  `json:"created_at"`

	ContextWindow int       `json:"context_window"`
	MaxTokens     int       `json:"max_tokens"`
	Temperature   float64   `json:"temperature"`
	Reasoning     Reasoning `json:"reasoning"`
	Compat        Compat    `json:"compat"`
}

// ReadLegacyModel reads a model record in the old format.
//
// A nil result means the record already has a provider, so it is in the new
// format.
func (s *Store) ReadLegacyModel(key string) (*legacyModel, error) {
	raw, found, err := s.GetBytes(BucketModels, key)
	if err != nil || !found {
		return nil, err
	}

	var out legacyModel
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode legacy model %s: %w", key, err)
	}
	if out.Provider != "" {
		return nil, nil
	}
	if out.Name == "" {
		out.Name = key
	}
	return &out, nil
}

// ToProviderAndModel converts an old record into a provider and a model.
func (l *legacyModel) ToProviderAndModel() (*Provider, *Model) {
	provider := &Provider{
		Name:      l.Name,
		Protocol:  l.Protocol,
		BaseURL:   l.BaseURL,
		CreatedAt: l.CreatedAt,
	}
	model := &Model{
		Provider:      l.Name,
		Name:          l.Name,
		Remote:        l.Remote,
		Default:       l.Default,
		ContextWindow: l.ContextWindow,
		MaxTokens:     l.MaxTokens,
		Temperature:   l.Temperature,
		Reasoning:     l.Reasoning,
		Compat:        l.Compat,
		CreatedAt:     l.CreatedAt,
	}
	return provider, model
}
