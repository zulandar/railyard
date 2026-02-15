# Railyard — Phase 1 Implementation Plan

## Scope

**In scope:** Phase 1, local-first. Single developer, 1–100 engines, all three agent roles (Engine, Yardmaster, Dispatch). Go + GORM + Dolt. Direct DB messaging.

**Out of scope:** Phase 2 (cross-railyard), production deployment (VPC/VMs/bastion), CocoIndex/Roundhouse, Kafka, log shipping/redaction/retention, `BeadDepExternal` model.

## Approach

**Thinnest end-to-end first** — 7 sequential epics, each a runnable increment. Foundation → Bead CRUD → Engine Loop → Messaging → Yardmaster → Dispatch → Local Orchestration.

**Hierarchy:** Epic → Feature → Task. Each level has rich context in its description. All acceptance criteria require >90% test coverage.

## Assumptions

- Dolt is installed (prerequisite, not a bead)
- Claude Code is the primary engine agent runtime
- `bd` CLI (beads v0.50.2) is used for issue tracking
- `ry` CLI is the main user-facing Go binary
- Engines run in tmux panes locally
- Messaging uses direct DB polling (messages table, 5s interval)

---

## Epic 1: Foundation — Dolt Database & Project Scaffold

**Goal:** `ry db init` creates a working Dolt database with all tables seeded from config.

**Context:** This is the bedrock of the entire system. Every subsequent epic depends on the Go project structure, GORM models, config loading, and Dolt connectivity established here. The directory layout follows Go conventions: `cmd/ry/` for the CLI entrypoint, `internal/` for private packages. GORM models are defined per ARCHITECTURE.md Section 1.5. Config is YAML-based per Section "VM Provisioning & Lifecycle".

### Feature 1.1: Project Scaffold & Dependencies

**Description:** Initialize the Go module, establish the canonical directory structure, and add all core dependencies. This creates the skeleton that every subsequent feature and task builds on. Directory layout: `cmd/ry/main.go` (CLI entrypoint), `internal/models/` (GORM structs), `internal/db/` (connection + migration), `internal/config/` (YAML loading), `internal/bead/` (bead operations), `internal/engine/` (engine daemon), `internal/messaging/` (agent communication), `internal/yardmaster/` (supervisor), `internal/dispatch/` (planner).

#### Tasks

- **T1.1.1: Initialize Go module and directory structure**
  Create `go.mod` with `go mod init github.com/zulandar/railyard`. Create all directories under `cmd/` and `internal/`. Add `cmd/ry/main.go` with a minimal cobra root command that prints version. Verify `go build ./cmd/ry` produces a working binary.

- **T1.1.2: Add core dependencies**
  `go get` for: `gorm.io/gorm`, `gorm.io/driver/mysql` (Dolt is MySQL-compatible), `github.com/spf13/cobra` (CLI), `gopkg.in/yaml.v3` (config). Create `go.sum`. Verify all imports resolve.

### Feature 1.2: Configuration System

**Description:** Implement the YAML-based configuration loader that reads `config.yaml` and provides typed access to all settings. The config struct mirrors ARCHITECTURE.md Section "VM Provisioning & Lifecycle" — owner, repo, branch_prefix, dolt connection, tracks (name, language, file_patterns, engine_slots, conventions). The loader validates required fields and provides sensible defaults.

#### Tasks

- **T1.2.1: Define config struct and YAML schema**
  Create `internal/config/config.go` with `Config`, `DoltConfig`, `TrackConfig` structs. Tags for YAML unmarshalling. Include all fields from ARCHITECTURE.md config.yaml example (owner, repo, branch_prefix, dolt host/port/database, tracks with name/language/file_patterns/engine_slots/conventions). Exclude provisioner section (production mode, out of scope).

- **T1.2.2: Implement config loader with validation**
  `Load(path string) (*Config, error)` — reads YAML file, unmarshals, validates required fields (owner, repo, at least one track). Returns clear error messages for missing/invalid fields. Defaults: dolt host=127.0.0.1, port=3306, database=`railyard_{owner}`.

### Feature 1.3: GORM Data Models

**Description:** Define all Phase 1 GORM model structs exactly as specified in ARCHITECTURE.md Section 1.5. These structs are the single source of truth for the database schema — GORM AutoMigrate creates tables from them. Models: Bead, BeadDep, BeadProgress, Track, Engine, Message, AgentLog, RailyardConfig, ReindexJob. Excludes BeadDepExternal (Phase 2).

#### Tasks

- **T1.3.1: Implement core work item models**
  Create `internal/models/bead.go` with `Bead`, `BeadDep`, `BeadProgress` structs. All fields, GORM tags, and relations exactly per ARCHITECTURE.md. Bead has: ID, Title, Description, Type, Status, Priority, Track, Assignee, ParentID, Branch, DesignNotes, Acceptance, timestamps, relations (Parent, Children, Deps, Progress).

- **T1.3.2: Implement infrastructure models**
  Create `internal/models/track.go` (Track), `internal/models/engine.go` (Engine), `internal/models/message.go` (Message), `internal/models/agent_log.go` (AgentLog), `internal/models/config.go` (RailyardConfig), `internal/models/reindex_job.go` (ReindexJob). All fields per ARCHITECTURE.md Section 1.5.

### Feature 1.4: Database Connection & Initialization

**Description:** Implement the Dolt connection layer and the `ry db init` command. The connection function uses GORM's MySQL driver to connect to Dolt (which is MySQL-compatible). AutoMigrate creates all tables. `ry db init` is the first runnable CLI command — it creates the railyard database, migrates all tables, seeds tracks from config.yaml, and writes the RailyardConfig row.

#### Tasks

- **T1.4.1: Implement Dolt connection function**
  Create `internal/db/connect.go` with `Connect(owner, host string, port int) (*gorm.DB, error)`. DSN format: `root@tcp(host:port)/railyard_{owner}?parseTime=true`. Handle connection errors with clear messages. Follows ARCHITECTURE.md Section 1.5 connection setup.

- **T1.4.2: Implement AutoMigrate and track seeding**
  Create `internal/db/migrate.go` with `AutoMigrate(db *gorm.DB) error` — migrates all Phase 1 models. `SeedTracks(db *gorm.DB, tracks []config.TrackConfig) error` — upserts Track rows from config. `SeedConfig(db *gorm.DB, cfg *config.Config) error` — writes RailyardConfig row.

- **T1.4.3: Implement `ry db init` CLI command**
  Add `db init` subcommand to cobra CLI. Flow: load config → create Dolt database (via `CREATE DATABASE IF NOT EXISTS`) → connect → AutoMigrate → seed tracks → seed config → print summary (tables created, tracks seeded, config written).

### Feature 1.5: Foundation Test Suite

**Description:** Unit and integration tests for the foundation layer. Config tests use fixture YAML files. Database tests require a running Dolt instance (integration). >90% coverage for `internal/config/` and `internal/db/`.

#### Tasks

- **T1.5.1: Config loading unit tests**
  Test: valid config loads correctly, missing required fields return errors, defaults are applied, invalid YAML returns parse error. Use fixture YAML files in `testdata/`.

- **T1.5.2: Database initialization integration tests**
  Test: Connect to Dolt, run AutoMigrate, verify all tables exist with correct columns. Seed tracks, verify Track rows. Seed config, verify RailyardConfig row. Requires running Dolt server (test helper starts/stops it, or uses Docker).

---

## Epic 2: Bead Management — CLI CRUD & Dependency Tracking

**Goal:** Full bead lifecycle through the `ry` CLI with dependency management and ready detection.

**Context:** This epic builds on Epic 1's GORM models and database layer. It adds the business logic for creating, querying, and managing beads — the core work unit in Railyard. The critical deliverable is the `ReadyBeads()` query (ARCHITECTURE.md Section 2), which the engine daemon uses to find claimable work. Bead IDs are short hashes (e.g., `be-a1b2c`). Branch names follow the pattern `ry/{owner}/{track}/{bead_id}`.

### Feature 2.1: Bead CRUD Operations

**Description:** The `internal/bead/` package providing Create, Get, List, Update functions, plus CLI commands. Bead IDs are auto-generated short hashes. Status defaults to "open". Branch name is auto-computed from owner + track + ID. The CLI commands provide the primary interface for managing beads before Dispatch is built.

#### Tasks

- **T2.1.1: Implement bead package core functions**
  Create `internal/bead/bead.go` with `Create(db, opts) (*Bead, error)`, `Get(db, id) (*Bead, error)`, `List(db, filters) ([]Bead, error)`, `Update(db, id, updates) error`. ID generation: 5-char hex hash with `be-` prefix (e.g., `be-a1b2c`). Ensure uniqueness check on create. Branch auto-computed as `ry/{owner}/{track}/{id}` from config.

- **T2.1.2: Implement `ry bead create` CLI command**
  Cobra subcommand: `ry bead create --title "..." --track backend --type task --priority 2 --description "..." --acceptance "..."`. Required: title, track. Defaults: type=task, priority=2, status=open. Prints created bead ID and branch name.

- **T2.1.3: Implement `ry bead list` and `ry bead show`**
  `ry bead list` with filters: `--track`, `--status`, `--type`, `--assignee`. Table-formatted output (ID, title, status, track, priority, assignee). `ry bead show <id>` — full detail view including description, acceptance, design notes, progress entries, dependencies.

