package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yourok/cdx/internal/bus"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/router"
	"github.com/yourok/cdx/internal/store"
)

// The Russian strings below are test data for the translation mechanism.
func TestPermissionQuestionsUseTheCatalog(t *testing.T) {
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

	ctx := context.Background()
	if _, err := mgr.LoadPlugin(ctx, filepath.Join("..", "..", "plugins", "i18n")); err != nil {
		t.Fatalf("load i18n plugin: %v", err)
	}
	if _, err := mgr.LoadPlugin(ctx, filepath.Join("..", "..", "plugins", "permissions")); err != nil {
		t.Fatalf("load permissions plugin: %v", err)
	}

	if _, err := eb.Request(ctx, "test", "i18n:register", map[string]any{
		"id": "test-ru",
		"tables": map[string]any{
			"ru": map[string]any{
				"permissions.ToolRead":            "Прочитать файл",
				"permissions.OutsideTitle":        "{action} вне рабочей папки",
				"permissions.OutsideBody":         "Действие выходит за рабочую папку.",
				"permissions.AllowOnce":           "Разрешить один раз",
				"permissions.Deny":                "Запретить",
				"permissions.DetailFile":          "Файл",
				"permissions.DetailPermission":    "Право",
				"permissions.DetailWorkingFolder": "Рабочая папка",
				"permissions.DetailOutside":       "Снаружи",
				"permissions.LevelRead":           "чтение",
			},
		},
	}); err != nil {
		t.Fatalf("register test catalog: %v", err)
	}

	body := strings.NewReader(`{"locale":"ru"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/plugins/i18n/locale", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rtRouter.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set locale returned %d: %s", w.Code, w.Body.String())
	}

	ns, err := st.PluginStorage("permissions")
	if err != nil {
		t.Fatalf("permissions storage: %v", err)
	}
	if err := ns.Set("config", map[string]any{
		"read_policy":      "allow",
		"edit_policy":      "allow",
		"dangerous_policy": "allow",
		"outside_policy":   "ask",
		"tool_rules":       map[string]any{},
	}); err != nil {
		t.Fatalf("set permissions config: %v", err)
	}

	out, err := eb.RunHook(ctx, "tool:before_call", map[string]any{
		"tool":       "read_file",
		"permission": "read",
		"workdir":    "/work",
		"arguments":  map[string]any{"path": "/etc/passwd"},
	})
	if err != nil {
		t.Fatalf("tool:before_call failed: %v", err)
	}
	ask, _ := out["ask"].(map[string]any)
	if ask == nil {
		t.Fatalf("expected an ask question, got %+v", out)
	}
	if title, _ := ask["title"].(string); title != "Прочитать файл вне рабочей папки" {
		t.Fatalf("unexpected translated title: %q", title)
	}
	if body, _ := ask["body"].(string); body != "Действие выходит за рабочую папку." {
		t.Fatalf("unexpected translated body: %q", body)
	}
	joined := strings.Join(questionDetails(ask), " ")
	for _, want := range []string{"Файл=/etc/passwd", "Право=чтение", "Рабочая папка=/work", "Снаружи=/etc/passwd"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the question details are missing %q: %v", want, joined)
		}
	}
	options, _ := ask["options"].([]any)
	if len(options) != 2 {
		t.Fatalf("unexpected options: %+v", ask["options"])
	}
	first, _ := options[0].(map[string]any)
	if first["label"] != "Разрешить один раз" {
		t.Fatalf("unexpected translated option: %+v", first)
	}
}
