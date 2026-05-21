# Agent Instructions

**RELEASE MANDATORY**
Make sure to tag the version within helm chart when performing a release. Anything tagged in github needs to be tagged for docker, and helm.

## Code Search & File Structure (MANDATORY)

**Always use the `railyard_codesearch` MCP server** for code search and project structure exploration. Do NOT use `grep`, `rg`, `find`, `ls -R`, or `Explore`/`general-purpose` agents for these tasks.

- `mcp__railyard_codesearch__search_code` — search for symbols, identifiers, strings, or patterns across the codebase
- `mcp__railyard_codesearch__get_project_structure` — get the file/directory layout

**Why:** the index is rebuilt on every commit, so it stays live and authoritative — searches against it are faster and more accurate than ad-hoc shell traversal.

**Freshness caveat:** the index reflects the **last commit**, not the working tree. For uncommitted edits, read the file directly (`Read`) or commit first. After a commit lands, the index is current again.

**Exceptions** (use shell tools): operating on the working tree (uncommitted diffs, untracked files), filesystem metadata (sizes, perms, mtimes), or output of running processes.

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

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd dolt push
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
<!-- END BEADS INTEGRATION -->