- **T2.1.4: Implement `ry bead update` with status validation**
  `ry bead update <id> --status claimed --assignee engine-01 --priority 1`. Status transition validation: valid transitions are open→ready→claimed→in_progress→done, open→cancelled, any→blocked. Invalid transitions return error with explanation.

### Feature 2.2: Dependency Management & Ready Detection

**Description:** Blocking relationships between beads and the critical `ReadyBeads()` query. A bead is "ready" when its status is "open" AND all beads that block it are "done" or "cancelled". This query (ARCHITECTURE.md Section 2) is the foundation of the engine daemon's work selection. Dependencies are stored in the `bead_deps` table.

#### Tasks

- **T2.2.1: Implement bead dependency functions**
  Create `internal/bead/deps.go` with `AddDep(db, beadID, blockedBy, depType) error`, `ListDeps(db, beadID) ([]BeadDep, error)`, `RemoveDep(db, beadID, blockedBy) error`. Validate both bead IDs exist. Prevent self-dependency. Detect simple cycles (A blocks B, B blocks A).

- **T2.2.2: Implement `ry bead dep` CLI commands**
  `ry bead dep add <bead> --blocked-by <blocker>` — creates blocking dependency. `ry bead dep list <bead>` — shows what blocks this bead and what this bead blocks. `ry bead dep remove <bead> --blocked-by <blocker>`.

- **T2.2.3: Implement ReadyBeads query and `ry bead ready`**
  `ReadyBeads(db, track) ([]Bead, error)` — exactly per ARCHITECTURE.md: beads where status=open, track matches, and NOT IN (beads that have incomplete blockers). `ry bead ready --track backend` — lists beads ready for claiming. This is the engine daemon's core query.

### Feature 2.3: Bead Hierarchy (Epics & Parents)

**Description:** Parent-child relationships for organizing beads. Epics are parent beads that contain child tasks/features. Uses the `ParentID` field on the Bead model. Supports `ry bead children <epic-id>` to list all children of a parent.

#### Tasks

- **T2.3.1: Implement parent-child relationships and CLI**
  `ry bead create --type epic --title "..." --track backend` creates an epic. `ry bead create --type task --parent <epic-id> --title "..."` creates a child. `ry bead children <parent-id>` lists children with status summary. Validate parent exists on create.

### Feature 2.4: Bead Management Test Suite

**Description:** Comprehensive tests for bead operations. >90% coverage for `internal/bead/`. Unit tests for ID generation, status validation, cycle detection. Integration tests with Dolt for CRUD, dependencies, and ready detection.

#### Tasks

- **T2.4.1: Unit tests for bead operations**
  Test: ID generation uniqueness, status transition validation (valid + invalid), cycle detection in dependencies, parent-child validation.

- **T2.4.2: Integration tests for dependencies and ready detection**
  Test: Create beads with dependency chain (A blocks B blocks C). Verify ReadyBeads returns only A. Complete A, verify B becomes ready. Create cross-track dependencies, verify correct behavior. Edge case: bead with cancelled blocker is ready.

---

## Epic 3: Engine Core Loop — Bead Execution Runtime

**Goal:** A running engine daemon that claims beads, spawns Claude Code with full context, and handles the complete lifecycle (completion, /clear cycles, stalls).

**Context:** This is the heart of Railyard. The engine daemon (ARCHITECTURE.md Section "Engine Daemon — The Core Loop") runs on each engine instance. It polls Dolt for ready beads on its track, claims one atomically, renders context (ARCHITECTURE.md Section "Context Injection Template"), spawns Claude Code, monitors the subprocess, and handles exit (done, /clear, stall). The daemon is a Go binary, not an AI agent — it manages the agent lifecycle.

### Feature 3.1: Engine Lifecycle Management

**Description:** Engine registration, heartbeating, and atomic bead claiming. When an engine starts, it registers in the `engines` table. A background goroutine updates `last_activity` every 10s. The `ClaimBead()` function uses `FOR UPDATE SKIP LOCKED` for safe concurrent claiming across multiple engines on the same track.

#### Tasks

- **T3.1.1: Implement engine registration and deregistration**
  Create `internal/engine/engine.go`. On start: generate engine ID (e.g., `eng-{short-hash}`), insert row into `engines` table (ID, track, role=engine, status=idle, started_at=now). On shutdown (signal handler): update status=dead. `Register(db, track) (*Engine, error)`, `Deregister(db, engineID) error`.

- **T3.1.2: Implement heartbeat goroutine**
  `StartHeartbeat(ctx context.Context, db *gorm.DB, engineID string, interval time.Duration)` — goroutine that updates `last_activity = NOW()` on the engine row every 10s. Respects context cancellation for clean shutdown. Other components use stale heartbeats to detect dead engines.

