# Agent Instructions

**RELEASE MANDATORY**
Make sure to tag the version within helm chart when performing a release. Anything tagged in github needs to be tagged for docker, and helm.

## Pre-Commit: Lint and Format

**Before every commit**, run these checks. Do NOT commit or push code that fails.

```bash
# Lint (includes gofmt enforcement — CI will reject unformatted code)
golangci-lint run

# Build check
go build ./...

# Tests
go test ./...
```

**CRITICAL:** Always run `golangci-lint run` before committing Go code. This enforces `gofmt` formatting and additional lint checks.

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - `golangci-lint run`, tests, builds
3. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   golangci-lint run # MUST pass
   go test ./...     # MUST pass
   git pull --rebase
   git push
   git status  # MUST show "up to date with origin"
   ```
4. **Clean up** - Clear stashes, prune remote branches
5. **Verify** - All changes committed AND pushed
6. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
