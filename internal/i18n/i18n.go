// Package i18n translates program messages through catalogs.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

//go:embed locales/*.json
var localeFS embed.FS

// DefaultLanguage is used when a translation is missing.
const DefaultLanguage = "en"

// Translator holds the loaded catalogs.
type Translator struct {
	mu   sync.RWMutex
	lang string
	// catalogs maps a language code to its catalog.
	catalogs map[string]map[string]string
}

var std = &Translator{catalogs: map[string]map[string]string{}}

// Init loads the embedded catalogs and picks the language.
func Init(lang string) error {
	catalogs := map[string]map[string]string{}
	for _, name := range availableLocales() {
		data, err := localeFS.ReadFile("locales/" + name)
		if err != nil {
			return fmt.Errorf("read locale %s: %w", name, err)
		}
		var catalog map[string]string
		if err := json.Unmarshal(data, &catalog); err != nil {
			return fmt.Errorf("parse locale %s: %w", name, err)
		}
		catalogs[strings.TrimSuffix(name, ".json")] = catalog
	}

	std.mu.Lock()
	std.catalogs = catalogs
	std.lang = pick(catalogs, Normalize(lang))
	std.mu.Unlock()
	return nil
}

// availableLocales lists the embedded catalogs.
func availableLocales() []string {
	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// pick returns the first available language from the preferences.
func pick(catalogs map[string]map[string]string, preferred string) string {
	if _, ok := catalogs[preferred]; ok {
		return preferred
	}
	if _, ok := catalogs[DefaultLanguage]; ok {
		return DefaultLanguage
	}
	for name := range catalogs {
		return name
	}
	return DefaultLanguage
}

// Normalize shortens a language code: ru_RU.UTF-8 -> ru.
func Normalize(tag string) string {
	if tag == "" || tag == "auto" {
		return detect()
	}
	tag = strings.ToLower(strings.TrimSpace(tag))
	if idx := strings.IndexAny(tag, "_-.@"); idx >= 0 {
		tag = tag[:idx]
	}
	return tag
}

// detect reads the language from the environment.
func detect() string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		value := os.Getenv(name)
		if value == "" {
			continue
		}
		return Normalize(value)
	}
	return DefaultLanguage
}

// SetLanguage switches the language.
func SetLanguage(lang string) {
	std.mu.Lock()
	std.lang = pick(std.catalogs, Normalize(lang))
	std.mu.Unlock()
}

// Language returns the current language.
func Language() string {
	std.mu.RLock()
	defer std.mu.RUnlock()
	return std.lang
}

// Tr translates a key and substitutes the arguments.
//
// A key missing in the selected language falls back to the English catalog,
// and when it is nowhere the key itself is returned so a typo stays visible.
func Tr(key string, args ...any) string {
	return std.translate(key, args...)
}

// translate resolves the key under a read lock.
func (t *Translator) translate(key string, args ...any) string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	text := key
	if catalog, ok := t.catalogs[t.lang]; ok {
		if value, found := catalog[key]; found {
			text = value
		}
	}
	if text == key {
		if catalog, ok := t.catalogs[DefaultLanguage]; ok {
			if value, found := catalog[key]; found {
				text = value
			}
		}
	}
	if len(args) == 0 {
		return text
	}
	return fmt.Sprintf(text, args...)
}

// Keys returns all catalog keys to check translation completeness.
func Keys(lang string) []string {
	std.mu.RLock()
	defer std.mu.RUnlock()

	catalog, ok := std.catalogs[Normalize(lang)]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(catalog))
	for key := range catalog {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// Languages lists the available languages.
func Languages() []string {
	std.mu.RLock()
	defer std.mu.RUnlock()

	out := make([]string, 0, len(std.catalogs))
	for name := range std.catalogs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
