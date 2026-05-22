// Package protov1 holds the generated gRPC stubs and a compat test that
// guards against accidental wire-breaks on the v1 plugin contract.
package protov1

import (
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestProtoBreakingCompat is the golden compat check that catches
// accidental wire-breaks. It runs `buf breaking` against the snapshot
// committed at pkg/plugin/proto/snapshots/v1/. Wire-incompatible
// changes (renames, renumbers, removals) fail this test; additive
// changes (new fields, enum values, oneof arms, messages) pass.
//
// The test skips (rather than fails) when buf is not installed, so a
// fresh checkout without dev tooling still passes `go test ./...`. CI
// runs the same check via scripts/proto.sh, which requires buf.
//
// When a deliberate wire change lands, refresh the snapshot:
//
//	cp pkg/plugin/proto/v1/plugin.proto pkg/plugin/proto/snapshots/v1/plugin.proto
//
// and commit both files in the same change. See docs/plugins/proto.md.
func TestProtoBreakingCompat(t *testing.T) {
	t.Parallel()

	bufBin := findBuf()
	if bufBin == "" {
		t.Skip("buf binary not found in PATH or $GOBIN; install with `go install github.com/bufbuild/buf/cmd/buf@latest`")
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Skipf("could not locate repo root: %v", err)
	}

	cmd := exec.Command(bufBin, "breaking", "--against", "pkg/plugin/proto/snapshots/v1")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("buf breaking detected wire-incompatible changes against pkg/plugin/proto/snapshots/v1:\n%s\n\n"+
				"If this change is intentional and additive, the snapshot may be stale — refresh it with:\n"+
				"  cp pkg/plugin/proto/v1/plugin.proto pkg/plugin/proto/snapshots/v1/plugin.proto\n"+
				"If this change is breaking, bump to v2 instead. See docs/plugins/proto.md.",
				string(out))
		}
		t.Fatalf("buf breaking failed to run: %v\n%s", err, out)
	}
}

// findBuf returns the path to a usable buf binary, checking PATH first
// and then the conventional $GOBIN / $GOPATH/bin locations.
func findBuf() string {
	if p, err := exec.LookPath("buf"); err == nil {
		return p
	}
	// $GOBIN, then $GOPATH/bin.
	for _, env := range []string{"GOBIN", "GOPATH"} {
		out, err := exec.Command("go", "env", env).Output()
		if err != nil {
			continue
		}
		path := strings.TrimSpace(string(out))
		if path == "" {
			continue
		}
		candidate := filepath.Join(path, "buf")
		if env == "GOPATH" {
			candidate = filepath.Join(path, "bin", "buf")
		}
		if runtime.GOOS == "windows" {
			candidate += ".exe"
		}
		if _, err := exec.Command(candidate, "--version").Output(); err == nil {
			return candidate
		}
	}
	return ""
}

// findRepoRoot returns the absolute path to the repo root, located via
// `git rev-parse --show-toplevel`.
func findRepoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
