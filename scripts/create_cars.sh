#!/bin/bash
set -euo pipefail

# Railyard Phase 1 — Bead Creation Script
# Creates all epics, features, and tasks from IMPLEMENTATION_PLAN.md
# Uses bd (beads) CLI with --silent for ID capture

echo "=== Creating Railyard Phase 1 Beads ==="
echo ""

# ============================================================
# EPICS
# ============================================================
echo "--- Creating Epics ---"

E1=$(bd create --type epic --silent \
  --title "Foundation — Dolt Database & Project Scaffold" \
  -p 0 \
  -l "phase-1,foundation" \
  -d "Bedrock of the entire Railyard system. Establishes Go project structure, all GORM models, config loading, Dolt connectivity, and the ry db init command. Every subsequent epic depends on this. Directory layout follows Go conventions: cmd/ry/ for CLI entrypoint, internal/ for private packages (models, db, config, bead, engine, messaging, yardmaster, dispatch). GORM models defined per ARCHITECTURE.md Section 1.5. Config is YAML-based per ARCHITECTURE.md VM Provisioning section. See IMPLEMENTATION_PLAN.md Epic 1 for full details." \
  --acceptance "ry db init creates railyard_{owner} database in Dolt with all tables. Tracks from config.yaml are seeded. RailyardConfig row written. Tables queryable via mysql client. >90% test coverage for internal/config/ and internal/db/." \
  --design "See ARCHITECTURE.md Section 1 (Dolt), Section 1.5 (GORM), Section 2 (Schema), and VM Provisioning (config.yaml)")
echo "Epic 1 (Foundation): $E1"

E2=$(bd create --type epic --silent \
  --title "Bead Management — CLI CRUD & Dependency Tracking" \
  -p 0 \
  -l "phase-1,bead-mgmt" \
  -d "Full bead lifecycle through the ry CLI. Adds business logic for creating, querying, and managing beads — the core work unit in Railyard. Critical deliverable: ReadyBeads() query (ARCHITECTURE.md Section 2) used by engine daemon to find claimable work. Bead IDs are short hashes (be-a1b2c). Branch names follow ry/{owner}/{track}/{bead_id}. Builds on Epic 1 GORM models and database layer. See IMPLEMENTATION_PLAN.md Epic 2." \
  --acceptance "Create epic with child tasks, set dependency chains. ry car ready --track backend returns only unblocked beads. Full CRUD via CLI (create, list, show, update). Status transitions validated. >90% test coverage for internal/bead/." \
  --design "See ARCHITECTURE.md Section 2 (Schema) for GORM operations: ClaimBead(), CompleteBead(), ReadyBeads()")
echo "Epic 2 (Bead Management): $E2"

E3=$(bd create --type epic --silent \
  --title "Engine Core Loop — Bead Execution Runtime" \
  -p 0 \
  -l "phase-1,engine" \
  -d "The heart of Railyard. Engine daemon polls Dolt for ready beads on its track, claims one atomically, renders context (ARCHITECTURE.md Context Injection Template), spawns Claude Code, monitors subprocess, handles exit (done, /clear, stall). The daemon is a Go binary that manages the AI agent lifecycle — it is NOT an AI agent itself. See ARCHITECTURE.md Section Engine Daemon and IMPLEMENTATION_PLAN.md Epic 3." \
  --acceptance "ry engine start --track backend claims a ready bead, creates git branch, spawns Claude Code with full context template. On completion pushes branch and goes idle. Handles /clear cycles with progress notes. Detects stalls and escalates. >90% test coverage for internal/engine/." \
  --design "See ARCHITECTURE.md sections: Engine Daemon — The Core Loop, Context Injection Template, Railyard API (MCP Server or CLI Wrapper)")
echo "Epic 3 (Engine Core Loop): $E3"

E4=$(bd create --type epic --silent \
  --title "Messaging — Agent-to-Agent Communication" \
  -p 1 \
  -l "phase-1,messaging" \
  -d "Agent-to-agent communication via direct DB polling (ARCHITECTURE.md Messaging section, Option A). Messages are rows in the messages table. Agents poll every 5s. Supports threading (parent-child messages), broadcast (to all engines), and acknowledgement. Integrates into engine daemon main loop for processing yardmaster instructions. See IMPLEMENTATION_PLAN.md Epic 4." \
  --acceptance "Engine sends message to yardmaster, yardmaster reads it, acknowledges. Broadcast delivers to all agents. Threading chains replies correctly. Engine loop processes inbox before claiming work. >90% test coverage for internal/messaging/." \
  --design "See ARCHITECTURE.md Section Messaging: Kafka vs Direct DB — using Option A (Direct DB)")
echo "Epic 4 (Messaging): $E4"

E5=$(bd create --type epic --silent \
  --title "Yardmaster — Supervisor Agent" \
  -p 1 \
  -l "phase-1,yardmaster" \
  -d "Supervisor agent that monitors all engines across all tracks, merges completed branches to main (switch), handles stalls, manages cross-track dependencies within this railyard. Yardmaster is a Claude Code session with a supervisor system prompt. Runs in its own tmux pane. See ARCHITECTURE.md Section Yardmaster (Agent2) and IMPLEMENTATION_PLAN.md Epic 5." \
  --acceptance "Yardmaster detects completed branch, runs tests, merges to main, unblocks downstream beads. Detects stalled engine, reassigns bead. Creates reindex_jobs rows after merge. Single instance per railyard enforced. >90% test coverage for internal/yardmaster/." \
  --design "See ARCHITECTURE.md Section Yardmaster (Agent2) — responsibilities: monitor health, switch branches, handle stalls, manage deps, trigger reindex")
echo "Epic 5 (Yardmaster): $E5"

E6=$(bd create --type epic --silent \
  --title "Dispatch — Planner Agent" \
  -p 1 \
  -l "phase-1,dispatch" \
  -d "User-facing planner agent that decomposes feature requests into structured bead plans across tracks with dependency chains. Dispatch is a Claude Code session with a planner prompt. Understands track boundaries (Go→backend, React→frontend, Terraform→infra). Creates epics, features, tasks with proper ordering. See ARCHITECTURE.md Section Dispatch (Agent1) and IMPLEMENTATION_PLAN.md Epic 6." \
  --acceptance "Tell Dispatch 'Add user auth with JWT backend and login frontend.' It creates backend epic with tasks, frontend epic with tasks, and cross-track dependencies. Beads have title, description, acceptance, correct track, type, and parent. >90% test coverage for internal/dispatch/." \
  --design "See ARCHITECTURE.md Section Dispatch (Agent1) — example interaction shows decomposition pattern")
echo "Epic 6 (Dispatch): $E6"

E7=$(bd create --type epic --silent \
  --title "Local Orchestration — ry start Full Railyard" \
  -p 1 \
  -l "phase-1,orchestration" \
  -d "Single command (ry start) brings up entire local railyard: Dolt, Dispatch, Yardmaster, and N engines in tmux. ry stop gracefully shuts down. ry status provides dashboard. Engine count user-controlled (1-100), adjustable dynamically via ry engine scale. See ARCHITECTURE.md Section Local Development Mode and IMPLEMENTATION_PLAN.md Epic 7." \
  --acceptance "ry start --engines 3 creates tmux session with Dispatch, Yardmaster, 3 engines. ry status shows all healthy. ry stop gracefully shuts down. ry engine scale adjusts count dynamically. >90% test coverage for orchestration logic." \
  --design "See ARCHITECTURE.md Section Local Development Mode — tmux session layout, startup sequence")
echo "Epic 7 (Local Orchestration): $E7"

echo ""
echo "=== Epics created ==="
echo ""

# ============================================================
# EPIC 1 FEATURES & TASKS
# ============================================================
echo "--- Epic 1: Foundation Features & Tasks ---"

F1_1=$(bd create --type feature --silent --parent "$E1" \
  --title "Project Scaffold & Dependencies" \
  -p 0 \
  -l "phase-1,foundation,scaffold" \
  -d "Initialize Go module, establish canonical directory structure, add all core dependencies. Creates the skeleton every subsequent feature builds on. Layout: cmd/ry/main.go (CLI entrypoint), internal/models/ (GORM structs), internal/db/ (connection + migration), internal/config/ (YAML loading), internal/bead/ (bead operations), internal/engine/ (engine daemon), internal/messaging/ (agent communication), internal/yardmaster/ (supervisor), internal/dispatch/ (planner)." \
  --acceptance "go mod init complete. All directories created. cmd/ry/main.go with minimal cobra root command that prints version. go build ./cmd/ry produces working binary. All dependencies resolvable. >90% test coverage.")
echo "  F1.1 (Scaffold): $F1_1"

T1_1_1=$(bd create --type task --silent --parent "$F1_1" \
  --title "Initialize Go module and directory structure" \
  -p 0 \
  -l "phase-1,foundation,scaffold" \
  -d "Create go.mod with go mod init github.com/zulandar/railyard. Create all directories: cmd/ry/, internal/models/, internal/db/, internal/config/, internal/bead/, internal/engine/, internal/messaging/, internal/yardmaster/, internal/dispatch/. Add cmd/ry/main.go with a minimal cobra root command that prints version info. Verify go build ./cmd/ry produces a working binary." \
  --acceptance "go.mod exists with correct module path. All directories exist. go build ./cmd/ry succeeds. Running the binary prints version. >90% test coverage for any testable code.")
echo "    T1.1.1: $T1_1_1"

T1_1_2=$(bd create --type task --silent --parent "$F1_1" \
  --title "Add core dependencies" \
  -p 0 \
  -l "phase-1,foundation,scaffold" \
  -d "go get for: gorm.io/gorm, gorm.io/driver/mysql (Dolt is MySQL-compatible), github.com/spf13/cobra (CLI framework), gopkg.in/yaml.v3 (config parsing). Create go.sum. Verify all imports resolve with go build." \
  --acceptance "go.sum exists. All four dependencies listed in go.mod. go build ./cmd/ry succeeds with all imports. >90% test coverage.")
echo "    T1.1.2: $T1_1_2"

