# Railyard — Architecture Document

## Overview

Railyard is a multi-agent AI orchestration system that coordinates coding agents across local machines and cloud VMs. Multiple employees can each run their own Railyard instance against the **same repo**, working on separate branches. Uses Dolt (version-controlled SQL) for task state, GORM for all database access, and per-track isolation to prevent context contamination between domains.

**Naming convention:**

| Railyard Term | Meaning |
|---|---|
| **Railyard** | An employee's orchestration instance (each person runs their own) |
| **Track** | An area of concern within the repo (backend, frontend, infra) |
| **Car** | A unit of work (from the [Beads](https://github.com/steveyegge/beads) model) |
| **Engine** | A worker agent (Claude Code, Codex, etc.) |
| **Yardmaster** | The supervisor agent — merges, monitors, coordinates |
| **Dispatch** | The planner agent — your interface, breaks down work |
| **Telegraph** | Chat bridge — connects Railyard to Slack/Discord for commands, events, and dispatch |
| **Roundhouse** | CocoIndex GPU box — re-indexes code after merges |
| **Coupling** | Car dependencies — cars linked together |
| **Switch** | Merging a branch back to main |

**Multi-railyard model:**
```
repo: github.com/org/myapp
├── alice's railyard  → branches: ry/alice/backend/car-001, ry/alice/frontend/car-010
├── bob's railyard    → branches: ry/bob/backend/car-050, ry/bob/infra/car-080
└── carol's railyard  → branches: ry/carol/frontend/car-030
```

Each railyard is fully independent (Phase 1). Phase 2 adds a shared merge queue and file-level conflict awareness across railyards.

---

## Core Components

### 1. Dolt — Railyard Database

Dolt replaces Beads' git-backed JSONL with a proper SQL database that retains version control semantics. All database access goes through **GORM** (Go ORM, MySQL-compatible — native fit with Dolt).

**Why Dolt over plain Postgres:**
- `dolt diff` on any table shows exactly what changed and when — full audit trail
- `dolt log` gives you commit history of orchestration state changes
- `dolt revert` lets you undo bad state changes (e.g., accidental mass-close of cars)
- Time-travel queries: `SELECT * FROM cars AS OF 'HEAD~5'` to debug what went wrong
- Each railyard instance gets its own Dolt database — true isolation between employees

**Database per railyard instance:**
Each employee's Railyard gets its own Dolt database. In local dev, each person runs their own Dolt server. In production, a shared Dolt server hosts multiple databases.

```
# Local (alice's machine)
railyard_alice/          — alice's cars, messages, logs, config

# Production (shared Dolt server)
railyard_alice/          — alice's railyard
railyard_bob/            — bob's railyard
railyard_carol/          — carol's railyard
railyard_shared/         — shared config, merge queue (Phase 2)
```

**Tracks are logical, not separate databases.** Since tracks are areas of concern within the same repo (backend, frontend, infra), they live as a column on the cars table — not as separate databases. This simplifies cross-track queries for the Yardmaster:

```sql
-- Yardmaster checks all tracks at once
SELECT * FROM cars WHERE status = 'done' AND track = 'backend';
SELECT * FROM cars WHERE status = 'blocked';  -- all tracks
```

**Branch namespacing:** Each railyard owns a branch prefix in the shared repo:
```
ry/{owner}/{track}/{car_id}
ry/alice/backend/car-001
ry/alice/frontend/car-010
ry/bob/backend/car-050
```
This prevents branch collisions between employees and makes ownership instantly clear.

### 1.5. GORM — Database Access Layer

All database access goes through GORM models. No raw SQL in application code (Dolt-specific queries like `dolt diff` use `db.Raw()` where needed).

```go
package models

import (
    "time"
    "gorm.io/gorm"
)

// Car is the core work item (from the Beads model).
type Car struct {
    ID          string     `gorm:"primaryKey;size:32"`     // e.g., car-a1b2c
    Title       string     `gorm:"not null"`
    Description string     `gorm:"type:text"`
    Type        string     `gorm:"size:16;default:task"`   // task, epic, bug, spike
    Status      string     `gorm:"size:16;default:open;index"` // open, ready, claimed, in_progress, done, blocked, cancelled
    Priority    int        `gorm:"default:2"`              // 0=critical → 4=backlog
    Track       string     `gorm:"size:64;index"`          // backend, frontend, infra
    Assignee    string     `gorm:"size:64"`                // engine ID
    ParentID    *string    `gorm:"size:32"`                // epic parent
    Branch      string     `gorm:"size:128"`               // git branch: ry/alice/backend/car-001
    DesignNotes string     `gorm:"type:text"`
    Acceptance  string     `gorm:"type:text"`
    CreatedAt   time.Time
    UpdatedAt   time.Time
    ClaimedAt   *time.Time
    CompletedAt *time.Time

    // Relations
    Parent   *Car        `gorm:"foreignKey:ParentID"`
    Children []Car       `gorm:"foreignKey:ParentID"`
    Deps     []CarDep    `gorm:"foreignKey:CarID"`
    Progress []CarProgress `gorm:"foreignKey:CarID"`
}

// CarDep tracks blocking relationships between cars.
type CarDep struct {
    CarID    string `gorm:"primaryKey;size:32"`
    BlockedBy string `gorm:"primaryKey;size:32"`
    DepType   string `gorm:"size:16;default:blocks"` // blocks, relates_to

    Car     Car `gorm:"foreignKey:CarID"`
    Blocker Car `gorm:"foreignKey:BlockedBy"`
}

// CarDepExternal tracks cross-railyard dependencies (Phase 2).
type CarDepExternal struct {
    CarID          string `gorm:"primaryKey;size:32"`
    BlockedByOwner  string `gorm:"primaryKey;size:64"`  // foreign railyard owner
    BlockedByID     string `gorm:"primaryKey;size:32"`  // foreign car ID
    DepType         string `gorm:"size:16;default:blocks"`
}

// CarProgress logs work done across /clear cycles.
type CarProgress struct {
    ID           uint      `gorm:"primaryKey;autoIncrement"`
    CarID       string    `gorm:"size:32;index"`
    Cycle        int                                    // /clear cycle number
    SessionID    string    `gorm:"size:64"`
    EngineID     string    `gorm:"size:64"`
    Note         string    `gorm:"type:text"`           // what was done, what's next
    FilesChanged string    `gorm:"type:json"`            // JSON list of files
    CommitHash   string    `gorm:"size:40"`
    CreatedAt    time.Time
}

// Track defines an area of concern within the repo.
type Track struct {
    Name         string `gorm:"primaryKey;size:64"`      // backend, frontend, infra
    Language     string `gorm:"size:32"`
    Conventions  string `gorm:"type:json"`               // structured project rules
    SystemPrompt string `gorm:"type:text"`               // agent prompt for this track
    FilePatterns string `gorm:"type:json"`               // ["*.go", "internal/**"]
    EngineSlots  int    `gorm:"default:3"`
    Active       bool   `gorm:"default:true"`
}

// Engine represents a worker agent.
type Engine struct {
    ID           string    `gorm:"primaryKey;size:64"`
    VMID         string    `gorm:"size:64"`
    Track        string    `gorm:"size:64;index"`
    Role         string    `gorm:"size:16"`              // engine, yardmaster, dispatch
    Status       string    `gorm:"size:16;index"`        // idle, working, clearing, stalled, dead
    CurrentCar  string    `gorm:"size:32"`
    SessionID    string    `gorm:"size:64"`
    StartedAt    time.Time
    LastActivity time.Time `gorm:"index"`
}

// Message handles agent-to-agent communication.
type Message struct {
    ID           uint      `gorm:"primaryKey;autoIncrement"`
    FromAgent    string    `gorm:"size:64;not null"`
    ToAgent      string    `gorm:"size:64;not null;index"` // engine ID, 'yardmaster', 'dispatch', 'broadcast'
    CarID       string    `gorm:"size:32"`                // optional car context
    ThreadID     *uint                                     // parent message for threading
    Subject      string    `gorm:"size:256"`
    Body         string    `gorm:"type:text"`
    Priority     string    `gorm:"size:8;default:normal"`
    Acknowledged bool      `gorm:"default:false;index"`
    CreatedAt    time.Time
}

// AgentLog captures complete I/O for debugging and replay.
type AgentLog struct {
    ID         uint      `gorm:"primaryKey;autoIncrement"`
    EngineID   string    `gorm:"size:64;index:idx_engine_session"`
    SessionID  string    `gorm:"size:64;index:idx_engine_session"`
    CarID     string    `gorm:"size:32;index"`
    Direction  string    `gorm:"size:4"`                 // 'in' or 'out'
    Content    string    `gorm:"type:mediumtext"`
    TokenCount int
    Model      string    `gorm:"size:64"`
    LatencyMs  int
    CreatedAt  time.Time
}

// RailyardConfig stores instance-level configuration.
type RailyardConfig struct {
    ID       uint   `gorm:"primaryKey;autoIncrement"`
    Owner    string `gorm:"size:64;uniqueIndex"`        // alice, bob, carol
    RepoURL  string `gorm:"type:text;not null"`
    Mode     string `gorm:"size:16;default:local"`      // local, production
    Settings string `gorm:"type:json"`                  // arbitrary config
}

// ReindexJob tracks Roundhouse re-indexing work.
type ReindexJob struct {
    ID            uint      `gorm:"primaryKey;autoIncrement"`
    Track         string    `gorm:"size:64;not null"`
    TriggerCommit string    `gorm:"size:40"`
    Status        string    `gorm:"size:16;default:pending"` // pending, running, done, failed
    FilesChanged  int
    ChunksUpdated int
    GPUBoxID      string    `gorm:"size:64"`
    StartedAt     *time.Time
    CompletedAt   *time.Time
    CreatedAt     time.Time
    ErrorMessage  string    `gorm:"type:text"`
}
```

**GORM connection setup:**
```go
package db

import (
    "fmt"
    "gorm.io/driver/mysql"
    "gorm.io/gorm"
)

// Connect opens a GORM connection to this railyard's Dolt database.
func Connect(owner, host string, port int) (*gorm.DB, error) {
    dsn := fmt.Sprintf("root@tcp(%s:%d)/railyard_%s?parseTime=true", host, port, owner)
    db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
    if err != nil {
        return nil, fmt.Errorf("dolt connect: %w", err)
    }
    return db, nil
}

// AutoMigrate creates/updates all tables.
func AutoMigrate(db *gorm.DB) error {
    return db.AutoMigrate(
        &models.Car{},
        &models.CarDep{},
        &models.CarDepExternal{},
        &models.CarProgress{},
        &models.Track{},
        &models.Engine{},
        &models.Message{},
        &models.AgentLog{},
        &models.RailyardConfig{},
        &models.ReindexJob{},
    )
}
```

### 2. Schema

Schema is defined by GORM models above (Section 1.5). GORM AutoMigrate creates all tables in Dolt. Key tables:

| Table | Purpose |
|---|---|
| `cars` | Work items — the core unit. Has `track` column for filtering. |
| `car_deps` | Blocking relationships between cars (same railyard) |
| `car_deps_external` | Cross-railyard dependencies (Phase 2) |
| `car_progress` | Work log across /clear cycles |
| `tracks` | Track definitions (backend, frontend, infra) |
| `engines` | Worker agent state and health |
| `messages` | Agent-to-agent communication |
| `agent_logs` | Complete I/O capture for debugging |
| `railyard_config` | Instance-level settings |
| `reindex_jobs` | Roundhouse re-indexing queue |

**Key GORM operations:**

```go
// Claim next ready car (atomic, scoped to track)
func ClaimCar(db *gorm.DB, engineID, track string) (*models.Car, error) {
    var car models.Car
    err := db.Transaction(func(tx *gorm.DB) error {
        // Lock the first ready car on this track
        if err := tx.Set("gorm:query_option", "FOR UPDATE SKIP LOCKED").
            Where("track = ? AND status = ? AND assignee IS NULL", track, "ready").
            Order("priority ASC, created_at ASC").
            First(&car).Error; err != nil {
            return err // no ready cars
        }
        // Claim it
        now := time.Now()
        return tx.Model(&car).Updates(map[string]interface{}{
            "status":    "claimed",
            "assignee":  engineID,
            "claimed_at": now,
        }).Error
    })
    return &car, err
}

// Mark car done
func CompleteCar(db *gorm.DB, carID, engineID, note string) error {
    now := time.Now()
    return db.Transaction(func(tx *gorm.DB) error {
        if err := tx.Model(&models.Car{}).Where("id = ?", carID).Updates(map[string]interface{}{
            "status":       "done",
            "completed_at": now,
        }).Error; err != nil {
            return err
        }
        return tx.Create(&models.CarProgress{
            CarID:   carID,
            EngineID: engineID,
            Note:     note,
        }).Error
    })
}

// Ready detection — car is ready when all blockers are done
func ReadyCars(db *gorm.DB, track string) ([]models.Car, error) {
    var cars []models.Car
    err := db.Where("track = ? AND status = ? AND assignee IS NULL", track, "open").
        Where("id NOT IN (?)",
            db.Table("car_deps").
                Select("car_id").
                Joins("JOIN cars blocker ON car_deps.blocked_by = blocker.id").
                Where("blocker.status NOT IN ?", []string{"done", "cancelled"}),
        ).
        Order("priority ASC, created_at ASC").
        Find(&cars).Error
    return cars, err
}
```

---

## Messaging: Kafka vs Direct DB

### Option A: Direct DB (Recommended to Start)

Messages are just rows in the `messages` table. Engines poll on interval. Dolt doesn't support `LISTEN/NOTIFY` like Postgres, but polling every 5s is fine for this workload.

**Pros:** No additional infrastructure. Full audit trail in Dolt (diffable, revertible). Works identically local and in VPC. Dead simple.

**Cons:** Polling latency (5s). Won't scale past ~50 engines efficiently.

```
Engine loop:
  1. Poll: SELECT * FROM messages WHERE to_agent = @me AND acknowledged = FALSE
  2. Process messages (Yardmaster instructions, car assignments, etc.)
  3. Acknowledge: UPDATE messages SET acknowledged = TRUE WHERE id = @msg_id
```

### Option B: Kafka (When You Need Scale)

Add Kafka when direct DB polling becomes a bottleneck. The Dolt messages table becomes the audit/persistence layer; Kafka handles real-time delivery.

```
Topic structure:
  railyard.{owner}.track.{track_name}.assignments  — new car assignments
  railyard.{owner}.track.{track_name}.completions   — car done notifications
  railyard.{owner}.track.{track_name}.messages      — general agent-to-agent
  railyard.{owner}.yardmaster.commands              — Yardmaster directives
  railyard.{owner}.system.heartbeats                — engine health
  railyard.{owner}.system.logs                      — centralized logging
```

**Pattern:** Write to Kafka for real-time delivery, consumer writes to Dolt for persistence/audit. Engines consume from their owner+track topic only.

### Recommendation

Start with direct DB. It's simpler, fully auditable, and Dolt's versioning gives you things Kafka can't (time-travel debugging, diffing message state). Add Kafka later only if you hit polling latency issues with many engines.

---

## Deployment Modes

### Local Development Mode

Everything runs on your laptop. Your own Dolt instance, GORM handles schema, agents in tmux panes. `ry` is the Railyard CLI.

```
┌─ Alice's Machine ──────────────────────────────────┐
│                                                      │
│  tmux session: railyard                              │
│  ┌──────────┬──────────┬───────────┬────────┐       │
│  │ Dolt     │ Dispatch │ Yardmaster│ Engine │       │
│  │ Server   │          │           │ (1..N) │       │
│  │ :3306    │          │           │        │       │
│  └──────────┴──────────┴───────────┴────────┘       │
│                                                      │
│  Dolt database: railyard_alice                       │
│  ┌──────────────────────────────────────────┐       │
│  │ cars (track=backend | frontend | infra) │       │
│  │ engines, messages, car_progress, ...    │       │
│  └──────────────────────────────────────────┘       │
│                                                      │
│  Background services:                                │
│  ┌──────────┬───────────────┬────────────────┐      │
│  │ Postgres │ CocoIndex     │ cocoindex-mcp  │      │
│  │ +pgvector│ indexer (CPU) │ server (:8080) │      │
│  │ :5432    │               │                │      │
│  └──────────┴───────────────┴────────────────┘      │
│                                                      │
│  ~/railyard/                                         │
│    repo/               — single repo checkout        │
│    config.yaml         — track definitions, owner    │
│    ry                  — CLI binary (Go)             │
│                                                      │
│  Git branches: ry/alice/backend/car-001, ...          │
│                                                      │
└──────────────────────────────────────────────────────┘
```

**Local startup (`ry start`):**
```bash
#!/bin/bash
# Driven by config.yaml, executed by `ry start`

OWNER=$(yq '.owner' config.yaml)  # e.g., "alice"

# 1. Start Dolt server
dolt sql-server --host 127.0.0.1 --port 3306 &
sleep 2

# 2. Create database + run GORM AutoMigrate
# (The Go binary handles this — creates railyard_{owner} and migrates all tables)
ry db init

# 3. Start Postgres + pgvector for CocoIndex
pg_isready -q || pg_ctl start -D ~/railyard/pgdata -l ~/railyard/pg.log
for track in $(yq '.tracks[].name' config.yaml); do
    psql -c "CREATE DATABASE IF NOT EXISTS cocoindex_${track}" 2>/dev/null
    psql -d "cocoindex_${track}" -c "CREATE EXTENSION IF NOT EXISTS vector" 2>/dev/null
done

# 4. Run initial CocoIndex indexing (CPU, local)
for track in $(yq '.tracks[].name' config.yaml); do
    python cocoindex_flow.py --track $track --mode index &
done
wait

# 5. Start cocoindex-mcp server (Roundhouse)
python cocoindex_mcp_server.py --port 8080 &

# 6. Start tmux session
tmux new-session -d -s railyard

# Pane 0: Dispatch (you talk to this)
tmux send-keys "claude --project dispatch" Enter

# Pane 1: Yardmaster
tmux split-window -h
tmux send-keys "ry yardmaster" Enter

# Panes 2+: Engines (based on config)
for i in $(seq 1 $LOCAL_ENGINES); do
    tmux split-window -v
    tmux send-keys "ry engine --local" Enter
done

tmux attach -t railyard
```

### Production Mode (Multi-VM)

Shared infrastructure hosts multiple railyard instances. Each employee's Dispatch runs locally (or on a management VM). Engines run on cloud VMs inside a VPC.

```
┌─ Alice's Machine ──────────┐  ┌─ Bob's Machine ──────────────┐
│  ry start --mode production │  │  ry start --mode production  │
│  SSH tunnel to VPC bastion  │  │  SSH tunnel to VPC bastion   │
└──────────────┬──────────────┘  └──────────────┬───────────────┘
               │ SSH / WireGuard / Tailscale     │
┌──────────────▼─────────────────────────────────▼──────────────┐
│  VPC (private subnet, no public IPs)                          │
│                                                                │
│  ┌──────────────────────┐  ┌────────────────────────────┐     │
│  │ Dolt Server          │  │ Git Server (shared repo)   │     │
│  │ (dedicated VM)       │  │ (Gitea / GitHub / bare)    │     │
│  │ :3306 internal       │  │ :22 internal               │     │
│  │                      │  │                            │     │
│  │ DBs:                 │  │ Branches:                  │     │
│  │  railyard_alice      │  │  ry/alice/backend/car-001   │     │
│  │  railyard_bob        │  │  ry/bob/backend/car-050     │     │
│  │  railyard_carol      │  │  ry/carol/frontend/car-030  │     │
│  │  railyard_shared     │  │                            │     │
│  └──────────────────────┘  └────────────────────────────┘     │
│                                                                │
│  ┌──────────────────────┐  ┌────────────────────────────┐     │
│  │ Postgres + pgvector  │  │ Roundhouse (GPU box)       │     │
│  │ :5432 internal       │  │ CocoIndex indexer          │     │
│  │ (per-track indexes)  │  │ Embedding service :9090    │     │
│  └──────────────────────┘  └────────────────────────────┘     │
│                                                                │
│  ┌──────────────────────┐  ┌────────────────────────────┐     │
│  │ Bastion / Jump       │  │ cocoindex-mcp :8080        │     │
│  │ (SSH gateway)        │  │ (Roundhouse query server)  │     │
│  │ :22 external         │  │                            │     │
│  └──────────────────────┘  └────────────────────────────┘     │
│                                                                │
│  Alice's engines:           Bob's engines:                     │
│  ┌─────────┐ ┌─────────┐  ┌─────────┐ ┌─────────┐           │
│  │ VM-01   │ │ VM-02   │  │ VM-03   │ │ VM-04   │           │
│  │ Engine  │ │ Engine  │  │ Engine  │ │ Engine  │           │
│  │ alice:car│ │ alice:fe│  │ bob:car  │ │ bob:inf │           │
│  └─────────┘ └─────────┘  └─────────┘ └─────────┘           │
│                                                                │
└────────────────────────────────────────────────────────────────┘
```

**Key difference from local:** All employees share infrastructure (Dolt, Postgres, GPU box) but each has their own database, branches, and engine fleet. Engines are tagged with `{owner}:{track}` and can only access their owner's Dolt database.

---

## VM Provisioning & Lifecycle

### Provisioner (Part of Dispatch)

Dispatch manages VM lifecycle. Could target any cloud provider (Terraform, Pulumi, or direct API).

```yaml
# config.yaml — per-employee railyard configuration
owner: alice                    # unique owner ID, used for DB name + branch prefix

repo: git@github.com:org/myapp.git   # shared repo (same for all employees)
branch_prefix: ry/alice              # all branches: ry/alice/{track}/{car_id}

dolt:
  host: 127.0.0.1              # local dev; production: dolt-server.vpc.internal
  port: 3306
  database: railyard_alice     # auto-derived from owner

provisioner:
  provider: aws            # aws, gcp, hetzner, etc.
  region: us-east-1
  instance_type: c6i.xlarge
  ami: ami-xxxxx           # pre-baked with Claude Code, git, dolt client
  vpc_id: vpc-xxxxx
  subnet_id: subnet-xxxxx
  security_group: sg-xxxxx # allows only VPC-internal + SSH from bastion
  ssh_key: railyard-key
  
  scaling:
    min_engines: 2
    max_engines: 20
    scale_up_threshold: 5    # ready cars per engine
    scale_down_idle_minutes: 15

tracks:
  - name: backend
    language: go
    file_patterns: ["cmd/**", "internal/**", "pkg/**", "*.go"]
    engine_slots: 5
    conventions:
      go_version: "1.22"
      style: "stdlib-first, no frameworks"
      test_framework: "stdlib table-driven"
      forbidden: ["python", "node", "CGO unless approved"]
      
  - name: frontend
    language: typescript
    file_patterns: ["src/**", "*.ts", "*.tsx", "*.css"]
    engine_slots: 3
    conventions:
      framework: "Next.js 15"
      styling: "Tailwind CSS, no CSS modules"
      state: "Zustand, no Redux"
      forbidden: ["jQuery", "styled-components", "MUI"]

  - name: infra
    language: mixed
    file_patterns: ["terraform/**", "docker/**", ".github/**", "Makefile"]
    engine_slots: 2
```

### VM Lifecycle States

```
provisioning → ready → claimed → working → draining → terminated
                 ↑                    │
                 └────── idle ────────┘  (reassignable)
```

**Pre-baked AMI / image contains:**
- Claude Code CLI (or whatever agent runtime)
- Git, configured with deploy keys
- Dolt client (MySQL compatible, just needs mysql CLI)
- Engine daemon script
- SSH server (key-only auth, VPC-internal)
- Logging agent (ships to Dolt or Kafka)
- Railyard CLI (`ry`)

**Spin-up flow:**
```
1. Dispatch detects: ready_cars / active_engines > threshold
2. Provisions new VM via cloud API
3. Waits for SSH availability
4. Assigns track based on which track has most queued ready cars
5. Seeds VM with config: 
   - Clones repo, checks out branch prefix (ry/{owner}/)
   - Writes track-specific AGENTS.md
   - Configures Dolt connection (owner's database only)
   - Starts engine daemon
6. Updates engines table
```

**Spin-down flow:**
```
1. Dispatch detects: engine idle > 15 minutes, no ready cars for its track
2. Sets VM status = 'draining'
3. Waits for current car to complete (or timeout)
4. Engine daemon exits cleanly
5. Terminates VM via cloud API
6. Updates railyard.vms table
```

---

## SSH Tunnels for Human Intervention

When an engine is stuck (stalled status, repeated /clear cycles, Yardmaster can't resolve), a human needs to jump in.

### Tunnel Setup

```bash
# From your machine, tunnel through bastion to a specific engine VM
# Dispatch provides this command when you ask to attach to a stuck engine

ssh -J bastion.vpc.internal engine-vm-07.vpc.internal \
    -L 3307:dolt-server.vpc.internal:3306 \
    -L 2222:localhost:22

# Now you have:
#   localhost:3307 → Dolt server (query railyard state)
#   localhost:2222 → Engine VM SSH (attach to tmux)
```

### Railyard CLI Commands

```bash
# List stuck engines
ry engine list --status stalled

# Get tunnel command for a specific engine
ry engine attach vm-07
# Outputs: ssh -J bastion... (copy/paste)
# Also prints: tmux attach -t engine (run after SSH)

# Force-reassign a car from a stuck engine
ry car reassign car-a1b2c --from vm-07 --reason "stuck on test failure"

# Drain a VM (finish current work, then idle)
ry vm drain vm-07

# Kill an engine session and restart fresh
ry engine restart vm-07
```

### What the Human Sees When Attached

```
┌─ tmux: engine @ vm-07 ──────────────────────────┐
│                                                   │
│  Claude Code session                              │
│  Car: car-a1b2c "Add /users endpoint"      │
│  Track: backend-api                                 │
│  Cycle: 3 (2 previous /clear cycles)             │
│                                                   │
│  [agent output / conversation visible]            │
│                                                   │
│  You can:                                         │
│  - Type directly to the agent                     │
│  - /clear and provide new instructions            │
│  - Ctrl-C to kill, daemon will restart            │
│  - Exit SSH when done, daemon continues           │
│                                                   │
└───────────────────────────────────────────────────┘
```

---

## Complete I/O Logging

Every token in, every token out. Essential for debugging, cost tracking, and improving prompts.

### Logging Architecture

```
Agent Session
    │
    ├─ stdout/stderr captured by engine daemon
    │   └─ Piped to logging agent on VM
    │       └─ Writes to local buffer (SQLite or file)
    │           └─ Async ships to Dolt: agent_logs table
    │               └─ Or Kafka topic: orchestrator.system.logs
    │
    ├─ API calls intercepted by proxy (if using API directly)
    │   └─ Request/response logged with latency, tokens, model
    │
    └─ Tool use / MCP calls logged separately
        └─ What tools were called, with what args, what returned
```

### Log Levels / Modes

```yaml
logging:
  # dev: everything, verbose, to local terminal too
  # staging: everything, to Dolt only
  # prod: everything, to Dolt, with redaction of secrets
  mode: prod
  
  capture:
    agent_input: true        # full prompts sent to model
    agent_output: true       # full responses from model
    tool_calls: true         # MCP/tool invocations
    file_changes: true       # git diffs per commit
    db_queries: true         # what SQL the engine ran against Dolt
    system_events: true      # /clear, session start/stop, errors
    
  retention:
    hot_days: 7              # in Dolt, full detail
    warm_days: 30            # compressed, in S3/object store
    cold_days: 365           # summaries only
    
  redaction:
    patterns:                # regex patterns to redact in prod
      - 'sk-[a-zA-Z0-9]{20,}'    # API keys
      - 'ghp_[a-zA-Z0-9]{36}'    # GitHub tokens
```

### What Gets Logged (agent_logs table)

Each row captures one interaction cycle:

```json
{
  "engine_id": "vm-07-engine",
  "session_id": "sess-a8f3c",
  "car_id": "car-a1b2c",
  "direction": "in",
  "content": "[full system prompt + user message sent to Claude]",
  "token_count": 4200,
  "model": "claude-sonnet-4-20250514",
  "latency_ms": 3200,
  "tool_calls": [
    {"tool": "bash", "args": "go test ./...", "result_summary": "FAIL: 2 tests"},
    {"tool": "edit", "file": "handlers/users.go", "lines_changed": 15}
  ],
  "created_at": "2026-02-14T10:23:45Z"
}
```

### Debugging with Dolt Time-Travel

Because it's Dolt, you can replay exactly what happened:

```sql
-- What was the car state when the engine claimed it?
SELECT * FROM cars AS OF 'HASHOF(commit-when-claimed)' WHERE id = 'car-a1b2c';

-- What messages did the engine receive during this session?
SELECT * FROM messages 
WHERE to_agent = 'vm-07-engine' 
  AND created_at BETWEEN '2026-02-14 10:00:00' AND '2026-02-14 11:00:00';

-- Diff car state between engine claiming and completing
SELECT * FROM dolt_diff_cars 
WHERE to_commit = @done_commit 
  AND from_commit = @claim_commit 
  AND to_id = 'car-a1b2c';

-- Full session replay: every log entry in order
SELECT direction, LEFT(content, 200) as preview, token_count, latency_ms, tool_calls
FROM agent_logs 
WHERE session_id = 'sess-a8f3c' 
ORDER BY created_at;
```

---

## Engine Daemon — The Core Loop

This runs on every engine VM. It's not an AI agent — it's a bash/Go/Python script that manages the agent lifecycle.

```
┌─────────────────────────────────────────────────┐
│              Engine Daemon                       │
│                                                  │
│  ┌──────────┐    ┌───────────────┐              │
│  │ Heartbeat│    │ Log Shipper   │              │
│  │ (10s)    │    │ (async batch) │              │
│  └──────────┘    └───────────────┘              │
│                                                  │
│  Main Loop:                                      │
│  ┌────────────────────────────────────────────┐ │
│  │ 1. Poll Dolt for ready car (track-scoped)   │ │
│  │ 2. Claim car (atomic transaction)       │ │
│  │ 3. Check for messages from Yardmaster      │ │
│  │ 4. Render context payload:                 │ │
│  │    - Track AGENTS.md (from config)         │ │
│  │    - Car details (title, desc, design)     │ │
│  │    - Progress log (if resuming)            │ │
│  │    - Recent commits on branch              │ │
│  │    - Yardmaster messages                   │ │
│  │ 5. Start Claude Code with context          │ │
│  │ 6. Monitor: capture I/O, detect stall      │ │
│  │ 7. On exit:                                │ │
│  │    a. Check if car marked done in DB      │ │
│  │    b. If done: push branch, next car      │ │
│  │    c. If /clear mid-task: ensure progress  │ │
│  │       note was written, restart loop at 1  │ │
│  │    d. If crash/stall: mark stalled in DB,  │ │
│  │       send message to supervisor           │ │
│  │ 8. Sleep 5s, back to 1                     │ │
│  └────────────────────────────────────────────┘ │
│                                                  │
│  Stall Detection:                                │
│  - No stdout for 120s                            │
│  - Token budget exceeded (configurable)          │
│  - Same error repeated 3x                        │
│  - /clear cycle count > threshold                │
│                                                  │
└──────────────────────────────────────────────────┘
```

---

## Context Injection Template

After every /clear (or fresh session start), this is what gets rendered and fed to the agent:

```markdown
# You are an engine on track: {track.name}
# Railyard owner: {config.owner}
# Branch prefix: ry/{config.owner}/{track.name}/

## Project Conventions
{track.system_prompt}

Language: {track.language}
{track.conventions as bullet points}

IMPORTANT: You ONLY work on this project. Do not use patterns, languages,
or frameworks from other projects. Follow the conventions above exactly.

## Your Current Car
Car: {car.id}
Title: {car.title}
Priority: {car.priority}
Branch: {car.branch}

### Description
{car.description}

### Design Notes
{car.design_notes}

### Acceptance Criteria
{car.acceptance}

## Previous Progress (if resuming)
{car_progress entries, most recent first}

## Yardmaster Messages
{unacknowledged messages for this engine}

## Recent Commits on Your Branch
{last 5 git log --oneline entries}

## When You're Done
1. Run tests, ensure they pass
2. Update car status: call ry.complete(car_id, "summary of what was done")
3. The daemon will handle git push and /clear

## If You're Stuck
1. Update progress: call ry.progress(car_id, "what you tried, what failed")
2. Send message: call ry.message("yardmaster", "need help with X")
3. The Yardmaster will receive your message and may provide guidance

## If You Need to Split Work
1. Create child cars: call ry.create_car(parent=car_id, title="sub-task")
2. Continue on the current car, children will be picked up by other engines
```

### Railyard API (MCP Server or CLI Wrapper)

The agent needs a way to interact with Dolt. Easiest: a small MCP server or CLI tool (`ry`) that wraps SQL calls.

```
ry claim(track)              → claims next ready car
ry complete(car_id, note)   → marks done, writes progress
ry progress(car_id, note)   → writes progress (for mid-task /clear)
ry message(to, body)         → sends message
ry inbox()                   → reads unacknowledged messages
ry create_car(...)          → creates child car (discovered work)
ry status(car_id, status)   → updates car status
```

---

## CocoIndex — Semantic Code Search

### What CocoIndex Does

CocoIndex uses Tree-sitter to parse code into syntax-aware chunks, then embeds those chunks using models like `sentence-transformers/all-MiniLM-L6-v2` or language-specific models like `microsoft/graphcodebert-base`. Embeddings are stored in Postgres+pgvector for fast cosine similarity search. It supports incremental indexing — only re-processes changed files.

Engines use CocoIndex via the `cocoindex-mcp` server (the **Roundhouse**) to find relevant code without needing to have seen it in a previous session. This is critical because after every `/clear`, the agent has zero memory of the codebase. The Roundhouse is how it re-orients fast.

### Two Resource Profiles

**Indexing (heavy):**
- Parses entire codebase with Tree-sitter
- Generates embeddings for every code chunk
- GPU accelerates embedding generation significantly (10-50x over CPU for large repos)
- Runs after merges or on a schedule
- Not latency-sensitive — can take minutes

**Query (light):**
- Embeds a single search query (one model forward pass)
- Runs pgvector cosine similarity search
- Must be fast (<500ms) — engines call this constantly
- Light on compute, heavy on Postgres I/O

### Architecture: Per-Track Isolation

Each track gets its own CocoIndex pipeline and pgvector table. Engines can only query their track's index. This prevents a Go backend engine from finding Python frontend code in search results.

```
Postgres + pgvector instance
├── cocoindex_backend_api      — embeddings for backend-api track
│   └── code_embeddings table  — (filename, location, code, embedding)
├── cocoindex_frontend         — embeddings for frontend track  
│   └── code_embeddings table
└── cocoindex_infra            — embeddings for infra track
    └── code_embeddings table
```

Each track's CocoIndex flow definition specifies:
- Source path (the track's repo checkout)
- File patterns to include (e.g., `*.go` for backend, `*.ts *.tsx` for frontend)
- Embedding model (can be language-specific per track)
- Target pgvector table

```python
# Per-track flow definition example
@cocoindex.flow_def(name=f"CodeEmbedding_{track_name}")
def code_embedding_flow(flow_builder, data_scope):
    data_scope["files"] = flow_builder.add_source(
        cocoindex.sources.LocalFile(
            path=f"/repos/{track_name}",
            included_patterns=track_config["file_patterns"],
            excluded_patterns=[".*", "vendor", "node_modules", "dist"]
        ),
        # Incremental: only re-index changed files
        refresh_interval=None  # triggered manually after merge
    )
    
    code_embeddings = data_scope.add_collector()
    
    with data_scope["files"].row() as doc:
        doc["chunks"] = doc["content"].transform(
            cocoindex.functions.SplitRecursively(),
            language=track_config["treesitter_language"],
            chunk_size=1500,
            chunk_overlap=300
        )
        with doc["chunks"].row() as chunk:
            chunk["embedding"] = code_to_embedding.transform(chunk["code"])
            code_embeddings.collect(
                filename=doc["filename"],
                location=chunk["location"],
                code=chunk["code"],
                embedding=chunk["embedding"]
            )
    
    code_embeddings.export(
        f"code_embeddings_{track_name}",
        cocoindex.storages.Postgres(),
        primary_key_fields=["filename", "location"],
        vector_indexes=[cocoindex.VectorIndex(
            "embedding", 
            cocoindex.VectorSimilarityMetric.COSINE_SIMILARITY
        )]
    )
```

### Deployment Options

#### Option A: Local / Single Machine

Everything on one box. CPU-only embedding is fine for small repos (<50k LOC). The Roundhouse runs in-process.

```
┌─ Your Machine ──────────────────────────────────┐
│                                                  │
│  Postgres + pgvector    (:5432)                  │
│  CocoIndex server       (indexing + query)       │
│  cocoindex-mcp server   (:8080, MCP protocol)   │
│  Dolt server            (:3306)                  │
│  Engines (tmux)                                  │
│                                                  │
└──────────────────────────────────────────────────┘
```

#### Option B: Dedicated GPU Box (Recommended for Production)

Split indexing and query. The GPU box handles the heavy embedding work. Query can run on a lighter machine since it's just one embedding + a pgvector query.

```
┌─ VPC ────────────────────────────────────────────────────────┐
│                                                               │
│  ┌─────────────────────┐   ┌──────────────────────────────┐  │
│  │ GPU Box             │   │ Services VM                  │  │
│  │ (e.g., g5.xlarge)   │   │                              │  │
│  │                     │   │  Postgres + pgvector (:5432)  │  │
│  │  CocoIndex Indexer  │──▶│  Dolt server        (:3306)  │  │
│  │  (writes embeddings │   │  cocoindex-mcp      (:8080)  │  │
│  │   to Postgres)      │   │  Git server         (:22)    │  │
│  │                     │   │                              │  │
│  │  Models cached:     │   └──────────────┬───────────────┘  │
│  │  - all-MiniLM-L6-v2 │                  │                  │
│  │  - graphcodebert     │                  │                  │
│  │  - unixcoder-base    │    ┌─────────────▼──────────────┐  │
│  │                     │    │ Engine VMs query             │  │
│  └─────────────────────┘    │ cocoindex-mcp for code search│  │
│                              └─────────────────────────────┘  │
│                                                               │
└───────────────────────────────────────────────────────────────┘
```

**Why separate the GPU box:**
- GPU instances are expensive ($0.50-2/hr). You only need it during indexing.
- You can spin it up for re-indexing, then shut it down.
- Or keep it running if you have continuous merges.
- Query-time embedding of a single search string is fast enough on CPU.

**Alternative: GPU box does both indexing AND query-time embedding:**
If you want maximum query quality with larger embedding models, the GPU box can also serve query-time embedding. The cocoindex-mcp server would proxy embedding requests to the GPU box:

```
Engine → cocoindex-mcp → (embed query on GPU box) → pgvector search → results
```

This adds ~50ms of network latency but lets you use heavier models like `microsoft/graphcodebert-base` (125M params) for query embedding without bogging down the services VM.

#### Option C: Hybrid (Best of Both)

GPU box runs as an **embedding service** via a simple HTTP API. Both the indexer and the MCP query server call it for embeddings. Postgres+pgvector lives on the services VM.

```
┌─ GPU Box ──────────────────┐
│                            │
│  Embedding Service (:9090) │
│  POST /embed               │
│  { "text": "...",          │
│    "model": "graphcodebert"│ 
│  }                         │
│  → [0.12, -0.34, ...]     │
│                            │
│  Models loaded in GPU VRAM │
│  Hot-swappable per request │
│                            │
│  Can scale down when idle  │
│  (spot instance friendly)  │
│                            │
└────────────────────────────┘
       ▲              ▲
       │              │
  ┌────┴────┐   ┌─────┴──────────────┐
  │ Indexer  │   │ cocoindex-mcp      │
  │ (batch   │   │ (query-time embed  │
  │  embed)  │   │  + pgvector search)│
  └─────────┘   └────────────────────┘
```

This is the most flexible. The embedding service is stateless and can be:
- A single GPU box in your VPC
- A spot/preemptible instance that spins up on demand
- Your local machine with an NVIDIA card (for dev mode)
- Multiple boxes behind a load balancer (if you need throughput)

### Re-Indexing Triggers

The Yardmaster triggers Roundhouse re-indexing. Add to the supervisor's responsibilities:

```
Yardmaster switch flow:
  1. Engine completes car, pushes branch
  2. Yardmaster pulls branch, runs tests
  3. If tests pass: switch to main (merge)
  4. After switch: trigger Roundhouse re-index for that track
  5. Incremental — only changed files get re-embedded
```

Implementation:
```sql
-- Yardmaster inserts a re-index job after switch
INSERT INTO reindex_jobs (track, trigger_commit, status, created_at)
VALUES ('backend', 'abc1234', 'pending', NOW());
```

The Roundhouse daemon (on the GPU box or services VM) polls for pending jobs:
```sql
SELECT * FROM reindex_jobs 
WHERE status = 'pending' 
ORDER BY created_at 
LIMIT 1 FOR UPDATE SKIP LOCKED;
```

Claims it, runs the CocoIndex flow for that track, marks done. Engines see updated search results on their next query.

### Roundhouse MCP Server Design

The `cocoindex-mcp` server (Roundhouse) is the bridge between engines and the index. It's an MCP server that each engine connects to.

**Tools exposed to agents:**

```
code_search(query, top_k=10)
  → Returns: [{filename, code_snippet, score, location}]
  → Scoped to engine's track automatically (track determined by engine auth/config)

code_search_by_file(filename_pattern, query, top_k=5)
  → Search within specific files matching a glob pattern
  → Useful: "find authentication logic in handlers/*.go"

index_status()
  → Returns: last indexed commit, total chunks, staleness
  → Engine can decide if index is fresh enough

related_code(filename, function_name, top_k=5)
  → Find code semantically similar to a specific function
  → Useful for understanding dependencies before making changes
```

**Track scoping:**
The Roundhouse knows which track the requesting engine belongs to (passed in connection config or auth token). All queries are automatically scoped to that track's pgvector table. A backend engine searching for "authentication" finds Go auth handlers, not React login components.

**Connection config per engine:**
```json
{
  "mcpServers": {
    "cocoindex": {
      "url": "http://cocoindex-mcp.vpc.internal:8080",
      "headers": {
        "X-Owner": "alice",
        "X-Track": "backend",
        "X-Engine-Id": "vm-07-engine"
      }
    }
  }
}
```

### Model Selection Per Track

Different languages benefit from different embedding models. Configure in track config:

```yaml
tracks:
  - name: backend
    language: go
    roundhouse:
      embedding_model: "microsoft/unixcoder-base"  # good for Go
      file_patterns: ["*.go"]
      chunk_size: 1500
      chunk_overlap: 300
      
  - name: frontend
    language: typescript
    roundhouse:
      embedding_model: "microsoft/graphcodebert-base"  # good for TS/JS
      file_patterns: ["*.ts", "*.tsx", "*.css"]
      chunk_size: 2000
      chunk_overlap: 500
      
  - name: infra
    language: mixed
    roundhouse:
      embedding_model: "sentence-transformers/all-MiniLM-L6-v2"  # general purpose
      file_patterns: ["*.tf", "*.yaml", "*.yml", "*.sh", "*.py"]
      chunk_size: 1000
      chunk_overlap: 200
```

### Cost Optimization

GPU time is the main cost driver. Strategies:

1. **Spot/preemptible instances** for the GPU box — indexing is interruptible and restartable
2. **Incremental indexing** — CocoIndex only re-embeds changed files, so a merge touching 5 files doesn't re-index the whole repo
3. **Schedule-based GPU** — spin up GPU box, run all pending reindex jobs, shut down. Works if you don't need real-time index freshness.
4. **CPU fallback for small models** — `all-MiniLM-L6-v2` is only 22M params and runs fine on CPU. Use GPU only for the bigger language-specific models.
5. **Query embedding on CPU** — single query embedding is fast even on CPU (~10ms for MiniLM). Only batch indexing needs GPU.

### Branch-Overlay Index — Semantic Search for Engine Branches

#### Motivation

Railyard's primary value proposition over alternative approaches is **lower token usage**. The biggest token burn in multi-agent workflows is codebase exploration — agents calling Glob, Grep, and Read repeatedly, hitting dead ends, and re-reading files after every `/clear` cycle. CocoIndex integration via the Roundhouse already provides semantic search over the `main` branch, but engines work on feature branches that diverge from `main`. Without branch awareness, an engine modifying `internal/auth/handler.go` would find the **old** version of that file in search results, leading to confusion and wasted tokens.

The branch-overlay index solves this with a two-tier architecture: a shared main index for the bulk of the codebase, plus a small per-engine overlay index covering only the files that differ on the engine's branch.

**Estimated token savings:**

| Scenario | Exploration tokens/cycle | Savings |
|---|---|---|
| Without CocoIndex | 10,000–60,000 | — |
| Main index only | 3,000–10,000 | ~70% |
| Main + overlay | 3,000–10,000 + own branch code | 70%+ |

Over 2–5 `/clear` cycles per car: **10,000–200,000 tokens saved per car**.

#### Two-Tier Architecture

```
Engine claims car
    |
    v
CreateBranch(workDir, branch)           -- existing (cmd/ry/engine.go)
    |
    v
BuildOverlayIndex(workDir, engineID)    -- NEW: git diff main...HEAD -> parse -> embed -> pgvector
    |
    v
WriteMCPConfig(workDir, engineID)       -- NEW: .mcp.json with engine-specific env vars
    |
    v
SpawnAgent(ctx, db, opts)               -- existing; Claude Code finds .mcp.json in worktree
    |
    v
Agent calls search_code("auth handler")
    |
    +---> Query main table (main_backend_embeddings)     ~100ms
    +---> Query overlay table (ovl_eng_a1b2c3d4)         ~15ms
    |
    v
Merge results (overlay wins on filename+location conflict)
    |
    v
Return top_k results to agent
```

- **Main index**: Shared, authoritative index of the `main` branch. One pgvector table per track (e.g., `main_backend_embeddings`, `main_frontend_embeddings`). Rebuilt incrementally after each switch (merge) via `CreateReindexJob`. All engines on the same track share the same main index.
- **Overlay index**: Small, ephemeral, per-engine index of ONLY the files that differ between the engine's branch and `main`. Created at engine startup (after `CreateBranch`), naturally refreshed each `/clear` cycle (the engine daemon loop restarts), and deleted after switch (merge) or engine deregistration.

#### Per-Track Main Indexes

Each track gets its own main index table in pgvector, scoped by the track's `file_patterns` from `railyard.yaml`. This replaces the current single-table approach with per-track isolation.

The CocoIndex flow in `cocoindex/main.py` accepts `--track` and `--file-patterns` CLI args to create tables named `main_{track}_embeddings`. The `code_to_embedding` transform must remain importable so that `overlay.py` can reuse it for vector space consistency (both indexes must use the same embedding model for cosine similarity scores to be directly comparable).

A build script iterates all tracks defined in `railyard.yaml`, maps each track's `file_patterns` to CocoIndex included patterns, and runs the flow. Output: one pgvector table per track with IVFFlat index.

#### Overlay Indexer — `cocoindex/overlay.py`

A Python script invoked as a one-shot subprocess by the Go engine daemon. Stateless — no long-running process, no state between invocations. Reuses `code_to_embedding` from `main.py` for vector space consistency.

**Subcommands:**
- `build --engine-id X --worktree /path --track backend` — index changed files
- `cleanup --engine-id X` — drop overlay table + metadata
- `status --engine-id X` — print overlay freshness info as JSON

**Build algorithm:**
1. `git diff --name-only main...HEAD` to get changed files
2. `git diff --name-only --diff-filter=D main...HEAD` to get deleted files
3. Filter changed files by the track's `file_patterns` (from config)
4. Parse and chunk with Tree-sitter, embed with SentenceTransformer (same model as main index)
5. `CREATE TABLE IF NOT EXISTS ovl_{engine_id}` with `vector(384)` column
6. Truncate and insert embeddings (full rebuild of the small overlay each time)
7. Upsert `overlay_meta` row with file counts, last commit, and deleted files list

**Expected runtime:** 5–15 seconds for 5–20 files on CPU. The overlay is intentionally small — it only contains files that differ from `main`, not the full codebase.

**Cleanup:** `DROP TABLE IF EXISTS ovl_{engine_id}`, `DELETE FROM overlay_meta WHERE engine_id = X`. Both are idempotent and non-fatal on missing data.

#### MCP Server Changes — `cocoindex/mcp_server.py`

The MCP server accepts engine identity via environment variables:
- `COCOINDEX_ENGINE_ID` — engine ID (e.g., `eng-a1b2c3d4`)
- `COCOINDEX_MAIN_TABLE` — main index table name (e.g., `main_backend_embeddings`)
- `COCOINDEX_OVERLAY_TABLE` — overlay table name (e.g., `ovl_eng_a1b2c3d4`; empty if none)
- `COCOINDEX_TRACK` — track name
- `COCOINDEX_WORKTREE` — worktree path

When env vars are absent, the server falls back to current single-table behavior (backward compatible for human interactive use with `.mcp.json`).

**Modified `search_code()`:** Queries both the main table and overlay table (in parallel for latency), then merges results with overlay-wins-on-conflict deduplication (see merge algorithm below).

**New MCP tools:**
- `overlay_status()` — returns `{engine_id, track, branch, last_commit, files_indexed, chunks_indexed, is_stale}` by querying `overlay_meta`
- `refresh_overlay()` — calls `overlay.py build` as subprocess and returns `{files_indexed, chunks_indexed, duration_ms}`. Rate-limited to max once per 30 seconds to prevent excessive rebuilds.

#### Search Merge Algorithm

```
merge_results(main_results, overlay_results):
  1. Index overlay results by (filename, location) — these take precedence
  2. Load deleted_files from overlay_meta
  3. Add all overlay results to the merged set
  4. For each main result:
     - Skip if (filename, location) already in merged set (overlay wins)
     - Skip if filename is in deleted_files list
     - Otherwise add to merged set
  5. Sort merged set by cosine similarity score descending
  6. Filter by min_score, return top_k
```

Both tables use the identical embedding model (same `code_to_embedding` transform), so cosine similarity scores are directly comparable across main and overlay results.

#### Per-Engine MCP Config — `internal/engine/overlay.go`

New Go file with three functions:

- `BuildOverlay(workDir, engineID, track string, cfg *config.Config) error` — shells out to `overlay.py build` with timeout from `cfg.CocoIndex.Overlay.BuildTimeoutSec`
- `CleanupOverlay(engineID string) error` — shells out to `overlay.py cleanup`
- `WriteMCPConfig(workDir, engineID, track string, cfg *config.Config) error` — writes `.mcp.json` into the engine's worktree

The `.mcp.json` written to each worktree:
```json
{
  "mcpServers": {
    "railyard_cocoindex": {
      "command": "<venv>/bin/python",
      "args": ["<scripts>/mcp_server.py"],
      "env": {
        "COCOINDEX_DATABASE_URL": "postgresql://...",
        "COCOINDEX_ENGINE_ID": "eng-a1b2c3d4",
        "COCOINDEX_MAIN_TABLE": "main_backend_embeddings",
        "COCOINDEX_OVERLAY_TABLE": "ovl_eng_a1b2c3d4",
        "COCOINDEX_TRACK": "backend",
        "COCOINDEX_WORKTREE": "/path/to/engines/eng-a1b2c3d4"
      }
    }
  }
}
```

Where `<venv>` and `<scripts>` are resolved from `cfg.CocoIndex.VenvPath` and `cfg.CocoIndex.ScriptsPath`.

#### pgvector Schema

**Per-track main index tables** (one per track, created by CocoIndex flow):
```sql
CREATE TABLE main_backend_embeddings (
    filename    TEXT NOT NULL,
    location    TEXT,
    code        TEXT NOT NULL,
    embedding   vector(384),
    PRIMARY KEY (filename, location)
);
CREATE INDEX ON main_backend_embeddings
    USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

-- Similarly: main_frontend_embeddings, main_infra_embeddings, etc.
```

**Overlay table** (per engine, created by `overlay.py build`):
```sql
CREATE TABLE ovl_eng_a1b2c3d4 (
    filename    TEXT NOT NULL,
    location    TEXT,
    code        TEXT NOT NULL,
    embedding   vector(384),
    PRIMARY KEY (filename, location)
);
CREATE INDEX ON ovl_eng_a1b2c3d4
    USING ivfflat (embedding vector_cosine_ops) WITH (lists = 10);
```

Overlay tables use `lists = 10` (vs 100 for main) because they're small — typically tens to hundreds of rows.

**Overlay metadata** (shared, one row per engine):
```sql
CREATE TABLE overlay_meta (
    engine_id       TEXT PRIMARY KEY,
    track           TEXT NOT NULL,
    branch          TEXT NOT NULL,
    last_commit     TEXT,
    files_indexed   INTEGER DEFAULT 0,
    chunks_indexed  INTEGER DEFAULT 0,
    deleted_files   TEXT DEFAULT '[]',   -- JSON array of filenames deleted on branch
    created_at      TIMESTAMP DEFAULT NOW(),
    updated_at      TIMESTAMP DEFAULT NOW()
);
```

#### Engine Model Change

Add `OverlayTable` field to `internal/models/engine.go`:
```go
OverlayTable string `gorm:"size:128"` // pgvector overlay table name (e.g., ovl_eng_a1b2c3d4)
```

Set when `BuildOverlay` succeeds, cleared when `CleanupOverlay` runs. Enables Yardmaster to find and clean up overlay tables for dead engines.

#### Engine Lifecycle Integration Points

**Engine daemon loop** (`cmd/ry/engine.go` — after `CreateBranch`, before `SpawnAgent`):
```go
if cfg.CocoIndex.Overlay.Enabled {
    if err := overlay.Build(workDir, eng.ID, track, cfg); err != nil {
        log.Printf("overlay build warning: %v", err)  // non-fatal
    }
    if err := overlay.WriteMCPConfig(workDir, eng.ID, track, cfg); err != nil {
        log.Printf("mcp config warning: %v", err)  // non-fatal
    }
}
```

On `/clear` cycle, the overlay is naturally refreshed when the engine daemon loop restarts and calls `BuildOverlay` again.

**Yardmaster switch flow** (`internal/yardmaster/switch.go` / `daemon.go` — after `CreateReindexJob`):
```go
if car.Assignee != "" {
    overlay.Cleanup(car.Assignee)  // non-fatal; drop the completing engine's overlay
}
```

**Engine deregistration** (`internal/engine/engine.go` — in `Deregister()`):
```go
overlay.Cleanup(engineID)  // non-fatal
```

**Stale engine handling** (`internal/yardmaster/daemon.go` — in `handleStaleEngines()`):
Clean up dead engine's overlay before restart.

#### Error Handling

All overlay operations are **non-fatal**. If overlay build fails, the engine works with the main index only — no degradation of core functionality, just slightly less accurate search results for branch-modified files. If cleanup fails, `ry overlay gc` handles eventual cleanup of orphaned tables.

#### Configuration — `railyard.yaml` Additions

```yaml
cocoindex:
  database_url: "postgresql://cocoindex:cocoindex@localhost:5481/cocoindex"
  venv_path: "cocoindex/.venv"
  scripts_path: "cocoindex"
  overlay:
    enabled: true           # master switch for overlay indexing
    max_chunks: 5000        # safety limit per overlay
    auto_refresh: true      # rebuild overlay on each /clear cycle
    build_timeout_sec: 60   # timeout for overlay.py build subprocess
```

Config loaded into `internal/config/config.go` as `CocoIndexConfig` struct with nested `OverlayConfig`. Defaults: `enabled=true`, `max_chunks=5000`, `auto_refresh=true`, `build_timeout_sec=60`.

#### CLI Commands — `cmd/ry/overlay.go`

```bash
ry overlay build --engine <id>     # Manual overlay build (runs overlay.py build)
ry overlay status [--engine <id>]  # Show overlay status (queries overlay_meta)
ry overlay cleanup --engine <id>   # Drop overlay table + metadata
ry overlay gc                      # Clean up orphaned overlays (cross-ref with engines table)
```

`ry overlay gc` cross-references `overlay_meta` with the engines table in Dolt. Any overlay whose `engine_id` doesn't correspond to an active engine gets cleaned up.

#### Implementation Phases

**Phase 1: Per-Track Main Indexes + Overlay Indexer Foundation**
- Modify `cocoindex/main.py` to accept track name and `file_patterns` as parameters
- Create per-track CocoIndex flow definitions (one pgvector table per track)
- Create `cocoindex/overlay.py` (build, cleanup, status) as subprocess model
- Create `overlay_meta` table in pgvector
- Create `internal/engine/overlay.go` (Go wrappers)
- Add `OverlayTable` to Engine model
- Integration test

**Phase 2: MCP Server Integration**
- Modify `mcp_server.py` for dual-table search (per-track main + overlay) with env vars
- Implement merge algorithm with overlay-wins dedup
- Add `overlay_status()` and `refresh_overlay()` tools
- Add `WriteMCPConfig()` to generate per-engine `.mcp.json`

**Phase 3: Engine Lifecycle Integration**
- Hook `BuildOverlay` + `WriteMCPConfig` into engine daemon loop
- Hook `CleanupOverlay` into Yardmaster switch flow
- Hook cleanup into deregistration and stale engine handling

**Phase 4: CLI and Observability**
- Create `cmd/ry/overlay.go` with build/status/cleanup/gc commands
- Add `cocoindex` section to config schema
- Add overlay info to `ry engine list` and `ry status`

---

## Yardmaster (Agent2)

The Yardmaster is also an AI agent, but with broader permissions and a different prompt. It runs alongside Dispatch (locally or on a management VM). Each railyard has its own Yardmaster — it only manages that owner's work.

**Responsibilities:**
1. Monitor engine health (heartbeats, stall detection)
2. Switch completed branches into main (merge per track, creates PR)
3. Run post-switch CI/tests
4. Trigger Roundhouse re-indexing after switch (insert reindex_jobs row)
5. Resolve cross-track dependency unblocking (within this railyard)
6. Reassign cars from dead/stalled engines
7. Escalate to human when stuck

**Yardmaster sees all tracks** (unlike engines which are track-scoped). Queries cars across all tracks within its owner's database.

---

## Dispatch (Agent1)

Dispatch is your interface. You tell it what you want built, it breaks it down into cars across tracks with proper dependency chains. All cars are created within your railyard's database with branches under your prefix.

**Example interaction:**
```
You: "Add user authentication. Backend needs JWT endpoints, 
      frontend needs login page and auth context."

Dispatch creates (in railyard_alice):
  track: backend
    car-001 [epic] "User Authentication Backend"
      car-002 [task] "POST /auth/login endpoint with JWT"
         branch: ry/alice/backend/car-002
      car-003 [task] "POST /auth/register endpoint"
         branch: ry/alice/backend/car-003
      car-004 [task] "JWT middleware for protected routes"
         branch: ry/alice/backend/car-004
      car-005 [task] "User model and database migration"
         branch: ry/alice/backend/car-005
      car-002 blocked_by car-005
      car-004 blocked_by car-002

  track: frontend
    car-f01 [epic] "User Authentication Frontend"
      car-f02 [task] "Login page with form and validation"
         branch: ry/alice/frontend/car-002
         blocked_by car-002 (cross-track, same railyard)
      car-f03 [task] "Auth context provider with JWT storage"
         branch: ry/alice/frontend/car-003
         blocked_by car-002 (cross-track, same railyard)
      car-f04 [task] "Protected route wrapper component"
         branch: ry/alice/frontend/car-004
```

---

## Local ↔ Production Parity

The same config.yaml, GORM models, and `ry` CLI work in both modes. The only differences:

| Aspect | Local | Production |
|--------|-------|-----------|
| Dolt server | localhost:3306 | dolt.vpc.internal:3306 |
| Dolt database | `railyard_alice` (single) | `railyard_alice`, `railyard_bob`, ... (shared server) |
| Postgres+pgvector | localhost:5432 | postgres.vpc.internal:5432 |
| Roundhouse indexer | CPU, in-process | GPU box (spot instance) |
| cocoindex-mcp | localhost:8080 | cocoindex-mcp.vpc.internal:8080 |
| Roundhouse embeddings | CPU local (MiniLM) | GPU box :9090 (GraphCodeBERT+) |
| Git repo | Local clone | Internal Gitea/GitHub |
| Engines | tmux panes | Separate VMs |
| Provisioner | No-op (manual tmux) | Cloud API |
| SSH tunnels | N/A (everything local) | Via bastion |
| Logging | Also to terminal | Dolt/Kafka only |
| Engine count | 1-3 | 2-20+ per railyard |

Environment variable `RAILYARD_MODE=local|production` switches behavior.

```bash
# Local
export RAILYARD_MODE=local
ry start  # starts dolt + postgres + tmux panes

# Production
export RAILYARD_MODE=production
ry start  # connects to shared Dolt, provisions engine VMs
```

---

## Phase 2: Cross-Railyard Coordination

Phase 1 (above) has each railyard fully independent. Multiple employees work on the same repo but don't see each other's work until PR merge. Phase 2 adds coordination.

### Shared Merge Queue

A `railyard_shared` database on the shared Dolt server tracks merge ordering:

```go
// Phase 2: shared across all railyards
type MergeRequest struct {
    ID          uint      `gorm:"primaryKey;autoIncrement"`
    Owner       string    `gorm:"size:64;not null"`       // alice, bob
    CarID      string    `gorm:"size:32;not null"`
    Track       string    `gorm:"size:64"`
    Branch      string    `gorm:"size:128;not null"`      // ry/alice/backend/car-002
    Status      string    `gorm:"size:16;default:pending"` // pending, testing, merged, conflict, rejected
    Priority    int       `gorm:"default:2"`
    FilesTouched string   `gorm:"type:json"`               // ["internal/auth/handler.go", ...]
    ConflictsWith *uint                                    // ID of conflicting MergeRequest
    CreatedAt   time.Time
    MergedAt    *time.Time
}
```

### File-Level Conflict Detection

Before an engine starts working on a car, check if another railyard's active cars touch the same files:

```go
// Check if any other railyard is touching the same files
func DetectConflicts(sharedDB *gorm.DB, owner, track string, files []string) ([]MergeRequest, error) {
    var conflicts []MergeRequest
    err := sharedDB.Where(
        "owner != ? AND status IN ? AND files_touched IS NOT NULL",
        owner, []string{"pending", "testing"},
    ).Find(&conflicts).Error
    
    // Compare files_touched JSON arrays for overlap
    // Return any that touch the same files
    return filterByFileOverlap(conflicts, files), err
}
```

When conflict detected, Yardmaster can:
1. **Warn** — "Bob is also working on `internal/auth/handler.go`, coordinate?"
2. **Block** — Automatically hold the car until the conflicting merge resolves
3. **Sequence** — Add to merge queue with ordering to minimize conflicts

### Cross-Railyard Dependencies

The `CarDepExternal` model enables Phase 2 dependencies:

```go
// Alice's car depends on Bob's car
dep := models.CarDepExternal{
    CarID:         "car-f02",           // alice's car
    BlockedByOwner: "bob",              // bob's railyard
    BlockedByID:    "car-050",           // bob's car
    DepType:        "blocks",
}
```

The Yardmaster polls the shared database to check if cross-railyard blockers are resolved. When Bob's `car-050` merges to main, Alice's `car-f02` becomes unblocked.

### Production Topology (Phase 2)

```
┌─ Shared Dolt Server ──────────────────┐
│                                        │
│  railyard_alice    (alice's cars)     │
│  railyard_bob      (bob's cars)      │
│  railyard_carol    (carol's cars)    │
│  railyard_shared   (merge queue,      │
│                     conflict tracking) │
│                                        │
└────────────────────────────────────────┘
        ▲          ▲           ▲
        │          │           │
  alice's ry   bob's ry   carol's ry
  (local or    (local or   (local or
   VM fleet)    VM fleet)   VM fleet)
```

Each Yardmaster periodically checks `railyard_shared` for:
- Merge queue ordering (whose PR goes first)
- File conflict warnings
- Cross-railyard dependency resolution
- Announcements (e.g., "main is broken, hold merges")
