# Agent Instructions

This project uses **bd** (beads) for issue tracking. Run `bd onboard` to get started.

## Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --status in_progress  # Claim work
bd close <id>         # Complete work
bd sync               # Sync with git
```

## Pre-Commit: Lint and Format

**Before every commit**, run these checks. Do NOT commit or push code that fails.

```bash
# Go formatting (MUST pass — CI will reject unformatted code)
gofmt -l .
# If any files listed, fix with: gofmt -w .

# Build check
go build ./...

# Tests
go test ./...
```

**CRITICAL:** Always run `gofmt -l .` before committing Go code. If any files are listed, run `gofmt -w .` to fix them before staging and committing.

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - `gofmt -l .`, tests, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   gofmt -l .        # MUST produce no output
   go test ./...     # MUST pass
   git pull --rebase
   bd sync
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds

