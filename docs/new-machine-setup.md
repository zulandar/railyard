# Setting Up Railyard on a New Machine

If you've cloned a repo that already has a `railyard.yaml` committed, you don't need to run `ry init` — that command is for first-time setup and will try to overwrite your existing config. Instead, just set up the infrastructure and initialize the database.

## Prerequisites

Make sure you have the [prerequisites](../README.md#prerequisites) installed:

- Go 1.25+
- MySQL 8.0+
- tmux
- At least one AI coding CLI (Claude Code, Codex, Gemini, or OpenCode)
- Docker (optional, for CocoIndex semantic search)
- Python 3.13+ (optional, for CocoIndex)

## Steps

**1. Build the CLI**

```bash
go build -o ry ./cmd/ry/
```

Or install it to your `$GOPATH/bin`:

```bash
go install ./cmd/ry/
```

**2. Start MySQL**

```bash
ry db start -c railyard.yaml
```

If this is the first time running MySQL on this machine, it will create the data directory and start the server. If MySQL is already running, this is a no-op.

**3. Initialize the database**

```bash
ry db init -c railyard.yaml
```

This creates the database, runs migrations, and seeds tracks from your config. It's idempotent — safe to run multiple times.

**4. (Optional) Set up semantic code search**

```bash
ry cocoindex init -c railyard.yaml
```

This starts a pgvector Docker container, creates a Python venv, installs dependencies, and runs schema migrations.

**5. Start Railyard**

```bash
ry start -c railyard.yaml --engines 2
```

**6. Verify everything is healthy**

```bash
ry doctor -c railyard.yaml
```

## After a Reboot

MySQL doesn't survive WSL/system restarts. Restart it before using Railyard:

```bash
ry db start -c railyard.yaml
```

If you're using CocoIndex, make sure Docker is running so the pgvector container is available.
