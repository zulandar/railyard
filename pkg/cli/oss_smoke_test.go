// Package cli — OSS non-regression smoke test.
//
// This file is the load-bearing contract that the OSS `ry` binary
// behaves identically to a pre-plugin-system railyard build. Every
// future change must keep this test passing; a failure here is a
// Phase 1 design violation per the plugin system spec (see
// docs/superpowers/specs/2026-05-20-railyard-plugin-system-design.md,
// §14 Risks and the OSS-non-regression contract referenced throughout).
//
// What the tests guarantee:
//
//  1. The OSS binary compiles cleanly via `go build ./cmd/ry`. The
//     surrounding `go test ./...` only proves test packages compile —
//     a separate build step is what an OSS user actually does.
//  2. The built binary runs, exits 0 for `--help`, and reports
//     "No plugins registered in this binary." for `plugins list`.
//  3. plugin.Registered() is empty at OSS build time, with no hidden
//     init() side-effects from transitive imports adding entries.
//
// Tests that shell out to the go toolchain follow the pattern in
// pkg/plugin/import_test.go: they Skip when `go` isn't on PATH and
// they bound the build with exec.CommandContext.
package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zulandar/railyard/pkg/plugin"
)

// TestOSSRegistryEmpty is the fast-path unit test that asserts the OSS
// binary's plugin registry is empty at process start. Any future PR
// that side-effect imports a package which calls plugin.Register at
// init time from inside the OSS module will break this test loudly.
//
// We also call Registered() twice to catch the (currently impossible
// but easy-to-regress) case where a lazy init populates the registry
// on first read.
func TestOSSRegistryEmpty(t *testing.T) {
	if got := plugin.Registered(); len(got) != 0 {
		t.Fatalf("OSS build registered %d plugins at init: %+v; the OSS binary must have zero", len(got), got)
	}

	// Second call: catches accidental lazy init. The registry is a
	// process-global with no public reset, so this is the only place
	// we can pin the property.
	if got := plugin.Registered(); len(got) != 0 {
		t.Fatalf("plugin.Registered() returned %d entries on second call: %+v; OSS registry must stay empty across calls", len(got), got)
	}
}

// TestOSSSmokeBuild builds the OSS `ry` binary into a temp dir and
// exercises two subcommands end-to-end. This is the only place that
// proves the binary itself (not just test packages) compiles and that
// the cobra command tree boots without side effects.
//
// Skip conditions:
//   - `go` toolchain not on PATH (matches pkg/plugin/import_test.go).
//   - `-short` is set: building the binary takes a few seconds, which
//     is longer than the rest of the cmd/ry test suite combined; we
//     keep `go test -short ./...` fast.
func TestOSSSmokeBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping OSS smoke build under -short; runs in full CI")
	}

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("finding repo root: %v", err)
	}

	outPath := filepath.Join(t.TempDir(), "ry-smoke")
	// On Windows the binary needs `.exe`; we don't run on Windows in CI
	// today but the cost of being correct here is one line.
	if runtime.GOOS == "windows" {
		outPath += ".exe"
	}

	// Build. 60s is generous — a cold build of cmd/ry on a CI runner
	// is well under that; the timeout's only job is to keep a wedged
	// toolchain from hanging the test suite.
	buildCtx, buildCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer buildCancel()

	buildCmd := exec.CommandContext(buildCtx, goBin, "build", "-o", outPath, "./cmd/ry")
	buildCmd.Dir = repoRoot
	var buildOut bytes.Buffer
	buildCmd.Stdout = &buildOut
	buildCmd.Stderr = &buildOut
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("`go build ./cmd/ry` failed: %v\nbuild output:\n%s", err, buildOut.String())
	}

	// Sanity-check the binary actually landed where we asked.
	if fi, err := os.Stat(outPath); err != nil || fi.Size() == 0 {
		t.Fatalf("built binary missing or empty at %s: %v", outPath, err)
	}

	t.Run("PluginsListReportsNone", func(t *testing.T) {
		stdout, stderr, err := runBinary(t, outPath, 15*time.Second, "plugins", "list")
		if err != nil {
			t.Fatalf("`ry plugins list` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		const want = "No plugins registered in this binary."
		if !strings.Contains(stdout, want) {
			t.Errorf("`ry plugins list` stdout did not contain %q\nstdout:\n%s\nstderr:\n%s", want, stdout, stderr)
		}
	})

	t.Run("HelpExitsZero", func(t *testing.T) {
		stdout, stderr, err := runBinary(t, outPath, 15*time.Second, "--help")
		if err != nil {
			t.Fatalf("`ry --help` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		// Cobra writes help to stdout by default. Just assert it's
		// not empty — the exact contents are covered by cmd_help_test.go.
		if strings.TrimSpace(stdout) == "" {
			t.Errorf("`ry --help` produced empty stdout; stderr:\n%s", stderr)
		}
	})
}

// runBinary executes the supplied binary with args, bounded by timeout,
// and returns captured stdout, stderr, and any exec error. It is a small
// convenience to keep the per-subcommand subtests focused on assertions.
func runBinary(t *testing.T, bin string, timeout time.Duration, args ...string) (string, string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// findRepoRoot walks up from this test file's on-disk location until it
// finds a directory containing go.mod. We use runtime.Caller rather than
// a hard-coded "..", ".." pair so the test stays correct if it is moved
// or invoked from a different working directory (e.g. `go test -C`).
//
// This mirrors the convention in pkg/plugin/import_test.go (which sets
// cmd.Dir = "." because go list operates on the package's own directory),
// but cmd/ry needs the module root since `go build ./cmd/ry` is a
// module-relative invocation.
func findRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", &smokeError{msg: "runtime.Caller failed"}
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", &smokeError{msg: "go.mod not found walking up from " + file}
		}
		dir = parent
	}
}

// smokeError keeps the import surface stdlib-only — we don't pull in
// fmt.Errorf just to wrap a sentinel.
type smokeError struct{ msg string }

func (e *smokeError) Error() string { return e.msg }
