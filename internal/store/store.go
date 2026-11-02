// Package store manages persistent state and per-plugin plain and encrypted buckets in BoltDB.
package store

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
	"sort"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Core bucket names and plugin bucket prefixes.
const (
	BucketState    = "core_state"
	BucketSessions = "core_sessions"
	BucketMessages = "core_messages"

	prefixPluginStorage = "plugin_storage:"
	prefixPluginSecrets = "plugin_secrets:"
)

// ErrInvalidPluginID is returned when an empty plugin identifier is passed.
var ErrInvalidPluginID = errors.New("plugin id is required")

// Options configures the database and master encryption key.
type Options struct {
	DBPath        string
	MasterKeyPath string
	Timeout       time.Duration
}

// Store wraps a BoltDB database and AES-256-GCM cipher for encrypted plugin secrets.
type Store struct {
	db   *bolt.DB
	aead cipher.AEAD
}

// Open opens or initializes the BoltDB database and loads or generates the AES-256-GCM master key.
func Open(opts Options) (*Store, error) {
	if opts.DBPath == "" {
		return nil, errors.New("database path is required")
	}
	if opts.MasterKeyPath == "" {
		opts.MasterKeyPath = filepath.Join(filepath.Dir(opts.DBPath), "master.key")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 3 * time.Second
	}

	if err := os.MkdirAll(filepath.Dir(opts.DBPath), 0o700); err != nil {
		return nil, fmt.Errorf("create database dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(opts.MasterKeyPath), 0o700); err != nil {
		return nil, fmt.Errorf("create master key dir: %w", err)
	}

	aead, err := loadOrCreateKey(opts.MasterKeyPath)
	if err != nil {
		return nil, err
	}

	db, err := bolt.Open(opts.DBPath, 0o600, &bolt.Options{Timeout: opts.Timeout})
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", opts.DBPath, err)
	}

	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{BucketState, BucketSessions, BucketMessages} {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return fmt.Errorf("create core bucket %s: %w", name, err)
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db, aead: aead}, nil
}

// Close closes the underlying BoltDB database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB exposes the raw BoltDB instance for core subsystems such as session history.
func (s *Store) DB() *bolt.DB {
	return s.db
}

// PutState stores a core state JSON value.
func (s *Store) PutState(key string, v any) error {
	return s.putJSON(BucketState, key, v)
}

// GetState reads a core state JSON value.
func (s *Store) GetState(key string, out any) (bool, error) {
	return s.getJSON(BucketState, key, out)
}

// DeleteState removes a core state entry.
func (s *Store) DeleteState(key string) error {
	return s.deleteKey(BucketState, key)
}

// PluginStorage returns an isolated plain JSON key-value store for a plugin.
func (s *Store) PluginStorage(pluginID string) (*PlainNamespace, error) {
	if pluginID == "" {
		return nil, ErrInvalidPluginID
	}
	bucket := prefixPluginStorage + pluginID
	if err := s.ensureBucket(bucket); err != nil {
		return nil, err
	}
	return &PlainNamespace{store: s, bucket: bucket}, nil
}

// PluginSecrets returns an isolated AES-256-GCM encrypted store for a plugin.
func (s *Store) PluginSecrets(pluginID string) (*SecretNamespace, error) {
	if pluginID == "" {
		return nil, ErrInvalidPluginID
	}
	bucket := prefixPluginSecrets + pluginID
	if err := s.ensureBucket(bucket); err != nil {
		return nil, err
	}
	return &SecretNamespace{store: s, bucket: bucket}, nil
}

func (s *Store) ensureBucket(name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(name))
		return err
	})
}

func (s *Store) putJSON(bucket, key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return err
		}
		return b.Put([]byte(key), data)
	})
}

func (s *Store) getJSON(bucket, key string, out any) (bool, error) {
	var raw []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		v := b.Get([]byte(key))
		if v != nil {
			raw = append([]byte(nil), v...)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if raw == nil {
		return false, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return false, fmt.Errorf("decode json: %w", err)
	}
	return true, nil
}

func (s *Store) deleteKey(bucket, key string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(key))
	})
}

func (s *Store) listKeys(bucket string) ([]string, error) {
	var keys []string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, _ []byte) error {
			keys = append(keys, string(k))
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)
	return keys, nil
}

