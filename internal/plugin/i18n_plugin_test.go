package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yourok/cdx/internal/bus"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/router"
	"github.com/yourok/cdx/internal/store"
)

func TestI18nPluginCatalogLayersAndLocale(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(store.Options{
		DBPath:        filepath.Join(tmp, "cdx.db"),
		MasterKeyPath: filepath.Join(tmp, "master.key"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	eb := bus.New()
	rtRouter := router.New()
	mgr := NewManager(RuntimeDependencies{
		Store:    st,
		Bus:      eb,
		Router:   rtRouter,
		Registry: NewRegistry(),
		Logger:   logx.New(),
	})
	defer mgr.CloseAll()

	userDir := filepath.Join(tmp, "user-plugins")
	sysDir := filepath.Join(tmp, "system-plugins")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("mkdir user plugins: %v", err)
	}
	if err := os.MkdirAll(sysDir, 0o755); err != nil {
		t.Fatalf("mkdir system plugins: %v", err)
	}
	if err := os.CopyFS(filepath.Join(sysDir, "i18n"), os.DirFS(filepath.Join("..", "..", "plugins", "i18n"))); err != nil {
		t.Fatalf("copy i18n plugin: %v", err)
	}

	writePluginFiles(t, userDir, "low-pack", `{"id":"low-pack","name":"Low Pack","version":"1.0.0","description":"low","entry":"main.js","order":2000}`, `
		function setup(cdx) {
			if (cdx.worker) return;
			cdx.bus.request("i18n:register", { id: cdx.id, tables: {
				en: { "pack.Message": "Low" },
				ru: { "pack.Message": "Низкий" }
			}});
		}
	`)
	writePluginFiles(t, userDir, "high-pack", `{"id":"high-pack","name":"High Pack","version":"1.0.0","description":"high","entry":"main.js","order":5000}`, `
		function setup(cdx) {
			if (cdx.worker) return;
			cdx.bus.request("i18n:register", { id: cdx.id, tables: {
				en: { "pack.Message": "High" },
				ru: { "pack.Message": "Высокий" }
			}});
		}
	`)

	ctx := context.Background()
	if err := mgr.LoadDirs(ctx, []string{userDir, sysDir}); err != nil {
		t.Fatalf("LoadDirs failed: %v", err)
	}

	catalog := func() map[string]any {
		res, err := eb.Request(ctx, "test", "i18n:catalog", map[string]any{})
		if err != nil {
			t.Fatalf("i18n:catalog failed: %v", err)
		}
		data, ok := res.(map[string]any)
		if !ok {
			t.Fatalf("unexpected catalog type %T", res)
		}
		return data
	}
	stringsOf := func(data map[string]any) map[string]string {
		raw, _ := data["strings"].(map[string]any)
		out := map[string]string{}
		for key, value := range raw {
			if text, ok := value.(string); ok {
				out[key] = text
			}
		}
		return out
	}

	en := stringsOf(catalog())
	if en["pack.Message"] != "High" {
		t.Fatalf("en layer order is wrong: %q", en["pack.Message"])
	}
	if en["i18n.Title"] != "Translations" {
		t.Fatalf("base catalog is missing: %q", en["i18n.Title"])
	}

	body := strings.NewReader(`{"locale":"ru"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/plugins/i18n/locale", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rtRouter.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set locale returned %d: %s", w.Code, w.Body.String())
	}

	ru := stringsOf(catalog())
	if ru["pack.Message"] != "Высокий" {
		t.Fatalf("ru layer order is wrong: %q", ru["pack.Message"])
	}
	if ru["i18n.Title"] != "Translations" {
		t.Fatalf("ru catalog does not fall back to en: %q", ru["i18n.Title"])
	}

	req = httptest.NewRequest(http.MethodGet, "/api/plugins/i18n/locales", nil)
	w = httptest.NewRecorder()
	rtRouter.ServeHTTP(w, req)
	var locales struct {
		Locale  string   `json:"locale"`
		Locales []string `json:"locales"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &locales); err != nil {
		t.Fatalf("decode locales: %v", err)
	}
	if locales.Locale != "ru" || len(locales.Locales) != 2 {
		t.Fatalf("unexpected locales response: %+v", locales)
	}

	if _, err := mgr.SetEnabled(ctx, "high-pack", false); err != nil {
		t.Fatalf("disable high pack: %v", err)
	}
	ru = stringsOf(catalog())
	if ru["pack.Message"] != "Низкий" {
		t.Fatalf("disabled plugin table was not dropped: %q", ru["pack.Message"])
	}

	if _, err := mgr.SetEnabled(ctx, "high-pack", true); err != nil {
		t.Fatalf("enable high pack: %v", err)
	}
	ru = stringsOf(catalog())
	if ru["pack.Message"] != "Высокий" {
		t.Fatalf("enabled plugin table was not restored: %q", ru["pack.Message"])
	}

	if err := mgr.Reload(ctx); err != nil {
		t.Fatalf("reload plugins: %v", err)
	}
	ru = stringsOf(catalog())
	if ru["pack.Message"] != "Высокий" {
		t.Fatalf("reload lost the translation tables: %q", ru["pack.Message"])
	}

	if _, err := eb.Request(ctx, "test", "i18n:register", map[string]any{}); err == nil {
		t.Fatal("expected an error for a registration without an id")
	}
}
