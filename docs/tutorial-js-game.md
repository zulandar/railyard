# Tutorial: Build a JavaScript Browser Game with Playwright PR Demos

Build a browser **Snake** game (vanilla JavaScript + Canvas) with Railyard orchestrating multiple AI engines in parallel, and the **Playwright PR Demo** feature turned on so every pull request carries a runnable spec demonstrating the UI change.

**What you'll learn:**
- Installing `ry` from a prebuilt binary (no Go toolchain) and running `ry init` on a plain-JS project
- How `ry init` detects a JavaScript project and offers the Playwright PR-demo on-ramp
- Decomposing a feature into dependency-ordered cars and running parallel engines
- The Playwright PR-demo loop: scaffolded template → per-car spec in the PR diff → CI recording

**Time:** ~10 minutes of setup, then the engines do the building.

> **What's transcribed from a real run vs. described:** every command and output through **Step 4 (create the work)** below was captured from a real run on a scratch project. **Step 5 onward spawns live AI engine sessions** (real `claude`/`codex` usage); those commands are exact, but their terminal output is described from Railyard's documented behavior rather than pasted, so this tutorial never shows fabricated agent output.

---

## Prerequisites

- **`ry`** — installed below (no Go toolchain needed)
- **Docker** running — Railyard starts MySQL in a container for you
- **An AI coding CLI** — e.g. Claude Code: `npm install -g @anthropic-ai/claude-code`
- **tmux** — hosts the engine panes
- **Node.js** — to run/serve the game (`node`/`npx`)

---

## Step 1: Install `ry` and create the project

Install the prebuilt binary:

```bash
curl -fsSL https://raw.githubusercontent.com/zulandar/railyard/main/install.sh | sh
```

Scaffold a minimal vanilla-JS project (the engines will build the game itself):

```bash
mkdir -p ~/projects/snake-game && cd ~/projects/snake-game
git init -b main
git remote add origin https://github.com/yourname/snake-game.git
```

```jsonc
// package.json
{
  "name": "snake-game",
  "version": "0.1.0",
  "scripts": { "test": "node --test", "start": "npx serve ." }
}
```

Add a stub `index.html` and `src/game.js`, then commit:

```bash
git add -A && git commit -m "Initial snake-game scaffold"
```

---

## Step 2: `ry init` — detection + Playwright opt-in

Run `ry init`. Because this is the flagship Playwright tutorial, we opt in non-interactively with `--with-playwright` (interactively, `ry init` prompts `Enable Playwright PR demos for track frontend? [y/N]` instead):

```console
$ ry init --yes --with-playwright
Detected git repository: /home/you/projects/snake-game
Detected remote: https://github.com/yourname/snake-game.git
Detected owner: yourname
Detected languages: javascript

Wrote /home/you/projects/snake-game/railyard.yaml
Scaffolded Playwright starter spec: .../tests/pr-demos/_template.spec.ts
Scaffolded reference CI workflow: .../.github/workflows/pr-demo.yml.example

Playwright PR demos enabled. Railyard does NOT run Playwright —
your CI does. To activate the reference workflow, rename it:
  mv .github/workflows/pr-demo.yml.example .github/workflows/pr-demo.yml

Setting up database at /home/you/.railyard/mysql-data...
Starting MySQL container on 127.0.0.1:3306...
Database is ready (took 2500ms)

Database railyard_yourname ready
Migrated 15 tables
Seeded 1 track(s) and config for owner "yourname"

Railyard initialized successfully!
```

Note **`Detected languages: javascript`** — a plain-JS repo (package.json, no `tsconfig.json`) correctly gets a JavaScript track whose patterns match `.js` files, not an empty TypeScript track.

The generated `railyard.yaml`:

```yaml
owner: yourname
repo: https://github.com/yourname/snake-game.git

database:
  host: 127.0.0.1
  port: 3306
  username: root

tracks:
  - name: frontend
    language: javascript
    file_patterns: ["**/*.js", "**/*.jsx", "**/*.mjs", "**/*.cjs"]
    engine_slots: 2
    test_command: "npm test"
    playwright:
      enabled: true
      spec_path: "tests/pr-demos"
      filename: "{car_id}.spec.ts"
      template: "tests/pr-demos/_template.spec.ts"
```

`ry init` commits `railyard.yaml` for you. The scaffolded `tests/pr-demos/_template.spec.ts` and reference CI workflow at `.github/workflows/pr-demo.yml.example` are left for you to commit so engines (and CI) see them:

```bash
git add tests/pr-demos/_template.spec.ts .github/workflows/pr-demo.yml.example
git commit -m "Add Playwright PR-demo scaffolding"
```

To activate CI, rename the workflow (it ships as `.example` so it never auto-runs until you do):

```bash
mv .github/workflows/pr-demo.yml.example .github/workflows/pr-demo.yml
```

