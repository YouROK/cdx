// Package secret stores API keys in an encrypted file.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// ErrNoKey is returned when the key is unavailable.
var ErrNoKey = errors.New("master key is missing")

// Keyring keeps model keys in memory and on disk.
type Keyring struct {
	mu   sync.RWMutex
	dir  string
	aead cipher.AEAD
	keys map[string]string
}

type payload struct {
	Version int               `json:"version"`
	Models  map[string]string `json:"models"`
}

// Open opens or creates the store in the directory.
func Open(dir string) (*Keyring, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create secret dir: %w", err)
	}

	masterPath := filepath.Join(dir, "master.key")
	aead, err := loadOrCreateKey(masterPath)
	if err != nil {
		return nil, err
	}

	k := &Keyring{
		dir:  dir,
		aead: aead,
		keys: map[string]string{},
	}
	if err := k.load(); err != nil {
		return nil, err
	}
	return k, nil
}

// loadOrCreateKey reads or creates the master key.
func loadOrCreateKey(path string) (cipher.AEAD, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read master key: %w", err)
		}
		raw = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, raw); err != nil {
			return nil, fmt.Errorf("generate master key: %w", err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return nil, fmt.Errorf("write master key: %w", err)
		}
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("master key %s: expected 32 bytes, got %d", path, len(raw))
	}

	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	return aead, nil
}

// load decrypts the secrets file.
func (k *Keyring) load() error {
	path := filepath.Join(k.dir, "secrets.enc")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read secrets: %w", err)
	}

	ns := k.aead.NonceSize()
	if len(data) < ns {
		return fmt.Errorf("secrets file is truncated")
	}

	plain, err := k.aead.Open(nil, data[:ns], data[ns:], nil)
	if err != nil {
		return fmt.Errorf("decrypt secrets: %w", err)
	}

	var p payload
	if err := json.Unmarshal(plain, &p); err != nil {
		return fmt.Errorf("decode secrets: %w", err)
	}
	if p.Models != nil {
		k.keys = p.Models
	}
	return nil
}

// save encrypts and writes the secrets.
func (k *Keyring) save() error {
	p := payload{Version: 1, Models: k.keys}
	plain, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode secrets: %w", err)
	}

	nonce := make([]byte, k.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	out := append(nonce, k.aead.Seal(nil, nonce, plain, nil)...)

	path := filepath.Join(k.dir, "secrets.enc")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("write secrets: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace secrets: %w", err)
	}
	return nil
}

// Get returns the model key.
func (k *Keyring) Get(model string) (string, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	v, ok := k.keys[model]
	return v, ok
}

// Has reports whether a model key is set.
func (k *Keyring) Has(model string) bool {
	_, ok := k.Get(model)
	return ok
}

// Set stores the model key.
func (k *Keyring) Set(model, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if key == "" {
		delete(k.keys, model)
	} else {
		k.keys[model] = key
	}
	return k.save()
}

// Delete removes the model key.
func (k *Keyring) Delete(model string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	delete(k.keys, model)
	return k.save()
}

// Names returns the model names that have keys.
func (k *Keyring) Names() []string {
	k.mu.RLock()
	defer k.mu.RUnlock()

	out := make([]string, 0, len(k.keys))
	for name := range k.keys {
		out = append(out, name)
	}
	return out
}
