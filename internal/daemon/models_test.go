package daemon

import (
	"testing"

	"github.com/yourok/cdx/internal/store"
	"github.com/yourok/cdx/internal/web"
)

// TestSaveProviderAndModel checks creating a provider and a model in it.
func TestSaveProviderAndModel(t *testing.T) {
	d := newTestDaemon(t)

	if err := d.saveProvider(providerEdit("gw", "https://api.example.com/v1")); err != nil {
		t.Fatalf("saveProvider: %v", err)
	}
	if err := d.saveModel(modelEdit("gw", "mini")); err != nil {
		t.Fatalf("saveModel: %v", err)
	}

	providers, err := d.store.ListProviders()
	if err != nil || len(providers) != 1 {
		t.Fatalf("expected one provider, got %+v err=%v", providers, err)
	}
	models, err := d.store.ListModels("gw")
	if err != nil || len(models) != 1 {
		t.Fatalf("expected one model, got %+v err=%v", models, err)
	}
}

// TestModelNameIsUniqueAcrossProviders checks model names are unique.
//
// The name shows up in the CLI and history, so two providers cannot give
// models the same name.
func TestModelNameIsUniqueAcrossProviders(t *testing.T) {
	d := newTestDaemon(t)

	for _, name := range []string{"one", "two"} {
		if err := d.saveProvider(providerEdit(name, "https://example.com/v1")); err != nil {
			t.Fatalf("saveProvider %s: %v", name, err)
		}
	}
	if err := d.saveModel(modelEdit("one", "same")); err != nil {
		t.Fatalf("saveModel: %v", err)
	}
	if err := d.saveModel(modelEdit("two", "same")); err == nil {
		t.Fatal("expected the duplicate model name to be rejected")
	}
}

// TestDeleteProviderDropsModels checks models go with their provider.
func TestDeleteProviderDropsModels(t *testing.T) {
	d := newTestDaemon(t)

	if err := d.saveProvider(providerEdit("gw", "https://example.com/v1")); err != nil {
		t.Fatalf("saveProvider: %v", err)
	}
	if err := d.saveModel(modelEdit("gw", "mini")); err != nil {
		t.Fatalf("saveModel: %v", err)
	}
	if err := d.deleteProvider("gw"); err != nil {
		t.Fatalf("deleteProvider: %v", err)
	}

	models, err := d.store.ListModels("")
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("models must be dropped with the provider, got %+v", models)
	}
}

// TestProviderRenameMovesModels checks models move on rename.
func TestProviderRenameMovesModels(t *testing.T) {
	d := newTestDaemon(t)

	if err := d.saveProvider(providerEdit("old", "https://example.com/v1")); err != nil {
		t.Fatalf("saveProvider: %v", err)
	}
	if err := d.saveModel(modelEdit("old", "mini")); err != nil {
		t.Fatalf("saveModel: %v", err)
	}

	rename := providerEdit("new", "https://example.com/v1")
	rename.Original = "old"
	if err := d.saveProvider(rename); err != nil {
		t.Fatalf("rename: %v", err)
	}

	models, err := d.store.ListModels("new")
	if err != nil || len(models) != 1 {
		t.Fatalf("expected the model under the new provider, got %+v err=%v", models, err)
	}
	if models[0].Provider != "new" {
		t.Fatalf("model still points at %q", models[0].Provider)
	}
}

// TestModelResolutionUsesProvider checks the connection comes from the provider.
func TestModelResolutionUsesProvider(t *testing.T) {
	d := newTestDaemon(t)

	if err := d.saveProvider(providerEdit("gw", "https://api.example.com/v1")); err != nil {
		t.Fatalf("saveProvider: %v", err)
	}
	if err := d.saveModel(modelEdit("gw", "mini")); err != nil {
		t.Fatalf("saveModel: %v", err)
	}

	m, err := d.modelByName("mini")
	if err != nil {
		t.Fatalf("modelByName: %v", err)
	}
	if m.BaseURL != "https://api.example.com/v1" || m.Provider != "gw" {
		t.Fatalf("unexpected model %+v", m)
	}
	if m.Remote != "remote-mini" {
		t.Fatalf("unexpected remote %q", m.Remote)
	}
}

// TestModelRequiresProvider checks an unknown provider is refused.
func TestModelRequiresProvider(t *testing.T) {
	d := newTestDaemon(t)

	if err := d.saveModel(modelEdit("missing", "mini")); err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
}

// TestProviderRejectsBadName checks provider name validation.
func TestProviderRejectsBadName(t *testing.T) {
	d := newTestDaemon(t)

	if err := d.saveProvider(providerEdit("bad name", "")); err == nil {
		t.Fatal("expected the name with a space to be rejected")
	}
	if err := d.saveProvider(providerEdit("-lead", "")); err == nil {
		t.Fatal("expected the leading dash to be rejected")
	}
}

