package plugin

import (
	"context"
	"fmt"
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

// newAsyncTestManager loads one plugin into an isolated manager.
func newAsyncTestManager(t *testing.T, id, script string) (*Manager, *Registry) {
	t.Helper()

	tmp := t.TempDir()
	st, err := store.Open(store.Options{
		DBPath:        filepath.Join(tmp, "cdx.db"),
		MasterKeyPath: filepath.Join(tmp, "master.key"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	reg := NewRegistry()
	mgr := NewManager(RuntimeDependencies{
		Store:    st,
		Bus:      bus.New(),
		Router:   router.New(),
		Registry: reg,
		Logger:   logx.New(),
	})
	t.Cleanup(mgr.CloseAll)

	pluginsDir := filepath.Join(tmp, "plugins")
	manifest := fmt.Sprintf(`{
		"id": %q,
		"name": %q,
		"version": "1.0.0",
		"description": "Async runtime test",
		"entry": "main.js"
	}`, id, id)
	writePluginFiles(t, pluginsDir, id, manifest, script)
	if _, err := mgr.LoadPlugin(context.Background(), filepath.Join(pluginsDir, id)); err != nil {
		t.Fatalf("load plugin: %v", err)
	}
	return mgr, reg
}

func TestAsyncWorkdirSurvivesAwait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	_, reg := newAsyncTestManager(t, "async-workdir", fmt.Sprintf(`
		function setup(cdx) {
			cdx.tools.register({
				name: "read_after_await",
				description: "Reads the workdir after an await",
				input_schema: { type: "object", properties: {} }
			}, async function (args) {
				await cdx.sys.fetch(%q, { method: "GET", timeout_ms: 5000 });
				return cdx.workdir();
			});
		}
	`, srv.URL))

	workdir := t.TempDir()
	ctx := WithWorkdir(context.Background(), workdir)
	res, err := reg.CallTool(ctx, "read_after_await", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res != workdir {
		t.Fatalf("workdir after await = %v, want %q", res, workdir)
	}
}

func TestAsyncHandlerRejects(t *testing.T) {
	_, reg := newAsyncTestManager(t, "async-reject", `
		function setup(cdx) {
			cdx.tools.register({
				name: "explode",
				description: "Fails after a pause",
				input_schema: { type: "object", properties: {} }
			}, async function (args) {
				await cdx.sleep(1);
				throw new Error("boom");
			});
		}
	`)

	_, err := reg.CallTool(context.Background(), "explode", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected the rejection reason, got %v", err)
	}
}