F1_2=$(bd create --type feature --silent --parent "$E1" \
  --title "Configuration System" \
  -p 0 \
  -l "phase-1,foundation,config" \
  -d "YAML-based configuration loader reading config.yaml. Config struct mirrors ARCHITECTURE.md VM Provisioning section: owner, repo, branch_prefix, dolt connection (host/port/database), tracks (name, language, file_patterns, engine_slots, conventions). Validates required fields, provides sensible defaults. Excludes provisioner section (production mode, out of scope)." \
  --acceptance "Load() reads valid YAML and returns typed Config. Missing required fields return clear errors. Defaults applied (dolt host=127.0.0.1, port=3306). >90% test coverage for internal/config/.")
echo "  F1.2 (Config): $F1_2"

T1_2_1=$(bd create --type task --silent --parent "$F1_2" \
  --title "Define config struct and YAML schema" \
  -p 0 \
  -l "phase-1,foundation,config" \
  -d "Create internal/config/config.go with Config, DoltConfig, TrackConfig structs. YAML tags for unmarshalling. Fields from ARCHITECTURE.md config.yaml: owner (string, required), repo (string, required), branch_prefix (string, derived from owner), dolt.host (string, default 127.0.0.1), dolt.port (int, default 3306), dolt.database (string, derived railyard_{owner}), tracks[] with name/language/file_patterns/engine_slots/conventions. Exclude provisioner section." \
  --acceptance "Config struct compiles. YAML tags correct. All ARCHITECTURE.md config fields present (minus provisioner). >90% test coverage.")
echo "    T1.2.1: $T1_2_1"

T1_2_2=$(bd create --type task --silent --parent "$F1_2" \
  --title "Implement config loader with validation" \
  -p 0 \
  -l "phase-1,foundation,config" \
  -d "Implement Load(path string) (*Config, error) in internal/config/config.go. Reads YAML file, unmarshals into Config struct, validates required fields (owner, repo, at least one track), applies defaults (dolt host=127.0.0.1, port=3306, database=railyard_{owner}, branch_prefix=ry/{owner}). Returns clear error messages for missing/invalid fields." \
  --acceptance "Load() with valid config returns correct struct. Load() with missing owner returns error mentioning 'owner'. Load() with no tracks returns error. Defaults applied when fields omitted. >90% test coverage.")
echo "    T1.2.2: $T1_2_2"

F1_3=$(bd create --type feature --silent --parent "$E1" \
  --title "GORM Data Models" \
  -p 0 \
  -l "phase-1,foundation,models" \
  -d "All Phase 1 GORM model structs exactly as specified in ARCHITECTURE.md Section 1.5. These are the single source of truth for database schema — GORM AutoMigrate creates tables from them. Models: Bead, BeadDep, BeadProgress, Track, Engine, Message, AgentLog, RailyardConfig, ReindexJob. Excludes BeadDepExternal (Phase 2)." \
  --acceptance "All 9 model structs compile. GORM tags match ARCHITECTURE.md exactly. Relations defined (Bead.Parent, Bead.Children, Bead.Deps, Bead.Progress). >90% test coverage.")
echo "  F1.3 (Models): $F1_3"

T1_3_1=$(bd create --type task --silent --parent "$F1_3" \
  --title "Implement core work item models (Bead, BeadDep, BeadProgress)" \
  -p 0 \
  -l "phase-1,foundation,models" \
  -d "Create internal/models/bead.go with Bead, BeadDep, BeadProgress structs. All fields and GORM tags exactly per ARCHITECTURE.md Section 1.5. Bead: ID (primaryKey, size:32), Title (not null), Description (text), Type (size:16, default:task), Status (size:16, default:open, index), Priority (default:2), Track (size:64, index), Assignee (size:64), ParentID (*string, size:32), Branch (size:128), DesignNotes (text), Acceptance (text), timestamps, ClaimedAt/CompletedAt (*time.Time). Relations: Parent, Children, Deps, Progress." \
  --acceptance "Structs compile. All fields present with correct GORM tags. Foreign key relations defined. BeadDep has composite primary key (BeadID, BlockedBy). BeadProgress has auto-increment ID. >90% test coverage.")
echo "    T1.3.1: $T1_3_1"

T1_3_2=$(bd create --type task --silent --parent "$F1_3" \
  --title "Implement infrastructure models (Track, Engine, Message, AgentLog, Config, ReindexJob)" \
  -p 0 \
  -l "phase-1,foundation,models" \
  -d "Create separate files in internal/models/: track.go (Track struct), engine.go (Engine), message.go (Message), agent_log.go (AgentLog), config.go (RailyardConfig), reindex_job.go (ReindexJob). All fields per ARCHITECTURE.md Section 1.5. Track: Name (primaryKey), Language, Conventions (json), SystemPrompt (text), FilePatterns (json), EngineSlots (default:3), Active (default:true). Engine: ID, VMID, Track, Role, Status, CurrentBead, SessionID, timestamps. Message: ID (auto), FromAgent, ToAgent (index), BeadID, ThreadID, Subject, Body, Priority, Acknowledged (index), timestamps. AgentLog: ID, EngineID+SessionID (composite index), BeadID (index), Direction, Content (mediumtext), TokenCount, Model, LatencyMs, timestamp. RailyardConfig: ID, Owner (uniqueIndex), RepoURL, Mode, Settings (json). ReindexJob: ID, Track, TriggerCommit, Status, FilesChanged, ChunksUpdated, GPUBoxID, timestamps, ErrorMessage." \
  --acceptance "All 6 model structs compile with correct GORM tags per ARCHITECTURE.md. Indexes defined. JSON fields typed correctly. >90% test coverage.")
echo "    T1.3.2: $T1_3_2"

F1_4=$(bd create --type feature --silent --parent "$E1" \
  --title "Database Connection & Initialization" \
  -p 0 \
  -l "phase-1,foundation,database" \
  -d "Dolt connection layer using GORM MySQL driver, AutoMigrate for all tables, track seeding from config, and the ry db init CLI command. This is the first runnable CLI command. Connection follows ARCHITECTURE.md Section 1.5 setup." \
  --acceptance "ry db init creates railyard_{owner} database, migrates all tables, seeds tracks, writes RailyardConfig. Tables queryable via mysql client. Idempotent (safe to run twice). >90% test coverage for internal/db/.")
echo "  F1.4 (Database): $F1_4"

T1_4_1=$(bd create --type task --silent --parent "$F1_4" \
  --title "Implement Dolt connection function" \
  -p 0 \
  -l "phase-1,foundation,database" \
  -d "Create internal/db/connect.go with Connect(owner, host string, port int) (*gorm.DB, error). DSN format: root@tcp(host:port)/railyard_{owner}?parseTime=true. Handle connection errors with clear messages (Dolt not running, wrong port, database not found). Follows ARCHITECTURE.md Section 1.5 connection setup exactly." \
  --acceptance "Connect() returns valid *gorm.DB when Dolt is running. Returns clear error when Dolt is not running. DSN format matches ARCHITECTURE.md. >90% test coverage.")
echo "    T1.4.1: $T1_4_1"

T1_4_2=$(bd create --type task --silent --parent "$F1_4" \
  --title "Implement AutoMigrate and track seeding" \
  -p 0 \
  -l "phase-1,foundation,database" \
  -d "Create internal/db/migrate.go with: AutoMigrate(db *gorm.DB) error — migrates all 9 Phase 1 models (Bead, BeadDep, BeadProgress, Track, Engine, Message, AgentLog, RailyardConfig, ReindexJob). SeedTracks(db *gorm.DB, tracks []config.TrackConfig) error — upserts Track rows from config. SeedConfig(db *gorm.DB, cfg *config.Config) error — writes/updates RailyardConfig row." \
  --acceptance "AutoMigrate creates all 9 tables in Dolt. SeedTracks populates tracks table from config. SeedConfig writes config row. All idempotent. >90% test coverage.")
echo "    T1.4.2: $T1_4_2"

T1_4_3=$(bd create --type task --silent --parent "$F1_4" \
  --title "Implement ry db init CLI command" \
  -p 0 \
  -l "phase-1,foundation,database" \
  -d "Add 'db init' subcommand to cobra CLI in cmd/ry/. Flow: load config from config.yaml (or --config flag) → create Dolt database via CREATE DATABASE IF NOT EXISTS railyard_{owner} → connect with GORM → run AutoMigrate → seed tracks from config → seed RailyardConfig → print summary (tables created, tracks seeded, config written). First runnable command in the system." \
  --acceptance "ry db init creates database and all tables. Prints human-readable summary. Idempotent. --config flag overrides default path. Errors clearly if Dolt not running. >90% test coverage.")
echo "    T1.4.3: $T1_4_3"

F1_5=$(bd create --type feature --silent --parent "$E1" \
  --title "Foundation Test Suite" \
  -p 1 \
  -l "phase-1,foundation,testing" \
  -d "Unit and integration tests for the foundation layer. Config tests use fixture YAML files in testdata/. Database tests require running Dolt instance. Target >90% coverage for internal/config/ and internal/db/." \
  --acceptance ">90% test coverage for internal/config/ and internal/db/. All tests pass. Integration tests documented with setup requirements.")
echo "  F1.5 (Tests): $F1_5"

T1_5_1=$(bd create --type task --silent --parent "$F1_5" \
  --title "Config loading unit tests" \
  -p 1 \
  -l "phase-1,foundation,testing" \
  -d "Create internal/config/config_test.go. Test cases: valid config loads correctly with all fields, missing required fields (owner, repo) return descriptive errors, defaults applied when optional fields omitted (dolt host, port, database), invalid YAML returns parse error, empty tracks array returns error. Use fixture YAML files in testdata/ directory." \
  --acceptance "All test cases pass. >90% line coverage for internal/config/. Fixture files in testdata/. go test -cover ./internal/config/ shows coverage.")
echo "    T1.5.1: $T1_5_1"

T1_5_2=$(bd create --type task --silent --parent "$F1_5" \
  --title "Database initialization integration tests" \
  -p 1 \
  -l "phase-1,foundation,testing" \
  -d "Create internal/db/db_test.go. Requires running Dolt server. Test cases: Connect() succeeds with valid params, AutoMigrate creates all 9 tables with correct columns, SeedTracks populates tracks from config, SeedConfig writes RailyardConfig row, idempotent (running twice doesn't error). Test helper to start/stop Dolt or use Docker. Build tag for integration tests." \
  --acceptance "All integration tests pass with Dolt running. >90% line coverage for internal/db/. Test helper manages Dolt lifecycle. Tests tagged for CI skip if Dolt unavailable.")