- **T3.1.3: Implement atomic bead claiming**
  `ClaimBead(db, engineID, track) (*Bead, error)` — exactly per ARCHITECTURE.md Section 2: transaction with `FOR UPDATE SKIP LOCKED`, find first ready bead on track (priority ASC, created_at ASC), set status=claimed, assignee=engineID, claimed_at=now. Returns nil if no ready beads. Note: calls `ReadyBeads` logic from Epic 2 within the transaction.

### Feature 3.2: Context Injection & Agent Execution

**Description:** The context renderer takes a claimed bead + track config and produces the full markdown prompt that gets fed to Claude Code. This follows ARCHITECTURE.md Section "Context Injection Template" exactly. The subprocess manager spawns `claude` CLI, pipes the context in, creates the git branch, and captures I/O to the agent_logs table.

#### Tasks

- **T3.2.1: Implement context injection template renderer**
  Create `internal/engine/context.go`. `RenderContext(bead *Bead, track *Track, config *Config, progress []BeadProgress, messages []Message) (string, error)` — renders the full context template from ARCHITECTURE.md. Sections: track conventions, bead details (title, description, design notes, acceptance), previous progress (most recent first), yardmaster messages, recent commits on branch. Include `ry complete` and `ry progress` usage instructions.

- **T3.2.2: Implement Claude Code subprocess management**
  Create `internal/engine/subprocess.go`. `SpawnAgent(ctx, bead, contextPayload) (*Session, error)` — spawns `claude` CLI with rendered context via stdin or `--prompt` flag. Captures stdout/stderr in real-time. Writes to `agent_logs` table (engine_id, session_id, bead_id, direction=out, content). Returns session handle for monitoring. Session ID generated per spawn.

- **T3.2.3: Implement git branch creation and push logic**
  Create `internal/engine/git.go`. `CreateBranch(branchName string) error` — creates and checks out `ry/{owner}/{track}/{bead_id}` from main. `PushBranch(branchName string) error` — pushes to remote. `RecentCommits(branchName string, n int) ([]string, error)` — returns last N commit messages for context injection. Uses `os/exec` to call git.

### Feature 3.3: Completion, Progress & Failure Handling

**Description:** Handles the three possible outcomes of an agent session: (1) bead completed successfully, (2) mid-task exit / `/clear` cycle, (3) stall or crash. The engine daemon detects the outcome and takes appropriate action per ARCHITECTURE.md Section "Engine Daemon".

#### Tasks

- **T3.3.1: Implement completion handling**
  `HandleCompletion(db, bead, engine) error` — called when agent exits and bead is marked done. Flow: push branch, log progress note, set engine status=idle, loop back for next bead. Uses `CompleteBead()` from ARCHITECTURE.md Section 2.

- **T3.3.2: Implement progress notes for /clear cycles**
  `HandleClearCycle(db, bead, engine, cycle int) error` — called when agent exits but bead is NOT done. Writes progress note to `bead_progress` (cycle number, files changed, what was done). Increments cycle counter. Engine re-claims the same bead on next loop iteration.

- **T3.3.3: Implement stall detection and escalation**
  `StallDetector` monitors the subprocess. Conditions: no stdout for 120s, same error repeated 3x, /clear cycle count > configurable threshold (default 5). On stall: mark engine status=stalled, mark bead status=blocked, send message to yardmaster via messaging package: "Engine {id} stalled on bead {id}: {reason}".

### Feature 3.4: Engine CLI Commands

**Description:** CLI commands to start the engine daemon and for agents to report completion/progress from within their Claude Code session.

#### Tasks

- **T3.4.1: Implement `ry engine start` command**
  Cobra subcommand: `ry engine start --track backend`. Loads config, connects to Dolt, registers engine, starts heartbeat, enters main daemon loop (poll → claim → render context → spawn → monitor → handle exit → sleep 5s → repeat). Handles SIGINT/SIGTERM for clean shutdown.

- **T3.4.2: Implement `ry complete` and `ry progress` commands**
  `ry complete <bead-id> "summary of what was done"` — marks bead as done, writes final progress note. Called by the agent from within Claude Code. `ry progress <bead-id> "what I did, what's next"` — writes progress note without completing. Used before `/clear` to preserve context.

### Feature 3.5: Engine Test Suite

**Description:** >90% coverage for `internal/engine/`. Unit tests for context rendering and claim logic. Integration tests with Dolt and a mock Claude Code subprocess.

#### Tasks

