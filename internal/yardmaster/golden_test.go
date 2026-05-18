package yardmaster

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

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
		t.Fatalf("read golden %q: %v\n(run `go test -update ./internal/yardmaster/...` to create)", path, err)
	}
	if got != string(want) {
		t.Errorf("output differs from golden %q\n--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s",
			path, len(want), string(want), len(got), got)
	}
}

func goldenCar() models.Car {
	return models.Car{
		ID:          "car-golden01",
		Title:       "Add login flow",
		Track:       "frontend",
		Branch:      "ry/alice/frontend/car-golden01",
		Priority:    1,
		Assignee:    "eng-golden01",
		Description: "Implement the login flow described in the design doc.",
		Acceptance:  "User can log in with valid credentials and is redirected to the dashboard.",
		DesignNotes: "Reuse the existing auth client; no new dependencies.",
	}
}

func TestBuildPRBody_Golden_NoPlaywright(t *testing.T) {
	db := testDB(t)
	c := goldenCar()
	db.Create(&c)

	// Empty configPath => playwright section is silently omitted.
	body := buildPRBody(db, &c, "/nonexistent", "main", "")
	compareGolden(t, "pr_body_no_playwright.golden", body)
}

func TestBuildPRBody_Golden_PlaywrightEnabled(t *testing.T) {
	db := testDB(t)
	c := goldenCar()
	db.Create(&c)

	yaml := `owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: frontend
    language: typescript
    playwright:
      enabled: true
      spec_path: tests/pr-demos
      filename: "{car_id}.spec.ts"
`
	configPath := writeYAMLConfig(t, yaml)
	body := buildPRBody(db, &c, "/nonexistent", "main", configPath)
	compareGolden(t, "pr_body_playwright_enabled.golden", body)
}

func TestBuildPRBody_Golden_PlaywrightDisabledMatchesNoBlock(t *testing.T) {
	// Regression: a track with playwright.enabled=false must produce
	// byte-identical output to a track with no playwright block at all.
	db := testDB(t)
	c := goldenCar()
	db.Create(&c)

	yaml := `owner: alice
repo: git@github.com:org/app.git
tracks:
  - name: frontend
    language: typescript
    playwright:
      enabled: false
      spec_path: tests/pr-demos
`
	configPath := writeYAMLConfig(t, yaml)
	body := buildPRBody(db, &c, "/nonexistent", "main", configPath)
	compareGolden(t, "pr_body_no_playwright.golden", body)
}
