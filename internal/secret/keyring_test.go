package secret

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSaveLoad checks storing and reading a key.
func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()

	k, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := k.Set("model-a", "secret-key"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	again, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := again.Get("model-a")
	if !ok || got != "secret-key" {
		t.Fatalf("expected stored key, got %q ok=%v", got, ok)
	}
}

// TestFilePermissions checks the permissions of the secret files.
func TestFilePermissions(t *testing.T) {
	dir := t.TempDir()
	k, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := k.Set("m", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	for _, name := range []string{"master.key", "secrets.enc"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("%s must be 600, got %o", name, perm)
		}
	}
}

// TestKeyNotPlaintext checks that the key is not stored in plain text.
func TestKeyNotPlaintext(t *testing.T) {
	dir := t.TempDir()
	k, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := k.Set("m", "very-secret-value"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "secrets.enc"))
	if err != nil {
		t.Fatalf("read secrets: %v", err)
	}
	if containsBytes(data, []byte("very-secret-value")) {
		t.Fatal("secrets file must not contain the plaintext key")
	}
}

// TestCorruptedFile checks refusal on a damaged file.
func TestCorruptedFile(t *testing.T) {
	dir := t.TempDir()
	k, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := k.Set("m", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	path := filepath.Join(dir, "secrets.enc")
	if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("expected error for corrupted secrets")
	}
}

// containsBytes reports whether the data contains the substring.
func containsBytes(data, sub []byte) bool {
	if len(sub) == 0 || len(data) < len(sub) {
		return false
	}
	for i := 0; i+len(sub) <= len(data); i++ {
		match := true
		for j := range sub {
			if data[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
