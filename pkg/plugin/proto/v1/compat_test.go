// Package protov1 holds the generated gRPC stubs and a compat test that
// guards against accidental wire-breaks on the v1 plugin contract.
package protov1

import (
	"errors"
	"os"
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
		t.Skip("no main ref distinct from HEAD (origin/main or main); skipping breaking check. " +
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
// local main — or "" when neither resolves (a shallow checkout, or a clone
// that has never seen main) or the only candidate points at HEAD. A ref
// equal to HEAD (sitting on main, or a just-merged commit) would make `buf
// breaking` compare the contract against itself and validate nothing, so it
// is treated as no baseline rather than a silent pass; the CI Proto job
// fetches the pre-merge main and is the authoritative gate.
func resolveMainRef(repoRoot string) string {
	head := gitRevParse(repoRoot, "HEAD")
	for _, ref := range []string{"refs/remotes/origin/main", "refs/heads/main"} {
		sha := gitRevParse(repoRoot, ref)
		if sha == "" {
			continue // ref does not resolve
		}
		if sha == head {
			continue // points at HEAD: comparing against it is a no-op
		}
		return ref
	}
	return ""
}

// gitRevParse returns the commit SHA that rev names, or "" if rev does not
// resolve.
func gitRevParse(repoRoot, rev string) string {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", rev)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// TestResolveMainRef exercises the baseline-ref selection, including the
// guard that refuses a ref pointing at HEAD: comparing the contract
// against itself validates nothing, so it must be treated as "no baseline"
// (skip) rather than a silent pass.
func TestResolveMainRef(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	t.Run("sitting on main with no origin returns empty", func(t *testing.T) {
		repo := t.TempDir()
		gitInitMain(t, repo)
		// HEAD == refs/heads/main and there is no origin/main, so the only
		// candidate points at HEAD: no meaningful baseline.
		if got := resolveMainRef(repo); got != "" {
			t.Fatalf("expected empty (HEAD is main, no distinct baseline), got %q", got)
		}
	})

	t.Run("feature branch uses local main when it differs from HEAD", func(t *testing.T) {
		repo := t.TempDir()
		gitInitMain(t, repo)
		gitCmd(t, repo, "checkout", "-b", "feature")
		gitCommit(t, repo, "a.txt", "2")
		if got := resolveMainRef(repo); got != "refs/heads/main" {
			t.Fatalf("expected refs/heads/main, got %q", got)
		}
	})

	t.Run("prefers origin/main when it differs from HEAD", func(t *testing.T) {
		repo := t.TempDir()
		gitInitMain(t, repo)
		base := gitCmd(t, repo, "rev-parse", "HEAD")
		gitCmd(t, repo, "checkout", "-b", "feature")
		gitCommit(t, repo, "a.txt", "2")
		gitCmd(t, repo, "update-ref", "refs/remotes/origin/main", base)
		if got := resolveMainRef(repo); got != "refs/remotes/origin/main" {
			t.Fatalf("expected refs/remotes/origin/main, got %q", got)
		}
	})

	t.Run("on main still uses origin/main when origin is behind HEAD", func(t *testing.T) {
		repo := t.TempDir()
		gitInitMain(t, repo)
		base := gitCmd(t, repo, "rev-parse", "HEAD")
		gitCommit(t, repo, "a.txt", "2") // main now ahead of origin
		gitCmd(t, repo, "update-ref", "refs/remotes/origin/main", base)
		// HEAD == refs/heads/main (skipped, == HEAD) but origin/main is a
		// distinct older commit, so it is the baseline.
		if got := resolveMainRef(repo); got != "refs/remotes/origin/main" {
			t.Fatalf("expected refs/remotes/origin/main, got %q", got)
		}
	})
}

// gitInitMain initialises a repo with a single commit on a `main` branch.
func gitInitMain(t *testing.T, dir string) {
	t.Helper()
	gitCmd(t, dir, "init")
	gitCommit(t, dir, "a.txt", "1")
	gitCmd(t, dir, "branch", "-M", "main")
}

// gitCommit writes file and commits it.
func gitCommit(t *testing.T, dir, file, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", file)
	gitCmd(t, dir, "commit", "-m", "c")
}

// gitCmd runs git in dir with a deterministic identity and fails the test
// on error, returning trimmed combined output.
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	base := []string{
		"-c", "user.name=test",
		"-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false",
	}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
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