- **T3.5.1: Unit tests for engine components**
  Test: context renderer produces correct markdown for various bead states (fresh, with progress, with messages). Claim logic handles no-ready-beads case. Stall detection triggers on correct conditions. Engine registration/deregistration.

- **T3.5.2: Integration tests with mock subprocess**
  Test: seed a ready bead, start engine, verify it claims and creates branch. Mock Claude Code (simple script that writes to stdout and exits). Verify agent_logs written. Verify bead status transitions. Test /clear cycle handling. Test stall detection with a hanging mock process.

---

## Epic 4: Messaging — Agent-to-Agent Communication

**Goal:** Engines, Yardmaster, and Dispatch communicate via the messages table using direct DB polling.

**Context:** ARCHITECTURE.md Section "Messaging: Kafka vs Direct DB" — using Option A (Direct DB). Messages are rows in the `messages` table. Agents poll every 5s. Supports threading (parent-child messages), broadcast (to all engines), and acknowledgement. The engine daemon integrates inbox polling into its main loop (Epic 3 dependency).

### Feature 4.1: Message Core Functions

**Description:** The `internal/messaging/` package providing Send, Inbox, Acknowledge, and threading. Messages have: from_agent, to_agent, subject, body, bead_id (optional context), thread_id (optional parent), priority, acknowledged flag.

#### Tasks

- **T4.1.1: Implement messaging package (Send, Inbox, Acknowledge)**
  Create `internal/messaging/messaging.go`. `Send(db, from, to, subject, body string, opts ...SendOption) (*Message, error)` — creates message row. Options: beadID, threadID, priority. `Inbox(db, agentID) ([]Message, error)` — unacknowledged messages for agent, ordered by priority then created_at. `Acknowledge(db, messageID) error` — sets acknowledged=true.

- **T4.1.2: Implement threading support**
  `GetThread(db, threadID uint) ([]Message, error)` — returns all messages in a thread, ordered chronologically. `Reply(db, parentMsgID uint, from, body string) (*Message, error)` — creates reply with same thread_id, to_agent, subject prefix "Re:".

- **T4.1.3: Implement broadcast messages**
  When `to_agent = "broadcast"`, `Inbox()` for ANY agent returns the message (until acknowledged by that agent). Requires a separate `broadcast_acks` tracking table or a per-agent check. Keep simple: broadcast messages appear in every agent's inbox; each agent acknowledges independently.

### Feature 4.2: Engine Inbox Integration

**Description:** The engine daemon checks its inbox at the start of each loop iteration. Yardmaster instructions (abort bead, switch track, pause, resume) are processed before claiming new work.

#### Tasks

- **T4.2.1: Integrate inbox polling into engine main loop**
  Modify engine daemon loop (Epic 3): after heartbeat, before claim, call `Inbox(db, engineID)`. Process each message based on subject/type. Acknowledge after processing.

