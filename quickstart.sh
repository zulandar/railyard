#!/usr/bin/env bash
set -euo pipefail

# Railyard Quickstart — Fresh WSL Setup
#
# For a FRESH WSL container where you've cloned the repo.
# Installs prerequisites, builds ry, starts Dolt, initializes the DB,
# and gets you ready to run `ry start`.
#
# Usage:
#   git clone <repo> && cd railyard
#   chmod +x quickstart.sh && ./quickstart.sh

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

# Check if a port is listening. Falls back through ss → lsof → netstat → /dev/tcp.
check_port() {
    local port=$1
    if command -v ss &>/dev/null; then
        ss -tlnp 2>/dev/null | grep -q ":${port} "
    elif command -v lsof &>/dev/null; then
        lsof -iTCP:"${port}" -sTCP:LISTEN &>/dev/null 2>&1
    elif command -v netstat &>/dev/null; then
        netstat -tlnp 2>/dev/null | grep -q ":${port} "
    else
        (echo > "/dev/tcp/127.0.0.1/${port}") 2>/dev/null
    fi
}

# Check if a specific process owns a port (best-effort, needs ss -p or lsof).
check_port_process() {
    local port=$1 process=$2
    if command -v ss &>/dev/null; then
        ss -tlnp 2>/dev/null | grep ":${port} " | grep -q "${process}"
    elif command -v lsof &>/dev/null; then
        lsof -iTCP:"${port}" -sTCP:LISTEN 2>/dev/null | grep -q "${process}"
    elif command -v netstat &>/dev/null; then
        netstat -tlnp 2>/dev/null | grep ":${port} " | grep -q "${process}"
    else
        return 1  # Can't determine process — assume not ours.
    fi
}

# Test that Dolt is actually query-ready, not just listening.
check_dolt_ready() {
    local host=$1 port=$2
    if command -v mysql &>/dev/null; then
        mysql -h "$host" -P "$port" -u root -e 'SELECT 1' &>/dev/null 2>&1
    else
        # TCP-level connect — confirms server accepts connections.
        (echo > "/dev/tcp/$host/$port") 2>/dev/null
    fi
}

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ ! -f "${REPO_DIR}/go.mod" ]; then
    fail "Run this from the railyard repo root."
fi
cd "${REPO_DIR}"

# ─── Cleanup trap ───────────────────────────────────────────────────────────
# If the script fails midway, stop any Dolt we started so it doesn't orphan.

SCRIPT_SUCCESS=false
DOLT_STARTED_BY_US=false
DOLT_PORT=3306  # May be overridden later by port conflict detection.

cleanup() {
    if ! $SCRIPT_SUCCESS && $DOLT_STARTED_BY_US; then
        warn "Script failed — stopping Dolt server we started on port ${DOLT_PORT}..."
        pkill -f "dolt sql-server.*--port ${DOLT_PORT}" 2>/dev/null || true
    fi
}
trap cleanup EXIT

echo ""
echo "╔══════════════════════════════════════════════════════╗"
echo "║         Railyard Quickstart — Fresh WSL Setup        ║"
echo "╚══════════════════════════════════════════════════════╝"
echo ""

# ─── Step 1: Check / install prerequisites ────────────────────────────────────

info "Checking prerequisites..."

# curl is needed to install Go and Dolt.
check_cmd curl || fail "curl is required but not found. Install: sudo apt-get install curl"

if ! check_cmd go; then
    GO_VERSION="1.25.0"
    GO_TAR="go${GO_VERSION}.linux-amd64.tar.gz"
    GO_URL="https://go.dev/dl/${GO_TAR}"
    warn "Go not found. Installing Go ${GO_VERSION}..."
    if ! curl -fsSL "${GO_URL}" -o "/tmp/${GO_TAR}"; then
        fail "Failed to download Go from ${GO_URL} — check that Go ${GO_VERSION} exists at https://go.dev/dl/"
    fi
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf "/tmp/${GO_TAR}"
    rm "/tmp/${GO_TAR}"
    export PATH="/usr/local/go/bin:$PATH"
    grep -q '/usr/local/go/bin' ~/.bashrc 2>/dev/null || echo 'export PATH="/usr/local/go/bin:$PATH"' >> ~/.bashrc
fi
ok "Go $(go version | grep -oP '\d+\.\d+' | head -1)"

if ! check_cmd dolt; then
    warn "Dolt not found. Installing..."
    sudo bash -c 'curl -L https://github.com/dolthub/dolt/releases/latest/download/install.sh | bash'
    check_cmd dolt || fail "Dolt installation failed."
fi
ok "Dolt $(dolt version 2>&1 | head -1)"

if ! check_cmd tmux; then
    warn "tmux not found. Installing..."
    sudo apt-get update -qq && sudo apt-get install -y -qq tmux
fi
ok "tmux $(tmux -V 2>&1)"

if check_cmd claude; then
    ok "Claude Code CLI found"
else
    warn "Claude Code CLI not found — engines need it to spawn agents."
    warn "Install: npm install -g @anthropic-ai/claude-code"
    echo ""
