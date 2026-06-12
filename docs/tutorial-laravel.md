# Tutorial: Build a Laravel/PHP Application with Railyard

Orchestrate AI engines on a **Laravel** app. Railyard detects PHP projects, and `ry init` now recognizes Laravel specifically — when an `artisan` console script is present, the generated track uses **`php artisan test`** (Laravel's convention) instead of raw PHPUnit.

**What you'll learn:**
- Running `ry init` on a Laravel app and the Laravel-aware test-command detection
- Adding a second track when the app has Blade/Vite frontend assets (multi-track on one repo)
- Decomposing an API feature into dependency-ordered cars with Yardmaster gating on the Laravel test suite

**Time:** ~10 minutes of setup, then the engines build.

> **Honesty note on this tutorial.** The Railyard-side flow — `ry init` detection (including the `php artisan test` recognition) and the car decomposition — was captured from a **real run**. However, the machine used to prepare this tutorial did **not** have PHP or Composer installed, so the `laravel new` scaffold and the actual `php artisan test` execution were **not** run here. Those commands are shown as you would run them; sections that depend on a live PHP toolchain or live AI engines are marked. Nothing below is fabricated terminal output.

---

## Prerequisites

- **`ry`** — `curl -fsSL https://raw.githubusercontent.com/zulandar/railyard/main/install.sh | sh`
- **Docker** running — Railyard starts MySQL in a container for you
- **An AI coding CLI** — e.g. Claude Code: `npm install -g @anthropic-ai/claude-code`
- **tmux**
- **PHP 8.2+ and Composer** — to scaffold and test the Laravel app (and so engines can run the suite)

---

## Step 1: Create the Laravel app

```bash
composer create-project laravel/laravel ~/projects/blog-api
cd ~/projects/blog-api
git init -b main
git remote add origin https://github.com/yourname/blog-api.git
git add -A && git commit -m "Initial Laravel app"
```

A fresh Laravel app ships a `composer.json` (which Railyard uses to detect PHP) and an `artisan` console script (which Railyard uses to detect the Laravel test convention).

---

## Step 2: `ry init` — PHP + Laravel detection

```console
$ ry init --yes
Detected git repository: /home/you/projects/blog-api
Detected remote: https://github.com/yourname/blog-api.git
Detected owner: yourname
Detected languages: php

Wrote /home/you/projects/blog-api/railyard.yaml

Database is already running on 127.0.0.1:3306
Database railyard_yourname ready
Migrated 15 tables
Seeded 1 track(s) and config for owner "yourname"

Railyard initialized successfully!
```

The generated track — note **`test_command: "php artisan test"`**, chosen because the `artisan` file is present (a plain PHP project without `artisan` gets `vendor/bin/phpunit` instead):

```yaml
owner: yourname
repo: https://github.com/yourname/blog-api.git

database:
  host: 127.0.0.1
  port: 3306
  username: root

tracks:
  - name: backend
    language: php
    file_patterns: ["**/*.php"]
    engine_slots: 2
    test_command: "php artisan test"
```

> This Laravel-aware detection was added in railyard-a37.7. If your project uses a different runner, just edit `test_command` in `railyard.yaml`.

### Optional: add a frontend track for Blade/Vite assets

If your app builds frontend assets (Vite + Blade, or Inertia/Vue/React), add a second track so UI work is routed separately — this is a multi-track config on one repo:

```yaml
tracks:
  - name: backend
    language: php
    file_patterns: ["app/**", "routes/**", "database/**", "tests/**", "**/*.php"]
    engine_slots: 2
    test_command: "php artisan test"

  - name: frontend
    language: typescript
    file_patterns: ["resources/js/**", "resources/css/**", "*.ts", "*.vue"]
    engine_slots: 1
    test_command: "npm test"
```

---

## Step 3: Dispatch an API feature into dependency-ordered cars

We'll add a posts API resource with auth and feature tests. (`ry dispatch` would build this from a plain-English prompt via a live planner session; here it's explicit.)

```console
$ ry car create --type epic --track backend --title "Posts API with auth + feature tests"
Created car car-a9d4c

$ ry car create --parent car-a9d4c --type task --title "Post model + migration"
Created car car-82f48
$ ry car create --parent car-a9d4c --type task --title "PostController (API resource)"
Created car car-fd375
$ ry car create --parent car-a9d4c --type task --title "Sanctum auth on /api/posts"
Created car car-f47e4
$ ry car create --parent car-a9d4c --type task --title "Feature tests (php artisan test)"
Created car car-f5a7b

$ ry car dep add car-fd375 --blocked-by car-82f48   # controller needs the model
$ ry car dep add car-f47e4 --blocked-by car-fd375   # auth guards the controller's routes
$ ry car dep add car-f5a7b --blocked-by car-f47e4   # feature tests come last

$ ry car publish car-a9d4c --recursive
Published 5 car(s) starting from car-a9d4c
```

```console
$ ry car children car-a9d4c
ID         TITLE                             STATUS  TRACK    PRI
car-82f48  Post model + migration            open    backend  2
car-fd375  PostController (API resource)     open    backend  2
car-f47e4  Sanctum auth on /api/posts        open    backend  2
car-f5a7b  Feature tests (php artisan test)  open    backend  2

Summary: 4 open
```

---

## Step 4: Run engines with Yardmaster gating on the Laravel suite

> ⚠️ **Live engines + PHP toolchain below.** `ry start` spawns AI sessions (real API usage) and Yardmaster runs `php artisan test` (needs PHP/Composer). Commands are exact; behavior is described from Railyard's design, not captured here.

```bash
ry start --engines 2
tmux attach -t railyard
```

- Engines claim ready cars on `ry/yourname/backend/<car-id>` branches, working the model → controller → auth → tests chain in dependency order.
- When an engine completes a car, **Yardmaster runs `php artisan test`** on the branch and merges only if it passes — the Laravel suite is the merge gate.

```bash
ry status --watch
ry logs --car <car-id>     # follow a specific car's engine output
```

---

## Troubleshooting

- **Engines can't run `php artisan test`** — the engine environment needs PHP + Composer and an installed `vendor/` (`composer install`). Engines work in git worktrees off your repo; make sure dependencies are available there.
- **`.env` on engine branches** — engines share the working repo, but Laravel needs a `.env` (and `php artisan key:generate`). `.env` is typically gitignored, so it won't appear on a fresh worktree — provision it (or a `.env.testing`) where engines run. Call this out before starting a long run.
- **Database for tests** — point Laravel's test DB at a SQLite file or a dedicated MySQL schema so feature tests don't collide with Railyard's own `railyard_<owner>` database.

---

See also: [README Quickstart](../README.md#quickstart) · [JS game tutorial](tutorial-js-game.md) · [Go service tutorial](tutorial-go.md) · [Mobile (React Native) tutorial](tutorial-mobile.md)
