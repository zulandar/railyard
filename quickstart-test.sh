#!/usr/bin/env bash
set -euo pipefail

# Railyard Quickstart Test — Isolated in /tmp
#
# Tests Railyard without modifying the source repo. Creates a fully isolated
# environment at /tmp/railyard-test with its own Dolt server (port 3307),
# dummy git repo, and sample cars.
#
# Run from the railyard source repo root:
#   chmod +x quickstart-test.sh && ./quickstart-test.sh
#
# What it creates:
#   /tmp/railyard-test/
#     project/           — dummy git repo (engines work here)
#       ry               — compiled binary
#       railyard.yaml    — config (Dolt :3307, owner testuser)
#     dolt-data/         — isolated Dolt database files
#     dolt.log           — Dolt server log
#     test-output.txt    — test results
#
# Clean up:
#   ./quickstart-test.sh --clean

# ─── Helpers ──────────────────────────────────────────────────────────────────

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info()  { printf "${BLUE}[info]${NC}  %s\n" "$1"; }
ok()    { printf "${GREEN}[ok]${NC}    %s\n" "$1"; }
warn()  { printf "${YELLOW}[warn]${NC}  %s\n" "$1"; }
fail()  { printf "${RED}[error]${NC} %s\n" "$1"; exit 1; }

check_cmd() { command -v "$1" &>/dev/null; }

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEST_DIR="/tmp/railyard-test"
PROJECT_DIR="${TEST_DIR}/project"
DOLT_DATA="${TEST_DIR}/dolt-data"
DOLT_PORT=3307

# ─── Handle --clean flag ─────────────────────────────────────────────────────

if [ "${1:-}" = "--clean" ]; then
    info "Cleaning up test environment..."
    (cd "${PROJECT_DIR}" 2>/dev/null && "${PROJECT_DIR}/ry" stop -c railyard.yaml 2>/dev/null) || true
    tmux kill-session -t railyard 2>/dev/null || true
    pkill -f "dolt sql-server.*--port ${DOLT_PORT}" 2>/dev/null || true
    sleep 1
    rm -rf "${TEST_DIR}"
    rm -f "${HOME}/.local/bin/ry"
    ok "Cleaned: ${TEST_DIR} removed, ry unlinked, Dolt stopped."
    exit 0
fi

if [ ! -f "${REPO_DIR}/go.mod" ]; then
    fail "Run this from the railyard source repo root."
fi

echo ""
echo "╔══════════════════════════════════════════════════════╗"
echo "║       Railyard Quickstart Test — Isolated /tmp       ║"
echo "╚══════════════════════════════════════════════════════╝"
echo ""
info "Source repo: ${REPO_DIR}"
info "Test env:    ${TEST_DIR}"
info "Dolt port:   ${DOLT_PORT} (isolated from default 3306)"
echo ""

# ─── Step 1: Verify prerequisites ────────────────────────────────────────────

info "Checking prerequisites..."
check_cmd go   || fail "Go required. Install from https://go.dev/dl/"
check_cmd dolt || fail "Dolt required. Install: sudo bash -c 'curl -L https://github.com/dolthub/dolt/releases/latest/download/install.sh | bash'"
check_cmd tmux || fail "tmux required. Install: sudo apt install tmux"
ok "go $(go version | grep -oP '\d+\.\d+' | head -1), dolt $(dolt version 2>&1 | grep -oP '\d+\.\d+\.\d+' | head -1), tmux $(tmux -V 2>&1 | grep -oP '[\d.]+')"

if check_cmd claude; then
    ok "Claude Code CLI found"
else
    warn "Claude Code CLI not found — engines need it."
    warn "Install: npm install -g @anthropic-ai/claude-code"
    echo ""
fi

# ─── Step 2: Build ry ────────────────────────────────────────────────────────

info "Building ry..."
mkdir -p "${TEST_DIR}"
(cd "${REPO_DIR}" && go build -o "${TEST_DIR}/ry-binary" ./cmd/ry/)
ok "Built ry"

# ─── Step 3: Clean previous test env (if any) ────────────────────────────────

if pgrep -f "dolt sql-server.*--port ${DOLT_PORT}" > /dev/null 2>&1; then
    info "Stopping previous Dolt on port ${DOLT_PORT}..."
    pkill -f "dolt sql-server.*--port ${DOLT_PORT}" 2>/dev/null || true
    sleep 2
fi
tmux kill-session -t railyard 2>/dev/null || true

# Remove old test dirs but keep the binary we just built.
rm -rf "${PROJECT_DIR}" "${DOLT_DATA}" "${TEST_DIR}/dolt.log"

# ─── Step 4: Create project directory ─────────────────────────────────────────

info "Creating test project..."
mkdir -p "${PROJECT_DIR}" "${DOLT_DATA}"

# Copy binary into project dir (engines run from here).
cp "${TEST_DIR}/ry-binary" "${PROJECT_DIR}/ry"
chmod +x "${PROJECT_DIR}/ry"

# Put ry on PATH for tmux panes.
mkdir -p "${HOME}/.local/bin"
ln -sf "${PROJECT_DIR}/ry" "${HOME}/.local/bin/ry"