echo "    T1.5.2: $T1_5_2"

echo ""

# ============================================================
# EPIC 2 FEATURES & TASKS
# ============================================================
echo "--- Epic 2: Bead Management Features & Tasks ---"

F2_1=$(bd create --type feature --silent --parent "$E2" \
  --title "Bead CRUD Operations" \
  -p 0 \
  -l "phase-1,bead-mgmt,crud" \
  -d "internal/bead/ package providing Create, Get, List, Update functions plus CLI commands. Bead IDs are auto-generated 5-char hex hashes with be- prefix. Status defaults to open. Branch auto-computed as ry/{owner}/{track}/{id}. CLI commands are the primary car management interface before Dispatch is built." \
  --acceptance "Create/Get/List/Update functions work via GORM. CLI commands ry car create/list/show/update functional. ID generation produces unique be-xxxxx format. Branch names computed correctly. Status transitions validated. >90% test coverage for CRUD operations.")
echo "  F2.1 (CRUD): $F2_1"

T2_1_1=$(bd create --type task --silent --parent "$F2_1" \
  --title "Implement bead package core functions" \
  -p 0 \
  -l "phase-1,bead-mgmt,crud" \
  -d "Create internal/bead/bead.go with: Create(db *gorm.DB, opts CreateOpts) (*models.Bead, error) — generates ID (5-char hex, be- prefix), sets status=open, computes branch=ry/{owner}/{track}/{id}. Get(db, id) (*models.Bead, error) — preloads Deps and Progress. List(db, filters ListFilters) ([]models.Bead, error) — filters by track, status, type, assignee. Update(db, id, updates) error — validates status transitions. ID uniqueness check on create." \
  --acceptance "Create generates unique IDs. Get returns full bead with relations. List filters correctly. Update validates transitions. Branch name format correct. >90% test coverage.")
echo "    T2.1.1: $T2_1_1"

T2_1_2=$(bd create --type task --silent --parent "$F2_1" \
  --title "Implement ry car create CLI command" \
  -p 0 \
  -l "phase-1,bead-mgmt,crud" \
  -d "Cobra subcommand: ry car create --title '...' --track backend --type task --priority 2 --description '...' --acceptance '...' --design '...'. Required: title, track. Defaults: type=task, priority=2, status=open. Prints created bead ID and branch name on success. Loads config for owner/branch_prefix." \
  --acceptance "Command creates bead in Dolt. Prints ID and branch. Required field validation. Defaults applied. Queryable via mysql after creation. >90% test coverage.")
echo "    T2.1.2: $T2_1_2"

T2_1_3=$(bd create --type task --silent --parent "$F2_1" \
  --title "Implement ry car list and ry car show" \
  -p 0 \
  -l "phase-1,bead-mgmt,crud" \
  -d "ry car list with filters: --track, --status, --type, --assignee. Table-formatted output showing ID, title, status, track, priority, assignee. ry car show <id> — full detail view including description, acceptance, design notes, progress entries, dependencies, parent info, branch name, timestamps." \
  --acceptance "List shows filtered results in table format. Show displays all bead fields. Empty results handled gracefully. Invalid ID returns clear error. >90% test coverage.")
echo "    T2.1.3: $T2_1_3"

T2_1_4=$(bd create --type task --silent --parent "$F2_1" \
  --title "Implement ry car update with status validation" \
  -p 0 \
  -l "phase-1,bead-mgmt,crud" \
  -d "ry car update <id> --status claimed --assignee engine-01 --priority 1 --description '...' etc. Status transition validation: valid transitions are open→ready, ready→claimed, claimed→in_progress, in_progress→done, open→cancelled, any→blocked. Invalid transitions return error with explanation of valid transitions from current state." \
  --acceptance "Update modifies fields in Dolt. Invalid status transitions rejected with helpful error. Multiple fields updatable in one command. Non-existent ID returns error. >90% test coverage.")
echo "    T2.1.4: $T2_1_4"

F2_2=$(bd create --type feature --silent --parent "$E2" \
  --title "Dependency Management & Ready Detection" \
  -p 0 \
  -l "phase-1,bead-mgmt,deps" \
  -d "Blocking relationships between beads and the critical ReadyBeads() query. A bead is 'ready' when status=open AND all blockers are done/cancelled. This query (ARCHITECTURE.md Section 2) is the foundation of engine daemon work selection. Dependencies stored in bead_deps table." \
  --acceptance "Dependencies created/listed/removed. ReadyBeads() returns correct beads (only those with all blockers resolved). Cycle detection prevents circular deps. Self-dependency prevented. >90% test coverage.")
echo "  F2.2 (Deps): $F2_2"

T2_2_1=$(bd create --type task --silent --parent "$F2_2" \
  --title "Implement bead dependency functions" \
  -p 0 \
  -l "phase-1,bead-mgmt,deps" \
  -d "Create internal/bead/deps.go with: AddDep(db, beadID, blockedBy, depType string) error — creates bead_deps row, validates both IDs exist, prevents self-dependency, detects simple cycles. ListDeps(db, beadID) (blockers []BeadDep, dependents []BeadDep, error) — returns what blocks this bead AND what this bead blocks. RemoveDep(db, beadID, blockedBy) error — deletes dependency row." \
  --acceptance "AddDep creates valid dependency. Self-dep returns error. Simple cycle (A→B→A) detected. ListDeps returns both directions. RemoveDep works. >90% test coverage.")
echo "    T2.2.1: $T2_2_1"

T2_2_2=$(bd create --type task --silent --parent "$F2_2" \
  --title "Implement ry car dep CLI commands" \
  -p 0 \
  -l "phase-1,bead-mgmt,deps" \
  -d "ry car dep add <bead> --blocked-by <blocker> — creates blocking dependency. ry car dep list <bead> — shows what blocks this bead and what this bead blocks, formatted as table. ry car dep remove <bead> --blocked-by <blocker> — removes dependency." \
  --acceptance "CLI commands map to dep functions correctly. Output formatted clearly. Invalid IDs return helpful errors. >90% test coverage.")
echo "    T2.2.2: $T2_2_2"

T2_2_3=$(bd create --type task --silent --parent "$F2_2" \
  --title "Implement ReadyBeads query and ry car ready" \
  -p 0 \
  -l "phase-1,bead-mgmt,deps" \
  -d "ReadyBeads(db *gorm.DB, track string) ([]models.Bead, error) — exactly per ARCHITECTURE.md Section 2: SELECT beads WHERE track=X AND status=open AND assignee IS NULL AND id NOT IN (SELECT bead_id FROM bead_deps JOIN beads blocker ON blocked_by=blocker.id WHERE blocker.status NOT IN ('done','cancelled')). Ordered by priority ASC, created_at ASC. ry car ready --track backend — CLI wrapper listing ready beads." \
  --acceptance "ReadyBeads returns only beads with all blockers done/cancelled. Beads with no deps and status=open are ready. Priority ordering correct. ry car ready displays results. >90% test coverage.")
echo "    T2.2.3: $T2_2_3"

F2_3=$(bd create --type feature --silent --parent "$E2" \
  --title "Bead Hierarchy (Epics & Parents)" \
  -p 1 \
  -l "phase-1,bead-mgmt,hierarchy" \
  -d "Parent-child relationships for organizing beads. Epics are parent beads containing child tasks/features. Uses ParentID field on Bead model. Supports listing children of a parent with status summary." \
  --acceptance "Epic creation works. Child beads link to parent via ParentID. ry car children shows all children with status. Parent validation on create. >90% test coverage.")
echo "  F2.3 (Hierarchy): $F2_3"

T2_3_1=$(bd create --type task --silent --parent "$F2_3" \
  --title "Implement parent-child relationships and CLI" \
  -p 1 \
  -l "phase-1,bead-mgmt,hierarchy" \
  -d "ry car create --type epic --title '...' --track backend creates epic. ry car create --type task --parent <epic-id> --title '...' creates child linked via ParentID. ry car children <parent-id> lists all children with status summary (count by status). Validate parent exists on create. Children inherit track from parent if not specified." \
  --acceptance "Epic created with type=epic. Child links to parent. Children command shows summary. Invalid parent ID returns error. Track inheritance works. >90% test coverage.")
echo "    T2.3.1: $T2_3_1"

F2_4=$(bd create --type feature --silent --parent "$E2" \
  --title "Bead Management Test Suite" \
  -p 1 \
  -l "phase-1,bead-mgmt,testing" \
  -d "Comprehensive tests for all bead operations. >90% coverage for internal/bead/. Unit tests for ID generation, status validation, cycle detection. Integration tests with Dolt for CRUD, dependencies, and ready detection." \
  --acceptance ">90% line coverage for internal/bead/. All unit and integration tests pass. Edge cases covered (empty results, cancelled blockers, cross-track deps).")
echo "  F2.4 (Tests): $F2_4"

T2_4_1=$(bd create --type task --silent --parent "$F2_4" \
  --title "Unit tests for bead operations" \
  -p 1 \
  -l "phase-1,bead-mgmt,testing" \
  -d "internal/bead/bead_test.go. Test: ID generation produces unique 5-char hex with be- prefix (generate 1000, check uniqueness). Status transition validation (all valid transitions succeed, invalid transitions fail with descriptive error). Cycle detection (A→B→A detected, A→B→C→A detected). Parent validation (valid parent succeeds, invalid parent fails)." \
  --acceptance "All unit tests pass. ID uniqueness verified over 1000 generations. All valid status transitions tested. All invalid transitions tested. Cycle detection works for depth 2 and 3. >90% coverage.")
echo "    T2.4.1: $T2_4_1"

T2_4_2=$(bd create --type task --silent --parent "$F2_4" \
  --title "Integration tests for dependencies and ready detection" \
  -p 1 \
  -l "phase-1,bead-mgmt,testing" \
  -d "internal/bead/deps_test.go (integration, requires Dolt). Test: create chain A blocks B blocks C — ReadyBeads returns only A. Complete A — ReadyBeads returns B. Complete B — ReadyBeads returns C. Cross-track deps: backend bead blocks frontend bead, same railyard. Edge case: bead with cancelled blocker is ready. Edge case: bead with mix of done and pending blockers is not ready." \
  --acceptance "All integration tests pass. Ready detection correct for chains, cross-track, cancelled blockers, partial completion. >90% coverage for deps.go.")
