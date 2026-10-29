package config

import (
	"testing"
)

// TestDefaultValid checks that the defaults pass validation.
func TestDefaultValid(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config must be valid: %v", err)
	}
}

// TestParseRoundTrip checks writing and reading the config.
func TestParseRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.yaml"

	cfg := Default()
	cfg.Agent.MaxIterations = 7
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Agent.MaxIterations != 7 {
		t.Fatalf("expected 7 iterations, got %d", loaded.Agent.MaxIterations)
	}
}

// TestValidateRejectsBadEnum checks rejection of an unknown value.
func TestValidateRejectsBadEnum(t *testing.T) {
	cfg := Default()
	cfg.Output.Color = "rainbow"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for unknown color")
	}
}

// TestValidateRejectsBadRule checks rejection of an invalid regex.
func TestValidateRejectsBadRule(t *testing.T) {
	cfg := Default()
	cfg.Danger = append(cfg.Danger, DangerRule{Name: "bad", Pattern: "([", Severity: "high"})
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for invalid pattern")
	}
}
