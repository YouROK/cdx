package pluginapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/perm"
)

// newAPI builds an API with permissive rights in a temporary directory.
func newAPI(t *testing.T) (*API, string) {
	t.Helper()

	dir := t.TempDir()
	cfg := config.Default()
	cfg.Perms.Read = "allow"
	cfg.Perms.Edit = "allow"
	pm, err := perm.New(cfg)
	if err != nil {
		t.Fatalf("perm: %v", err)
	}
	return New(Options{Perms: pm, Root: dir}), dir
}

// TestReadWrite covers writing then reading a file.
func TestReadWrite(t *testing.T) {
	api, _ := newAPI(t)

	if _, err := api.Write("a.txt", "привет"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	res, err := api.Read("a.txt")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.Text != "привет" {
		t.Fatalf("unexpected text %q", res.Text)
	}
}

// TestWriteCreated covers the created flag.
func TestWriteCreated(t *testing.T) {
	api, _ := newAPI(t)

	first, err := api.Write("new.txt", "x")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !first.Created {
		t.Fatal("first write must report creation")
	}
	second, err := api.Write("new.txt", "y")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if second.Created {
		t.Fatal("second write must not report creation")
	}
}

// TestPathEscapesRoot covers the ban on leaving the root.
func TestPathEscapesRoot(t *testing.T) {
	api, _ := newAPI(t)

	for _, path := range []string{"../outside.txt", "/etc/passwd"} {
		if _, err := api.Read(path); err == nil {
			t.Fatalf("path %q must be rejected", path)
		}
	}
}

// TestEditStrict covers the strict fragment replacement.
//
// An ambiguous fragment is refused, otherwise the model damages the wrong place.
func TestEditStrict(t *testing.T) {
	api, _ := newAPI(t)
	if _, err := api.Write("f.txt", "one two one"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := api.Edit("f.txt", "one", "1", false); err == nil {
		t.Fatal("ambiguous fragment must be rejected")
	}
	res, err := api.Edit("f.txt", "one", "1", true)
	if err != nil {
		t.Fatalf("Edit all: %v", err)
	}
	if res.Replaced != 2 {
		t.Fatalf("expected two replacements, got %d", res.Replaced)
	}
}

// TestEditMissingFragment covers a fragment that is not there.
func TestEditMissingFragment(t *testing.T) {
	api, _ := newAPI(t)
	if _, err := api.Write("f.txt", "текст"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := api.Edit("f.txt", "нет такого", "x", false); err == nil {
		t.Fatal("missing fragment must be rejected")
	}
}

// TestReadDirectory covers refusing to read a directory.
func TestReadDirectory(t *testing.T) {
	api, _ := newAPI(t)
	if err := api.Mkdir("sub"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if _, err := api.Read("sub"); err == nil {
		t.Fatal("reading a directory must fail")
	}
}

// TestReadBinary covers refusing a binary file.
func TestReadBinary(t *testing.T) {
	api, dir := newAPI(t)
	if err := os.WriteFile(filepath.Join(dir, "b.bin"), []byte{0x00, 0xff, 0xfe}, 0o644); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if _, err := api.Read("b.bin"); err == nil {
		t.Fatal("binary file must be rejected")
	}
}

// TestReadTruncates covers cutting a large file.
func TestReadTruncates(t *testing.T) {
	api, _ := newAPI(t)
	api.limits.MaxReadBytes = 10

	if _, err := api.Write("big.txt", strings.Repeat("a", 100)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	res, err := api.Read("big.txt")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !res.Truncated || len(res.Text) != 10 {
		t.Fatalf("expected truncation to 10 bytes, got %d truncated=%v", len(res.Text), res.Truncated)
	}
}

// TestList covers a directory listing.
func TestList(t *testing.T) {
	api, _ := newAPI(t)
	if err := api.Mkdir("sub"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if _, err := api.Write("file.txt", "x"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	list, err := api.List(".")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected two entries, got %d", len(list))
	}
	// Directories come first.
	if !list[0].Dir {
		t.Fatal("directories must come first")
	}
}

// TestGlob covers a name pattern search.
func TestGlob(t *testing.T) {
	api, _ := newAPI(t)
	if err := api.Mkdir("sub"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	for _, name := range []string{"a.go", "b.go", "c.txt", "sub/d.go"} {
		if _, err := api.Write(name, "x"); err != nil {
			t.Fatalf("Write %s: %v", name, err)
		}
	}

	plain, err := api.Glob("*.go")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(plain) != 2 {
		t.Fatalf("expected two files, got %v", plain)
	}

	deep, err := api.Glob("**/*.go")
	if err != nil {
		t.Fatalf("Glob deep: %v", err)
	}
	if len(deep) != 3 {
		t.Fatalf("expected three files with **, got %v", deep)
	}
}

// TestSearchSubstring covers a substring search.
func TestSearchSubstring(t *testing.T) {
	api, _ := newAPI(t)
	if _, err := api.Write("a.go", "func main() {\n\tprintln(1)\n}"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	res, err := api.Search("main", SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Matches) != 1 || res.Matches[0].Line != 1 {
		t.Fatalf("unexpected matches %+v", res.Matches)
	}
}

// TestSearchRegex covers a regular expression search.
func TestSearchRegex(t *testing.T) {
	api, _ := newAPI(t)
	if _, err := api.Write("a.go", "var x = 42"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	res, err := api.Search(`\d+`, SearchOptions{Regex: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Matches) != 1 {
		t.Fatalf("expected one match, got %+v", res.Matches)
	}
}

// TestSearchBadRegex covers the readable error on a bad expression.
func TestSearchBadRegex(t *testing.T) {
	api, _ := newAPI(t)

	// RE2 has no lookahead, and the error must say so.
	_, err := api.Search(`(?=x)`, SearchOptions{Regex: true})
	if err == nil {
		t.Fatal("invalid regex must be rejected")
	}
	if !strings.Contains(err.Error(), "RE2") {
		t.Fatalf("error must name the engine: %v", err)
	}
}

// TestSearchIgnoreCase covers a case-insensitive search.
func TestSearchIgnoreCase(t *testing.T) {
	api, _ := newAPI(t)
	if _, err := api.Write("a.txt", "Привет Мир"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	res, err := api.Search("привет", SearchOptions{IgnoreCase: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Matches) != 1 {
		t.Fatalf("expected one match, got %+v", res.Matches)
	}
}

// TestSearchSkipsBinary covers skipping binary files.
func TestSearchSkipsBinary(t *testing.T) {
	api, dir := newAPI(t)
	if err := os.WriteFile(filepath.Join(dir, "b.bin"), []byte{0x00, 0xff}, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// The large file is skipped by size, the small binary one is read but
	// must yield no matches.
	res, err := api.Search("\x00", SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Matches) != 0 {
		t.Fatalf("binary content must not match, got %+v", res.Matches)
	}
}