echo "    T2.4.2: $T2_4_2"

echo ""

# ============================================================
# EPIC 3 FEATURES & TASKS
# ============================================================
echo "--- Epic 3: Engine Core Loop Features & Tasks ---"

F3_1=$(bd create --type feature --silent --parent "$E3" \
  --title "Engine Lifecycle Management" \
  -p 0 \
  -l "phase-1,engine,lifecycle" \
  -d "Engine registration in engines table, heartbeat goroutine updating last_activity every 10s, and atomic bead claiming with FOR UPDATE SKIP LOCKED. Foundation for the engine daemon. See ARCHITECTURE.md Engine Daemon section." \
  --acceptance "Engine registers on start, deregisters on shutdown. Heartbeat updates every 10s. ClaimBead atomically claims one ready bead per track. Concurrent claims don't double-assign. >90% test coverage.")
echo "  F3.1 (Lifecycle): $F3_1"

T3_1_1=$(bd create --type task --silent --parent "$F3_1" \
  --title "Implement engine registration and deregistration" \
  -p 0 \
  -l "phase-1,engine,lifecycle" \
  -d "Create internal/engine/engine.go. Register(db *gorm.DB, track string) (*models.Engine, error) — generates engine ID (eng-{5-char-hex}), inserts row: ID, track, role=engine, status=idle, started_at=now. Deregister(db, engineID) error — updates status=dead. Signal handler (SIGINT/SIGTERM) calls Deregister for clean shutdown." \
  --acceptance "Register creates engine row in Dolt. Deregister updates status to dead. Signal handler triggers deregister. ID format is eng-xxxxx. >90% test coverage.")
echo "    T3.1.1: $T3_1_1"

T3_1_2=$(bd create --type task --silent --parent "$F3_1" \
  --title "Implement heartbeat goroutine" \
  -p 0 \
  -l "phase-1,engine,lifecycle" \
  -d "StartHeartbeat(ctx context.Context, db *gorm.DB, engineID string, interval time.Duration) — goroutine that runs UPDATE engines SET last_activity=NOW() WHERE id=engineID every interval (default 10s). Respects context cancellation for clean shutdown. Other components (yardmaster) use stale last_activity to detect dead engines." \
  --acceptance "Heartbeat updates last_activity in Dolt every 10s. Stops cleanly on context cancel. Stale detection works (engine with old last_activity detectable). >90% test coverage.")
echo "    T3.1.2: $T3_1_2"

T3_1_3=$(bd create --type task --silent --parent "$F3_1" \
  --title "Implement atomic bead claiming (ClaimBead)" \
  -p 0 \
  -l "phase-1,engine,lifecycle" \
  -d "ClaimBead(db *gorm.DB, engineID, track string) (*models.Bead, error) — exactly per ARCHITECTURE.md Section 2: db.Transaction with FOR UPDATE SKIP LOCKED, find first bead WHERE track=X AND status=ready AND assignee IS NULL, ORDER BY priority ASC, created_at ASC, LIMIT 1. Update status=claimed, assignee=engineID, claimed_at=now. Returns nil,nil if no ready beads (not an error). Note: integrates with ReadyBeads logic from Epic 2." \
  --acceptance "Claims one bead atomically. Concurrent claims (2 engines same track) don't double-assign. Returns nil when no beads ready. Priority ordering respected. claimed_at set. >90% test coverage.")
echo "    T3.1.3: $T3_1_3"

F3_2=$(bd create --type feature --silent --parent "$E3" \
  --title "Context Injection & Agent Execution" \
  -p 0 \
  -l "phase-1,engine,context" \
  -d "Context renderer produces full markdown prompt for Claude Code per ARCHITECTURE.md Context Injection Template. Subprocess manager spawns claude CLI, captures I/O to agent_logs. Git branch creation/push for each bead." \
  --acceptance "RenderContext produces correct markdown with all sections. Claude Code spawned with context. I/O captured to agent_logs. Git branch created before spawn, pushed after completion. >90% test coverage.")
echo "  F3.2 (Context): $F3_2"

T3_2_1=$(bd create --type task --silent --parent "$F3_2" \
  --title "Implement context injection template renderer" \
  -p 0 \
  -l "phase-1,engine,context" \
  -d "Create internal/engine/context.go. RenderContext(bead *models.Bead, track *models.Track, config *config.Config, progress []models.BeadProgress, messages []models.Message) (string, error). Renders full template from ARCHITECTURE.md Context Injection Template section. Sections: track header (name, owner, branch prefix), project conventions (system_prompt, language, conventions), current bead (id, title, priority, branch, description, design_notes, acceptance), previous progress (most recent first), yardmaster messages (unacknowledged), recent commits, completion instructions (ry complete, ry progress), stuck instructions (ry message yardmaster)." \
  --acceptance "Output matches ARCHITECTURE.md template structure. All sections populated from inputs. Empty progress/messages handled. Markdown well-formed. >90% test coverage.")
echo "    T3.2.1: $T3_2_1"

T3_2_2=$(bd create --type task --silent --parent "$F3_2" \
  --title "Implement Claude Code subprocess management" \
  -p 0 \
  -l "phase-1,engine,context" \
  -d "Create internal/engine/subprocess.go. SpawnAgent(ctx context.Context, bead *models.Bead, contextPayload string, db *gorm.DB, engineID string) (*Session, error). Spawns claude CLI with rendered context via --prompt flag or stdin. Captures stdout/stderr in real-time via pipe. Writes to agent_logs table: engine_id, session_id (generated per spawn), bead_id, direction=out, content, created_at. Returns Session handle with PID, stdin pipe, wait channel." \
  --acceptance "Claude CLI spawned as subprocess. stdout/stderr captured. agent_logs rows written to Dolt. Session ID unique per spawn. Context cancellation kills subprocess. >90% test coverage.")
echo "    T3.2.2: $T3_2_2"

T3_2_3=$(bd create --type task --silent --parent "$F3_2" \
  --title "Implement git branch creation and push logic" \
  -p 0 \
  -l "phase-1,engine,context" \
  -d "Create internal/engine/git.go. CreateBranch(branchName string) error — git checkout -b ry/{owner}/{track}/{bead_id} from main. PushBranch(branchName string) error — git push origin {branchName}. RecentCommits(branchName string, n int) ([]string, error) — git log --oneline -n N for context injection. Uses os/exec for git commands. Handles: branch already exists (checkout existing), push failures (retry once)." \
  --acceptance "Branch created with correct naming convention. Push succeeds to remote. RecentCommits returns last N commits. Existing branch handled gracefully. >90% test coverage.")
echo "    T3.2.3: $T3_2_3"

F3_3=$(bd create --type feature --silent --parent "$E3" \
  --title "Completion, Progress & Failure Handling" \
  -p 0 \
  -l "phase-1,engine,completion" \
  -d "Handles three outcomes of agent session: (1) bead completed — push branch, go idle; (2) mid-task /clear — write progress note, re-claim; (3) stall/crash — mark stalled, message yardmaster. Per ARCHITECTURE.md Engine Daemon section." \
  --acceptance "Completion flow: push branch, mark done, engine idle. Clear cycle: progress note written, same bead re-claimed. Stall: engine stalled, bead blocked, yardmaster notified. >90% test coverage.")
echo "  F3.3 (Completion): $F3_3"

T3_3_1=$(bd create --type task --silent --parent "$F3_3" \
  --title "Implement completion handling" \
  -p 0 \
  -l "phase-1,engine,completion" \
  -d "HandleCompletion(db *gorm.DB, bead *models.Bead, engine *models.Engine) error. Called when agent exits and bead status=done. Flow: PushBranch(bead.Branch), write final progress note to bead_progress, set engine status=idle, return (daemon loop picks up next bead). Uses CompleteBead() pattern from ARCHITECTURE.md Section 2." \
  --acceptance "Branch pushed on completion. Progress note written. Engine status set to idle. Bead status=done with completed_at timestamp. >90% test coverage.")
echo "    T3.3.1: $T3_3_1"

T3_3_2=$(bd create --type task --silent --parent "$F3_3" \
  --title "Implement progress notes for /clear cycles" \
  -p 0 \
  -l "phase-1,engine,completion" \
  -d "HandleClearCycle(db *gorm.DB, bead *models.Bead, engine *models.Engine, cycle int) error. Called when agent exits but bead is NOT done. Writes progress note to bead_progress: cycle number, engine_id, session_id, note (from ry progress output or auto-generated), files_changed (git diff --name-only). Increments cycle counter. Engine re-claims same bead on next loop iteration (do not release to other engines)." \
  --acceptance "Progress note written with cycle number. Files changed captured from git diff. Same bead re-claimed (not released to pool). Cycle counter increments. >90% test coverage.")
echo "    T3.3.2: $T3_3_2"

T3_3_3=$(bd create --type task --silent --parent "$F3_3" \
  --title "Implement stall detection and escalation" \
  -p 0 \
  -l "phase-1,engine,completion" \
  -d "StallDetector monitors subprocess. Conditions: no stdout for 120s (configurable), same error string repeated 3x in output, /clear cycle count exceeds threshold (default 5, configurable). On stall: update engine status=stalled, update bead status=blocked, send message to yardmaster via messaging package with subject 'engine-stalled' and body containing engine ID, bead ID, stall reason, last output snippet." \
  --acceptance "Stall detected on: stdout timeout, repeated errors, excessive cycles. Engine marked stalled. Bead marked blocked. Yardmaster message sent with context. Configurable thresholds. >90% test coverage.")
echo "    T3.3.3: $T3_3_3"

F3_4=$(bd create --type feature --silent --parent "$E3" \
  --title "Engine CLI Commands" \
  -p 0 \
  -l "phase-1,engine,cli" \
  -d "CLI commands to start engine daemon and for agents to report completion/progress from within Claude Code sessions." \
  --acceptance "ry engine start runs daemon loop. ry complete marks bead done. ry progress writes notes. Clean shutdown on SIGINT. >90% test coverage.")