fi

# ─── Step 2: Build and install ry ─────────────────────────────────────────────

info "Building ry binary..."
go build -o ry ./cmd/ry/
mkdir -p "${HOME}/.local/bin"
ln -sf "${REPO_DIR}/ry" "${HOME}/.local/bin/ry"

# Ensure ~/.local/bin is on PATH for this session and future shells.
if [[ ":${PATH}:" != *":${HOME}/.local/bin:"* ]]; then
    export PATH="${HOME}/.local/bin:${PATH}"
    info "Added ~/.local/bin to current session PATH"
fi
if ! grep -q '\.local/bin' "${HOME}/.bashrc" 2>/dev/null; then
    echo 'export PATH="${HOME}/.local/bin:${PATH}"' >> "${HOME}/.bashrc"
    info "Added ~/.local/bin to ~/.bashrc"
fi

ok "Built and installed: $(./ry version 2>&1)"

# ─── Step 3: Run tests ───────────────────────────────────────────────────────

info "Running tests..."
if go test ./... -count=1 -timeout 120s > /tmp/ry-test.txt 2>&1; then
    ok "All tests passed"
else
    warn "Some tests failed (may need Dolt running):"
    tail -10 /tmp/ry-test.txt
fi

# ─── Step 4: Setup Dolt ──────────────────────────────────────────────────────

DOLT_DATA="${HOME}/.railyard/dolt-data"
info "Setting up Dolt at ${DOLT_DATA}..."
mkdir -p "${DOLT_DATA}"
if [ ! -d "${DOLT_DATA}/.dolt" ]; then
    (cd "${DOLT_DATA}" && dolt init --name "railyard" --email "railyard@local")
fi

# Start Dolt if not already running.
# Check for port conflicts — if something else (e.g. MySQL) occupies 3306, use 3307.
DOLT_RUNNING=false

if check_port 3306; then
    # Something is on 3306 — check if it's actually Dolt.
    if check_port_process 3306 "dolt"; then
        DOLT_RUNNING=true
    else
        warn "Port 3306 is in use by another process (MySQL/MariaDB?)."
        warn "Switching Dolt to port 3307."
        DOLT_PORT=3307
        if check_port 3307; then
            if check_port_process 3307 "dolt"; then
                DOLT_RUNNING=true
            else
                fail "Ports 3306 and 3307 are both in use by non-Dolt processes. Free one and retry."
            fi
        fi
    fi
fi

if ! $DOLT_RUNNING; then
    info "Starting Dolt server on port ${DOLT_PORT}..."
    mkdir -p "${HOME}/.railyard"
    (cd "${DOLT_DATA}" && nohup dolt sql-server --host 127.0.0.1 --port "${DOLT_PORT}" > "${HOME}/.railyard/dolt.log" 2>&1 &)
    DOLT_STARTED_BY_US=true
    READY=false
    for i in $(seq 1 20); do
        if check_dolt_ready 127.0.0.1 "${DOLT_PORT}"; then
            READY=true
            break
        fi
        sleep 1
    done
    $READY || fail "Dolt failed to become ready on port ${DOLT_PORT}. Check ~/.railyard/dolt.log"
fi
ok "Dolt server running and ready on port ${DOLT_PORT}"

# ─── Step 5: Create railyard.yaml ─────────────────────────────────────────────

if [ ! -f railyard.yaml ]; then
    info "Creating railyard.yaml..."
    OWNER=$(whoami)
    cat > railyard.yaml <<EOF
owner: ${OWNER}
repo: ${REPO_DIR}

dolt:
  host: 127.0.0.1
  port: ${DOLT_PORT}

tracks:
  - name: backend
    language: go
    file_patterns: ["cmd/**", "internal/**", "pkg/**", "*.go"]
    engine_slots: 2
    conventions:
      go_version: "1.25"
      style: "stdlib-first, no frameworks"
EOF
    ok "Created railyard.yaml (owner: ${OWNER})"
else
    ok "railyard.yaml already exists"
fi

# ─── Step 6: Initialize database ──────────────────────────────────────────────

info "Initializing database..."
./ry db init -c railyard.yaml 2>&1
ok "Database ready"

# ─── Done ─────────────────────────────────────────────────────────────────────

SCRIPT_SUCCESS=true

echo ""
echo "╔══════════════════════════════════════════════════════╗"
echo "║         Setup Complete!                              ║"
echo "╚══════════════════════════════════════════════════════╝"
echo ""
echo "  # Check status"
echo "  ry status -c railyard.yaml"
echo ""
echo "  # Create work items"
echo "  ry car create -c railyard.yaml --title 'Add feature X' --track backend --type task"
echo ""
echo "  # Start the full railyard"
echo "  ry start -c railyard.yaml --engines 2"
echo ""
echo "  # Attach to watch agents work"
echo "  tmux attach -t railyard"
echo ""
echo "  # Stop"
echo "  ry stop -c railyard.yaml"
echo ""
info "Dolt log: ~/.railyard/dolt.log"
info "Stop Dolt: pkill -f 'dolt sql-server'"
echo ""
