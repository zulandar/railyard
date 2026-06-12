package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/models"
)

// Playwright scaffolding defaults. These match docs/playwright-pr-demo.md:
// the documented default filename is "{car_id}.spec.ts", specs live under
// "tests/pr-demos", and the optional starter template is "_template.spec.ts"
// inside that directory.
const (
	playwrightSpecPath     = "tests/pr-demos"
	playwrightFilename     = "{car_id}.spec.ts"
	playwrightTemplatePath = "tests/pr-demos/_template.spec.ts"

	// playwrightExampleWorkflowName is shipped with a ".example" suffix so
	// GitHub Actions never auto-runs it. Users rename it to activate CI.
	playwrightExampleWorkflowName = "pr-demo.yml.example"
)

// isFrontendLanguage reports whether a track language is a frontend (Node)
// flavor that should be offered the Playwright PR-demo on-ramp. Both
// "typescript" and "javascript" qualify (railyard-a37.3 makes plain-JS repos
// produce a "javascript" track).
func isFrontendLanguage(lang string) bool {
	return lang == "typescript" || lang == "javascript"
}

// offerPlaywright walks the generated tracks and, for each frontend (ts/js)
// track, decides whether to enable Playwright PR demos on it. The decision
// follows the contract:
//
//   - --yes: Playwright stays OFF unless withPlaywright was passed (then ON,
//     no prompt).
//   - interactive: if withPlaywright, enable without prompting; otherwise
//     prompt with a default of No.
//
// Enabled tracks get a *models.PlaywrightConfig wired with the documented
// defaults. It mutates tracks in place and returns whether any track was
// enabled (the caller uses this to decide whether to scaffold files).
func offerPlaywright(in io.Reader, out io.Writer, tracks []config.TrackConfig, yes, withPlaywright bool) bool {
	any := false
	for i := range tracks {
		if !isFrontendLanguage(tracks[i].Language) {
			continue
		}

		enable := false
		switch {
		case withPlaywright:
			// Explicit opt-in via flag — never prompt, even interactively.
			enable = true
		case yes:
			// Non-interactive default off.
			enable = false
		default:
			enable = promptYesNo(in, out,
				fmt.Sprintf("Enable Playwright PR demos for track %s?", tracks[i].Name), false)
		}

		if enable {
			tracks[i].Playwright = &models.PlaywrightConfig{
				Enabled:  true,
				SpecPath: playwrightSpecPath,
				Filename: playwrightFilename,
				Template: playwrightTemplatePath,
			}
			any = true
		}
	}
	return any
}

// scaffoldPlaywright writes the starter Playwright template spec and a
// reference GitHub Actions workflow (shipped as ".example" so it never
// auto-runs) into the project rooted at root. Existing files are never
// clobbered. Write failures warn but never fail init.
func scaffoldPlaywright(out io.Writer, root string) {
	wrote := false

	// 1. Starter template spec.
	specPath := filepath.Join(root, filepath.FromSlash(playwrightTemplatePath))
	if writeIfAbsent(out, specPath, playwrightTemplateSpec) {
		fmt.Fprintf(out, "Scaffolded Playwright starter spec: %s\n", specPath)
		wrote = true
	}

	// 2. Reference CI workflow (.example suffix — never auto-runs).
	wfPath := filepath.Join(root, ".github", "workflows", playwrightExampleWorkflowName)
	if writeIfAbsent(out, wfPath, playwrightExampleWorkflow) {
		fmt.Fprintf(out, "Scaffolded reference CI workflow: %s\n", wfPath)
		wrote = true
	}

	if wrote {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Playwright PR demos enabled. Railyard does NOT run Playwright —")
		fmt.Fprintf(out, "your CI does. To activate the reference workflow, rename it:\n")
		fmt.Fprintf(out, "  mv .github/workflows/%s .github/workflows/pr-demo.yml\n", playwrightExampleWorkflowName)
		fmt.Fprintln(out, "See docs/playwright-pr-demo.md for details.")
	}
}

