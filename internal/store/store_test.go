package store

import (
	"bytes"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(Options{
		DBPath:        filepath.Join(dir, "cdx.db"),
		MasterKeyPath: filepath.Join(dir, "master.key"),
	})
	if err != nil {
		t.Fatalf("Open store failed: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestPlainNamespaceIsolation(t *testing.T) {
	s := openTestStore(t)

	p1, err := s.PluginStorage("openai")
	if err != nil {
		t.Fatalf("PluginStorage openai: %v", err)
	}
	p2, err := s.PluginStorage("anthropic")
	if err != nil {
		t.Fatalf("PluginStorage anthropic: %v", err)
	}

	if err := p1.Set("base_url", "https://api.openai.com/v1"); err != nil {
		t.Fatalf("p1.Set failed: %v", err)
	}
	if err := p2.Set("base_url", "https://api.anthropic.com"); err != nil {
		t.Fatalf("p2.Set failed: %v", err)
	}

	var got1, got2 string
	if found, err := p1.Get("base_url", &got1); err != nil || !found {
		t.Fatalf("p1.Get failed: found=%v err=%v", found, err)
	}
	if found, err := p2.Get("base_url", &got2); err != nil || !found {
		t.Fatalf("p2.Get failed: found=%v err=%v", found, err)
	}
	if got1 == got2 {
		t.Fatalf("expected isolated namespaces, got same value %q", got1)
	}

	all, err := p1.List()
	if err != nil {
		t.Fatalf("p1.List failed: %v", err)
	}
	if all["base_url"] != "https://api.openai.com/v1" {
		t.Fatalf("unexpected List value: %v", all)
	}
}

func TestSecretNamespaceEncryptionAndIsolation(t *testing.T) {
	s := openTestStore(t)

	sec1, err := s.PluginSecrets("openai")
	if err != nil {
		t.Fatalf("PluginSecrets openai: %v", err)
	}
	sec2, err := s.PluginSecrets("anthropic")
	if err != nil {
		t.Fatalf("PluginSecrets anthropic: %v", err)
	}

	secretVal := "sk-super-secret-token-12345"
	if err := sec1.Set("api_key", secretVal); err != nil {
		t.Fatalf("sec1.Set failed: %v", err)
	}

	// Verify that the raw bytes stored in BoltDB do not contain the plaintext secret.
	err = s.DB().View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(prefixPluginSecrets + "openai"))
		raw := b.Get([]byte("api_key"))
		if len(raw) == 0 {
			t.Fatal("expected encrypted bytes in database")
		}
		if bytes.Contains(raw, []byte(secretVal)) {
			t.Fatal("plaintext secret was found unencrypted in database")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("db view failed: %v", err)
	}

	// Verify decryption works for owner plugin.
	got, found, err := sec1.Get("api_key")
	if err != nil || !found || got != secretVal {
		t.Fatalf("sec1.Get = (%q, %v, %v), want %q", got, found, err, secretVal)
	}

	// Verify another plugin does not see the secret.
	if _, found, err := sec2.Get("api_key"); err != nil || found {
		t.Fatalf("expected sec2 to not have api_key, got found=%v err=%v", found, err)
	}

	// Verify AAD prevents moving ciphertext from one plugin/key to another.
	err = s.DB().Update(func(tx *bolt.Tx) error {
		b1 := tx.Bucket([]byte(prefixPluginSecrets + "openai"))
		b2 := tx.Bucket([]byte(prefixPluginSecrets + "anthropic"))
		return b2.Put([]byte("api_key"), b1.Get([]byte("api_key")))
	})
	if err != nil {
		t.Fatalf("copy ciphertext failed: %v", err)
	}
	if _, _, err := sec2.Get("api_key"); err == nil {
		t.Fatal("expected decryption failure when ciphertext is copied to another plugin namespace")
	}

	// Verify MaskedMap does not expose secret values.
	masked, err := sec1.MaskedMap()
	if err != nil {
		t.Fatalf("MaskedMap failed: %v", err)
	}
	if !masked["api_key"]["has_value"] {
		t.Fatalf("expected MaskedMap to report has_value=true, got %v", masked)
	}
}
