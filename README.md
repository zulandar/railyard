# Railyard

Multi-agent AI orchestration for coding. Railyard coordinates multiple Claude Code agents across tracks (backend, frontend, infra) with per-branch isolation, a version-controlled SQL database, and automated supervision.

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
              └─────┬─────┘──────────┘
              ┌─────▼─────┐
              │Yardmaster │  ← Merges, monitors, coordinates
              └───────────┘
```

- **Dolt** (version-controlled MySQL) stores all state: cars, engine status, messages, logs
- **tmux** manages agent sessions — each engine, Dispatch, and Yardmaster runs in its own pane
- **Engines** poll for ready cars, spawn Claude Code sessions, and handle completion/stall outcomes
- **Yardmaster** runs tests and merges completed branches back to main

## Prerequisites

- **Go 1.25+**
- **Dolt** — version-controlled SQL database ([install](https://docs.dolthub.com/introduction/installation))
- **tmux** — terminal multiplexer
- **Claude Code CLI** — `npm install -g @anthropic-ai/claude-code`

## Quickstart

The quickstart script handles everything: installing prerequisites, building the `ry` binary, starting Dolt, and initializing the database.

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

Create a `railyard.yaml` in your repo root:

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

**4. Initialize the database**

```bash
ry db init -c railyard.yaml
```

**5. Start Railyard**

```bash
ry start -c railyard.yaml --engines 2
```

**6. Attach to watch agents work**

```bash
tmux attach -t railyard
```

## Usage

### Orchestration

```bash
ry start -c railyard.yaml --engines 2   # Start Dispatch + Yardmaster + N engines
ry status -c railyard.yaml              # Dashboard: engines, cars, messages
ry status -c railyard.yaml --watch      # Auto-refresh every 5s
ry stop -c railyard.yaml                # Graceful shutdown
```

### Car Management

```bash
# Create work items
ry car create -c railyard.yaml --title "Add auth middleware" --track backend --type task
ry car create -c railyard.yaml --title "Auth epic" --track backend --type epic

# List and inspect
ry car list -c railyard.yaml --track backend --status open
ry car show <car-id>
ry car ready                           # Cars with no blockers, ready for work
ry car children <epic-id>              # Children of an epic with status summary

# Update
ry car update <car-id> --status in_progress
ry car update <car-id> --priority 0 --description "Updated scope"

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

## Configuration Reference

```yaml
owner: alice                            # Your identity (branch prefix: ry/alice/...)
repo: git@github.com:org/repo.git       # Target repository
# branch_prefix: ry/alice               # Override default ry/{owner}

dolt:
  host: 127.0.0.1
  port: 3306
  # database: railyard_alice            # Override default railyard_{owner}

stall:
  stdout_timeout_sec: 120               # No stdout for 120s = stall
  repeated_error_max: 3                 # Same error 3x = stall
  max_clear_cycles: 5                   # More than 5 /clear cycles = stall

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
4. When an agent finishes, it calls `ry complete` — the engine daemon picks up the next car
5. **Yardmaster** monitors for stalls (no stdout, repeated errors, excessive /clear cycles), runs tests on completed branches, and merges them back to main via `ry switch`
6. All state lives in Dolt — fully auditable with `dolt diff`, `dolt log`, and time-travel queries

## Project Structure

```
cmd/ry/              CLI entry point (Cobra commands)
internal/
  car/              Car CRUD, dependencies, ready detection
  config/            YAML config loading and validation
  db/                Dolt/GORM connection and migrations
  dispatch/          Dispatch planner agent (decomposition)
  engine/            Engine daemon: claim, spawn, stall detection, outcomes
  messaging/         Agent-to-agent message passing via DB
  models/            GORM models (Car, Engine, Message, Track, etc.)
  orchestration/     tmux session management, start/stop/scale/status
  yardmaster/        Yardmaster supervisor: health checks, switch/merge
```

## Tutorials

- **[Build a Todo App with Railyard](docs/tutorial-todo-app.md)** — Step-by-step walkthrough building a Go API with multiple agents working in parallel

## License

All rights reserved.
