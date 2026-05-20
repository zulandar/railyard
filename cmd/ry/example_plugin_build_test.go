// Package main — verification that the documented hello-world plugin
// at examples/plugins/hello/ still compiles against the current SDK.
//
// This test is the regression contract for docs/plugins/authoring.md §2:
// if the pkg/plugin signatures ever drift in a way that breaks the
// documented quickstart, this test catches it before the guide goes
// stale. The example directory is a sibling Go module with its own
// go.mod + replace directive, so we shell out to `go build` exactly the
// way a plugin author would.
package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestOSSExamplePluginBuilds runs `go build ./...` inside
// examples/plugins/hello/ and asserts a clean exit. The example is its
// own module with `replace github.com/zulandar/railyard => ../../..`,
// so this builds the in-tree SDK against the in-tree plugin and proves
// the documented authoring contract is still satisfied.
//
// Skip conditions match TestOSSSmokeBuild in oss_smoke_test.go:
//   - `go` toolchain not on PATH
//   - `-short` is set (the build takes a few seconds: it has to resolve
//     gopkg.in/yaml.v3 and compile the SDK).
func TestOSSExamplePluginBuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping example plugin build under -short; runs in full CI")
	}

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("finding repo root: %v", err)
	}

	exampleDir := filepath.Join(repoRoot, "examples", "plugins", "hello")
	if fi, err := os.Stat(exampleDir); err != nil || !fi.IsDir() {
		t.Fatalf("example dir missing at %s: %v", exampleDir, err)
	}

	// 60s mirrors the budget in TestOSSSmokeBuild — generous enough that
	// a cold build with module-cache miss still passes; tight enough that
	// a wedged toolchain does not hang CI.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, goBin, "build", "./...")
	cmd.Dir = exampleDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("`go build ./...` in %s failed: %v\nbuild output:\n%s", exampleDir, err, out.String())
	}
}
