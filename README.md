# Railyard

Multi-agent AI orchestration for coding. Railyard coordinates multiple Claude Code agents across tracks (backend, frontend, infra) with per-branch isolation, a version-controlled SQL database, semantic code search, and automated supervision.

Each developer runs their own Railyard instance against the same repo. Agents work on isolated branches (`ry/{owner}/{track}/{car-id}`), and a supervisor (Yardmaster) handles merges, stall detection, and dependency management.

## Terminology

| Term | Meaning |
|---|---|
| **Track** | An area of concern within the repo (backend, frontend, infra) |
| **Car** | A unit of work (task, bug, feature, epic) |
| **Engine** | A worker agent (Claude Code session that claims and executes cars) |
| **Yardmaster** | Supervisor agent — merges branches, monitors engines, handles stalls |
| **Dispatch** | Planner agent — your interface, decomposes requests into cars |
| **Switch** | Merging a completed car's branch back to main |
| **Overlay** | Per-engine semantic index of changed files for context-aware search |

## Architecture

```
                    ┌───────────┐
                    │  Dispatch │  ← You talk to this
                    └─────┬─────┘
                          │ creates cars
                    ┌─────▼─────┐
              ┌─────┤   Dolt DB  ├─────┐
              │     └─────┬─────┘     │
              │           │           │
        ┌─────▼───┐ ┌─────▼───┐ ┌────▼────┐
        │Engine 1 │ │Engine 2 │ │Engine N │  ← Claude Code agents
        └─────┬───┘ └─────┬───┘ └────┬────┘
              │           │          │
              │     ┌─────▼─────┐   │
              └─────┤ pgvector  ├───┘  ← Semantic code search
                    └─────┬─────┘
              ┌─────▼─────┐
              │Yardmaster │  ← Merges, monitors, coordinates
              └───────────┘
```

- **Dolt** (version-controlled MySQL) stores all state: cars, engine status, messages, logs
- **pgvector** (PostgreSQL + vector extension) stores semantic code embeddings for search
- **tmux** manages agent sessions — each engine, Dispatch, and Yardmaster runs in its own pane
- **Engines** poll for ready cars, spawn Claude Code sessions with MCP-powered code search, and handle completion/stall outcomes
- **Yardmaster** runs tests and merges completed branches back to main

## Prerequisites