echo "  F3.4 (Engine CLI): $F3_4"

T3_4_1=$(bd create --type task --silent --parent "$F3_4" \
  --title "Implement ry engine start command (daemon loop)" \
  -p 0 \
  -l "phase-1,engine,cli" \
  -d "Cobra subcommand: ry engine start --track backend. Flow: load config, connect to Dolt, register engine, start heartbeat goroutine, enter main loop: (1) check inbox, (2) claim ready bead or re-claim current, (3) render context, (4) create branch, (5) spawn Claude Code, (6) monitor subprocess, (7) handle exit (completion/clear/stall), (8) sleep 5s, (9) repeat. Handle SIGINT/SIGTERM for clean deregister." \
  --acceptance "Daemon loop runs continuously. Claims and executes beads. Handles all exit types. Clean shutdown on signal. --track flag required. >90% test coverage.")
echo "    T3.4.1: $T3_4_1"

T3_4_2=$(bd create --type task --silent --parent "$F3_4" \
  --title "Implement ry complete and ry progress commands" \
  -p 0 \
  -l "phase-1,engine,cli" \
  -d "ry complete <bead-id> 'summary of what was done' — marks bead status=done, completed_at=now, writes final progress note. Called by agent from within Claude Code session. ry progress <bead-id> 'what I did, what is next' — writes progress note to bead_progress without completing bead. Used before /clear to preserve context for next cycle." \
  --acceptance "ry complete transitions bead to done with summary. ry progress writes note without changing status. Both commands work from within Claude Code session. Invalid bead ID returns error. >90% test coverage.")
echo "    T3.4.2: $T3_4_2"

F3_5=$(bd create --type feature --silent --parent "$E3" \
  --title "Engine Test Suite" \
  -p 1 \
  -l "phase-1,engine,testing" \
  -d ">90% coverage for internal/engine/. Unit tests for context rendering and claim logic. Integration tests with Dolt and mock Claude Code subprocess." \
  --acceptance ">90% line coverage for internal/engine/. All tests pass. Mock subprocess for automated testing. Integration tests with Dolt.")
echo "  F3.5 (Tests): $F3_5"

T3_5_1=$(bd create --type task --silent --parent "$F3_5" \
  --title "Unit tests for engine components" \
  -p 1 \
  -l "phase-1,engine,testing" \
  -d "internal/engine/*_test.go. Test: context renderer produces correct markdown for fresh bead (no progress), bead with 3 progress entries, bead with yardmaster messages. Claim logic returns nil for no ready beads. Stall detection triggers on each condition independently. Engine registration creates correct row. Deregistration updates status." \
  --acceptance "All unit tests pass. Context renderer tested for multiple bead states. Claim edge cases covered. Stall conditions individually tested. >90% coverage.")
echo "    T3.5.1: $T3_5_1"

T3_5_2=$(bd create --type task --silent --parent "$F3_5" \
  --title "Integration tests with mock subprocess" \
  -p 1 \
  -l "phase-1,engine,testing" \
  -d "Integration tests requiring Dolt. Seed ready bead, start engine, verify: (1) bead claimed, (2) branch created, (3) mock Claude Code (simple script echoing output and exiting) runs, (4) agent_logs written, (5) bead status transitions correct. Test /clear cycle: mock exits without completing, verify progress note written, bead re-claimed. Test stall: mock hangs, verify stall detection triggers." \
  --acceptance "Full engine lifecycle tested end-to-end with mock. Claim, execute, complete flow works. Clear cycle flow works. Stall detection flow works. >90% coverage.")
echo "    T3.5.2: $T3_5_2"

echo ""

# ============================================================
# EPIC 4 FEATURES & TASKS
# ============================================================
echo "--- Epic 4: Messaging Features & Tasks ---"

F4_1=$(bd create --type feature --silent --parent "$E4" \
  --title "Message Core Functions" \
  -p 0 \
  -l "phase-1,messaging,core" \
  -d "internal/messaging/ package: Send, Inbox, Acknowledge, threading, broadcast. Messages have from_agent, to_agent, subject, body, bead_id (optional), thread_id (optional), priority, acknowledged flag. See ARCHITECTURE.md Messaging section." \
  --acceptance "Send creates message row. Inbox returns unacknowledged messages ordered by priority. Acknowledge marks processed. Threading chains replies. Broadcast reaches all agents. >90% test coverage.")
echo "  F4.1 (Core): $F4_1"

T4_1_1=$(bd create --type task --silent --parent "$F4_1" \
  --title "Implement messaging package (Send, Inbox, Acknowledge)" \
  -p 0 \
  -l "phase-1,messaging,core" \
  -d "Create internal/messaging/messaging.go. Send(db, from, to, subject, body string, opts ...SendOption) (*models.Message, error) — creates message row with optional beadID, threadID, priority. Inbox(db, agentID string) ([]models.Message, error) — SELECT WHERE to_agent=agentID AND acknowledged=false ORDER BY priority ASC, created_at ASC. Acknowledge(db, messageID uint) error — UPDATE SET acknowledged=true." \
  --acceptance "Send creates message. Inbox returns unacknowledged only. Acknowledge removes from inbox. Priority ordering works. SendOptions work (beadID, threadID). >90% test coverage.")
echo "    T4.1.1: $T4_1_1"

T4_1_2=$(bd create --type task --silent --parent "$F4_1" \
  --title "Implement threading support" \
  -p 0 \
  -l "phase-1,messaging,core" \
  -d "GetThread(db *gorm.DB, threadID uint) ([]models.Message, error) — returns all messages with matching ThreadID ordered by created_at ASC. Reply(db, parentMsgID uint, from, body string) (*models.Message, error) — creates reply: same ThreadID (or set to parent ID if first reply), same to_agent as parent from_agent, subject='Re: ' + parent subject." \
  --acceptance "GetThread returns full chain. Reply creates correctly linked message. Thread ID propagated through chain. >90% test coverage.")
echo "    T4.1.2: $T4_1_2"

T4_1_3=$(bd create --type task --silent --parent "$F4_1" \
  --title "Implement broadcast messages" \
  -p 1 \
  -l "phase-1,messaging,core" \
  -d "When to_agent='broadcast', Inbox() for ANY agent returns the message until that agent acknowledges it. Implementation: Inbox query includes WHERE to_agent=agentID OR (to_agent='broadcast' AND id NOT IN (SELECT message_id FROM broadcast_acks WHERE agent_id=agentID)). Create broadcast_acks table or use simple approach: broadcast messages appear for all, each agent acks independently." \
  --acceptance "Broadcast message appears in every agent inbox. Each agent acknowledges independently. Acknowledged broadcast does not reappear for that agent. Still appears for others. >90% test coverage.")
echo "    T4.1.3: $T4_1_3"

F4_2=$(bd create --type feature --silent --parent "$E4" \
  --title "Engine Inbox Integration" \
  -p 0 \
  -l "phase-1,messaging,engine" \
  -d "Engine daemon checks inbox at start of each loop iteration. Yardmaster instructions processed before claiming new work. Instruction types: abort, switch-track, pause, resume, guidance." \
  --acceptance "Engine polls inbox each loop. Yardmaster instructions processed correctly. Abort stops current bead. Pause/resume work. Unknown message types logged and acked. >90% test coverage.")
echo "  F4.2 (Engine Inbox): $F4_2"

T4_2_1=$(bd create --type task --silent --parent "$F4_2" \
  --title "Integrate inbox polling into engine main loop" \
  -p 0 \
  -l "phase-1,messaging,engine" \
  -d "Modify engine daemon loop from Epic 3: after heartbeat update, before bead claim step, call messaging.Inbox(db, engineID). Process each message. Acknowledge after processing. If abort instruction received, stop current bead work and mark bead as blocked." \
  --acceptance "Engine checks inbox every loop iteration. Messages processed before claim. Acknowledged after processing. No messages = no-op, continues to claim. >90% test coverage.")
echo "    T4.2.1: $T4_2_1"

T4_2_2=$(bd create --type task --silent --parent "$F4_2" \
  --title "Implement yardmaster instruction processing" \
  -p 0 \
  -l "phase-1,messaging,engine" \
  -d "Handle known instruction types from yardmaster messages (identified by subject prefix or convention): 'abort' — stop current bead, mark blocked, release claim. 'switch-track' — change engine track in engines table, release current bead if any. 'pause' — set flag to stop claiming new beads (but finish current). 'resume' — clear pause flag. 'guidance' — log advice text, inject into next context render. Unknown types logged and acknowledged." \
  --acceptance "Each instruction type handled correctly. Abort releases bead. Switch-track changes engine track. Pause/resume toggle claiming. Guidance stored for injection. Unknown types don't crash. >90% test coverage.")
echo "    T4.2.2: $T4_2_2"

F4_3=$(bd create --type feature --silent --parent "$E4" \
  --title "Messaging CLI Commands" \
  -p 1 \
  -l "phase-1,messaging,cli" \
  -d "Manual messaging CLI for debugging and human intervention. Send messages to any agent, read any agent inbox, acknowledge messages." \
  --acceptance "ry message send/inbox/ack work from command line. Thread view shows conversation. >90% test coverage.")
echo "  F4.3 (Message CLI): $F4_3"

T4_3_1=$(bd create --type task --silent --parent "$F4_3" \
  --title "Implement ry message send, ry inbox, ry message ack" \
  -p 1 \
  -l "phase-1,messaging,cli" \
  -d "ry message send --from human --to yardmaster --subject 'need help' --body 'engine stuck on test failures'. ry inbox --agent yardmaster — lists unacknowledged messages with ID, from, subject, timestamp. ry message ack <id> — acknowledges message. ry message thread <id> — shows full conversation thread." \
  --acceptance "All CLI commands functional. Send creates message. Inbox shows correct messages. Ack removes from inbox. Thread shows chain. >90% test coverage.")
echo "    T4.3.1: $T4_3_1"

F4_4=$(bd create --type feature --silent --parent "$E4" \
  --title "Messaging Test Suite" \
  -p 1 \
  -l "phase-1,messaging,testing" \
  -d ">90% coverage for internal/messaging/. Unit tests for CRUD, threading, priority ordering. Integration tests for inbox polling in engine loop and broadcast delivery." \
  --acceptance ">90% line coverage for internal/messaging/. All tests pass.")
