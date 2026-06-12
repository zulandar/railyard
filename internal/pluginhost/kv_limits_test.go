package pluginhost

import (
	"testing"

	"github.com/zulandar/railyard/pkg/plugin/plugintest"
)

// TestKVLimitsMatchFakeStore pins the host KV limits to the plugintest
// FakeStore limits (railyard-uv8.11). plugintest lives in pkg/ and cannot
// import internal/pluginhost, so the two constant sets are necessarily
// duplicated; this test — in a package that can import both — fails if they
// ever drift, so a FakeStore-based plugin unit test keeps matching what the
// real host enforces.
func TestKVLimitsMatchFakeStore(t *testing.T) {
	if KVMaxValueBytes != plugintest.FakeStoreMaxValueBytes {
		t.Errorf("KVMaxValueBytes=%d != plugintest.FakeStoreMaxValueBytes=%d", KVMaxValueBytes, plugintest.FakeStoreMaxValueBytes)
	}
	if KVMaxKeyBytes != plugintest.FakeStoreMaxKeyBytes {
		t.Errorf("KVMaxKeyBytes=%d != plugintest.FakeStoreMaxKeyBytes=%d", KVMaxKeyBytes, plugintest.FakeStoreMaxKeyBytes)
	}
	if KVMaxKeys != plugintest.FakeStoreMaxKeys {
		t.Errorf("KVMaxKeys=%d != plugintest.FakeStoreMaxKeys=%d", KVMaxKeys, plugintest.FakeStoreMaxKeys)
	}
}
