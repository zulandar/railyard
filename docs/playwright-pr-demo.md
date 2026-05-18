# Playwright PR Demo Setup Guide

Playwright PR Demo is an opt-in, per-track feature that instructs engines working on a track to commit a single Playwright spec demonstrating the user-visible behavior of their change. Yardmaster then surfaces a pointer to that spec in the draft PR body so reviewers know where to look.

Railyard does **not** run Playwright. Your project's own CI is expected to execute the spec (with video recording on) and surface the recording. Railyard's scope is the engine prompt instruction and the PR-body pointer.

## Overview

When enabled on a track, two things change:

1. **Engine prompt** — when a car on that track is dispatched, the engine prompt gains a "Required: Playwright PR Demo" section instructing the engine to write a new spec at a deterministic path (e.g. `tests/pr-demos/car-abc123.spec.ts`). The spec is treated as part of acceptance; the engine is told its work is not complete without it.

2. **PR body** — when Yardmaster opens the draft PR for the car, the PR body gains a "Playwright Demo" section pointing reviewers at the spec file. This is unconditional once the track has the block enabled — there is no presence check, by design (see [Limitations](#limitations)).

When the block is absent or `enabled: false`, behavior is unchanged from before — no prompt change, no PR-body change, no validation cost.

## Why

A reviewer on a frontend PR today has no UI-level evidence of the change — they either trust the engine's prose summary or check out the branch and run it. The Playwright spec gives them:

- A recording attached to the PR they can watch without checking out the branch (assuming project CI is wired up to record + surface)
- Self-verification on the agent side: producing the spec forces the engine to think through the user-visible flow, and the spec itself is an acceptance artifact regardless of whether downstream CI runs it

## Prerequisites

- A Railyard project with at least one track you want to enable this on (typically frontend or full-stack).
- Project CI configured to run Playwright with video recording on. Railyard does not provide or ship a CI workflow — see the [CI responsibilities](#ci-responsibilities) section for what's expected.
- (Optional) A starter Playwright spec template the engine can copy from when writing new specs.

## Configuration

### Local / `railyard.yaml`

Add a `playwright:` block to the track:

```yaml
tracks:
  - name: frontend
    language: typescript
    engine_slots: 3
    test_command: "npm test"
    playwright:
      enabled: true
      spec_path: "tests/pr-demos"                   # required when enabled
      filename: "{car_id}.spec.ts"                  # optional, this is the default
      template: "tests/pr-demos/_template.spec.ts"  # optional starter spec
```

### Helm / k8s

The chart templates the same block under each track. Keys are camelCase in `values.yaml` and rendered to snake_case in the pod's `railyard.yaml`:

```yaml
tracks:
  - name: frontend
    language: typescript
    engineSlots: 3
    testCommand: "npm test"
    playwright:
      enabled: true
      specPath: "tests/pr-demos"
      filename: "{car_id}.spec.ts"
      template: "tests/pr-demos/_template.spec.ts"
```

After updating values, `helm upgrade` to apply. Yardmaster re-reads the rendered `railyard.yaml` at PR-open time, so changes take effect on the next car switch without a Yardmaster restart.

## Field reference

| Field | Type | Required when enabled | Description |
|---|---|---|---|
| `enabled` | bool | — | Gate for the feature on this track. When `false` or the block is absent, nothing changes. |
| `spec_path` | string | yes | Directory (relative to the repo root) where engines write new spec files. Config load fails if missing when `enabled: true`. |
| `filename` | string | no | Naming pattern for new specs. `{car_id}` is substituted with the dispatched car's ID. Defaults to `{car_id}.spec.ts` when omitted. |
| `template` | string | no | Optional path (relative to the repo root) to a starter spec the engine can copy from. The "Start from the template" bullet only appears in the engine prompt when this is set AND the file exists in the engine's worktree at dispatch time. The file is not validated for existence at config-parse time. |

## What the engine sees

When a car on a Playwright-enabled track is dispatched, this section is appended to the engine prompt:

```
## Required: Playwright PR Demo

This track requires a Playwright spec that demonstrates the user-visible
behavior of your change. This is part of acceptance — your work is not
complete without it.

- Write a NEW spec at: tests/pr-demos/car-abc123.spec.ts
- Exercise the change end-to-end through the UI as a real user would
- Use existing page objects/fixtures/base URL config where present
- Start from the template at tests/pr-demos/_template.spec.ts
- Project CI will run this spec on the PR with video recording; the
  recording is the artifact reviewers will check
```

The "Start from the template" bullet is conditional on `template` being set AND the template file existing in the worktree at dispatch time. If the template path is configured but the file is missing, the bullet is omitted (so the engine isn't pointed at a file that isn't there).

## What the PR body contains

When Yardmaster opens the draft PR for a car whose track has Playwright enabled at PR-open time, the PR body gains this section:

```markdown
## 📹 Playwright Demo

A demo spec has been added at `tests/pr-demos/car-abc123.spec.ts`.
Once CI completes, the recording is available on the workflow run for this PR.
```

The path is rendered from the current (PR-open-time) track config. If you change the track's `playwright` block between dispatch and PR-open — for example, disabling it after the car was dispatched, or changing the `spec_path` — the PR body reflects the current config, not the dispatch-time config.

## CI responsibilities

Railyard's scope ends with the engine prompt and the PR-body pointer. The project's own CI is expected to:

1. **Run the spec on the PR.** A typical setup scopes test execution to files changed in the PR diff (so each PR runs only its new demo, not the whole accumulated history).
2. **Record video.** Configure Playwright's `use.video: 'on'` (or equivalent) so a recording is produced for every test.
3. **Surface the recording.** Upload the video as a workflow artifact, post a comment with a link, or otherwise make it reachable from the PR.

The PR body deliberately doesn't link to a specific CI artifact URL — Railyard has no general way to know how a given project's CI exposes recordings.

### Suggested filename convention

The default `{car_id}.spec.ts` filename makes diff-scoped CI trivial:

- Each PR introduces exactly one new spec file.
- Restricting Playwright execution to the PR's changed files runs only the new demo.
- Old demos accumulate in the repo as historical artifacts but are not re-executed on subsequent PRs.

You can change the filename pattern, but if you do, make sure your diff-scoping logic still works.

## Limitations

These are deliberate choices for the initial version. They may change later if real usage shows they're a problem.

- **No presence check.** If the engine fails to produce the spec, the PR body still claims it exists. The missing file shows up in the PR diff and is caught by the reviewer (or the Inspection Pit agent) as an acceptance miss. Railyard does not gate PR open on file presence.
- **No spec-content validation.** Railyard does not check that the spec actually exercises the changed files — only that the engine was told to write one.
- **One demo per car.** Cars that span multiple user-visible flows still get only one spec at the deterministic path. If you need multiple, you'd need to split the work across cars.
- **No reusable CI workflow shipped.** Railyard does not provide a GitHub Actions workflow for running Playwright + uploading recordings. You wire that up project-side.

## Troubleshooting

**Engine prompt doesn't contain the section.**
Check that the track in `railyard.yaml` has `playwright.enabled: true` AND `spec_path` set. If `enabled: true` without `spec_path`, config load fails — check the daemon logs for `track "<name>" has playwright.enabled but missing spec_path`. On k8s, run `kubectl get configmap <release>-config -o yaml` and confirm the `playwright:` block is present in the rendered `railyard.yaml`.

**PR body doesn't contain the section.**
The PR-body section is generated by Yardmaster, which re-reads `railyard.yaml` at PR-open time. If you enabled the block but the PR body is missing the section, check that Yardmaster is using the updated config (on k8s, after `helm upgrade`, configmap changes propagate to pods on the next mount sync — usually under a minute).

**Template bullet missing from the engine prompt.**
The template bullet is conditional on the template file existing in the engine's worktree at dispatch time. Verify the path (relative to the repo root) is correct and the file exists on the branch the engine was dispatched against.

## Related

- [`railyard.example.yaml`](../railyard.example.yaml) — the schema documented inline alongside other track fields
- [Bull Setup Guide](bull-setup.md) — for the GitHub issue triage daemon (different feature, similar opt-in pattern)
- [Inspection Pit](../README.md#inspection-pit-automated-pr-review) — the automated PR review daemon that may flag missing specs as acceptance failures