- **Go 1.25+**
- **Dolt** — version-controlled SQL database ([install](https://docs.dolthub.com/introduction/installation))
- **tmux** — terminal multiplexer
- **Claude Code CLI** — `npm install -g @anthropic-ai/claude-code`
- **Docker** (optional) — for pgvector/CocoIndex semantic search
- **Python 3.13+** (optional) — for CocoIndex semantic search

## Quickstart

The quickstart script handles everything: installing prerequisites, building the `ry` binary, starting Dolt, initializing the database, and optionally setting up pgvector for semantic code search.

```bash
git clone https://github.com/zulandar/railyard.git
cd railyard
chmod +x quickstart.sh
./quickstart.sh
```

### Manual Setup

If you prefer to set things up step by step:

**1. Build the CLI**

```bash
go build -o ry ./cmd/ry/
```

**2. Start Dolt**

```bash
mkdir -p ~/.railyard/dolt-data
cd ~/.railyard/dolt-data
dolt init --name "railyard" --email "railyard@local"
dolt sql-server --host 127.0.0.1 --port 3306 &
cd -
```

**3. Configure**

Copy the example config and edit it for your project:

```bash
cp railyard.example.yaml railyard.yaml
# Edit owner, repo, tracks to match your project
```

Or create a `railyard.yaml` manually in your repo root:

```yaml
owner: yourname
repo: git@github.com:org/repo.git

dolt:
  host: 127.0.0.1
  port: 3306

tracks:
  - name: backend
    language: go
    file_patterns: ["cmd/**", "internal/**", "pkg/**", "*.go"]
    engine_slots: 2
    conventions:
      go_version: "1.25"
      style: "stdlib-first, no frameworks"
```

See [`railyard.example.yaml`](railyard.example.yaml) for a fully documented template with all available options.

**4. Initialize the database**

```bash
ry db init -c railyard.yaml
```

> **After a reboot:** Dolt doesn't survive WSL/system restarts. Run `ry db start -c railyard.yaml` to restart it.

**5. (Optional) Set up semantic code search**

```bash
ry cocoindex init -c railyard.yaml
```

This starts a pgvector container, creates a Python venv, installs dependencies, and runs schema migrations. See [Semantic Code Search](#semantic-code-search-cocoindex) for details.

**6. Start Railyard**

```bash
ry start -c railyard.yaml --engines 2
```

**7. Attach to watch agents work**

```bash
tmux attach -t railyard
```

## Usage

### Orchestration

```bash
ry start -c railyard.yaml --engines 2   # Start Dispatch + Yardmaster + N engines
ry status -c railyard.yaml              # Dashboard: engines, cars, messages
ry status -c railyard.yaml --watch      # Auto-refresh every 5s
ry dashboard -c railyard.yaml           # Web UI at http://localhost:8080
ry dashboard -c railyard.yaml -p 9090   # Custom port
ry stop -c railyard.yaml                # Graceful shutdown
```

### Car Management

```bash
# Create work items (created in draft status — engines won't pick them up yet)
ry car create -c railyard.yaml --title "Add auth middleware" --track backend --type task
ry car create -c railyard.yaml --title "Auth epic" --track backend --type epic

# Publish cars so engines can claim them (draft → open)
ry car publish <car-id>                # Single car
ry car publish <epic-id> --recursive   # Epic + all draft children

# List and inspect
ry car list -c railyard.yaml --track backend --status open
ry car show <car-id>
ry car ready                           # Cars with no blockers, ready for work
ry car children <epic-id>              # Children of an epic with status summary

# Update
ry car update <car-id> --status in_progress
ry car update <car-id> --priority 0 --description "Updated scope"
ry car update <car-id> --skip-tests       # Skip test gate during merge

# Dependencies
ry car dep add <car-id> --blocked-by <blocker-id>
ry car dep list <car-id>
ry car dep remove <car-id> --blocked-by <blocker-id>
```

### Engine Management

```bash
ry engine list                          # Show all engines with status/uptime
ry engine scale --track backend --count 3  # Scale engines on a track
ry engine restart <engine-id>           # Restart a stalled engine
```

### Agent Commands

```bash
ry dispatch                             # Start interactive Dispatch planner
ry yardmaster                           # Start Yardmaster supervisor
ry engine start --track backend         # Start a single engine daemon

# Called by agents during work
ry complete <car-id> "summary"         # Mark car done
ry progress <car-id> "checkpoint"      # Log progress without completing
```

### Merging

```bash
ry switch <car-id>                     # Run tests + merge branch to main
ry switch <car-id> --dry-run           # Run tests only, don't merge
```

### Messaging

```bash
ry message send --to <engine-id> --subject "..." --body "..."
ry inbox                                # Check messages for current engine
```

### Monitoring and Diagnostics

```bash
ry doctor -c railyard.yaml             # Check prerequisites, config, DB, schema, git
ry logs -c railyard.yaml               # View agent log output
ry logs --engine <id> --follow         # Tail logs for a specific engine
ry logs --car <id>                     # Logs for a specific car
ry watch -c railyard.yaml              # Stream messages in real-time
ry watch --all                         # Watch all agent messages
```

### Semantic Code Search

```bash
ry cocoindex init                      # Set up pgvector + Python venv + migrations
ry cocoindex init --skip-venv          # Skip Python venv creation
ry overlay build --engine <id>         # Build overlay index for an engine
ry overlay status                      # Show overlay status for all engines
ry overlay status --engine <id>        # Show overlay status for one engine
ry overlay cleanup --engine <id>       # Drop overlay table + metadata
ry overlay gc                          # Clean up orphaned overlays
```

### Project Utilities

```bash
ry gitignore                           # Update .gitignore for detected languages
ry gitignore --detect                  # Detect languages from project files
ry gitignore --dry-run                 # Preview changes without modifying
```

## Semantic Code Search (CocoIndex)

Railyard integrates with [CocoIndex](https://github.com/cocoindex/cocoindex) and pgvector to give engines semantic code search via MCP (Model Context Protocol). Engines can search the codebase by meaning, not just keywords.

### How It Works

1. **Main indexes** — per-track embeddings of the entire codebase (built from `main` branch)
2. **Overlay indexes** — per-engine embeddings of files changed on the engine's feature branch
3. **MCP server** — each engine gets a `.mcp.json` that launches a search server with dual-table lookup (main + overlay, overlay wins on conflict)

### Setup

```bash
# Initialize pgvector (Docker), Python venv, and schema
ry cocoindex init -c railyard.yaml

# The quickstart.sh script does this automatically if Docker is available
```

This starts a PostgreSQL 16 container with pgvector on port 5481 (auto-detects conflicts), creates a Python 3.13+ venv at `cocoindex/.venv`, installs dependencies, and runs migrations.

### Configuration

Add the CocoIndex section to your `railyard.yaml`:

```yaml
cocoindex:
  database_url: "postgresql://cocoindex:cocoindex@localhost:5481/cocoindex"
  overlay:
    enabled: true           # Auto-build overlay indexes for engines
    auto_refresh: true      # Rebuild overlay when engine switches cars
    build_timeout_sec: 60   # Timeout for overlay builds
```

When `database_url` is set, overlay indexing is enabled by default. Engines automatically get MCP-powered semantic search.

### Manual Operations

```bash
# Build overlay for a specific engine
ry overlay build --engine eng-abc123 -c railyard.yaml

# Check overlay status
ry overlay status -c railyard.yaml

# Clean up a specific engine's overlay
ry overlay cleanup --engine eng-abc123 -c railyard.yaml

# Garbage collect orphaned overlays (engines that no longer exist)
ry overlay gc -c railyard.yaml --dry-run
ry overlay gc -c railyard.yaml
```

## Configuration Reference

See [`railyard.example.yaml`](railyard.example.yaml) for a copy-paste ready template.

```yaml
owner: alice                            # Your identity (branch prefix: ry/alice/...)
repo: git@github.com:org/repo.git       # Target repository
# branch_prefix: ry/alice               # Override default ry/{owner}
# default_acceptance: "Tests pass, code reviewed"  # Default acceptance criteria for Dispatch
# require_pr: true                      # Create draft PRs instead of direct merge to main

dolt:
  host: 127.0.0.1
  port: 3306
  # database: railyard_alice            # Override default railyard_{owner}

stall:
  stdout_timeout_sec: 120               # No stdout for 120s = stall
  repeated_error_max: 3                 # Same error 3x = stall
  max_clear_cycles: 5                   # More than 5 /clear cycles = stall

# Optional: CocoIndex semantic search (requires Docker + Python 3.13+)
# cocoindex:
#   database_url: "postgresql://cocoindex:cocoindex@localhost:5481/cocoindex"
#   overlay:
#     enabled: true
#     auto_refresh: true
#     build_timeout_sec: 60

tracks:
  - name: backend
    language: go
    file_patterns: ["cmd/**", "internal/**", "pkg/**", "*.go"]
    engine_slots: 2                     # Max concurrent engines on this track
    test_command: "go test ./..."       # Command to validate before merge (default: go test ./...)
    conventions:
      go_version: "1.25"
      style: "stdlib-first, no frameworks"
      test_framework: "stdlib table-driven"

  - name: frontend
    language: typescript
    file_patterns: ["src/**", "*.ts", "*.tsx", "*.css"]
    engine_slots: 2
    test_command: "npm test"            # Any shell command works
    conventions:
      framework: "Next.js 15"
      styling: "Tailwind CSS"
```

## How It Works

1. **Dispatch** decomposes your request into structured cars with dependencies
2. **Engines** poll the database for ready cars (no unresolved blockers), claim one atomically, and spawn a Claude Code session with full context (car description, track conventions, prior progress, recent commits)
3. Each engine works on an isolated git branch (`ry/{owner}/{track}/{car-id}`)
4. If CocoIndex is configured, each engine gets an MCP server for semantic code search — the overlay index tracks files changed on the engine's branch so search results are always current
5. When an agent finishes, it calls `ry complete` — the engine daemon picks up the next car
6. **Yardmaster** monitors for stalls (no stdout, repeated errors, excessive /clear cycles), runs tests on completed branches, and merges them back to main via `ry switch`
7. All state lives in Dolt — fully auditable with `dolt diff`, `dolt log`, and time-travel queries

## CI/CD

Railyard includes GitHub Actions workflows:

- **CI** (`.github/workflows/ci.yml`) — runs on PRs and pushes to main: full test suite with race detector, `go vet`, and `gofmt` checks
- **Release** (`.github/workflows/release.yml`) — triggers on `v*` tag push: runs tests, cross-compiles binaries (linux/darwin, amd64/arm64), generates a grouped changelog, and publishes a GitHub Release with attached archives

To create a release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

## Project Structure

```
cmd/ry/              CLI entry point (Cobra commands)
internal/
  car/               Car CRUD, dependencies, ready detection
  config/            YAML config loading and validation
  db/                Dolt/GORM connection and migrations
  dispatch/          Dispatch planner agent (decomposition)
  engine/            Engine daemon: claim, spawn, stall detection, outcomes, overlay
  messaging/         Agent-to-agent message passing via DB
  models/            GORM models (Car, Engine, Message, Track, etc.)
  orchestration/     tmux session management, start/stop/scale/status
  yardmaster/        Yardmaster supervisor: health checks, switch/merge
cocoindex/           Python-based semantic search (CocoIndex + pgvector)
  overlay.py         Per-engine overlay indexer (build, cleanup, status)
  mcp_server.py      MCP server for dual-table semantic search
  build_all.py       Per-track main index builder
  migrate.py         pgvector schema migrations
  config.py          CocoIndex YAML config loader
docker/              Docker Compose files (pgvector)
.github/workflows/   CI and release pipelines
```

## Tutorials

- **[Build a Todo App with Railyard](docs/tutorial-todo-app.md)** — Step-by-step walkthrough building an API with multiple agents working in parallel

## License

All rights reserved.
