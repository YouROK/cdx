package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/yourok/cdx/internal/bus"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/router"
	"github.com/yourok/cdx/internal/store"
)

func TestEffectiveOrder(t *testing.T) {
	cases := []struct {
		name  string
		scope string
		order int
		want  int
	}{
		{"system vital", "system", 0, 0},
		{"system builtin", "system", 110, 110},
		{"user omitted", "user", 0, orderExternalDefault},
		{"user low", "user", 101, 1101},
		{"user base", "user", orderExternalBase, orderExternalBase},
		{"user external", "user", 2000, 2000},
	}
	for _, c := range cases {
		if got := effectiveOrder(c.scope, c.order); got != c.want {
			t.Fatalf("%s: effectiveOrder(%q, %d) = %d, want %d", c.name, c.scope, c.order, got, c.want)
		}
	}
}

func writeLifecyclePlugin(t *testing.T, root, id string, order int) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	manifest := fmt.Sprintf(`{"id":%q,"name":%q,"version":"1.0.0","description":"test plugin","entry":"main.js","order":%d}`, id, id, order)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte("function setup(cdx) {}"), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
}

type lifecycleRecorder struct {
	mu     sync.Mutex
	topics []string
	ids    []string
	orders []int
}

func (r *lifecycleRecorder) attach(b *bus.Bus) {
	topics := []string{
		EventPluginEnabled,
		EventPluginDisabled,
		EventPluginRemoved,
		EventPluginInstalled,
		EventPluginsReloaded,
	}
	for _, topic := range topics {
		topic := topic
		b.Subscribe("test", topic, func(_ context.Context, payload any) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.topics = append(r.topics, topic)
			data, _ := payload.(map[string]any)
			id, _ := data["id"].(string)
			order, _ := data["order"].(int)
			r.ids = append(r.ids, id)
			r.orders = append(r.orders, order)
		})
	}
}

func (r *lifecycleRecorder) snapshot() ([]string, []string, []int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.topics...), append([]string(nil), r.ids...), append([]int(nil), r.orders...)
}

func TestPluginLifecycleEvents(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(store.Options{
		DBPath:        filepath.Join(tmp, "cdx.db"),
		MasterKeyPath: filepath.Join(tmp, "master.key"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	b := bus.New()
	mgr := NewManager(RuntimeDependencies{
		Store:    st,
		Bus:      b,
		Router:   router.New(),
		Registry: NewRegistry(),
		Logger:   logx.New(),
	})
	defer mgr.CloseAll()

	userDir := filepath.Join(tmp, "user-plugins")
	sysDir := filepath.Join(tmp, "system-plugins")
	writeLifecyclePlugin(t, userDir, "alpha", 5000)
	writeLifecyclePlugin(t, sysDir, "beta", 100)

	rec := &lifecycleRecorder{}
	rec.attach(b)

	ctx := context.Background()
	if err := mgr.LoadDirs(ctx, []string{userDir, sysDir}); err != nil {
		t.Fatalf("LoadDirs failed: %v", err)
	}
	if topics, _, _ := rec.snapshot(); len(topics) != 0 {
		t.Fatalf("startup emitted events: %v", topics)
	}

	if _, err := mgr.SetEnabled(ctx, "alpha", false); err != nil {
		t.Fatalf("disable alpha: %v", err)
	}
	if _, err := mgr.SetEnabled(ctx, "alpha", false); err != nil {
		t.Fatalf("disable alpha again: %v", err)
	}
	if _, err := mgr.SetEnabled(ctx, "alpha", true); err != nil {
		t.Fatalf("enable alpha: %v", err)
	}
	if err := mgr.RemovePlugin(ctx, "alpha"); err != nil {
		t.Fatalf("remove alpha: %v", err)
	}

	zipBytes := buildTestZip(t, map[string]string{
		"manifest.json": `{"id":"gamma","name":"Gamma","version":"1.0.0","description":"test plugin","entry":"main.js","order":5000}`,
		"main.js":       `function setup(cdx) {}`,
	})
	if _, err := mgr.InstallFromZipBytes(ctx, zipBytes, userDir, "https://example.com/gamma.zip"); err != nil {
		t.Fatalf("install gamma: %v", err)
	}
	if err := mgr.Reload(ctx); err != nil {
		t.Fatalf("reload plugins: %v", err)
	}

	topics, ids, orders := rec.snapshot()
	wantTopics := []string{
		EventPluginDisabled,
		EventPluginEnabled,
		EventPluginDisabled,
		EventPluginRemoved,
		EventPluginInstalled,
		EventPluginsReloaded,
	}
	if len(topics) != len(wantTopics) {
		t.Fatalf("unexpected events: %v", topics)
	}
	for i := range wantTopics {
		if topics[i] != wantTopics[i] {
			t.Fatalf("event %d = %q, want %q (all: %v)", i, topics[i], wantTopics[i], topics)
		}
	}
	if ids[0] != "alpha" || orders[0] != 5000 {
		t.Fatalf("unexpected disabled payload: id=%q order=%d", ids[0], orders[0])
	}
	if ids[1] != "alpha" || orders[1] != 5000 {
		t.Fatalf("unexpected enabled payload: id=%q order=%d", ids[1], orders[1])
	}
	if ids[2] != "alpha" || ids[3] != "alpha" {
		t.Fatalf("unexpected remove payloads: %v", ids[:4])
	}
	if ids[4] != "gamma" {
		t.Fatalf("unexpected installed payload: %q", ids[4])
	}
}

func TestUserPluginOrderShifted(t *testing.T) {
	tmp := t.TempDir()
	mgr := NewManager(RuntimeDependencies{
		Bus:      bus.New(),
		Router:   router.New(),
		Registry: NewRegistry(),
		Logger:   logx.New(),
	})
	defer mgr.CloseAll()

	userDir := filepath.Join(tmp, "user-plugins")
	sysDir := filepath.Join(tmp, "system-plugins")
	writeLifecyclePlugin(t, userDir, "alpha", 101)
	writeLifecyclePlugin(t, sysDir, "beta", 900)

	if err := mgr.LoadDirs(context.Background(), []string{userDir, sysDir}); err != nil {
		t.Fatalf("LoadDirs failed: %v", err)
	}

	alpha, ok := mgr.GetPluginInfo("alpha")
	if !ok || alpha.Scope != "user" || alpha.Order != 1101 {
		t.Fatalf("unexpected user plugin info: %+v", alpha)
	}
	beta, ok := mgr.GetPluginInfo("beta")
	if !ok || beta.Scope != "system" || beta.Order != 900 {
		t.Fatalf("unexpected system plugin info: %+v", beta)
	}
}
