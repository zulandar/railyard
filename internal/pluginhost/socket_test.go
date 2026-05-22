package pluginhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveSocketPathXDG covers the preferred branch: when
// XDG_RUNTIME_DIR is set, the socket path lives under
// $XDG_RUNTIME_DIR/railyard/plugins/<name>.sock.
func TestResolveSocketPathXDG(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", tmp)

	path, err := resolveSocketPath("hello")
	if err != nil {
		t.Fatalf("resolveSocketPath: %v", err)
	}
	want := filepath.Join(tmp, "railyard", "plugins", "hello.sock")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat parent: %v", err)
	}
	if perm := info.Mode().Perm(); perm != socketDirPerm {
		t.Errorf("parent dir perm = %o, want %o", perm, socketDirPerm)
	}
}

// TestResolveSocketPathTmpFallback exercises the fallback branch. We
// clear XDG_RUNTIME_DIR and trust that /run/railyard/plugins is not
// writable under the test runtime — both candidates miss and the helper
// drops into /tmp/railyard-<uid>/plugins.
func TestResolveSocketPathTmpFallback(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")

	path, err := resolveSocketPath("hello")
	if err != nil {
		t.Fatalf("resolveSocketPath: %v", err)
	}
	// We accept either the /run branch (if the test happens to run as
	// root with /run/railyard writable, e.g. in some CI containers) or
	// the /tmp fallback. The contract is "it landed somewhere
	// reasonable".
	if !strings.Contains(path, "railyard") || !strings.HasSuffix(path, "hello.sock") {
		t.Errorf("unexpected path = %q", path)
	}
}

// TestResolveSocketPathEmptyName is a guardrail: an empty name is a
// programmer error.
func TestResolveSocketPathEmptyName(t *testing.T) {
	if _, err := resolveSocketPath(""); err == nil {
		t.Error("expected error for empty name")
	}
}