echo "  F4.4 (Tests): $F4_4"

T4_4_1=$(bd create --type task --silent --parent "$F4_4" \
  --title "Unit tests for messaging operations" \
  -p 1 \
  -l "phase-1,messaging,testing" \
  -d "Test: Send creates message with correct fields. Inbox returns unacknowledged only, ordered by priority then created_at. Acknowledge sets flag, message no longer in inbox. Reply creates correctly threaded message. GetThread returns ordered chain. Priority='urgent' sorts before 'normal'." \
  --acceptance "All unit tests pass. Edge cases: empty inbox, multiple messages same priority, thread with no replies. >90% coverage.")
echo "    T4.4.1: $T4_4_1"

T4_4_2=$(bd create --type task --silent --parent "$F4_4" \
  --title "Integration tests for inbox polling and broadcast" \
  -p 1 \
  -l "phase-1,messaging,testing" \
  -d "Integration tests with Dolt. Test: send message to engine, verify engine loop processes and acknowledges it. Broadcast message: send broadcast, verify appears in multiple agents' inboxes, each acks independently. Yardmaster instructions: send abort message to engine, verify bead released. Send pause, verify engine stops claiming." \
  --acceptance "Full inbox polling tested in engine loop context. Broadcast delivery verified. Instruction processing verified. >90% coverage.")
echo "    T4.4.2: $T4_4_2"

echo ""

# ============================================================
# EPIC 5 FEATURES & TASKS
# ============================================================
echo "--- Epic 5: Yardmaster Features & Tasks ---"

F5_1=$(bd create --type feature --silent --parent "$E5" \
  --title "Yardmaster Agent Framework" \
  -p 0 \
  -l "phase-1,yardmaster,framework" \
  -d "Yardmaster package scaffold and system prompt. Launched as Claude Code session with supervisor prompt. Has access to ry CLI commands. Runs in own tmux pane. See ARCHITECTURE.md Yardmaster section." \
  --acceptance "Yardmaster starts as Claude Code session. System prompt includes all responsibilities and available commands. Subprocess managed with restart on crash. >90% test coverage.")
echo "  F5.1 (Framework): $F5_1"

T5_1_1=$(bd create --type task --silent --parent "$F5_1" \
  --title "Implement yardmaster package scaffold" \
  -p 0 \
  -l "phase-1,yardmaster,framework" \
  -d "Create internal/yardmaster/yardmaster.go. Start(db *gorm.DB, config *config.Config) error — loads system prompt, launches Claude Code with prompt via claude --prompt or AGENTS.md injection. Manages subprocess lifecycle: restart on unexpected exit, clean shutdown on signal. Registers in engines table with role=yardmaster." \
  --acceptance "Yardmaster starts Claude Code with prompt. Restarts on crash. Clean shutdown works. Registered in engines table. >90% test coverage.")
echo "    T5.1.1: $T5_1_1"

T5_1_2=$(bd create --type task --silent --parent "$F5_1" \
  --title "Write yardmaster system prompt" \
  -p 0 \
  -l "phase-1,yardmaster,framework" \
  -d "Create yardmaster system prompt (internal/yardmaster/prompt.go or yardmaster_prompt.md). Content: role definition (supervisor of all engines across all tracks for this railyard), responsibilities (monitor engine health, merge completed branches, handle stalls, manage cross-track deps, create reindex jobs), available ry commands (ry engine list, ry car list, ry car ready, ry car reassign, ry switch, ry message send, ry inbox, ry status), poll interval (30s default), escalation rules (when to message human: repeated stalls, test failures after 3 retries, merge conflicts)." \
  --acceptance "Prompt is comprehensive and unambiguous. All ry commands listed with usage. Responsibilities clearly defined. Escalation rules explicit. >90% test coverage for prompt rendering.")
echo "    T5.1.2: $T5_1_2"

F5_2=$(bd create --type feature --silent --parent "$E5" \
  --title "Engine Health Monitoring & Stall Handling" \
  -p 0 \
  -l "phase-1,yardmaster,health" \
  -d "Yardmaster queries engines table for stale heartbeats and stalled engines. Reads context and decides intervention: reassign bead, provide guidance, or escalate to human." \
  --acceptance "Stale heartbeat detection works with configurable threshold. Stalled engines identified. Bead reassignment releases and re-queues. Human escalation via message. >90% test coverage.")
echo "  F5.2 (Health): $F5_2"

T5_2_1=$(bd create --type task --silent --parent "$F5_2" \
  --title "Implement heartbeat staleness detection" \
  -p 0 \
  -l "phase-1,yardmaster,health" \
  -d "Create internal/yardmaster/health.go. CheckEngineHealth(db *gorm.DB, threshold time.Duration) ([]models.Engine, error) — returns engines WHERE last_activity < NOW() - threshold AND status NOT IN ('dead'). StaleEngines(db) — convenience wrapper with 60s default threshold." \
  --acceptance "Stale engines detected with configurable threshold. Dead engines excluded. Returns correct engines list. >90% test coverage.")
echo "    T5.2.1: $T5_2_1"

T5_2_2=$(bd create --type task --silent --parent "$F5_2" \
  --title "Implement stalled engine handling and bead reassignment" \
  -p 0 \
  -l "phase-1,yardmaster,health" \
  -d "ReassignBead(db *gorm.DB, beadID, fromEngineID, reason string) error — in transaction: update bead status=open, assignee=nil, claimed_at=nil. Write progress note 'Reassigned from {engineID}: {reason}'. Update old engine status=dead. Send broadcast message about reassignment. ry car reassign CLI command wraps this function." \
  --acceptance "Bead unclaimed and re-queued. Progress note records reason. Old engine marked dead. Broadcast sent. CLI command works. >90% test coverage.")
echo "    T5.2.2: $T5_2_2"

F5_3=$(bd create --type feature --silent --parent "$E5" \
  --title "Branch Merging (Switch)" \
  -p 0 \
  -l "phase-1,yardmaster,switch" \
  -d "When engine completes bead, yardmaster pulls branch, runs track test suite, merges to main if tests pass. 'Switch' in Railyard terminology. Handles test failures and cross-track dependency unblocking." \
  --acceptance "Switch flow: pull, test, merge. Test failures send bead back. Cross-track deps unblocked after merge. ry switch CLI works. >90% test coverage.")
echo "  F5.3 (Switch): $F5_3"

T5_3_1=$(bd create --type task --silent --parent "$F5_3" \
  --title "Implement branch merge flow" \
  -p 0 \
  -l "phase-1,yardmaster,switch" \
  -d "Create internal/yardmaster/switch.go. Switch(db *gorm.DB, beadID string, config *config.Config) error — flow: (1) get bead and track info, (2) git fetch origin {bead.Branch}, (3) git checkout {bead.Branch}, (4) run track test command (track.Conventions['test_command'] or default: go test ./... for Go, npm test for TS), (5) if tests pass: git checkout main && git merge {bead.Branch} --no-ff, (6) if tests fail: set bead status=blocked, send message to assignee engine with test output. (7) git checkout main after either outcome." \
  --acceptance "Merge succeeds when tests pass. Bead blocked when tests fail. Test output sent to engine. Main branch updated. >90% test coverage.")
echo "    T5.3.1: $T5_3_1"

T5_3_2=$(bd create --type task --silent --parent "$F5_3" \
  --title "Implement ry switch CLI command" \
  -p 0 \
  -l "phase-1,yardmaster,switch" \
  -d "ry switch <bead-id> — runs the Switch flow manually. Prints test results and merge status. ry switch --dry-run <bead-id> — runs tests on the branch without actually merging (useful for pre-validation)." \
  --acceptance "ry switch executes full flow. --dry-run runs tests only. Output shows test results and merge status. Invalid bead ID returns error. >90% test coverage.")
echo "    T5.3.2: $T5_3_2"

T5_3_3=$(bd create --type task --silent --parent "$F5_3" \
  --title "Implement cross-track dependency unblocking after merge" \
  -p 0 \
  -l "phase-1,yardmaster,switch" \
  -d "After successful merge in Switch(), query bead_deps for beads on OTHER tracks that have the completed bead as a blocker: SELECT bd.bead_id FROM bead_deps bd JOIN beads b ON bd.bead_id=b.id WHERE bd.blocked_by=completedBeadID AND b.track != completedBead.track. For each, check if ALL blockers are now done/cancelled. If so, transition from blocked→open. Log each unblocking. Single-railyard only." \
  --acceptance "Cross-track blocked beads detected. Only fully unblocked beads transition to open. Partially blocked beads stay blocked. Unblocking logged. >90% test coverage.")
echo "    T5.3.3: $T5_3_3"

F5_4=$(bd create --type feature --silent --parent "$E5" \
  --title "Post-Merge Operations" \
  -p 1 \
  -l "phase-1,yardmaster,post-merge" \
  -d "After successful merge, create reindex job placeholder for future CocoIndex epic." \
  --acceptance "Reindex job row created in reindex_jobs table after merge. Status=pending. Track and commit recorded. >90% test coverage.")
echo "  F5.4 (Post-Merge): $F5_4"

T5_4_1=$(bd create --type task --silent --parent "$F5_4" \
  --title "Implement reindex job creation (placeholder)" \
  -p 1 \
  -l "phase-1,yardmaster,post-merge" \
  -d "After successful Switch(), insert row: INSERT INTO reindex_jobs (track, trigger_commit, status, created_at) VALUES (bead.Track, mergeCommitHash, 'pending', NOW()). No consumer exists yet — CocoIndex epic is deferred. This ensures the interface is ready when CocoIndex is built." \
  --acceptance "Reindex job row created with correct track and commit. Status=pending. No errors when no consumer exists. >90% test coverage.")
echo "    T5.4.1: $T5_4_1"

F5_5=$(bd create --type feature --silent --parent "$E5" \
  --title "Yardmaster CLI & Test Suite" \
  -p 1 \
  -l "phase-1,yardmaster,testing" \
  -d "CLI command to start yardmaster and comprehensive tests. >90% coverage for internal/yardmaster/." \
  --acceptance "ry yardmaster starts agent. Single instance enforced. All unit and integration tests pass. >90% coverage.")
