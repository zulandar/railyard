package engine

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
)

var updateGoldens = flag.Bool("update", false, "update golden test files")

func compareGolden(t *testing.T, goldenName, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", goldenName)
	if *updateGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %q: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %q: %v\n(run `go test -update ./internal/engine/...` to create)", path, err)
	}
	if got != string(want) {
		t.Errorf("output differs from golden %q\n--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s",
			path, len(want), string(want), len(got), got)
	}
}

func goldenInput() ContextInput {
	in := makeInput()
	in.EngineID = "eng-golden01"
	return in
}

func TestRenderContext_Golden_NoPlaywright(t *testing.T) {
	out, err := RenderContext(goldenInput())
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "engine_no_playwright.golden", out)
}

func TestRenderContext_Golden_PlaywrightEnabled(t *testing.T) {
	in := goldenInput()
	in.Config.Tracks = []config.TrackConfig{
		{
			Name: "backend",
			Playwright: &models.PlaywrightConfig{
				Enabled:  true,
				SpecPath: "tests/pr-demos",
				Filename: "{car_id}.spec.ts",
			},
		},
	}
	out, err := RenderContext(in)
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "engine_playwright_enabled.golden", out)
}

func TestRenderContext_Golden_PlaywrightDisabledMatchesNoBlock(t *testing.T) {
	// Regression check: disabled playwright config must produce byte-identical
	// output to no playwright block at all. This is the same invariant tested
	// in TestRenderContext_PlaywrightDisabledByteIdentical, framed as a golden
	// equivalence — disabled output must match the no-playwright golden.
	in := goldenInput()
	in.Config.Tracks = []config.TrackConfig{
		{
			Name: "backend",
			Playwright: &models.PlaywrightConfig{
				Enabled:  false,
				SpecPath: "tests/pr-demos",
				Filename: "{car_id}.spec.ts",
			},
		},
	}
	out, err := RenderContext(in)
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "engine_no_playwright.golden", out)
}