# Init git repo.
(cd "${PROJECT_DIR}" && git init -q && git commit --allow-empty -m "Initial commit" -q)
ok "Git repo at ${PROJECT_DIR}"

# ─── Step 5: Write config ────────────────────────────────────────────────────

cat > "${PROJECT_DIR}/railyard.yaml" <<EOF
owner: testuser
repo: ${PROJECT_DIR}

dolt:
  host: 127.0.0.1
  port: ${DOLT_PORT}

stall:
  stdout_timeout_sec: 120
  repeated_error_max: 3
  max_clear_cycles: 5

tracks:
  - name: backend
    language: go
    file_patterns: ["*.go", "cmd/**", "internal/**"]
    engine_slots: 2
    conventions:
      go_version: "1.25"
      style: "stdlib-first"
EOF
ok "Config at ${PROJECT_DIR}/railyard.yaml"

# ─── Step 6: Start Dolt ──────────────────────────────────────────────────────

info "Initializing Dolt..."
(cd "${DOLT_DATA}" && dolt init --name "railyard-test" --email "test@local" > /dev/null 2>&1)

info "Starting Dolt server on port ${DOLT_PORT}..."
(cd "${DOLT_DATA}" && nohup dolt sql-server --host 127.0.0.1 --port "${DOLT_PORT}" > "${TEST_DIR}/dolt.log" 2>&1 &)

READY=false
for i in $(seq 1 20); do
    if ss -tlnp 2>/dev/null | grep -q ":${DOLT_PORT} "; then
        READY=true
        break
    fi
    sleep 1
done
$READY || fail "Dolt failed to start. Check ${TEST_DIR}/dolt.log"
ok "Dolt running on port ${DOLT_PORT}"

# ─── Step 7: Initialize database ─────────────────────────────────────────────

info "Initializing database..."
(cd "${PROJECT_DIR}" && ./ry db init -c railyard.yaml 2>&1)

# ─── Step 8: Create sample cars ─────────────────────────────────────────────

info "Creating sample cars with dependencies..."
(
    cd "${PROJECT_DIR}"
    B1=$(./ry car create -c railyard.yaml --title "Add user model" --track backend --type task --priority 0 \
        --description "Define User struct with GORM model and migrations" 2>&1 | grep -oP 'car-\w+')
    B2=$(./ry car create -c railyard.yaml --title "Add /users GET endpoint" --track backend --type task --priority 1 \
        --description "REST endpoint returning all users as JSON" 2>&1 | grep -oP 'car-\w+')
    B3=$(./ry car create -c railyard.yaml --title "Add /users POST endpoint" --track backend --type task --priority 1 \
        --description "REST endpoint to create a new user" 2>&1 | grep -oP 'car-\w+')

    # GET and POST depend on user model
    ./ry car dep add -c railyard.yaml "${B2}" --blocked-by "${B1}" > /dev/null 2>&1
    ./ry car dep add -c railyard.yaml "${B3}" --blocked-by "${B1}" > /dev/null 2>&1

    echo ""
    echo "  Car            Title                   Status"
    echo "  ─────────────  ──────────────────────  ──────"
    echo "  ${B1}  Add user model          READY (no blockers)"
    echo "  ${B2}  Add /users GET          blocked by ${B1}"
    echo "  ${B3}  Add /users POST         blocked by ${B1}"
)

# ─── Step 9: Verify everything ────────────────────────────────────────────────

echo ""
info "Verifying..."
(
    cd "${PROJECT_DIR}"
    echo ""
    echo "── Car List ──"
    ./ry car list -c railyard.yaml 2>&1
    echo ""
    echo "── Ready Cars ──"
    ./ry car ready -c railyard.yaml --track backend 2>&1
    echo ""
    echo "── Status Dashboard ──"
    ./ry status -c railyard.yaml 2>&1
)

# ─── Step 10: Run unit tests ─────────────────────────────────────────────────

echo ""
info "Running tests..."
if (cd "${REPO_DIR}" && go test ./... -count=1 -timeout 120s > "${TEST_DIR}/test-output.txt" 2>&1); then
    ok "All tests passed"
else
    warn "Some tests had issues (see ${TEST_DIR}/test-output.txt)"
fi

# ─── Done ─────────────────────────────────────────────────────────────────────

echo ""
echo "╔══════════════════════════════════════════════════════╗"
echo "║         Test Environment Ready!                      ║"
echo "╚══════════════════════════════════════════════════════╝"
echo ""
info "Your source repo is untouched. Everything is in ${TEST_DIR}."
echo ""
echo "  cd ${PROJECT_DIR}"
echo ""
echo "  # Start the full railyard (Dispatch + Yardmaster + 1 engine)"
echo "  ry start -c railyard.yaml --engines 1"
echo ""
echo "  # Watch the agents work"
echo "  tmux attach -t railyard"
echo "  # (Ctrl-b d to detach)"
echo ""
echo "  # Check on things"
echo "  ry status -c railyard.yaml"
echo "  ry engine list -c railyard.yaml"
echo "  ry car list -c railyard.yaml"
echo ""
echo "  # Stop"
echo "  ry stop -c railyard.yaml"
echo ""
echo "  # Clean up everything"
echo "  cd ${REPO_DIR} && ./quickstart-test.sh --clean"
echo ""
