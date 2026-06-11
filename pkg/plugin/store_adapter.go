package plugin

import (
	"context"
	"fmt"

	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// hostStore is the in-plugin adapter that satisfies the [Store] interface
// by translating each call into the matching KV RPC on the host's
// HostService (railyard-77h.11). The host scopes every operation to this
// plugin's connection-bound identity, so the adapter never sends a
// namespace itself.
type hostStore struct {
	hsc protov1.HostServiceClient
}

// Store implements [Host.Store]. The returned [Store] is cheap to create
// and safe for concurrent use (the underlying gRPC stub is).
func (h *hostClient) Store() Store {
	return &hostStore{hsc: h.hsc}
}

// Get implements [Store.Get].
func (s *hostStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	resp, err := s.hsc.KVGet(ctx, &protov1.KVGetRequest{Key: key})
	if err != nil {
		return nil, false, fmt.Errorf("plugin: Store.Get %q: %w", key, err)
	}
	if resp == nil || !resp.Found {
		return nil, false, nil
	}
	return resp.Value, true, nil
}

// Put implements [Store.Put]. A host-side limit rejection surfaces as the
// returned error.
func (s *hostStore) Put(ctx context.Context, key string, value []byte) error {
	if _, err := s.hsc.KVPut(ctx, &protov1.KVPutRequest{Key: key, Value: value}); err != nil {
		return fmt.Errorf("plugin: Store.Put %q: %w", key, err)
	}
	return nil
}

// Delete implements [Store.Delete]. Deleting an absent key is a no-op.
func (s *hostStore) Delete(ctx context.Context, key string) error {
	if _, err := s.hsc.KVDelete(ctx, &protov1.KVDeleteRequest{Key: key}); err != nil {
		return fmt.Errorf("plugin: Store.Delete %q: %w", key, err)
	}
	return nil
}

// List implements [Store.List].
func (s *hostStore) List(ctx context.Context, prefix string) ([]string, error) {
	resp, err := s.hsc.KVList(ctx, &protov1.KVListRequest{Prefix: prefix})
	if err != nil {
		return nil, fmt.Errorf("plugin: Store.List %q: %w", prefix, err)
	}
	if resp == nil {
		return nil, nil
	}
	return resp.Keys, nil
}
