// Package store keeps daemon state in BoltDB.
package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Bucket names.
const (
	BucketProviders     = "providers"
	BucketModels        = "models"
	BucketSessions      = "sessions"
	BucketSessionsIndex = "sessions_index"
	BucketSessionsActiv = "sessions_active"
	BucketMessages      = "messages"
	BucketPermissions   = "permissions"
	BucketPluginCache   = "plugin_cache"
	BucketPluginHashes  = "plugin_hashes"
	BucketStatistics    = "statistics"
	BucketState         = "state"
)

var allBuckets = []string{
	BucketProviders, BucketModels, BucketSessions, BucketSessionsIndex, BucketSessionsActiv,
	BucketMessages, BucketPermissions, BucketPluginCache, BucketPluginHashes,
	BucketStatistics, BucketState,
}

// Store wraps the database.
type Store struct {
	db *bolt.DB
}

// Open opens the database and creates the buckets.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 3 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}

	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range allBuckets {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return fmt.Errorf("create bucket %s: %w", name, err)
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// PutJSON stores a value as JSON.
func (s *Store) PutJSON(bucket, key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode value: %w", err)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s is missing", bucket)
		}
		return b.Put([]byte(key), data)
	})
}

// GetJSON reads a value as JSON.
func (s *Store) GetJSON(bucket, key string, v any) (bool, error) {
	var found bool
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s is missing", bucket)
		}
		data := b.Get([]byte(key))
		if data == nil {
			return nil
		}
		if err := json.Unmarshal(data, v); err != nil {
			return fmt.Errorf("decode value: %w", err)
		}
		found = true
		return nil
	})
	return found, err
}

// PutString stores a string.
func (s *Store) PutString(bucket, key, value string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s is missing", bucket)
		}
		return b.Put([]byte(key), []byte(value))
	})
}

// GetString reads a string.
func (s *Store) GetString(bucket, key string) (string, bool, error) {
	var out string
	var found bool
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s is missing", bucket)
		}
		data := b.Get([]byte(key))
		if data == nil {
			return nil
		}
		out = string(data)
		found = true
		return nil
	})
	return out, found, err
}

// GetBytes reads a value unchanged.
func (s *Store) GetBytes(bucket, key string) ([]byte, bool, error) {
	var out []byte
	var found bool
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s is missing", bucket)
		}
		data := b.Get([]byte(key))
		if data == nil {
			return nil
		}
		out = append([]byte(nil), data...)
		found = true
		return nil
	})
	return out, found, err
}

// Delete removes a key.
func (s *Store) Delete(bucket, key string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s is missing", bucket)
		}
		return b.Delete([]byte(key))
	})
}

// List returns every key of a bucket.
func (s *Store) List(bucket string) ([]string, error) {
	var out []string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s is missing", bucket)
		}
		return b.ForEach(func(k, _ []byte) error {
			out = append(out, string(k))
			return nil
		})
	})
	return out, err
}

// TxWriter holds operations inside one write transaction.
type TxWriter interface {
	PutJSON(bucket, key string, v any) error
}

// txWriter implements TxWriter over a BoltDB transaction.
type txWriter struct {
	tx *bolt.Tx
}

// PutJSON stores a value as JSON inside a transaction.
func (w *txWriter) PutJSON(bucket, key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode value: %w", err)
	}
	b := w.tx.Bucket([]byte(bucket))
	if b == nil {
		return fmt.Errorf("bucket %s is missing", bucket)
	}
	return b.Put([]byte(key), data)
}

// Update runs a function in one write transaction.
func (s *Store) Update(fn func(tx TxWriter) error) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return fn(&txWriter{tx: tx})
	})
}

// UpdateRaw runs a function with access to the transaction.
func (s *Store) UpdateRaw(fn func(tx *bolt.Tx) error) error {
	return s.db.Update(fn)
}

// ListPrefix returns the keys that start with a prefix.
func (s *Store) ListPrefix(bucket, prefix string) ([]string, error) {
	var out []string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s is missing", bucket)
		}
		c := b.Cursor()
		p := []byte(prefix)
		for k, _ := c.Seek(p); k != nil && bytes.HasPrefix(k, p); k, _ = c.Next() {
			out = append(out, string(k))
		}
		return nil
	})
	return out, err
}

// DeleteRange removes the keys that start with a prefix.
func (s *Store) DeleteRange(bucket, prefix string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s is missing", bucket)
		}
		c := b.Cursor()
		p := []byte(prefix)
		var keys [][]byte
		for k, _ := c.Seek(p); k != nil && bytes.HasPrefix(k, p); k, _ = c.Next() {
			keys = append(keys, append([]byte(nil), k...))
		}
		for _, k := range keys {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

// BucketExists reports whether a bucket exists.
func (s *Store) BucketExists(name string) bool {
	exists := false
	_ = s.db.View(func(tx *bolt.Tx) error {
		exists = tx.Bucket([]byte(name)) != nil
		return nil
	})
	return exists
}

// FreePageRatio returns the share of free pages.
func (s *Store) FreePageRatio() float64 {
	stats := s.db.Stats()
	free := stats.FreePageN + stats.PendingPageN
	total := free + int(stats.TxStats.PageCount)
	if total == 0 {
		return 0
	}
	return float64(free) / float64(total)
}
