package pluginhost

import (
	"fmt"
	"os"
	"path/filepath"
)

// socketDirPerm is the permission applied to the per-plugin parent socket
// directory.
const socketDirPerm os.FileMode = 0o700

// resolveSocketPath returns the absolute Unix socket path the host should
// create for the named plugin.
//
// Priority order:
//
//  1. $XDG_RUNTIME_DIR/railyard/plugins/<name>.sock   (preferred)
//  2. /run/railyard/plugins/<name>.sock              (systemd installs, if writable)
//  3. /tmp/railyard-<uid>/plugins/<name>.sock        (fallback)
//
// In every case the parent directory is created with mode 0700. The
// returned path is the socket itself; the caller is responsible for
// removing any stale file before binding and for honoring the mode policy
// when the listener is created.
func resolveSocketPath(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("pluginhost: socket name must not be empty")
	}

	uid := os.Getuid()

	candidates := []string{}
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		candidates = append(candidates, filepath.Join(xdg, "railyard", "plugins"))
	}
	if isWritableDir("/run/railyard/plugins") {
		candidates = append(candidates, "/run/railyard/plugins")
	}
	candidates = append(candidates, filepath.Join(os.TempDir(), fmt.Sprintf("railyard-%d", uid), "plugins"))

	for _, dir := range candidates {
		if err := os.MkdirAll(dir, socketDirPerm); err != nil {
			continue
		}
		// Best-effort tighten if the directory already existed with looser
		// perms.
		_ = os.Chmod(dir, socketDirPerm)
		return filepath.Join(dir, name+".sock"), nil
	}

	return "", fmt.Errorf("pluginhost: no writable socket directory candidate worked for plugin %q", name)
}

// predictSocketPath returns the would-be UDS path for the named plugin
// WITHOUT creating any directories. Used by the read-only discovery path
// ([DiscoverPlugins]) so `ry plugins list` does not mutate the filesystem.
//
// The selection mirrors [resolveSocketPath] in priority order, but each
// candidate is accepted as soon as its parent chain looks plausibly
// usable — XDG and /run only when they exist as directories today. The
// /tmp fallback is always available conceptually, so it serves as the
// last resort. Returns an empty string for an empty name (matches the
// guardrail in resolveSocketPath; discovery callers tolerate an empty
// string as "unknown").
func predictSocketPath(name string) string {
	if name == "" {
		return ""
	}
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return filepath.Join(xdg, "railyard", "plugins", name+".sock")
	}
	if info, err := os.Stat("/run/railyard/plugins"); err == nil && info.IsDir() {
		return filepath.Join("/run/railyard/plugins", name+".sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("railyard-%d", os.Getuid()), "plugins", name+".sock")
}

// isWritableDir reports whether path is a directory writable by the
// current process. It is best-effort — race conditions between the check
// and a subsequent bind are tolerated because the caller falls back to a
// later candidate on failure anyway.
func isWritableDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	// Attempt to create + remove a sentinel file to confirm writability.
	probe, err := os.CreateTemp(path, ".railyard-probe-")
	if err != nil {
		return false
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return true
}

// removeSocket deletes the socket file at path. Missing files are not an
// error. Best-effort; logs are the caller's responsibility.
func removeSocket(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}
