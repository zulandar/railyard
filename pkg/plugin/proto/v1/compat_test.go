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
// accidental wire-breaks. It runs `buf breaking` against the last-merged
// contract on the main branch (a git ref, not a committed snapshot), so a
// wire-incompatible edit is caught even when the proto and any baseline
// are touched in the same commit. Wire-incompatible changes (renames,
// renumbers, removals) fail this test; additive changes (new fields, enum
// values, oneof arms, messages) pass.
//
// The test skips (rather than fails) when it cannot run a meaningful
// comparison — buf not installed, or no resolvable main ref (a shallow
// checkout or a clone without origin/main). A fresh `go test ./...` on a
// machine without dev tooling or full history therefore still passes. The
// authoritative, non-skippable gate is the CI "Proto" job, which fetches
// main explicitly before running the same check; see
// .github/workflows/ci.yml and docs/plugins/proto.md.
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

	mainRef := resolveMainRef(repoRoot)
	if mainRef == "" {
		t.Skip("no resolvable main ref (origin/main or main); skipping breaking check. " +
			"The CI Proto job fetches main and runs the authoritative gate.")
	}

	against := ".git#ref=" + mainRef
	cmd := exec.Command(bufBin, "breaking", "--against", against)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("buf breaking detected wire-incompatible changes against %s:\n%s\n\n"+
				"Additive changes (new fields, enum values, oneof arms, messages) are allowed; "+
				"renames, renumbers, and removals are breaking and require a v2 package. "+
				"See docs/plugins/proto.md.",
				mainRef, string(out))
		}
		t.Fatalf("buf breaking failed to run: %v\n%s", err, out)
	}
}

// resolveMainRef returns the first git ref that names the last-merged
// contract baseline — preferring the remote-tracking origin/main, then a
// local main — or "" when neither resolves (a shallow checkout, or a
// clone that has never seen main). It never returns the currently checked
// out HEAD: comparing against the local main while sitting on it is the
// intended no-op when main is the working branch.
func resolveMainRef(repoRoot string) string {
	for _, ref := range []string{"refs/remotes/origin/main", "refs/heads/main"} {
		cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", ref)
		cmd.Dir = repoRoot
		if err := cmd.Run(); err == nil {
			return ref
		}
	}
	return ""
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
