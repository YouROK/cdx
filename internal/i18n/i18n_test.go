package i18n

import (
	"sort"
	"testing"
)

// TestInitLoadsLocales checks loading the catalogs.
func TestInitLoadsLocales(t *testing.T) {
	if err := Init("en"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	langs := Languages()
	if len(langs) < 2 {
		t.Fatalf("expected at least two locales, got %v", langs)
	}
	found := map[string]bool{}
	for _, name := range langs {
		found[name] = true
	}
	for _, want := range []string{"en", "ru"} {
		if !found[want] {
			t.Fatalf("locale %q must be embedded, got %v", want, langs)
		}
	}
}

// TestTranslateKnownKey checks translating a known key.
func TestTranslateKnownKey(t *testing.T) {
	if err := Init("ru"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if got := Tr("cli.daemon.started"); got != "демон запущен" {
		t.Fatalf("unexpected translation %q", got)
	}

	if err := Init("en"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if got := Tr("cli.daemon.started"); got != "daemon started" {
		t.Fatalf("unexpected translation %q", got)
	}
}

// TestTranslateWithArgs checks argument substitution.
func TestTranslateWithArgs(t *testing.T) {
	if err := Init("en"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	got := Tr("cli.session.deleted", "abc")
	if got != "session abc deleted" {
		t.Fatalf("unexpected translation %q", got)
	}
}

// TestUnknownKeyReturnsKey checks the behaviour for an unknown key.
func TestUnknownKeyReturnsKey(t *testing.T) {
	if err := Init("en"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if got := Tr("no.such.key"); got != "no.such.key" {
		t.Fatalf("unknown key must be returned as is, got %q", got)
	}
}

// TestFallbackToEnglish checks the fallback to English.
func TestFallbackToEnglish(t *testing.T) {
	if err := Init("ru"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// A key absent from every catalog is returned as is.
	if got := Tr("absent.key"); got != "absent.key" {
		t.Fatalf("unexpected fallback %q", got)
	}
}

// TestNormalize checks language code normalization.
func TestNormalize(t *testing.T) {
	// Empty variables make the result independent of the environment.
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "")

	cases := map[string]string{
		"ru_RU.UTF-8": "ru",
		"en_US":       "en",
		"RU":          "ru",
		"de-DE":       "de",
		"":            DefaultLanguage,
		"auto":        DefaultLanguage,
	}
	for input, want := range cases {
		if got := Normalize(input); got != want {
			t.Fatalf("Normalize(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestNormalizeDetectsEnvironment checks detection from the environment.
func TestNormalizeDetectsEnvironment(t *testing.T) {
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "ru_RU.UTF-8")

	if got := Normalize("auto"); got != "ru" {
		t.Fatalf("expected ru from environment, got %q", got)
	}
}

// TestUnknownLanguageFallsBack checks the fallback to English.
func TestUnknownLanguageFallsBack(t *testing.T) {
	if err := Init("xx"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if got := Language(); got != DefaultLanguage {
		t.Fatalf("expected fallback to %q, got %q", DefaultLanguage, got)
	}
}

// TestSetLanguage checks switching the language.
func TestSetLanguage(t *testing.T) {
	if err := Init("en"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	SetLanguage("ru")
	if got := Language(); got != "ru" {
		t.Fatalf("expected ru, got %q", got)
	}
}

// TestCatalogsHaveSameKeys checks translation completeness.
func TestCatalogsHaveSameKeys(t *testing.T) {
	if err := Init("en"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	english := Keys("en")
	if len(english) == 0 {
		t.Fatal("english catalog must not be empty")
	}

	for _, lang := range Languages() {
		if lang == "en" {
			continue
		}
		other := Keys(lang)
		if diff := diffKeys(english, other); len(diff) > 0 {
			t.Fatalf("locale %q is missing keys: %v", lang, diff)
		}
		if diff := diffKeys(other, english); len(diff) > 0 {
			t.Fatalf("locale %q has extra keys: %v", lang, diff)
		}
	}
}

// diffKeys returns keys of the first collection missing from the second.
func diffKeys(a, b []string) []string {
	have := make(map[string]bool, len(b))
	for _, key := range b {
		have[key] = true
	}

	var out []string
	for _, key := range a {
		if !have[key] {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// TestKeysUnknownLanguage checks the empty result for an unknown language.
func TestKeysUnknownLanguage(t *testing.T) {
	if err := Init("en"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if got := Keys("xx"); got != nil {
		t.Fatalf("expected nil for unknown language, got %v", got)
	}
}