// TestMigrateLegacyModel checks an old-format record is migrated.
func TestMigrateLegacyModel(t *testing.T) {
	d := newTestDaemon(t)

	// An old-style record: the connection sits inside the model.
	legacy := map[string]any{
		"name": "old", "protocol": "openai-chat",
		"base_url": "https://legacy.example.com/v1", "model": "remote-old",
		"context_window": 1000, "max_tokens": 100, "default": true,
	}
	if err := d.store.PutJSON(store.BucketModels, "old", legacy); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	d.migrateLegacyModels()

	providers, err := d.store.ListProviders()
	if err != nil || len(providers) != 1 {
		t.Fatalf("expected one migrated provider, got %+v err=%v", providers, err)
	}
	if providers[0].BaseURL != "https://legacy.example.com/v1" {
		t.Fatalf("provider lost the address: %+v", providers[0])
	}

	models, err := d.store.ListModels("old")
	if err != nil || len(models) != 1 {
		t.Fatalf("expected one migrated model, got %+v err=%v", models, err)
	}
	if models[0].Remote != "remote-old" || !models[0].Default {
		t.Fatalf("model lost its settings: %+v", models[0])
	}

	// A second run creates no duplicates.
	d.migrateLegacyModels()
	all, err := d.store.ListModels("")
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("migration must be idempotent, got %+v", all)
	}
}

// providerEdit builds a provider edit for a test.
func providerEdit(name, baseURL string) web.ProviderEdit {
	return web.ProviderEdit{Name: name, BaseURL: baseURL, Protocol: "openai-chat"}
}

// modelEdit builds a model edit for a test.
func modelEdit(provider, name string) web.ModelEdit {
	return web.ModelEdit{
		Provider: provider, Name: name, Remote: "remote-" + name,
		ContextWindow: 1000, MaxTokens: 1000, Temperature: 1,
	}
}

// TestSettingsWithoutLimits checks empty values are not set.
//
// The answer limit and context window come from the model alone: the
// program has no global defaults.
func TestSettingsWithoutLimits(t *testing.T) {
	d := newTestDaemon(t)

	if err := d.saveProvider(providerEdit("gw", "https://example.com/v1")); err != nil {
		t.Fatalf("saveProvider: %v", err)
	}
	if err := d.saveModel(modelEdit("gw", "mini")); err != nil {
		t.Fatalf("saveModel: %v", err)
	}

	m, err := d.modelByName("mini")
	if err != nil {
		t.Fatalf("modelByName: %v", err)
	}
	if got := m.settings().MaxTokens; got != 1000 {
		t.Fatalf("model limit must be used, got %d", got)
	}
	if got := m.contextWindow(); got != 1000 {
		t.Fatalf("model context window must be used, got %d", got)
	}
}

// TestSettingsEmptyLimitsStayZero checks empty model values stay zero.
func TestSettingsEmptyLimitsStayZero(t *testing.T) {
	d := newTestDaemon(t)

	if err := d.saveProvider(providerEdit("gw", "https://example.com/v1")); err != nil {
		t.Fatalf("saveProvider: %v", err)
	}
	edit := modelEdit("gw", "mini")
	edit.MaxTokens = 0
	edit.ContextWindow = 0
	if err := d.saveModel(edit); err != nil {
		t.Fatalf("saveModel: %v", err)
	}

	m, err := d.modelByName("mini")
	if err != nil {
		t.Fatalf("modelByName: %v", err)
	}
	// Zero means unset: such a value is not sent to the provider.
	if got := m.settings().MaxTokens; got != 0 {
		t.Fatalf("unset limit must stay zero, got %d", got)
	}
	if got := m.contextWindow(); got != 0 {
		t.Fatalf("unset window must stay zero, got %d", got)
	}
}

// TestSetDefaultModel checks switching the default model.
//
// The list button changes only this flag and must not touch the other
// model fields.
func TestSetDefaultModel(t *testing.T) {
	d := newTestDaemon(t)

	if err := d.saveProvider(providerEdit("gw", "https://example.com/v1")); err != nil {
		t.Fatalf("saveProvider: %v", err)
	}
	for _, name := range []string{"first", "second"} {
		if err := d.saveModel(modelEdit("gw", name)); err != nil {
			t.Fatalf("saveModel %s: %v", name, err)
		}
	}

	if err := d.setDefaultModel("gw", "first"); err != nil {
		t.Fatalf("setDefaultModel: %v", err)
	}
	if err := d.setDefaultModel("gw", "second"); err != nil {
		t.Fatalf("setDefaultModel: %v", err)
	}

	def, err := d.store.DefaultModel()
	if err != nil {
		t.Fatalf("DefaultModel: %v", err)
	}
	if def.Name != "second" {
		t.Fatalf("expected the newest default, got %q", def.Name)
	}

	// The other fields are unchanged.
	first, _, err := d.store.GetModel("gw", "first")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if first.Remote != "remote-first" || first.ContextWindow != 1000 {
		t.Fatalf("other fields must stay intact: %+v", first)
	}
}

// TestSetDefaultModelUnknown checks an unknown model is refused.
func TestSetDefaultModelUnknown(t *testing.T) {
	d := newTestDaemon(t)

	if err := d.saveProvider(providerEdit("gw", "https://example.com/v1")); err != nil {
		t.Fatalf("saveProvider: %v", err)
	}
	if err := d.setDefaultModel("gw", "nope"); err == nil {
		t.Fatal("expected an error for an unknown model")
	}
}