echo "  F5.5 (YM Tests): $F5_5"

T5_5_1=$(bd create --type task --silent --parent "$F5_5" \
  --title "Implement ry yardmaster CLI command" \
  -p 1 \
  -l "phase-1,yardmaster,testing" \
  -d "Cobra subcommand: ry yardmaster. Loads config, connects to Dolt, checks no other yardmaster running (query engines WHERE role=yardmaster AND status != dead), starts yardmaster agent. Single instance per railyard enforced — error if one already running." \
  --acceptance "ry yardmaster starts agent. Second instance returns error. Clean shutdown updates status. >90% test coverage.")
echo "    T5.5.1: $T5_5_1"

T5_5_2=$(bd create --type task --silent --parent "$F5_5" \
  --title "Unit tests for yardmaster operations" \
  -p 1 \
  -l "phase-1,yardmaster,testing" \
  -d "Test: heartbeat staleness detection (engine with last_activity 30s ago = healthy with 60s threshold, 90s ago = stale). Bead reassignment (status transitions, progress note written, old engine dead). Dependency unblocking (bead with all blockers done transitions, bead with partial blockers stays). Reindex job creation (row created with correct fields)." \
  --acceptance "All unit tests pass. Edge cases: no stale engines, multiple stale engines, reassignment of unclaimed bead. >90% coverage.")
echo "    T5.5.2: $T5_5_2"

T5_5_3=$(bd create --type task --silent --parent "$F5_5" \
  --title "Integration tests for merge flow and reassignment" \
  -p 1 \
  -l "phase-1,yardmaster,testing" \
  -d "Integration tests with Dolt and git repo. Test: create bead with branch, add commits, run Switch(), verify merge to main. Test: stalled engine detected, bead reassigned, new engine can claim it. Test: completed backend bead unblocks frontend bead (cross-track). Test: test failure in Switch() blocks bead and sends message." \
  --acceptance "Full merge flow tested end-to-end. Reassignment tested. Cross-track unblocking tested. Test failure handling tested. >90% coverage.")
echo "    T5.5.3: $T5_5_3"

echo ""

# ============================================================
# EPIC 6 FEATURES & TASKS
# ============================================================
echo "--- Epic 6: Dispatch Features & Tasks ---"

F6_1=$(bd create --type feature --silent --parent "$E6" \
  --title "Dispatch Agent Framework" \
  -p 0 \
  -l "phase-1,dispatch,framework" \
  -d "Dispatch package scaffold and system prompt. Claude Code session with planner prompt. Has access to ry car create, ry car dep add, track config. Interactive mode — user types requests. See ARCHITECTURE.md Dispatch section." \
  --acceptance "Dispatch starts as Claude Code session. System prompt includes track definitions and ry commands. Interactive mode works. >90% test coverage.")
echo "  F6.1 (Framework): $F6_1"

T6_1_1=$(bd create --type task --silent --parent "$F6_1" \
  --title "Implement dispatch package scaffold" \
  -p 0 \
  -l "phase-1,dispatch,framework" \
  -d "Create internal/dispatch/dispatch.go. Start(db *gorm.DB, config *config.Config) error — loads system prompt, launches Claude Code in interactive mode (user types directly to it). Registers in engines table with role=dispatch. Manages subprocess: restart on crash, clean shutdown on signal." \
  --acceptance "Dispatch starts Claude Code interactively. User can type requests. Registered in engines table. Restart on crash. >90% test coverage.")
echo "    T6.1.1: $T6_1_1"

T6_1_2=$(bd create --type task --silent --parent "$F6_1" \
  --title "Write dispatch system prompt with track awareness" \
  -p 0 \
  -l "phase-1,dispatch,framework" \
  -d "Create dispatch system prompt. Content: role (planner that decomposes user requests into beads across tracks), track definitions from config (for each track: name, language, conventions, file_patterns — so Dispatch knows what belongs where), available ry commands (ry car create with all flags, ry car dep add, ry car list, ry car ready, ry car children), decomposition examples (from ARCHITECTURE.md Dispatch section — the auth example showing epic/task creation with cross-track deps), rules (one bead per atomic work unit, always set acceptance criteria with >90% test coverage, always set dependencies, use epic for multi-task groups, use task for atomic units)." \
  --acceptance "Prompt includes track definitions, ry commands, examples, and rules. Decomposition guidance clear. >90% test coverage for prompt rendering.")
echo "    T6.1.2: $T6_1_2"

F6_2=$(bd create --type feature --silent --parent "$E6" \
  --title "Work Decomposition" \
  -p 0 \
  -l "phase-1,dispatch,decomposition" \
  -d "Prompt engineering and logic for Dispatch to decompose feature requests into structured bead hierarchies with dependency chains. Creates epics per track, tasks under each, proper ordering." \
  --acceptance "Dispatch creates correct bead structure from natural language requests. Cross-track dependencies set. No cycles. Acceptance criteria on every bead. >90% test coverage.")
echo "  F6.2 (Decomposition): $F6_2"

T6_2_1=$(bd create --type task --silent --parent "$F6_2" \
  --title "Implement work decomposition patterns" \
  -p 0 \
  -l "phase-1,dispatch,decomposition" \
  -d "Dispatch prompt guides Claude Code to: create epic per track when work spans tracks, create tasks under each epic, set priorities (backend foundations = higher priority/lower number), use bead types correctly (epic for containers, task for atomic work, spike for research/unknowns). Prompt includes patterns like: backend model → backend handler → frontend consumer. Validate by testing with the auth example from ARCHITECTURE.md." \
  --acceptance "Dispatch creates correct hierarchy from auth example. Epic per track. Tasks ordered by priority. Types used correctly. >90% test coverage.")
echo "    T6.2.1: $T6_2_1"

T6_2_2=$(bd create --type task --silent --parent "$F6_2" \
  --title "Implement dependency chain creation with cycle detection" \
  -p 0 \
  -l "phase-1,dispatch,decomposition" \
  -d "Dispatch creates dependencies via ry car dep add. Prompt enforces ordering: data model before API handler, API handler before frontend consumer, migration before model. After creating all beads, Dispatch runs ry car dep list on each to verify chain and checks for cycles. If cycles detected, resolves by removing weakest dependency." \
  --acceptance "Dependencies created correctly. No cycles in output. Cross-track deps set (frontend blocked by backend). Dispatch verifies its own work. >90% test coverage.")
echo "    T6.2.2: $T6_2_2"

F6_3=$(bd create --type feature --silent --parent "$E6" \
  --title "Dispatch CLI & Test Suite" \
  -p 1 \
  -l "phase-1,dispatch,testing" \
  -d "CLI command to start Dispatch and tests for decomposition quality. >90% coverage for internal/dispatch/." \
  --acceptance "ry dispatch starts agent. Integration tests verify decomposition quality. >90% coverage.")
echo "  F6.3 (Dispatch Tests): $F6_3"

T6_3_1=$(bd create --type task --silent --parent "$F6_3" \
  --title "Implement ry dispatch CLI command" \
  -p 1 \
  -l "phase-1,dispatch,testing" \
  -d "Cobra subcommand: ry dispatch. Loads config, connects to Dolt, starts dispatch agent in interactive mode. Opens Claude Code in current terminal for direct conversation. User's primary interface for requesting work." \
  --acceptance "ry dispatch starts interactive session. User can type requests. Beads created in Dolt. >90% test coverage.")
echo "    T6.3.1: $T6_3_1"

T6_3_2=$(bd create --type task --silent --parent "$F6_3" \
  --title "Integration tests for multi-track decomposition" \
  -p 1 \
  -l "phase-1,dispatch,testing" \
  -d "Test: give Dispatch the auth feature request from ARCHITECTURE.md ('Add user authentication with JWT backend and React login page'). Verify: backend epic created with tasks (model, endpoint, middleware), frontend epic created with tasks (login page, auth context, protected route), cross-track deps (frontend tasks blocked by backend JWT endpoint), all beads have title+description+acceptance+track+type+parent populated." \
  --acceptance "Auth decomposition matches ARCHITECTURE.md example. All fields populated. Dependencies correct. No cycles. >90% coverage.")
echo "    T6.3.2: $T6_3_2"

echo ""

# ============================================================
# EPIC 7 FEATURES & TASKS
# ============================================================
echo "--- Epic 7: Local Orchestration Features & Tasks ---"

F7_1=$(bd create --type feature --silent --parent "$E7" \
  --title "Lifecycle Commands (start/stop/status)" \
  -p 0 \
  -l "phase-1,orchestration,lifecycle" \
  -d "Core orchestration commands. ry start creates tmux session with Dispatch (pane 0), Yardmaster (pane 1), and N engines. ry stop gracefully shuts down. ry status shows dashboard. See ARCHITECTURE.md Local Development Mode." \
  --acceptance "ry start brings up full railyard in tmux. ry stop graceful shutdown. ry status shows dashboard with engines, beads, messages. >90% test coverage.")
echo "  F7.1 (Lifecycle): $F7_1"

T7_1_1=$(bd create --type task --silent --parent "$F7_1" \
  --title "Implement ry start command" \
  -p 0 \
  -l "phase-1,orchestration,lifecycle" \
  -d "Cobra subcommand: ry start [--engines N]. Flow: (1) validate config.yaml, (2) check Dolt running (ps aux for dolt sql-server, or try connect), (3) ry db init if database doesn't exist, (4) create tmux session 'railyard', (5) pane 0: launch ry dispatch, (6) pane 1: launch ry yardmaster, (7) panes 2..N+1: launch ry engine start --track {assigned_track} for each engine. Engine count from --engines flag or sum of track engine_slots. Track assignment per Feature 7.2 logic." \
  --acceptance "ry start creates tmux session with all components. All agents register in Dolt. Pane layout correct. Engine count configurable. >90% test coverage.")
echo "    T7.1.1: $T7_1_1"

