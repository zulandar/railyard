# Contributing to Railyard

Thanks for your interest in contributing to Railyard! This guide covers everything you need to get started.

## Prerequisites

- **Go 1.25+**
- **Dolt** — version-controlled SQL database ([install](https://docs.dolthub.com/introduction/installation))
- **tmux** — terminal multiplexer
- **AI coding CLI** (at least one) — Claude Code (default): `npm install -g @anthropic-ai/claude-code`; alternatives: Codex, Gemini, OpenCode (see README for install commands)
- **Docker** (optional) — for pgvector/CocoIndex semantic search
- **Python 3.13+** (optional) — for CocoIndex semantic search

## Getting Started

1. Fork and clone the repository:

```bash
git clone https://github.com/<your-username>/railyard.git
cd railyard
```

2. Build the CLI:

```bash
go build -o ry ./cmd/ry/
```

3. Run the quickstart to set up Dolt and the database:

```bash
./quickstart.sh
```

Or follow the [Manual Setup](README.md#manual-setup) steps in the README.

4. Verify everything works:

```bash
go test ./... -count=1 -timeout 300s -race
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
    providers/       AI CLI provider implementations (Claude, Codex, Gemini, OpenCode)
  messaging/         Agent-to-agent message passing via DB
  models/            GORM models (Car, Engine, Message, Track, etc.)
  orchestration/     tmux session management, start/stop/scale/status
  yardmaster/        Yardmaster supervisor: health checks, switch/merge
  telegraph/         Telegraph chat bridge: adapters, routing, watcher, digests
    slack/           Slack Socket Mode adapter
    discord/         Discord Gateway adapter
  dashboard/         Web UI server with SSE
  logutil/           Logging utilities
cocoindex/           Python-based semantic search (CocoIndex + pgvector)
docker/              Docker Compose files (pgvector)
.github/workflows/   CI and release pipelines
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for a deep dive into the system design.

## Development Workflow

### Branching

- Work off `main`. Create a feature branch for your changes.
- Branch names: `feature/short-description`, `fix/short-description`, or `chore/short-description`.

### Code Quality

Before every commit, run these checks. CI will reject code that fails any of them.

```bash
# Format (MUST pass — CI rejects unformatted code)
gofmt -l .
# If any files listed, fix with:
gofmt -w .

# Vet
go vet ./...

# Build
go build ./...

# Tests (with race detector)
go test ./... -count=1 -timeout 300s -race
```

### Code Style

- **Go**: stdlib-first, no frameworks (except Gin for HTTP and Cobra for CLI). Table-driven tests using the standard `testing` package.
- **Formatting**: `gofmt` is the law. No exceptions.
- **Imports**: stdlib first, then third-party, then internal packages.
- **Error handling**: Return errors, don't panic. Wrap errors with context using `fmt.Errorf("doing X: %w", err)`.
- **Naming**: Follow standard Go conventions. Exported names get doc comments.
- **Tests**: Every new feature or bug fix should include tests. Files follow the `_test.go` convention alongside the code they test.

### Python (CocoIndex)

The `cocoindex/` directory contains Python code for semantic search. If you're modifying it:

```bash
cd cocoindex
python -m pytest
```

### Commit Messages

Write clear, concise commit messages. Use the imperative mood:

- `fix(engine): handle stall detection timeout`
- `feat(telegraph): add Discord adapter`
- `test(car): add dependency cycle detection tests`
- `chore: update Go dependencies`

Format: `type(scope): description`

Types: `feat`, `fix`, `test`, `chore`, `docs`, `refactor`, `style`, `perf`

## Testing

### Unit Tests

```bash
# Run all tests
go test ./...

# Run tests for a specific package
go test ./internal/engine/...

# Run with verbose output
go test -v ./internal/car/...

# Run a specific test
go test -v -run TestClaimCar ./internal/engine/...
```

### Integration Tests

Integration tests require a running Dolt instance and are tagged accordingly:

```bash
# These run automatically if Dolt is available
go test ./... -count=1 -timeout 300s
```

### Race Detection

CI runs tests with `-race`. Always test with it locally to catch data races:

```bash
go test ./... -race
```

## Submitting Changes

1. Ensure all checks pass (`gofmt`, `go vet`, `go test ./... -race`).
2. Push your branch and open a pull request against `main`.
3. Fill out the PR description with:
   - What the change does and why
   - How to test it
   - Any breaking changes
4. CI will run automatically — all checks must pass before merge.

## Reporting Issues

Use [GitHub Issues](https://github.com/zulandar/railyard/issues) to report bugs or request features. Include:

- Steps to reproduce (for bugs)
- Expected vs actual behavior
- Environment details (OS, Go version, Dolt version)

## Architecture Overview

Railyard coordinates multiple AI coding agents:

- **Dispatch** — decomposes requests into structured work items (cars)
- **Engines** — worker agents (Claude, Codex, Gemini, or OpenCode) that claim and execute cars on isolated branches
- **Yardmaster** — supervisor that monitors engines, runs tests, and merges completed work
- **Telegraph** — chat bridge for Slack/Discord integration

All state lives in **Dolt** (version-controlled MySQL). See [ARCHITECTURE.md](ARCHITECTURE.md) for details.

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
