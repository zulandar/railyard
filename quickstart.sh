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

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ ! -f "${REPO_DIR}/go.mod" ]; then
    fail "Run this from the railyard repo root."
fi
cd "${REPO_DIR}"

echo ""
echo "╔══════════════════════════════════════════════════════╗"
echo "║         Railyard Quickstart — Fresh WSL Setup        ║"
echo "╚══════════════════════════════════════════════════════╝"
echo ""

# ─── Step 1: Check / install prerequisites ────────────────────────────────────

info "Checking prerequisites..."

if ! check_cmd go; then
    warn "Go not found. Installing Go 1.25..."
    GO_TAR="go1.25.0.linux-amd64.tar.gz"
    curl -fsSL "https://go.dev/dl/${GO_TAR}" -o "/tmp/${GO_TAR}"
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

# Start Dolt if not already running on :3306.
DOLT_RUNNING=false
ss -tlnp 2>/dev/null | grep -q ":3306 " && DOLT_RUNNING=true
if ! $DOLT_RUNNING; then
    info "Starting Dolt server on port 3306..."
    mkdir -p "${HOME}/.railyard"
    (cd "${DOLT_DATA}" && nohup dolt sql-server --host 127.0.0.1 --port 3306 > "${HOME}/.railyard/dolt.log" 2>&1 &)
    for i in $(seq 1 15); do
        ss -tlnp 2>/dev/null | grep -q ":3306 " && break
        sleep 1
    done
fi
ok "Dolt server running on port 3306"

# ─── Step 5: Create railyard.yaml ─────────────────────────────────────────────

if [ ! -f railyard.yaml ]; then
    info "Creating railyard.yaml..."
    OWNER=$(whoami)
    cat > railyard.yaml <<EOF
owner: ${OWNER}
repo: ${REPO_DIR}

dolt:
  host: 127.0.0.1
  port: 3306

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
