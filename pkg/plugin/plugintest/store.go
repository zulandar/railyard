package plugintest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Store-fake limits mirror the real host's per-plugin caps
// (internal/pluginhost: 64 KiB value, 256-byte key, 1024 keys) so a
// plugin's unit tests exercise the same boundaries it will hit in
// production (railyard-77h.11). They are exported so tests can assert
// against them.
const (
	// FakeStoreMaxValueBytes is the largest value the fake accepts.
	FakeStoreMaxValueBytes = 64 * 1024
	// FakeStoreMaxKeyBytes is the longest key the fake accepts.
	FakeStoreMaxKeyBytes = 256
	// FakeStoreMaxKeys is the per-store key cap the fake enforces.
	FakeStoreMaxKeys = 1024
)

// FakeStore is an in-memory [plugin.Store] implementation for unit tests.
// It is namespaced by construction: one FakeStore holds one plugin's
// keys, so two plugins under test (each with its own [FakeHost]) never
// see each other's data. It enforces the same value/key/count limits the
// real host does so tests catch over-limit writes locally.
//
// FakeStore is safe for concurrent use.
type FakeStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

// NewFakeStore returns an empty FakeStore. Most tests obtain one via
// [FakeHost.Store]; construct one directly only when testing a plugin
// that takes a [plugin.Store] without a full host.
func NewFakeStore() *FakeStore {
	return &FakeStore{data: make(map[string][]byte)}
}

// Get returns the value stored under key and whether it was present. A
// missing key returns (nil, false, nil).
func (s *FakeStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[key]
	if !ok {
		return nil, false, nil
	}
	// Return a copy so a caller mutating the slice can't corrupt store
	// state — matching the real host, which returns freshly-decoded bytes.
	cp := make([]byte, len(v))
	copy(cp, v)
	return cp, true, nil
}

// Put inserts or overwrites the value under key, enforcing the same
// limits as the real host. An over-limit write returns an error and does
// not modify the store.
func (s *FakeStore) Put(_ context.Context, key string, value []byte) error {
	if key == "" {
		return fmt.Errorf("plugintest: Store.Put: key must not be empty")
	}
	if len(key) > FakeStoreMaxKeyBytes {
		return fmt.Errorf("plugintest: Store.Put: key length %d exceeds max %d bytes", len(key), FakeStoreMaxKeyBytes)
	}
	if len(value) > FakeStoreMaxValueBytes {
		return fmt.Errorf("plugintest: Store.Put: value size %d exceeds max %d bytes", len(value), FakeStoreMaxValueBytes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.data[key]; !exists && len(s.data) >= FakeStoreMaxKeys {
		return fmt.Errorf("plugintest: Store.Put: already holds %d keys (max %d)", len(s.data), FakeStoreMaxKeys)
	}
	cp := make([]byte, len(value))
	copy(cp, value)
	s.data[key] = cp
	return nil
}

// Delete removes key. Deleting an absent key is a no-op.
func (s *FakeStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

// List returns the keys beginning with prefix, sorted ascending. An empty
// prefix returns every key.
func (s *FakeStore) List(_ context.Context, prefix string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []string
	for k := range s.data {
		if prefix == "" || strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// snapshot returns a deep copy of the store's contents for inspection by
// [FakeHost.StoredValues].
func (s *FakeStore) snapshot() map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]byte, len(s.data))
	for k, v := range s.data {
		cp := make([]byte, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}