// writeIfAbsent creates the parent directory and writes content to path only
// when path does not already exist. It returns true when it wrote the file.
// Any error is reported as a warning to out (never fatal) and returns false.
func writeIfAbsent(out io.Writer, path, content string) bool {
	if _, err := os.Stat(path); err == nil {
		// File exists — do not clobber.
		return false
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(out, "Warning: could not stat %s: %v\n", path, err)
		return false
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(out, "Warning: could not create directory for %s: %v\n", path, err)
		return false
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		fmt.Fprintf(out, "Warning: could not write %s: %v\n", path, err)
		return false
	}
	return true
}

// playwrightTemplateSpec is the starter Playwright spec written to
// tests/pr-demos/_template.spec.ts on opt-in. It is intentionally minimal:
// one example test plus a comment block explaining page objects + baseURL.
// Engines copy this when writing per-car demo specs.
const playwrightTemplateSpec = `import { test, expect } from '@playwright/test';

/**
 * Playwright PR Demo — starter template.
 *
 * Copy this file to tests/pr-demos/<car_id>.spec.ts and replace the example
 * below with a test that exercises YOUR change end-to-end through the UI as a
 * real user would.
 *
 * Conventions:
 *   - baseURL: set ` + "`use.baseURL`" + ` in playwright.config (or PLAYWRIGHT_BASE_URL)
 *     so page.goto('/') targets your app. Avoid hard-coding absolute URLs.
 *   - Page objects / fixtures: prefer existing page objects and fixtures in
 *     your repo over ad-hoc selectors so demos stay readable and stable.
 *   - One demo per PR: each car adds exactly one spec at the deterministic
 *     path, which keeps diff-scoped CI execution trivial.
 *
 * Railyard does NOT run this spec — your project's CI does (with video on).
 * See docs/playwright-pr-demo.md.
 */
test('example: home page loads', async ({ page }) => {
	await page.goto('/');
	await expect(page).toHaveTitle(/.*/);
});
`

// playwrightExampleWorkflow is the reference GitHub Actions workflow written
// to .github/workflows/pr-demo.yml.example on opt-in. The ".example" suffix
// keeps GitHub from auto-running it until the user renames it. It demonstrates
// diff-scoped execution (only the PR's changed spec files), video recording,
// and uploading the recording as an artifact.
const playwrightExampleWorkflow = `# Railyard Playwright PR Demo — REFERENCE workflow.
#
# This file ships with a ".example" suffix so GitHub Actions does NOT run it.
# To activate it, rename it to pr-demo.yml:
#
#     mv .github/workflows/pr-demo.yml.example .github/workflows/pr-demo.yml
#
# It runs Playwright scoped to the spec files changed in the PR diff, records
# video for every test, and uploads the recordings as a workflow artifact so
# reviewers can watch the demo without checking out the branch.
#
# Adjust the Node version, install command, and base URL to match your project.
name: PR Demo (Playwright)

on:
  pull_request:

jobs:
  pr-demo:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          # Need the PR base ref to diff against.
          fetch-depth: 0

      - uses: actions/setup-node@v4
        with:
          node-version: '20'

      - name: Install dependencies
        run: npm ci

      - name: Install Playwright browsers
        run: npx playwright install --with-deps

      - name: Determine changed demo specs
        id: changed
        run: |
          BASE="origin/${{ github.base_ref }}"
          git fetch origin "${{ github.base_ref }}" --depth=1
          # Only the demo spec files added/changed in this PR under tests/pr-demos/.
          SPECS="$(git diff --name-only --diff-filter=ACMR "$BASE"...HEAD -- 'tests/pr-demos/**/*.spec.ts' | tr '\n' ' ')"
          echo "specs=$SPECS" >> "$GITHUB_OUTPUT"
          if [ -z "$SPECS" ]; then
            echo "No PR demo specs changed — nothing to run."
          fi

      - name: Run changed demo specs (video on)
        if: steps.changed.outputs.specs != ''
        # --video on records every test; diff-scoped to just the PR's specs.
        run: npx playwright test --video on ${{ steps.changed.outputs.specs }}

      - name: Upload recordings
        if: always() && steps.changed.outputs.specs != ''
        uses: actions/upload-artifact@v4
        with:
          name: pr-demo-recordings
          path: |
            test-results/**/*.webm
            playwright-report/
          if-no-files-found: ignore
`
