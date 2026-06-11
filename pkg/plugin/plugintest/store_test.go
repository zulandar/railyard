package plugintest_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/zulandar/railyard/pkg/plugin"
	"github.com/zulandar/railyard/pkg/plugin/plugintest"
)

// TestFakeHostStoreRoundTrip exercises the in-memory Store fake through
// the plugin.Store interface and the StoredValues inspector.
func TestFakeHostStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	fh := &plugintest.FakeHost{}
	var _ plugin.Host = fh // compile-time: FakeHost satisfies plugin.Host

	st := fh.Store()
	if _, found, err := st.Get(ctx, "k"); err != nil || found {
		t.Fatalf("Get(missing) = (found=%v, err=%v), want (false, nil)", found, err)
	}
	if err := st.Put(ctx, "cursor", []byte("7")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	v, found, err := st.Get(ctx, "cursor")
	if err != nil || !found || !bytes.Equal(v, []byte("7")) {
		t.Fatalf("Get = (%q, %v, %v), want (\"7\", true, nil)", v, found, err)
	}

	_ = st.Put(ctx, "seen:a", []byte("1"))
	keys, _ := st.List(ctx, "seen:")
	if len(keys) != 1 || keys[0] != "seen:a" {
		t.Fatalf("List = %v, want [seen:a]", keys)
	}

	// StoredValues exposes what the plugin persisted.
	got := fh.StoredValues()
	if !bytes.Equal(got["cursor"], []byte("7")) {
		t.Fatalf("StoredValues[cursor] = %q, want \"7\"", got["cursor"])
	}

	if err := st.Delete(ctx, "cursor"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, _ := st.Get(ctx, "cursor"); found {
		t.Fatal("Get after Delete: found=true")
	}
}

// TestFakeHostStoreNamespacedPerHost proves two FakeHosts have isolated
// stores — mirroring the real host's per-plugin namespacing.
func TestFakeHostStoreNamespacedPerHost(t *testing.T) {
	ctx := context.Background()
	a := &plugintest.FakeHost{}
	b := &plugintest.FakeHost{}

	if err := a.Store().Put(ctx, "shared", []byte("a")); err != nil {
		t.Fatalf("a.Put: %v", err)
	}
	if _, found, _ := b.Store().Get(ctx, "shared"); found {
		t.Fatal("FakeHost b saw FakeHost a's key: stores must be per-host")
	}
}

// TestFakeHostStoreLimits proves the fake enforces the same caps as the
// real host so over-limit writes fail locally in unit tests.
func TestFakeHostStoreLimits(t *testing.T) {
	ctx := context.Background()
	fh := &plugintest.FakeHost{}
	st := fh.Store()

	if err := st.Put(ctx, "k", make([]byte, plugintest.FakeStoreMaxValueBytes+1)); err == nil {
		t.Error("oversized value: want error, got nil")
	}
	if err := st.Put(ctx, strings.Repeat("x", plugintest.FakeStoreMaxKeyBytes+1), []byte("v")); err == nil {
		t.Error("oversized key: want error, got nil")
	}
}
