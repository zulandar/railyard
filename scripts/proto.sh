#!/usr/bin/env bash
# Regenerate the railyard plugin gRPC stubs from pkg/plugin/proto/v1/plugin.proto.
#
# Usage: scripts/proto.sh [--check]
#
#   (no flag) — regenerate stubs in place and run lint + breaking checks
#   --check  — fail if regeneration would change anything (CI mode)
#
# Required tools (install with the commands shown if missing):
#   buf                go install github.com/bufbuild/buf/cmd/buf@latest
#   protoc-gen-go      go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   protoc-gen-go-grpc go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
#
# The breaking-change baseline is the last-merged contract on the main
# branch — `buf breaking --against '.git#ref=refs/remotes/origin/main'` — not a committed
# snapshot. There is nothing to refresh after a deliberate wire change:
# the new contract becomes the baseline as soon as it merges to main. The
# compat test (pkg/plugin/proto/v1/compat_test.go) runs the same check,
# and the CI "Proto" job is the authoritative, non-skippable gate.

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# Make tools installed via `go install` discoverable even when GOBIN is
# not on the user's PATH (a common state on dev laptops).
if command -v go >/dev/null 2>&1; then
  GOBIN=$(go env GOBIN)
  if [[ -z "$GOBIN" ]]; then
    GOBIN="$(go env GOPATH)/bin"
  fi
  if [[ -d "$GOBIN" ]] && [[ ":$PATH:" != *":$GOBIN:"* ]]; then
    export PATH="$GOBIN:$PATH"
  fi
fi

CHECK=0
if [[ "${1:-}" == "--check" ]]; then
  CHECK=1
fi

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "scripts/proto.sh: missing required tool '$1'" >&2
    echo "  see header of this script for install commands" >&2
    exit 1
  fi
}

require buf
require protoc-gen-go
require protoc-gen-go-grpc

if (( CHECK )); then
  TMPDIR=$(mktemp -d)
  trap 'rm -rf "$TMPDIR"' EXIT
  cp pkg/plugin/proto/v1/plugin.pb.go "$TMPDIR/plugin.pb.go.orig"
  cp pkg/plugin/proto/v1/plugin_grpc.pb.go "$TMPDIR/plugin_grpc.pb.go.orig"
fi

buf lint
buf generate

if (( CHECK )); then
  if ! diff -q pkg/plugin/proto/v1/plugin.pb.go "$TMPDIR/plugin.pb.go.orig" >/dev/null \
     || ! diff -q pkg/plugin/proto/v1/plugin_grpc.pb.go "$TMPDIR/plugin_grpc.pb.go.orig" >/dev/null; then
    echo "scripts/proto.sh --check: generated stubs are out of date; run scripts/proto.sh and commit the result" >&2
    exit 1
  fi
fi

# Breaking-change check against the last-merged contract on main, so a
# wire-incompatible edit is caught even when proto and baseline are
# touched in the same commit. Additive changes (new fields, new enum
# values, new messages) pass; renames, renumbers, and removals fail.
#
# Resolve a baseline ref locally: prefer the remote-tracking origin/main,
# then a local main. If neither exists (a shallow clone, or a checkout
# that has never seen main) skip the breaking check with a warning rather
# than fail regeneration — the CI Proto job remains the authoritative
# gate.
MAIN_REF=""
for ref in refs/remotes/origin/main refs/heads/main; do
  if git rev-parse --verify --quiet "$ref" >/dev/null; then
    MAIN_REF="$ref"
    break
  fi
done

if [[ -n "$MAIN_REF" ]]; then
  buf breaking --against ".git#ref=${MAIN_REF}"
else
  echo "scripts/proto.sh: no main ref (origin/main or main) to compare against;" >&2
  echo "  skipping breaking-change check. CI runs the authoritative gate." >&2
fi
