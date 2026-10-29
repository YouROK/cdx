package store

import (
	"testing"
)

// TestReasoningWireValue checks the level to API value mapping.
func TestReasoningWireValue(t *testing.T) {
	r := Reasoning{
		Levels:  map[string]string{"low": "low", "medium": "medium", "max": "max"},
		Default: "medium",
	}

	if !r.Enabled() {
		t.Fatal("reasoning must be enabled")
	}
	if wire, ok := r.WireValue(""); !ok || wire != "medium" {
		t.Fatalf("expected default medium, got %q ok=%v", wire, ok)
	}
	if wire, ok := r.WireValue("max"); !ok || wire != "max" {
		t.Fatalf("expected max, got %q ok=%v", wire, ok)
	}
	if _, ok := r.WireValue("unknown"); ok {
		t.Fatal("unknown level must not resolve")
	}
}

// TestReasoningEmptyValue checks a level with no API value.
func TestReasoningEmptyValue(t *testing.T) {
	r := Reasoning{Levels: map[string]string{"off": ""}, Default: "off"}
	if _, ok := r.WireValue(""); ok {
		t.Fatal("empty wire value must mean no parameter")
	}
}

// TestReasoningLevelOrder checks the level order.
func TestReasoningLevelOrder(t *testing.T) {
	r := Reasoning{Levels: map[string]string{
		"max": "max", "low": "low", "high": "high", "medium": "medium",
	}}
	want := []string{"low", "medium", "high", "max"}
	got := r.LevelNames()
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

// TestPutModelRejectsBadDefault checks the refusal of an unknown default level.
func TestPutModelRejectsBadDefault(t *testing.T) {
	st := newTestStore(t)

	err := st.PutModel(&Model{
		Provider: "p", Name: "m",
		Reasoning: Reasoning{Levels: map[string]string{"low": "low"}, Default: "max"},
	})
	if err == nil {
		t.Fatal("expected error for unknown default level")
	}
}

// TestPutModelRequiresProvider checks that a provider is required.
func TestPutModelRequiresProvider(t *testing.T) {
	st := newTestStore(t)

	if err := st.PutModel(&Model{Name: "m"}); err == nil {
		t.Fatal("expected error for a model without a provider")
	}
}

// TestModelRoundTrip checks that settings survive a save and read.
func TestModelRoundTrip(t *testing.T) {
	st := newTestStore(t)

	m := &Model{
		Provider: "p", Name: "test", Remote: "remote-id",
		ContextWindow: 180000, MaxTokens: 32000, Temperature: 0.7,
		Reasoning: Reasoning{Levels: map[string]string{"medium": "medium"}, Default: "medium"},
		Compat:    Compat{SupportsReasoningEffort: true},
	}
	if err := st.PutModel(m); err != nil {
		t.Fatalf("PutModel: %v", err)
	}

	got, found, err := st.GetModel("p", "test")
	if err != nil || !found {
		t.Fatalf("GetModel: %v found=%v", err, found)
	}
	if got.Remote != "remote-id" || got.ContextWindow != 180000 || got.MaxTokens != 32000 {
		t.Fatalf("unexpected model %+v", got)
	}
	if !got.Compat.SupportsReasoningEffort {
		t.Fatal("compat must be preserved")
	}
}

// TestProviderScopesModels checks that one model name can live in two providers.
func TestProviderScopesModels(t *testing.T) {
	st := newTestStore(t)

	for _, provider := range []string{"a", "b"} {
		if err := st.PutProvider(&Provider{Name: provider}); err != nil {
			t.Fatalf("PutProvider %s: %v", provider, err)
		}
		if err := st.PutModel(&Model{Provider: provider, Name: "same", Remote: provider}); err != nil {
			t.Fatalf("PutModel %s: %v", provider, err)
		}
	}

	all, err := st.ListModels("")
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected two models, got %d", len(all))
	}

	one, err := st.ListModels("a")
	if err != nil {
		t.Fatalf("ListModels a: %v", err)
	}
	if len(one) != 1 || one[0].Provider != "a" {
		t.Fatalf("unexpected models of provider a: %+v", one)
	}
}

// TestDeleteProviderRemovesModels checks that models go with their provider.
func TestDeleteProviderRemovesModels(t *testing.T) {
	st := newTestStore(t)

	if err := st.PutProvider(&Provider{Name: "p"}); err != nil {
		t.Fatalf("PutProvider: %v", err)
	}
	if err := st.PutModel(&Model{Provider: "p", Name: "m"}); err != nil {
		t.Fatalf("PutModel: %v", err)
	}

	if err := st.DeleteProvider("p"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}

	models, err := st.ListModels("")
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("models must be dropped with the provider, got %+v", models)
	}
}

// TestDefaultModelSingle checks that a single model is picked without the flag.
func TestDefaultModelSingle(t *testing.T) {
	st := newTestStore(t)

	if _, err := st.DefaultModel(); err == nil {
		t.Fatal("expected an error with no models")
	}

	if err := st.PutModel(&Model{Provider: "p", Name: "only"}); err != nil {
		t.Fatalf("PutModel: %v", err)
	}
	got, err := st.DefaultModel()
	if err != nil {
		t.Fatalf("DefaultModel: %v", err)
	}
	if got.Name != "only" {
		t.Fatalf("expected the only model, got %q", got.Name)
	}
}

// TestDefaultModelFlag checks that the flag is cleared on the previous model.
func TestDefaultModelFlag(t *testing.T) {
	st := newTestStore(t)

	if err := st.PutModel(&Model{Provider: "p", Name: "first", Default: true}); err != nil {
		t.Fatalf("PutModel first: %v", err)
	}
	if err := st.PutModel(&Model{Provider: "p", Name: "second", Default: true}); err != nil {
		t.Fatalf("PutModel second: %v", err)
	}

	got, err := st.DefaultModel()
	if err != nil {
		t.Fatalf("DefaultModel: %v", err)
	}
	if got.Name != "second" {
		t.Fatalf("expected the newest default, got %q", got.Name)
	}

	first, _, err := st.GetModel("p", "first")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if first.Default {
		t.Fatal("previous default must be cleared")
	}
}
