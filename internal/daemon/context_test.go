package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yourok/cdx/internal/agent"
)

// TestContextFilesBothLevels checks reading from both directories.
func TestContextFilesBothLevels(t *testing.T) {
	d := newTestDaemon(t)
	d.cfg.Files.ContextFiles = []string{"agent.md", "skills.md"}

	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	writeFile(t, root, "agent.md", "правила проекта")
	writeFile(t, root, "skills.md", "навыки проекта")
	writeFile(t, sub, "agent.md", "правила поддиректории")

	files := d.contextFiles(sub, root)
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d: %+v", len(files), files)
	}
	// The session root comes first, then the current directory.
	if files[0].Dir != root || files[0].Name != "agent.md" {
		t.Fatalf("first file must be agent.md from the session root, got %+v", files[0])
	}
	if files[1].Dir != root || files[1].Name != "skills.md" {
		t.Fatalf("second file must be skills.md from the session root, got %+v", files[1])
	}
	if files[2].Dir != sub || files[2].Name != "agent.md" {
		t.Fatalf("third file must be agent.md from the cwd, got %+v", files[2])
	}
}

// TestContextFilesSameDirOnce checks there are no duplicates.
func TestContextFilesSameDirOnce(t *testing.T) {
	d := newTestDaemon(t)
	dir := t.TempDir()
	writeFile(t, dir, "agent.md", "правила")

	files := d.contextFiles(dir, dir)
	if len(files) != 1 {
		t.Fatalf("expected single read when dirs match, got %d", len(files))
	}
}

// TestContextFilesNoSession checks reading from the current directory only.
func TestContextFilesNoSession(t *testing.T) {
	d := newTestDaemon(t)
	dir := t.TempDir()
	writeFile(t, dir, "agent.md", "правила")

	files := d.contextFiles(dir, "")
	if len(files) != 1 || files[0].Dir != dir {
		t.Fatalf("expected only cwd file, got %+v", files)
	}
}

// TestContextFilesDisabled checks disabling via an empty list.
func TestContextFilesDisabled(t *testing.T) {
	d := newTestDaemon(t)
	d.cfg.Files.ContextFiles = nil

	dir := t.TempDir()
	writeFile(t, dir, "agent.md", "правила")

	if files := d.contextFiles(dir, ""); len(files) != 0 {
		t.Fatalf("expected no files when list is empty, got %+v", files)
	}
}

// TestContextFilesMissing checks a missing file is not an error.
func TestContextFilesMissing(t *testing.T) {
	d := newTestDaemon(t)
	if files := d.contextFiles(t.TempDir(), ""); len(files) != 0 {
		t.Fatalf("expected no files, got %+v", files)
	}
}

// TestReadTextFileTruncates checks a large file is cut.
func TestReadTextFileTruncates(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", 3000)
	writeFile(t, dir, "big.md", big)

	content, ok := readTextFile(dir, "big.md", 1)
	if !ok {
		t.Fatal("expected file to be read")
	}
	if len(content) > 2100 {
		t.Fatalf("content must be truncated, got %d bytes", len(content))
	}
	if !strings.Contains(content, "truncated") {
		t.Fatal("truncation must be marked in the content")
	}
}

// TestReadTextFileRejectsPath checks a name with a path is refused.
func TestReadTextFileRejectsPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "secret.md", "x")

	for _, name := range []string{"../secret.md", "sub/secret.md", ""} {
		if _, ok := readTextFile(dir, name, 50); ok {
			t.Fatalf("name %q must be rejected", name)
		}
	}
}

// TestReadTextFileSkipsDirectory checks a directory is skipped.
func TestReadTextFileSkipsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agent.md"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, ok := readTextFile(dir, "agent.md", 50); ok {
		t.Fatal("directory must not be read as a file")
	}
}

// TestRenderContextFiles checks the prompt block format.
func TestPromptPartsOrder(t *testing.T) {
	files := []agent.ContextFile{
		{Dir: "/root", Name: "agent.md", Content: "правила"},
		{Dir: "/root/sub", Name: "agent.md", Content: "уточнение"},
	}
	parts := agent.PromptParts{
		Stable:      "STABLE PART",
		Files:       files,
		Environment: "## Environment\n- cwd: /root",
	}

	out := parts.Render()
	if !strings.Contains(out, "## agent.md (/root)") {
		t.Fatalf("missing header for root file: %s", out)
	}
	if !strings.Contains(out, "правила") || !strings.Contains(out, "уточнение") {
		t.Fatalf("missing contents: %s", out)
	}

	// Order: prompt, then files, then environment.
	stableAt := strings.Index(out, "STABLE PART")
	fileAt := strings.Index(out, "## agent.md")
	envAt := strings.Index(out, "## Environment")
	if stableAt < 0 || fileAt < 0 || envAt < 0 {
		t.Fatalf("all blocks must be present: %s", out)
	}
	if !(stableAt < fileAt && fileAt < envAt) {
		t.Fatalf("wrong block order: stable=%d file=%d env=%d", stableAt, fileAt, envAt)
	}

	if (agent.PromptParts{}).Render() != "" {
		t.Fatal("empty parts must render nothing")
	}
}

// TestToContextFiles checks the file conversion.
func TestToContextFiles(t *testing.T) {
	out := toContextFiles([]contextFile{{Dir: "/d", Name: "agent.md", Content: "x"}})
	if len(out) != 1 || out[0].Name != "agent.md" {
		t.Fatalf("unexpected result %+v", out)
	}
	if toContextFiles(nil) != nil {
		t.Fatal("nil input must give nil output")
	}
}

// TestMarkerWriteRemove checks the pointer file.
func TestMarkerWriteRemove(t *testing.T) {
	dir := t.TempDir()

	if err := writeSessionMarker(dir, "abc123"); err != nil {
		t.Fatalf("write: %v", err)
	}

	id, ok := readSessionMarker(dir)
	if !ok || id != "abc123" {
		t.Fatalf("expected abc123, got %q ok=%v", id, ok)
	}

	if err := removeSessionMarker(dir); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := readSessionMarker(dir); ok {
		t.Fatal("marker must be removed")
	}
	// The .cdx directory is removed too.
	if _, err := os.Stat(filepath.Join(dir, ".cdx")); !os.IsNotExist(err) {
		t.Fatalf("marker dir must be removed, err=%v", err)
	}
}

// TestMarkerMissing checks a missing marker.
func TestMarkerMissing(t *testing.T) {
	if _, ok := readSessionMarker(t.TempDir()); ok {
		t.Fatal("missing marker must not be read")
	}
}

// TestMarkerRemoveMissing checks removing a missing marker.
func TestMarkerRemoveMissing(t *testing.T) {
	if err := removeSessionMarker(t.TempDir()); err != nil {
		t.Fatalf("remove on empty dir must not fail: %v", err)
	}
}