// PlainNamespace provides isolated JSON key-value operations for a single plugin.
type PlainNamespace struct {
	store  *Store
	bucket string
}

// Set stores a JSON-serializable value under key.
func (p *PlainNamespace) Set(key string, value any) error {
	if key == "" {
		return errors.New("key is required")
	}
	return p.store.putJSON(p.bucket, key, value)
}

// Get retrieves and decodes a JSON value into out.
func (p *PlainNamespace) Get(key string, out any) (bool, error) {
	return p.store.getJSON(p.bucket, key, out)
}

// GetAny retrieves a JSON value as a generic Go value (map, slice, string, float64, bool).
func (p *PlainNamespace) GetAny(key string) (any, bool, error) {
	var val any
	found, err := p.store.getJSON(p.bucket, key, &val)
	return val, found, err
}

// Delete removes a key from the plugin's plain storage.
func (p *PlainNamespace) Delete(key string) error {
	return p.store.deleteKey(p.bucket, key)
}

// Keys returns all keys in sorted order.
func (p *PlainNamespace) Keys() ([]string, error) {
	return p.store.listKeys(p.bucket)
}

// List returns all key-value pairs in the plugin's plain storage.
func (p *PlainNamespace) List() (map[string]any, error) {
	out := map[string]any{}
	err := p.store.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(p.bucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var decoded any
			if err := json.Unmarshal(v, &decoded); err != nil {
				return fmt.Errorf("decode key %q: %w", string(k), err)
			}
			out[string(k)] = decoded
			return nil
		})
	})
	return out, err
}

// SecretNamespace provides isolated AES-256-GCM encrypted storage for a single plugin.
type SecretNamespace struct {
	store  *Store
	bucket string
}

// Set encrypts and stores a secret string under key. Setting an empty string deletes the key.
func (sec *SecretNamespace) Set(key, plaintext string) error {
	if key == "" {
		return errors.New("secret key is required")
	}
	if plaintext == "" {
		return sec.Delete(key)
	}

	nonce := make([]byte, sec.store.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	// Authenticate the bucket and key name as additional data so ciphertexts cannot be swapped across keys or plugins.
	aad := []byte(sec.bucket + ":" + key)
	sealed := sec.store.aead.Seal(nonce, nonce, []byte(plaintext), aad)

	return sec.store.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(sec.bucket))
		if err != nil {
			return err
		}
		return b.Put([]byte(key), sealed)
	})
}

// Get decrypts and returns the secret string stored under key.
func (sec *SecretNamespace) Get(key string) (string, bool, error) {
	var sealed []byte
	err := sec.store.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(sec.bucket))
		if b == nil {
			return nil
		}
		v := b.Get([]byte(key))
		if v != nil {
			sealed = append([]byte(nil), v...)
		}
		return nil
	})
	if err != nil {
		return "", false, err
	}
	if sealed == nil {
		return "", false, nil
	}

	ns := sec.store.aead.NonceSize()
	if len(sealed) < ns {
		return "", false, errors.New("corrupted secret: ciphertext too short")
	}

	aad := []byte(sec.bucket + ":" + key)
	plain, err := sec.store.aead.Open(nil, sealed[:ns], sealed[ns:], aad)
	if err != nil {
		return "", false, fmt.Errorf("decrypt secret %q: %w", key, err)
	}
	return string(plain), true, nil
}

// Has reports whether a secret is set for key without returning the plaintext.
func (sec *SecretNamespace) Has(key string) (bool, error) {
	var found bool
	err := sec.store.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(sec.bucket))
		if b == nil {
			return nil
		}
		found = b.Get([]byte(key)) != nil
		return nil
	})
	return found, err
}

// Delete removes a secret key.
func (sec *SecretNamespace) Delete(key string) error {
	return sec.store.deleteKey(sec.bucket, key)
}

// Keys returns all secret key names in sorted order without exposing their values.
func (sec *SecretNamespace) Keys() ([]string, error) {
	return sec.store.listKeys(sec.bucket)
}

// MaskedMap returns a map of key -> {"has_value": true} safe for UI responses.
func (sec *SecretNamespace) MaskedMap() (map[string]map[string]bool, error) {
	keys, err := sec.Keys()
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = map[string]bool{"has_value": true}
	}
	return out, nil
}

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
		return nil, fmt.Errorf("create aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm cipher: %w", err)
	}
	return aead, nil
}
