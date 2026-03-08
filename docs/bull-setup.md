# Bull Setup Guide

Bull is Railyard's GitHub issue triage daemon. It connects your GitHub Issues to Railyard's car-based work system, automatically triaging new issues with AI, creating cars from accepted issues, and syncing statuses back via labels.

## Overview

Bull runs a six-phase polling cycle:

1. **Poll** — fetch new/updated issues from the GitHub API
2. **Filter tracked** — skip issues already in the `bull_issues` table
3. **Heuristic filter** — fast rejection of spam, duplicates, and known non-actionable patterns
4. **AI triage** — send passing issues through the configured AI provider for severity assessment, track assignment, and effort estimation
5. **Reverse sync** — update GitHub labels based on car status changes (under review -> in progress -> fix merged)
6. **Release scan** — detect merged PRs / releases and close resolved issues

## Prerequisites

- **GitHub Personal Access Token (PAT)** with these scopes:
  - `repo` (for reading issues and writing labels/comments on private repos)
  - OR `public_repo` (for public repos only)
- Railyard instance with Dolt database initialized (`ry db init`)
- At least one track configured in your `railyard.yaml`

## Configuration

Add to your `railyard.yaml`:

```yaml
bull:
  enabled: true
  github_token: ${GITHUB_TOKEN}
  poll_interval_sec: 60
  triage_mode: standard
  comments:
    enabled: true
    answer_questions: true
  labels:
    under_review: "bull: under review"
    in_progress: "bull: in progress"
    fix_merged: "bull: fix merged"
    ignore: "bull: ignore"
```

Set the environment variable:

```bash
export GITHUB_TOKEN="ghp-your-token-here"
```

Token fields support `${ENV_VAR}` substitution — set secrets as environment variables rather than hardcoding them.

## Configuration Reference

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `bull.enabled` | bool | `false` | Enable the Bull daemon |
| `bull.github_token` | string | (required) | GitHub PAT with `repo` or `public_repo` scope |
| `bull.poll_interval_sec` | int | `60` | How often to poll GitHub for new/updated issues (seconds) |
| `bull.triage_mode` | string | `"standard"` | Triage mode: `"standard"` or `"full"` |
| `bull.comments.enabled` | bool | `false` | Post comments on rejected issues |
| `bull.comments.reject_template` | string | `""` | Custom template for rejection comments |
| `bull.comments.answer_questions` | bool | `false` | Answer questions detected in issues |
| `bull.labels.under_review` | string | `"bull: under review"` | Label applied during triage |
| `bull.labels.in_progress` | string | `"bull: in progress"` | Label applied when a car is claimed by an engine |
| `bull.labels.fix_merged` | string | `"bull: fix merged"` | Label applied when the car's branch is merged |
| `bull.labels.ignore` | string | `"bull: ignore"` | Label applied to rejected or manually ignored issues |

## Label Scheme

Bull uses four GitHub labels to track issue lifecycle:

- **"bull: under review"** — applied when Bull picks up an issue for triage. The issue is being assessed by heuristic filters and/or AI.
- **"bull: in progress"** — applied when a car is created from the issue and an engine claims it for work.
- **"bull: fix merged"** — applied when the car's branch is merged back to main.
- **"bull: ignore"** — applied by Bull to rejected issues, or manually applied by humans to skip issues entirely. Issues with this label are never triaged.

Labels are additive (old labels are not removed) so the full history is visible on the issue.

## Triage Modes

### Standard Mode

The default mode. Heuristic filters run first to quickly reject spam, duplicates, and known non-actionable patterns. Only issues that pass heuristics go through AI triage. This is faster and lower cost.

### Full Mode

All issues go directly to AI triage regardless of heuristic results. More thorough but higher API cost. Use this when you want every issue assessed by AI, even ones that look like spam or duplicates.

Set the mode in your config:

```yaml
bull:
  triage_mode: full    # "standard" (default) or "full"
```

## One-Shot Triage

```bash
ry bull triage -c railyard.yaml --issue 42
```

Triages a single issue without starting the daemon. Useful for testing your configuration or manually processing a specific issue. The issue goes through the same triage pipeline (heuristic + AI or AI-only depending on `triage_mode`), and labels/comments are applied as configured.

## Question Answering

When `comments.answer_questions` is `true`, Bull detects questions in issues and posts answers based on codebase context. This lets Bull serve as a first-responder for "how does X work?" or "where is Y configured?" style issues.

```yaml
bull:
  comments:
    enabled: true
    answer_questions: true
```

## Running Bull

```bash
ry bull start -c railyard.yaml   # Start the Bull daemon
```

Bull runs in the foreground and polls GitHub on the configured interval. Use tmux or a process manager to keep it running in the background.

## Kubernetes Deployment

Bull runs as an optional Deployment in the Helm chart. Enable with `bull.enabled: true` in Helm values. The Deployment needs `GITHUB_TOKEN` from the auth secret. Bull does not need a git repo volume — it only interacts with GitHub via the API and Railyard via the database.

See [`charts/railyard/README.md`](../charts/railyard/README.md) for Helm values reference.

## Troubleshooting

### "bull.github_token is required"

The token is not set or the environment variable is not resolved. Verify that `GITHUB_TOKEN` is exported in your shell and that the config uses `${GITHUB_TOKEN}` syntax.

### Rate limiting

Bull backs off automatically when it hits GitHub API rate limits. If you see frequent rate limiting, increase `poll_interval_sec`. The default of 60 seconds is safe for most repos.

### Labels not appearing on issues

Ensure your PAT has the `repo` scope (for private repos) or `public_repo` scope (for public repos). The `public_repo` scope alone is not sufficient for private repositories.

### Issues not being triaged

- Check that `triage_mode` is set correctly (`"standard"` or `"full"`)
- Verify the issue does not already have the `"bull: ignore"` label
- Check that the issue is not already tracked in the `bull_issues` table
- Ensure Bull is polling the correct repository (check `repo` in your `railyard.yaml`)