- **T4.2.2: Implement yardmaster instruction processing**
  Handle known instruction types: `abort` (stop current bead, mark blocked), `switch-track` (change engine's track), `pause` (stop claiming new beads), `resume` (unpause), `guidance` (log advice for current bead context). Unknown message types are logged and acknowledged.

### Feature 4.3: Messaging CLI Commands

**Description:** Manual messaging for debugging and human intervention. Allows a human to send messages to any agent and read any agent's inbox.

#### Tasks

- **T4.3.1: Implement `ry message send`, `ry inbox`, `ry message ack`**
  `ry message send --from human --to yardmaster --subject "..." --body "..."`. `ry inbox --agent yardmaster` — lists unacknowledged messages. `ry message ack <id>`. `ry message thread <id>` — shows full conversation thread.

### Feature 4.4: Messaging Test Suite

**Description:** >90% coverage for `internal/messaging/`. Unit tests for CRUD and threading. Integration tests for inbox polling and broadcast.

#### Tasks

- **T4.4.1: Unit tests for messaging operations**
  Test: send creates message, inbox returns unacknowledged only, acknowledge removes from inbox. Threading: reply creates correct thread chain. Priority ordering in inbox.

- **T4.4.2: Integration tests for inbox polling and broadcast**
  Test: send message to engine, verify engine loop processes it. Broadcast message appears in multiple agents' inboxes. Each agent acknowledges independently. Yardmaster instructions (abort, pause) affect engine behavior.

---

## Epic 5: Yardmaster — Supervisor Agent

**Goal:** The Yardmaster agent monitors all engines, merges completed branches to main, handles stalls, and manages cross-track dependencies.

**Context:** ARCHITECTURE.md Section "Yardmaster (Agent2)". The Yardmaster is a Claude Code session with a supervisor system prompt. It sees all tracks (unlike engines which are track-scoped). It runs in its own tmux pane. Key responsibilities: engine health monitoring, branch merge (switch), stall handling, dependency unblocking, reindex job creation.

### Feature 5.1: Yardmaster Agent Framework

**Description:** The yardmaster package scaffold and system prompt. The Yardmaster is launched as a Claude Code session with a carefully crafted prompt that gives it access to `ry` CLI commands and defines its monitoring/merging responsibilities.

#### Tasks

- **T5.1.1: Implement yardmaster package scaffold**
  Create `internal/yardmaster/yardmaster.go`. `Start(db *gorm.DB, config *Config) error` — launches Claude Code with the yardmaster system prompt. Manages the subprocess lifecycle. Handles restart on crash.

- **T5.1.2: Write yardmaster system prompt**
  Create `internal/yardmaster/prompt.go` or `yardmaster_prompt.md`. Prompt defines: role (supervisor of all engines across all tracks), responsibilities (monitor health, merge branches, handle stalls, manage deps), available `ry` commands (engine list, bead list, bead reassign, switch, message send, inbox), poll interval (30s), escalation rules (when to message human).

### Feature 5.2: Engine Health Monitoring & Stall Handling

**Description:** The Yardmaster periodically checks engine health by querying the `engines` table for stale heartbeats and stalled status. When an engine is stalled, it reads context (progress notes, agent_logs) and decides how to intervene.

#### Tasks

- **T5.2.1: Implement heartbeat staleness detection**
  Create `internal/yardmaster/health.go`. `CheckEngineHealth(db *gorm.DB, threshold time.Duration) ([]Engine, error)` — returns engines where `last_activity` is older than threshold and status is not dead. `StaleEngines(db) ([]Engine, error)` — convenience wrapper with default 60s threshold.

- **T5.2.2: Implement stalled engine handling and bead reassignment**
  `ReassignBead(db, beadID, fromEngineID, reason string) error` — unclaims bead (status→open, assignee→nil), writes progress note with reassignment reason, marks old engine as dead, sends message to broadcast about reassignment. `ry bead reassign` CLI wraps this.

### Feature 5.3: Branch Merging (Switch)

**Description:** When an engine completes a bead, the Yardmaster pulls the branch, runs the track's test suite, and merges to main. This is the "switch" operation in Railyard terminology. If tests fail, the bead is sent back to an engine.

#### Tasks

- **T5.3.1: Implement branch merge flow**
  Create `internal/yardmaster/switch.go`. `Switch(db, beadID string) error` — flow: fetch branch, checkout, run track test command (`go test ./...` for Go tracks, `npm test` for frontend), if pass: merge to main (fast-forward or merge commit), if fail: set bead status=blocked, send test failure message to engine.

- **T5.3.2: Implement `ry switch` CLI command**
  `ry switch <bead-id>` — runs the switch flow manually. Prints test results and merge status. `ry switch --dry-run <bead-id>` — runs tests without merging.

- **T5.3.3: Implement cross-track dependency unblocking after merge**
  After successful merge, query `bead_deps` for beads on OTHER tracks that were blocked by the completed bead. Transition each from blocked→open. Log the unblocking. This is single-railyard only (same owner's database).

### Feature 5.4: Post-Merge Operations

**Description:** After a successful merge, the Yardmaster creates a reindex job for CocoIndex (placeholder for the future CocoIndex epic).

#### Tasks

- **T5.4.1: Implement reindex job creation**
  After switch: `INSERT INTO reindex_jobs (track, trigger_commit, status, created_at)`. The row sits as "pending" — no consumer exists yet (CocoIndex epic is deferred). This ensures the interface is ready when CocoIndex is built.

### Feature 5.5: Yardmaster CLI & Test Suite

**Description:** CLI command to start the yardmaster and comprehensive tests. >90% coverage for `internal/yardmaster/`.

#### Tasks

- **T5.5.1: Implement `ry yardmaster` CLI command**
  Cobra subcommand: `ry yardmaster`. Loads config, connects to Dolt, starts yardmaster agent. Single instance per railyard (check for existing, error if running).

- **T5.5.2: Unit tests for yardmaster operations**
  Test: heartbeat staleness detection (stale vs healthy engines), bead reassignment (status transitions, progress notes), dependency unblocking logic (cross-track deps resolved after merge).

- **T5.5.3: Integration tests for merge flow and reassignment**
  Test: create bead, create branch with commits, run switch flow, verify merge to main. Test: stalled engine detected, bead reassigned to new engine. Test: completed bead unblocks cross-track dependent.

---

## Epic 6: Dispatch — Planner Agent

**Goal:** The Dispatch agent decomposes feature requests into structured bead plans across tracks with dependency chains.

**Context:** ARCHITECTURE.md Section "Dispatch (Agent1)". Dispatch is the user's primary interface. You describe what you want, it creates epics, features, and tasks across the appropriate tracks with correct dependency ordering. It understands track boundaries (Go→backend, React→frontend, Terraform→infra) and creates cross-track dependencies within the same railyard.

### Feature 6.1: Dispatch Agent Framework

**Description:** The dispatch package scaffold and system prompt. Dispatch is a Claude Code session with a planner prompt that has access to `ry bead create`, `ry bead dep add`, and track configuration.

#### Tasks

- **T6.1.1: Implement dispatch package scaffold**
  Create `internal/dispatch/dispatch.go`. `Start(db *gorm.DB, config *Config) error` — launches Claude Code with the dispatch system prompt. Interactive mode — user types requests, Dispatch creates beads.

- **T6.1.2: Write dispatch system prompt with track awareness**
  Create `internal/dispatch/prompt.go` or `dispatch_prompt.md`. Prompt includes: role (planner that decomposes work into beads), track definitions from config (name, language, conventions, file patterns), available `ry` commands (bead create, bead dep add, bead list, bead ready), examples of good decomposition (from ARCHITECTURE.md Section "Dispatch" — the auth example), rules (one bead per atomic unit of work, always set acceptance criteria, always set dependencies).

### Feature 6.2: Work Decomposition

**Description:** The logic and prompt engineering for Dispatch to break down feature requests into properly structured bead hierarchies with dependency chains across tracks.

#### Tasks

- **T6.2.1: Implement work decomposition patterns**
  The dispatch prompt must guide Claude Code to: create an epic per track when work spans tracks, create tasks under each epic, set appropriate priorities (backend foundations lower number = higher priority), use bead types correctly (epic for containers, task for atomic work, spike for research).

- **T6.2.2: Implement dependency chain creation with cycle detection**
  Dispatch creates dependencies via `ry bead dep add`. Prompt must enforce: backend model before backend handler, backend API before frontend consumer. After creating all beads, Dispatch runs `ry bead dep list` to verify the chain and checks for cycles. If cycles detected, it resolves them.

### Feature 6.3: Dispatch CLI & Test Suite

**Description:** CLI command to start Dispatch and tests for decomposition quality. >90% coverage for `internal/dispatch/`.

#### Tasks

- **T6.3.1: Implement `ry dispatch` CLI command**
  Cobra subcommand: `ry dispatch`. Loads config, connects to Dolt, starts dispatch agent. Interactive — opens Claude Code in the current terminal for direct conversation.

- **T6.3.2: Integration tests for multi-track decomposition**
  Test: give a multi-track feature request (e.g., "Add user auth with JWT backend and React login page"), verify Dispatch creates: backend epic with tasks, frontend epic with tasks, cross-track dependencies (frontend blocked by backend API). Verify bead fields are populated (title, description, acceptance, track, type, parent).

---

## Epic 7: Local Orchestration — `ry start` Full Railyard

**Goal:** A single command (`ry start`) brings up the entire local railyard: Dolt, Dispatch, Yardmaster, and N engines in tmux.

**Context:** ARCHITECTURE.md Section "Local Development Mode". Everything runs on one machine in a tmux session. The `ry start` command orchestrates the full startup sequence. `ry stop` gracefully shuts everything down. `ry status` provides a real-time dashboard. Engine count is user-controlled (1–100) and can be adjusted dynamically via `ry engine scale`.

### Feature 7.1: Lifecycle Commands

**Description:** The core orchestration commands: start, stop, status. `ry start` creates a tmux session with the correct pane layout. `ry stop` drains engines gracefully. `ry status` shows a dashboard of the entire railyard state.

#### Tasks

- **T7.1.1: Implement `ry start` command**
  Cobra subcommand: `ry start [--engines N]`. Flow: validate config, check Dolt is running (or start it), run `ry db init` if needed, create tmux session "railyard", pane 0: Dispatch, pane 1: Yardmaster, panes 2..N+1: engines (track assignment from config engine_slots). Engine count from `--engines` flag or sum of engine_slots in config.

- **T7.1.2: Implement `ry stop` command**
  `ry stop [--timeout 60s]`. Flow: send "drain" broadcast message to all engines, wait for in-progress beads to complete (up to timeout), kill all engine processes, kill yardmaster, kill dispatch, kill tmux session, update all engine statuses to dead in Dolt.

- **T7.1.3: Implement `ry status` command**
  `ry status`. Dashboard output: engine table (ID, track, status, current bead, last activity, uptime), bead summary per track (open/ready/claimed/in_progress/done/blocked counts), message queue depth, tmux session status. Table-formatted, refreshable with `--watch`.

### Feature 7.2: Engine Scaling & Track Assignment

**Description:** Dynamic engine count management. Users can scale engines up/down per track. Track assignment distributes engines proportionally based on config engine_slots and current ready bead counts.

#### Tasks

- **T7.2.1: Implement `ry engine scale` command**
  `ry engine scale --count 5 --track backend`. Calculates delta between desired and current engine count for the track. Spins up new tmux panes for additional engines, or sends drain messages to excess engines. Respects max engine_slots per track from config.

- **T7.2.2: Implement track assignment logic**
  `AssignTracks(config *Config, totalEngines int) map[string]int` — distributes N engines across tracks. Algorithm: proportional to engine_slots in config, with floor of 1 per active track. If ready bead counts available, weight toward tracks with more ready beads.

- **T7.2.3: Implement `ry engine list` and `ry engine restart`**
  `ry engine list` — shows all engines with status, track, current bead, uptime. `ry engine restart <id>` — kills the engine's tmux pane and creates a new one. Engine deregisters and re-registers on restart.

### Feature 7.3: Orchestration Test Suite

**Description:** Integration tests for the full lifecycle. >90% coverage for orchestration logic in `internal/orchestration/` or wherever the start/stop/scale logic lives.

#### Tasks

- **T7.3.1: Integration tests for start/stop lifecycle**
  Test: `ry start --engines 2` creates tmux session with correct pane count, engines register in Dolt, `ry status` shows them. `ry stop` kills session and updates engine statuses. Verify no orphaned processes.

- **T7.3.2: Integration tests for engine scaling**
  Test: start with 2 engines, scale to 5, verify 3 new panes created and engines registered. Scale down to 1, verify drain messages sent and excess engines shut down.

---

## Dependency Chain

```
Epic 1 (Foundation)
  └─► Epic 2 (Bead Management)
        └─► Epic 3 (Engine Core Loop)
              ├─► Epic 4 (Messaging)
              │     └─► Epic 5 (Yardmaster)
              │           └─► Epic 7 (Local Orchestration)
              └─► Epic 6 (Dispatch)
                    └─► Epic 7 (Local Orchestration)
```

### Feature-Level Dependencies (within and across epics)

```
F1.1 (Scaffold) → F1.2 (Config) → F1.3 (Models) → F1.4 (DB Layer) → F1.5 (Tests)
F1.4 → F2.1 (Bead CRUD) → F2.2 (Deps & Ready) → F2.3 (Hierarchy) → F2.4 (Tests)
F2.2 → F3.1 (Engine Lifecycle) → F3.2 (Context & Execution) → F3.3 (Completion) → F3.4 (CLI) → F3.5 (Tests)
F3.1 → F4.1 (Message Core) → F4.2 (Engine Inbox) → F4.3 (Message CLI) → F4.4 (Tests)
F4.2 → F5.1 (YM Framework) → F5.2 (Health Monitor) → F5.3 (Switch/Merge) → F5.4 (Post-Merge) → F5.5 (Tests)
F3.4 → F6.1 (Dispatch Framework) → F6.2 (Decomposition) → F6.3 (Tests)
F5.5 + F6.3 → F7.1 (Lifecycle) → F7.2 (Scaling) → F7.3 (Tests)
```

## Decision Log

| # | Decision | Alternatives | Rationale |
|---|----------|-------------|-----------|
| 1 | Phase 1 only | Phase 1+2 | Ship usable first. Cross-railyard adds complexity with no single-user value. |
| 2 | Local-first | Both local+prod | Validates architecture without cloud overhead. Production is a future epic. |
| 3 | Runnable vertical slices | Compilable modules, feature-complete components | Catches integration issues early. Each slice testable end-to-end. |
| 4 | CocoIndex deferred | Include as final slices | Engines work without semantic search. Reduces scope significantly. |
| 5 | All three agent roles | Engine only, Engine+Yardmaster | Full orchestration from start. Dispatch avoids manual bead creation. |
| 6 | Agent logs in Dolt via GORM | Full logging arch, stdout only | Queryable debugging without shipping/redaction complexity. |
| 7 | End-to-end first (Approach A) | Two-track parallel, horizontal | Natural dependency chain. Running system by Epic 3. Lowest integration risk. |
| 8 | Direct DB messaging | Kafka | Polling 5s fine for local. Kafka is future scale optimization. |
| 9 | Variable engine count (1-100) | Fixed count | User controls concurrency dynamically. Proportional track assignment. |
| 10 | 3-level hierarchy (Epic→Feature→Task) | 2-level (Epic→Task) | Features group related tasks, providing intermediate context. Matches beads issue_type support. |
| 11 | >90% test coverage per bead | Minimal tests, full e2e suite | Each slice is testable independently. High coverage catches regressions as epics build on each other. |
