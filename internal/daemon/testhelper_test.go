package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/ipc"
	"github.com/yourok/cdx/internal/perm"
	"github.com/yourok/cdx/internal/registry"
	"github.com/yourok/cdx/internal/secret"
	"github.com/yourok/cdx/internal/sessions"
	"github.com/yourok/cdx/internal/store"
)

// newTestDaemon builds a daemon on temp directories, with no network.
func newTestDaemon(t *testing.T) *Daemon {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cdx.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	keys, err := secret.Open(dir)
	if err != nil {
		t.Fatalf("open secrets: %v", err)
	}

	cfg := config.Default()
	return newDaemonForTest(cfg, st, keys)
}

// writeFile writes a file into the test directory.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// newDaemonForTest builds a daemon from ready dependencies.
func newDaemonForTest(cfg *config.Config, st *store.Store, keys *secret.Keyring) *Daemon {
	pm, _ := perm.New(cfg)
	reg := registry.NewRegistry()
	_ = reg.Add(&registry.Tool{
		Spec: registry.Spec{
			ID: "plugin.test.echo", LLMName: "echo",
			Description: "Echo", Level: registry.LevelRead,
			Schema: map[string]any{"type": "object"},
		},
		Run: func(_ context.Context, _ map[string]any) (registry.Result, error) {
			return registry.Result{Content: "ok", OK: true}, nil
		},
	})

	return &Daemon{
		cfg:    cfg,
		store:  st,
		keys:   keys,
		reg:    reg,
		perms:  pm,
		sess:   sessions.New(st),
		state:  StateStarting,
		conns:  map[*ipc.Conn]struct{}{},
		stopCh: make(chan struct{}),
	}
}

// newEmptyRegistry returns a registry with no tools.
func newEmptyRegistry() *registry.Registry {
	return registry.NewRegistry()
}
