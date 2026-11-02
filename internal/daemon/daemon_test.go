package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yourok/cdx/internal/paths"
)

func repoPluginsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "plugins"))
}

func TestDefaultPluginsAndWebUI(t *testing.T) {
	tmp := t.TempDir()
	layout := paths.ResolveWithHome(tmp, func(string) string { return "" })
	layout.PluginDirs = []string{repoPluginsDir(t)}

	d, err := New(context.Background(), Options{
		Layout:   layout,
		HTTPAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatalf("daemon.New failed: %v", err)
	}
	if err := d.Start(); err != nil {
		t.Fatalf("daemon.Start failed: %v", err)
	}
	defer d.Stop()

	baseURL := "http://" + d.HTTPAddr()

	// 1. Verify WebUI plugin serves index.html on "/".
	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "<title>cdx</title>") {
		t.Fatalf("unexpected GET / response: %d %s", resp.StatusCode, string(body))
	}

	// 2. Verify all 6 default plugins are loaded in /api/core/status.
	statusResp, err := http.Get(baseURL + "/api/core/status")
	if err != nil {
		t.Fatalf("GET /api/core/status failed: %v", err)
	}
	var statusData struct {
		Plugins []map[string]any `json:"plugins"`
		Tools   []map[string]any `json:"tools"`
		UI      []map[string]any `json:"ui"`
	}
	_ = json.NewDecoder(statusResp.Body).Decode(&statusData)
	statusResp.Body.Close()

	if len(statusData.Plugins) != 6 {
		t.Fatalf("expected 6 default plugins loaded, got %d: %+v", len(statusData.Plugins), statusData.Plugins)
	}
	if len(statusData.Tools) != 5 {
		t.Fatalf("expected 5 local tools registered, got %d: %+v", len(statusData.Tools), statusData.Tools)
	}

	// 3. Test self-contained Providers plugin: add a provider with an encrypted API key and add a model.
	provPayload := `{"name":"deepseek","protocol":"openai-chat","base_url":"https://api.deepseek.com/v1","api_key":"sk-test-secret-key"}`
	pResp, err := http.Post(baseURL+"/api/plugins/providers/providers", "application/json", bytes.NewBufferString(provPayload))
	if err != nil {
		t.Fatalf("POST provider failed: %v", err)
	}
	var pOut map[string]any
	_ = json.NewDecoder(pResp.Body).Decode(&pOut)
	pResp.Body.Close()
	if pOut["has_api_key"] != true {
		t.Fatalf("expected provider has_api_key=true, got %+v", pOut)
	}

	modelPayload := `{"provider":"deepseek","name":"ds-chat","remote":"deepseek-chat","context_window":64000,"default":true}`
	mResp, err := http.Post(baseURL+"/api/plugins/providers/models", "application/json", bytes.NewBufferString(modelPayload))
	if err != nil {
		t.Fatalf("POST model failed: %v", err)
	}
	mResp.Body.Close()
	if mResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 adding model, got %d", mResp.StatusCode)
	}

	// Verify the core now sees the model registered by the providers plugin.
	models, err := d.registry.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "deepseek/ds-chat" {
		t.Fatalf("expected core registry to list deepseek/ds-chat, got %+v (err=%v)", models, err)
	}
}
