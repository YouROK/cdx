package plugin

import (
	"archive/zip"
	"bytes"
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

func buildTestZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestInstallFromZipURLValidateEnableDisableAndRemove(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(store.Options{
		DBPath:        filepath.Join(tmp, "cdx.db"),
		MasterKeyPath: filepath.Join(tmp, "master.key"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	mgr := NewManager(RuntimeDependencies{
		Store:    st,
		Bus:      bus.New(),
		Router:   router.New(),
		Registry: NewRegistry(),
		Logger:   logx.New(),
	})
	defer mgr.CloseAll()

	userDir := filepath.Join(tmp, "user-plugins")
	_ = mgr.LoadDirs(context.Background(), []string{userDir})

	// 1. Valid plugin inside a subfolder in the zip.
	validZip := buildTestZip(t, map[string]string{
		"hello-plugin/manifest.json": `{
			"id": "hello-zip",
			"name": "Hello Zip",
			"version": "1.2.0",
			"min_cdx_version": "0.1.0",
			"description": "Installed from zip",
			"entry": "main.js"
		}`,
		"hello-plugin/main.js": `
			function setup(cdx) {
				cdx.tools.register({
					name: "zip_greet",
					description: "Greets from zip",
					input_schema: { type: "object" }
				}, function() {
					return "hello from zip";
				});
			}
		`,
	})

	// 2. Invalid plugin with higher min_cdx_version.
	futureVersionZip := buildTestZip(t, map[string]string{
		"manifest.json": `{
			"id": "future-plug",
			"name": "Future",
			"version": "1.0.0",
			"min_cdx_version": "99.0.0",
			"description": "Requires future cdx",
			"entry": "main.js"
		}`,
		"main.js": `function setup(cdx) {}`,
	})

	// 3. Invalid plugin with broken JS syntax.
	brokenJSZip := buildTestZip(t, map[string]string{
		"manifest.json": `{
			"id": "broken-js",
			"name": "Broken JS",
			"version": "1.0.0",
			"description": "Syntax error",
			"entry": "main.js"
		}`,
		"main.js": `function setup(cdx) { {{{ invalid syntax `,
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/valid.zip":
			_, _ = w.Write(validZip)
		case "/future.zip":
			_, _ = w.Write(futureVersionZip)
		case "/broken.zip":
			_, _ = w.Write(brokenJSZip)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// Reject incompatible min_cdx_version before installation.
	if _, err := mgr.InstallFromURL(context.Background(), srv.URL+"/future.zip", userDir); err == nil || !strings.Contains(err.Error(), "requires cdx >=") {
		t.Fatalf("expected min_cdx_version error, got %v", err)
	}

	// Reject syntax error in JS before installation.
	if _, err := mgr.InstallFromURL(context.Background(), srv.URL+"/broken.zip", userDir); err == nil || !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("expected JS syntax error, got %v", err)
	}

	// Install valid zip.
	info, err := mgr.InstallFromURL(context.Background(), srv.URL+"/valid.zip", userDir)
	if err != nil {
		t.Fatalf("InstallFromURL valid.zip failed: %v", err)
	}
	if info.ID != "hello-zip" || !info.Enabled || !info.Removable || len(info.Tools) != 1 {
		t.Fatalf("unexpected installed PluginInfo: %+v", info)
	}

	// Disable plugin and verify its tool is unregistered.
	disabledInfo, err := mgr.SetEnabled(context.Background(), "hello-zip", false)
	if err != nil || disabledInfo.Enabled {
		t.Fatalf("SetEnabled(false) failed: info=%+v err=%v", disabledInfo, err)
	}
	if _, ok := mgr.Registry().GetTool("zip_greet"); ok {
		t.Fatal("expected zip_greet tool to be unregistered when plugin is disabled")
	}

	// Re-enable plugin and verify its tool is active again.
	enabledInfo, err := mgr.SetEnabled(context.Background(), "hello-zip", true)
	if err != nil || !enabledInfo.Enabled {
		t.Fatalf("SetEnabled(true) failed: info=%+v err=%v", enabledInfo, err)
	}
	if _, ok := mgr.Registry().GetTool("zip_greet"); !ok {
		t.Fatal("expected zip_greet tool to be registered when plugin is re-enabled")
	}

	// Remove the installed plugin.
	if err := mgr.RemovePlugin("hello-zip"); err != nil {
		t.Fatalf("RemovePlugin failed: %v", err)
	}
	if _, ok := mgr.GetPluginInfo("hello-zip"); ok {
		t.Fatal("expected hello-zip to be removed from known plugins")
	}
}
