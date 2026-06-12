# Tutorial: Build a Go Service with Railyard

Build a REST **todo API** in Go (stdlib `net/http`) with Railyard decomposing the work into dependency-ordered cars and running parallel engines. Go is Railyard's home turf — fast tests make the Yardmaster merge-gate loop visible, so this is the best tutorial for seeing **dependency ordering** and **parallel engines** in action.

**What you'll learn:**
- Installing `ry` (Go users can `go install`, but the prebuilt binary works without cloning) and running `ry init` on a Go module
- Decomposing an epic into dependency-ordered cars (`ry car dep`)
- Running 2 engines in parallel with Yardmaster gating merges on `go test ./...`

**Time:** ~5 minutes of setup, then the engines build.

> **What's transcribed from a real run vs. described:** everything through **Step 3 (create the work)** was captured from a real run on a scratch Go module. **Step 4 onward spawns live AI engine sessions** (real API usage); those commands are exact, but their output is described from Railyard's documented behavior rather than pasted — this tutorial shows no fabricated agent output.

---

## Prerequisites

- **`ry`** — installed below
- **Docker** running — Railyard starts MySQL in a container for you
- **An AI coding CLI** — e.g. Claude Code: `npm install -g @anthropic-ai/claude-code`
- **tmux** — hosts the engine panes
- **Go 1.26+** — to build/test the service

---

## Step 1: Install `ry` and create the module

```bash
curl -fsSL https://raw.githubusercontent.com/zulandar/railyard/main/install.sh | sh
```

> Go users can also `go install github.com/zulandar/railyard/cmd/ry@latest` — but the prebuilt binary means you don't have to.

```bash
mkdir -p ~/projects/todo-service && cd ~/projects/todo-service
git init -b main
git remote add origin https://github.com/yourname/todo-service.git
go mod init github.com/yourname/todo-service
```

Add a minimal stdlib server in `main.go` (a `/healthz` handler is enough to start — engines build the rest) and commit:

```bash
git add -A && git commit -m "Initial todo-service scaffold"
```

---

## Step 2: `ry init`

```console
$ ry init --yes
Detected git repository: /home/you/projects/todo-service
Detected remote: https://github.com/yourname/todo-service.git
Detected owner: yourname
Detected languages: go

Wrote /home/you/projects/todo-service/railyard.yaml

Database is already running on 127.0.0.1:3306
Database railyard_yourname ready
Migrated 15 tables
Seeded 1 track(s) and config for owner "yourname"

Railyard initialized successfully!
```

The generated `railyard.yaml` — a Go backend track gated on `go test ./...`:

```yaml
owner: yourname
repo: https://github.com/yourname/todo-service.git

database:
  host: 127.0.0.1
  port: 3306
  username: root

tracks:
  - name: backend
    language: go
    file_patterns: ["**/*.go"]
    engine_slots: 2
    test_command: "go test ./..."
```

> **Optional — semantic code search.** Run `ry cocoindex init -c railyard.yaml` (or accept the prompt during an interactive `ry init`) to start pgvector and give each engine MCP-powered search by meaning. Go projects are the cheapest place to demo the full stack.

---

## Step 3: Decompose the API into dependency-ordered cars

This is the showcase: a storage layer that handlers and auth depend on, and integration tests that depend on both — so the work fans out and then re-converges. (`ry dispatch` would build this from a plain-English prompt via a live planner session; here we create it explicitly.)

```console
$ ry car create --type epic --track backend --title "REST todo API"
Created car car-786ab

$ ry car create --parent car-786ab --type task --title "Storage layer"
Created car car-aec28
$ ry car create --parent car-786ab --type task --title "HTTP handlers (CRUD)"
Created car car-4a3c2
$ ry car create --parent car-786ab --type task --title "Auth middleware"
Created car car-de37e
$ ry car create --parent car-786ab --type task --title "Integration tests"
Created car car-3b29c

# Handlers and auth both build on the storage layer:
$ ry car dep add car-4a3c2 --blocked-by car-aec28
Added dependency: car-4a3c2 blocked by car-aec28
$ ry car dep add car-de37e --blocked-by car-aec28
Added dependency: car-de37e blocked by car-aec28
# Integration tests need handlers + auth:
$ ry car dep add car-3b29c --blocked-by car-4a3c2
Added dependency: car-3b29c blocked by car-4a3c2
$ ry car dep add car-3b29c --blocked-by car-de37e
Added dependency: car-3b29c blocked by car-de37e

$ ry car publish car-786ab --recursive
Published 5 car(s) starting from car-786ab
```

Only the storage layer is ready; everything else is correctly blocked:

```console
$ ry car ready
ID         TITLE          TRACK    PRI
car-aec28  Storage layer  backend  2
```

---

## Step 4: Run engines and watch the merge-gate loop

> ⚠️ **Live engines below.** `ry start` spawns AI coding sessions that consume real API usage. Commands are exact; the behavior described is from Railyard's design, not captured here.

```bash
ry start --engines 2
tmux attach -t railyard
```

What you'll see, in order:
1. One engine claims **Storage layer** on branch `ry/yourname/backend/car-aec28`. (The other idles — handlers/auth are still blocked.)
2. The engine completes; **Yardmaster** runs `go test ./...` on the branch and merges it. Go's fast tests make this loop quick and visible.
3. **HTTP handlers** and **Auth middleware** become ready simultaneously — both engines now work in parallel on isolated branches.
4. Once both merge, **Integration tests** unblocks and a final engine completes the epic.

Useful while it runs:

```bash
ry status --watch                # live engine + per-track car counts
ry car dep list car-3b29c        # confirm what's still blocking the integration car
ry engine restart <engine-id>    # restart a stalled engine
ry switch <car-id> --dry-run     # run a car's tests without merging
```

---

## Troubleshooting

- **`ry init` hangs at "Starting MySQL container"** — ensure Docker is running; first-time MySQL init takes 15–30s.
- **Yardmaster won't merge** — the branch's `go test ./...` is failing; check `ry logs --car <id>`.
- **An engine stalls** — `ry engine restart <id>`; see stall thresholds in `railyard.example.yaml`.

---

## A note on the older tutorial

This tutorial **replaces** the previous multi-language `docs/tutorial-todo-app.md`, whose Go strand is superseded here, its PHP strand by the [Laravel tutorial](tutorial-laravel.md), and its Node strand by the [JS game tutorial](tutorial-js-game.md). A single doc spanning three languages was hard to keep "transcribed from real runs"; per-stack tutorials each stay honest.

---

See also: [README Quickstart](../README.md#quickstart) · [JS game tutorial](tutorial-js-game.md) · [Laravel tutorial](tutorial-laravel.md) · [Mobile (React Native) tutorial](tutorial-mobile.md)