The reference workflow runs Playwright **scoped to the spec files changed in each PR** and uploads the video recordings as an artifact. Playwright has no `--video` CLI flag, so recording comes from your config — see Step 3. See [Playwright PR Demo Setup Guide](playwright-pr-demo.md) for the full schema and CI responsibilities.

---

## Step 3: Set up the test + Playwright tooling

```bash
npm install --save-dev @playwright/test
npx playwright install --with-deps
```

Add a minimal `playwright.config.ts` pointing `baseURL` at your dev server (e.g. `http://localhost:3000`) and enabling video recording — Playwright has no `--video` CLI flag, so the CI workflow relies on this config:

```ts
import { defineConfig } from '@playwright/test';

export default defineConfig({
  use: {
    baseURL: 'http://localhost:3000',
    video: process.env.CI ? 'on' : 'off',
  },
});
```

The scaffolded `_template.spec.ts` is a valid starter — `npx playwright test --list` will list its example test.

---

## Step 4: Dispatch the work into dependency-ordered cars

The fastest way is conversational — `ry dispatch` decomposes a plain-English request into cars (this spawns a live planner session; see the note below). To build the same structure explicitly, create the cars yourself:

```console
$ ry car create --type epic --track frontend --title "Build the Snake game"
Created car car-612a8

$ ry car create --parent car-612a8 --type task --title "Game loop and canvas rendering"
Created car car-951ee
$ ry car create --parent car-612a8 --type task --title "Score tracking and HUD"
Created car car-5fb6a
$ ry car create --parent car-612a8 --type task --title "Game-over screen and restart"
Created car car-71b20

# The game-over screen needs the loop and score in place first:
$ ry car dep add car-71b20 --blocked-by car-951ee
Added dependency: car-71b20 blocked by car-951ee
$ ry car dep add car-71b20 --blocked-by car-5fb6a
Added dependency: car-71b20 blocked by car-5fb6a

$ ry car publish car-612a8 --recursive
Published 4 car(s) starting from car-612a8
```

`ry car ready` shows only the cars with no unresolved blockers — the dependency graph is real, so the game-over car is held back:

```console
$ ry car ready
ID         TITLE                           TRACK     PRI
car-951ee  Game loop and canvas rendering  frontend  2
car-5fb6a  Score tracking and HUD          frontend  2
```

---

## Step 5: Start engines and watch them work

> ⚠️ **Live engines below.** `ry start` spawns AI coding sessions that consume real API usage. The commands are exact; the descriptions of what you'll see are from Railyard's documented behavior (not captured here).

```bash
ry start --engines 2     # launches the Yardmaster + 2 engines in a tmux session
tmux attach -t railyard  # watch them work
```

What happens:
- Each engine polls for a **ready** car (no unresolved blockers), claims one atomically, and works on an isolated branch `ry/yourname/frontend/<car-id>`.
- With 2 engines, the **game loop** and **score** cars run in parallel; the **game-over** car stays blocked until both merge.
- Because Playwright is enabled on the track, each engine's prompt instructs it to add a demo spec at `tests/pr-demos/<car-id>.spec.ts` (copying the template) exercising its change through the UI.
- **Yardmaster** runs `npm test` on each completed branch and merges it (or opens a draft PR when `require_pr: true`), then the next blocked car becomes ready.

Check status any time:

```bash
ry status            # engines, per-track car counts, message queue
ry status --watch    # auto-refresh
```

---

## Step 6: The Playwright PR-demo loop

When `require_pr: true`, Yardmaster opens a **draft PR** per car. With Playwright enabled, the PR body gains a **Playwright Demo** section pointing reviewers at the new spec, and the spec file appears in the PR diff. Your renamed `pr-demo.yml` CI workflow then runs that spec diff-scoped with video on and uploads the recording — so a reviewer can watch the agent's UI change without checking out the branch.

This is the end-to-end "watch the agents prove their UI work" loop: prompt opt-in → per-car spec in the diff → PR-body pointer → CI recording. Railyard never runs Playwright itself; your CI does.

---

## Troubleshooting

- **`ry init` hangs at "Starting MySQL container"** — make sure Docker is running (`docker info`). First-time MySQL init can take 15–30s.
- **`claude: command not found` when engines start** — install an AI CLI (`npm install -g @anthropic-ai/claude-code`) and ensure it's authenticated.
- **Port 3306 already in use** — another MySQL is bound to it; stop it or set a different `database.port` in `railyard.yaml`.
- **No cars get claimed** — confirm they're published (`ry car list --status open`) and that engines are on the matching track (`ry status`).

---

See also: [README Quickstart](../README.md#quickstart) · [Playwright PR Demo Setup](playwright-pr-demo.md) · [Go service tutorial](tutorial-go.md) · [Laravel tutorial](tutorial-laravel.md) · [Mobile (React Native) tutorial](tutorial-mobile.md)