T7_1_2=$(bd create --type task --silent --parent "$F7_1" \
  --title "Implement ry stop command" \
  -p 0 \
  -l "phase-1,orchestration,lifecycle" \
  -d "ry stop [--timeout 60s]. Flow: (1) send 'drain' broadcast message via messaging package, (2) wait for in-progress beads to complete (poll engines table for status=working, up to timeout), (3) kill all engine processes (tmux send-keys C-c or kill), (4) kill yardmaster, (5) kill dispatch, (6) kill tmux session 'railyard', (7) update all engine statuses to dead in Dolt." \
  --acceptance "Graceful shutdown: engines finish current work. Timeout forces kill. All engine statuses updated. Tmux session killed. No orphaned processes. >90% test coverage.")
echo "    T7.1.2: $T7_1_2"

T7_1_3=$(bd create --type task --silent --parent "$F7_1" \
  --title "Implement ry status command" \
  -p 0 \
  -l "phase-1,orchestration,lifecycle" \
  -d "ry status [--watch]. Dashboard output: (1) engine table: ID, track, status, current bead, last activity, uptime. (2) bead summary per track: open/ready/claimed/in_progress/done/blocked counts. (3) message queue depth (unacknowledged count). (4) tmux session status (running/stopped). Table-formatted. --watch refreshes every 5s." \
  --acceptance "Dashboard shows all engines with current state. Bead counts per track correct. Message depth shown. --watch refreshes. >90% test coverage.")
echo "    T7.1.3: $T7_1_3"

F7_2=$(bd create --type feature --silent --parent "$E7" \
  --title "Engine Scaling & Track Assignment" \
  -p 0 \
  -l "phase-1,orchestration,scaling" \
  -d "Dynamic engine count management (1-100). Scale up/down per track. Track assignment proportional to config engine_slots and ready bead counts." \
  --acceptance "ry engine scale adjusts count dynamically. Track assignment proportional. Engine list/restart work. >90% test coverage.")
echo "  F7.2 (Scaling): $F7_2"

T7_2_1=$(bd create --type task --silent --parent "$F7_2" \
  --title "Implement ry engine scale command" \
  -p 0 \
  -l "phase-1,orchestration,scaling" \
  -d "ry engine scale --count 5 --track backend. Calculates delta between desired and current engine count for the track. Scale up: create new tmux panes, launch ry engine start --track {track}. Scale down: send drain messages to excess engines (LIFO — newest engines drained first), wait for current bead completion, kill panes. Respects max engine_slots per track from config." \
  --acceptance "Scale up creates new engine panes. Scale down drains gracefully. Max slots respected. Delta calculation correct. >90% test coverage.")
echo "    T7.2.1: $T7_2_1"

T7_2_2=$(bd create --type task --silent --parent "$F7_2" \
  --title "Implement track assignment logic" \
  -p 0 \
  -l "phase-1,orchestration,scaling" \
  -d "AssignTracks(config *config.Config, totalEngines int) map[string]int — distributes N engines across tracks. Algorithm: proportional to engine_slots in config with floor of 1 per active track. Example: backend=5 slots, frontend=3 slots, infra=2 slots, total=10 engines → backend=5, frontend=3, infra=2. If totalEngines < tracks, prioritize by ready bead count. If ready bead counts available, weight toward tracks with more ready beads." \
  --acceptance "Proportional distribution correct. Floor of 1 per track. Ready bead weighting works. Edge cases: 1 engine, 100 engines, 0 ready beads. >90% test coverage.")
echo "    T7.2.2: $T7_2_2"

T7_2_3=$(bd create --type task --silent --parent "$F7_2" \
  --title "Implement ry engine list and ry engine restart" \
  -p 1 \
  -l "phase-1,orchestration,scaling" \
  -d "ry engine list — table showing all engines: ID, track, status, current bead, last activity, uptime (calculated from started_at). Filters: --track, --status. ry engine restart <id> — kills engine's tmux pane (send C-c, wait, kill), engine deregisters via signal handler, creates new tmux pane with same track, new engine registers." \
  --acceptance "Engine list shows all engines with correct data. Filters work. Restart kills and recreates cleanly. New engine gets new ID. >90% test coverage.")
echo "    T7.2.3: $T7_2_3"

F7_3=$(bd create --type feature --silent --parent "$E7" \
  --title "Orchestration Test Suite" \
  -p 1 \
  -l "phase-1,orchestration,testing" \
  -d "Integration tests for full lifecycle. >90% coverage for orchestration logic." \
  --acceptance "Start/stop lifecycle tested. Engine scaling tested. >90% coverage.")
echo "  F7.3 (Orch Tests): $F7_3"

T7_3_1=$(bd create --type task --silent --parent "$F7_3" \
  --title "Integration tests for start/stop lifecycle" \
  -p 1 \
  -l "phase-1,orchestration,testing" \
  -d "Test: ry start --engines 2 creates tmux session with 4 panes (dispatch, yardmaster, 2 engines). Verify tmux session exists. Verify engines registered in Dolt. Verify ry status shows all components. ry stop kills session and updates engine statuses to dead. Verify no orphaned processes (ps check)." \
  --acceptance "Full lifecycle tested. Tmux session verified. Engine registration verified. Clean shutdown verified. No orphans. >90% coverage.")
echo "    T7.3.1: $T7_3_1"

T7_3_2=$(bd create --type task --silent --parent "$F7_3" \
  --title "Integration tests for engine scaling" \
  -p 1 \
  -l "phase-1,orchestration,testing" \
  -d "Test: start with 2 engines, ry engine scale --count 5 --track backend, verify 3 new panes created and 3 new engines registered. Scale down: ry engine scale --count 1 --track backend, verify drain messages sent, excess engines shut down, only 1 remains. Verify track assignment logic with multiple tracks." \
  --acceptance "Scale up verified (new panes, new registrations). Scale down verified (drain, shutdown). Track assignment correct. >90% coverage.")
echo "    T7.3.2: $T7_3_2"

echo ""

# ============================================================
# DEPENDENCIES
# ============================================================
echo "=== Setting up dependencies ==="

# Epic-level dependencies
echo "--- Epic dependencies ---"
bd dep "$E1" --blocks "$E2" 2>&1 | head -1
bd dep "$E2" --blocks "$E3" 2>&1 | head -1
bd dep "$E3" --blocks "$E4" 2>&1 | head -1
bd dep "$E3" --blocks "$E6" 2>&1 | head -1
bd dep "$E4" --blocks "$E5" 2>&1 | head -1
bd dep "$E5" --blocks "$E7" 2>&1 | head -1
bd dep "$E6" --blocks "$E7" 2>&1 | head -1

# Feature-level dependencies (within epics — sequential)
echo "--- Feature dependencies (Epic 1) ---"
bd dep "$F1_1" --blocks "$F1_2" 2>&1 | head -1
bd dep "$F1_2" --blocks "$F1_3" 2>&1 | head -1
bd dep "$F1_3" --blocks "$F1_4" 2>&1 | head -1
bd dep "$F1_4" --blocks "$F1_5" 2>&1 | head -1

echo "--- Feature dependencies (Epic 2) ---"
bd dep "$F1_4" --blocks "$F2_1" 2>&1 | head -1  # cross-epic: DB layer → Bead CRUD
bd dep "$F2_1" --blocks "$F2_2" 2>&1 | head -1
bd dep "$F2_2" --blocks "$F2_3" 2>&1 | head -1
bd dep "$F2_3" --blocks "$F2_4" 2>&1 | head -1

echo "--- Feature dependencies (Epic 3) ---"
bd dep "$F2_2" --blocks "$F3_1" 2>&1 | head -1  # cross-epic: Ready detection → Engine lifecycle
bd dep "$F3_1" --blocks "$F3_2" 2>&1 | head -1
bd dep "$F3_2" --blocks "$F3_3" 2>&1 | head -1
bd dep "$F3_3" --blocks "$F3_4" 2>&1 | head -1
bd dep "$F3_4" --blocks "$F3_5" 2>&1 | head -1

echo "--- Feature dependencies (Epic 4) ---"
bd dep "$F3_1" --blocks "$F4_1" 2>&1 | head -1  # cross-epic: Engine lifecycle → Message core
bd dep "$F4_1" --blocks "$F4_2" 2>&1 | head -1
bd dep "$F4_2" --blocks "$F4_3" 2>&1 | head -1
bd dep "$F4_3" --blocks "$F4_4" 2>&1 | head -1

echo "--- Feature dependencies (Epic 5) ---"
bd dep "$F4_2" --blocks "$F5_1" 2>&1 | head -1  # cross-epic: Engine inbox → YM framework
bd dep "$F5_1" --blocks "$F5_2" 2>&1 | head -1
bd dep "$F5_2" --blocks "$F5_3" 2>&1 | head -1
bd dep "$F5_3" --blocks "$F5_4" 2>&1 | head -1
bd dep "$F5_4" --blocks "$F5_5" 2>&1 | head -1

echo "--- Feature dependencies (Epic 6) ---"
bd dep "$F3_4" --blocks "$F6_1" 2>&1 | head -1  # cross-epic: Engine CLI → Dispatch framework
bd dep "$F6_1" --blocks "$F6_2" 2>&1 | head -1
bd dep "$F6_2" --blocks "$F6_3" 2>&1 | head -1

echo "--- Feature dependencies (Epic 7) ---"
bd dep "$F5_5" --blocks "$F7_1" 2>&1 | head -1  # cross-epic: YM tests → Lifecycle
bd dep "$F6_3" --blocks "$F7_1" 2>&1 | head -1  # cross-epic: Dispatch tests → Lifecycle
bd dep "$F7_1" --blocks "$F7_2" 2>&1 | head -1
bd dep "$F7_2" --blocks "$F7_3" 2>&1 | head -1

echo ""
echo "=== All beads created successfully ==="
echo ""
echo "Summary:"
echo "  Epics: 7"
echo "  Features: 22"
echo "  Tasks: ~50"
echo ""

# Show final count
bd list --json 2>/dev/null | python3 -c "
import sys, json
data = json.load(sys.stdin)
types = {}
for d in data:
    t = d.get('issue_type', 'unknown')
    types[t] = types.get(t, 0) + 1
print('Issue counts by type:')
for t, c in sorted(types.items()):
    print(f'  {t}: {c}')
print(f'  TOTAL: {sum(types.values())}')
" 2>/dev/null || bd list 2>&1 | tail -5
