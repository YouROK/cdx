package plugin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// newRepo starts a test server holding a plugin directory.
func newRepo(t *testing.T, name, manifest, code string) *httptest.Server {
	t.Helper()

	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.js"), []byte(code), 0o644); err != nil {
		t.Fatalf("code: %v", err)
	}

	srv := httptest.NewServer(http.FileServer(http.Dir(root)))
	t.Cleanup(srv.Close)
	return srv
}

// TestFetchDownloadsBoth checks that the manifest and the code are fetched.
func TestFetchDownloadsBoth(t *testing.T) {
	srv := newRepo(t, "demo",
		`{"name":"demo","version":"1.0.0","entry":"plugin.js","tools":[{"name":"t","input_schema":{"type":"object"}}]}`,
		`cdx.exports={t:function(){return 1;}};`)

	m, files, err := Fetch(srv.URL + "/demo/manifest.json")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if m.Name != "demo" || m.Version != "1.0.0" {
		t.Fatalf("unexpected manifest %+v", m)
	}
	if len(files) != 2 {
		t.Fatalf("expected two files, got %d", len(files))
	}
	if string(files["plugin.js"]) == "" {
		t.Fatal("entry code must be downloaded")
	}
}

// TestFetchDirectoryURL covers a directory URL without a file name.
func TestFetchDirectoryURL(t *testing.T) {
	srv := newRepo(t, "demo",
		`{"name":"demo","version":"1.0.0","entry":"plugin.js","tools":[{"name":"t","input_schema":{"type":"object"}}]}`,
		`cdx.exports={t:function(){return 1;}};`)

	if _, _, err := Fetch(srv.URL + "/demo"); err != nil {
		t.Fatalf("Fetch by directory: %v", err)
	}
}

// TestFetchMissingManifest covers a missing manifest.
func TestFetchMissingManifest(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	if _, _, err := Fetch(srv.URL + "/nope/manifest.json"); err == nil {
		t.Fatal("expected error for 404")
	}
}

// TestFetchEmptyURL covers an empty address.
func TestFetchEmptyURL(t *testing.T) {
	if _, _, err := Fetch(""); err == nil {
		t.Fatal("expected error for empty url")
	}
}

// TestValidateFilesRejectsBadEntry covers validation before writing.
func TestValidateFilesRejectsBadEntry(t *testing.T) {
	m := &Manifest{Name: "demo", Version: "1.0.0", Entry: "../evil.js"}
	if err := m.ValidateFiles(map[string][]byte{}); err == nil {
		t.Fatal("path traversal must be rejected")
	}
}

// TestInstallWritesFiles checks that files reach the disk.
func TestInstallWritesFiles(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		"manifest.json": []byte(`{}`),
		"plugin.js":     []byte(`code`),
	}

	if err := Install(root, "demo", files); err != nil {
		t.Fatalf("Install: %v", err)
	}
	for _, name := range []string{"manifest.json", "plugin.js"} {
		if _, err := os.Stat(filepath.Join(root, "demo", name)); err != nil {
			t.Fatalf("%s must be written: %v", name, err)
		}
	}
}

// TestInstallRejectsBadName covers a name containing a path.
func TestInstallRejectsBadName(t *testing.T) {
	for _, name := range []string{"", "../evil", "a/b"} {
		if err := Install(t.TempDir(), name, map[string][]byte{}); err == nil {
			t.Fatalf("name %q must be rejected", name)
		}
	}
}
