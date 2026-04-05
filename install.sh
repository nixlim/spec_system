#!/usr/bin/env bash
# Adversarial Spec System — Installer
# Usage: curl -fsSL https://raw.githubusercontent.com/nixlim/spec_system/main/install.sh | bash
# Or:    ./install.sh [options]
#
# Options:
#   --dir DIR          Install server binary to DIR (default: /usr/local/bin)
#   --workspace DIR    Create workspace at DIR (default: ./workspace)
#   --skip-claude      Skip Claude CLI installation check
#   --skip-beads       Skip bd (Beads) installation
#   --skip-taskval     Skip taskval installation
#   --skip-codex       Skip Codex CLI installation prompt
#   --no-color         Disable coloured output
#   --dry-run          Print what would be done without doing it

set -euo pipefail

# ─────────────────────────────────────────────
# Colour helpers
# ─────────────────────────────────────────────
USE_COLOR=true
if [[ "${NO_COLOR:-}" != "" ]] || [[ ! -t 1 ]]; then
  USE_COLOR=false
fi

bold=""  green=""  yellow=""  red=""  cyan=""  reset=""
if $USE_COLOR; then
  bold="\033[1m"
  green="\033[0;32m"
  yellow="\033[0;33m"
  red="\033[0;31m"
  cyan="\033[0;36m"
  reset="\033[0m"
fi

info()    { echo -e "${cyan}[info]${reset}  $*"; }
ok()      { echo -e "${green}[ok]${reset}    $*"; }
warn()    { echo -e "${yellow}[warn]${reset}  $*"; }
error()   { echo -e "${red}[error]${reset} $*" >&2; }
header()  { echo -e "\n${bold}$*${reset}"; }
die()     { error "$*"; exit 1; }

# ─────────────────────────────────────────────
# Argument parsing
# ─────────────────────────────────────────────
INSTALL_DIR="/usr/local/bin"
WORKSPACE_DIR="./workspace"
SKIP_CLAUDE=false
SKIP_BEADS=false
SKIP_TASKVAL=false
SKIP_CODEX=false
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dir)        INSTALL_DIR="$2"; shift 2 ;;
    --workspace)  WORKSPACE_DIR="$2"; shift 2 ;;
    --skip-claude) SKIP_CLAUDE=true; shift ;;
    --skip-beads)  SKIP_BEADS=true; shift ;;
    --skip-taskval) SKIP_TASKVAL=true; shift ;;
    --skip-codex)  SKIP_CODEX=true; shift ;;
    --no-color)   USE_COLOR=false; shift ;;
    --dry-run)    DRY_RUN=true; shift ;;
    -h|--help)
      cat <<EOF
Adversarial Spec System Installer

Usage: $0 [options]

  --dir DIR          Install binary to DIR (default: /usr/local/bin)
  --workspace DIR    Create workspace at DIR (default: ./workspace)
  --skip-claude      Skip Claude CLI check/install
  --skip-beads       Skip bd (Beads) install
  --skip-taskval     Skip taskval install
  --skip-codex       Skip Codex CLI prompt
  --no-color         Disable colour output
  --dry-run          Show what would happen without doing it
  -h, --help         Show this help

EOF
      exit 0 ;;
    *) die "Unknown option: $1. Run '$0 --help' for usage." ;;
  esac
done

run() {
  if $DRY_RUN; then
    echo -e "${yellow}[dry-run]${reset} $*"
  else
    "$@"
  fi
}

# ─────────────────────────────────────────────
# Platform detection
# ─────────────────────────────────────────────
OS="$(uname -s)"
ARCH="$(uname -m)"
case "$OS" in
  Linux)  PLATFORM="linux" ;;
  Darwin) PLATFORM="macos" ;;
  *)      die "Unsupported OS: $OS. This installer supports macOS and Linux." ;;
esac

echo -e "${bold}Adversarial Spec System — Installer${reset}"
echo "Platform: $OS ($ARCH)"
echo

# ─────────────────────────────────────────────
# Helper: check if a command exists
# ─────────────────────────────────────────────
has() { command -v "$1" &>/dev/null; }

# ─────────────────────────────────────────────
# Step 1: Go
# ─────────────────────────────────────────────
header "Step 1: Go"

MIN_GO_MAJOR=1
MIN_GO_MINOR=21

if ! has go; then
  die "Go is not installed. Please install Go $MIN_GO_MAJOR.$MIN_GO_MINOR+ from https://golang.org/dl/ and re-run this installer."
fi

GO_VERSION_FULL="$(go version | awk '{print $3}' | sed 's/go//')"
GO_MAJOR="$(echo "$GO_VERSION_FULL" | cut -d. -f1)"
GO_MINOR="$(echo "$GO_VERSION_FULL" | cut -d. -f2)"

if [[ "$GO_MAJOR" -lt "$MIN_GO_MAJOR" ]] || \
   ( [[ "$GO_MAJOR" -eq "$MIN_GO_MAJOR" ]] && [[ "$GO_MINOR" -lt "$MIN_GO_MINOR" ]] ); then
  die "Go $GO_VERSION_FULL is too old. Need $MIN_GO_MAJOR.$MIN_GO_MINOR+. Install from https://golang.org/dl/"
fi

ok "Go $GO_VERSION_FULL"

# ─────────────────────────────────────────────
# Step 2: Locate / clone the repository
# ─────────────────────────────────────────────
header "Step 2: Repository"

REPO_URL="https://github.com/nixlim/spec_system.git"

# If we're running from inside the repo, use that. Otherwise clone it.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || echo "")"
REPO_DIR=""

if [[ -f "${SCRIPT_DIR}/go.mod" ]] && grep -q "adversarial-spec-system" "${SCRIPT_DIR}/go.mod" 2>/dev/null; then
  REPO_DIR="$SCRIPT_DIR"
  ok "Running from repository: $REPO_DIR"
elif [[ -d "./adversarial-spec-system" ]]; then
  REPO_DIR="$(pwd)/adversarial-spec-system"
  ok "Found existing clone: $REPO_DIR"
else
  info "Cloning repository..."
  if ! has git; then
    die "git is required to clone the repository. Install git and re-run."
  fi
  run git clone "$REPO_URL" adversarial-spec-system
  REPO_DIR="$(pwd)/adversarial-spec-system"
  ok "Cloned to $REPO_DIR"
fi

# ─────────────────────────────────────────────
# Step 3: Build the server
# ─────────────────────────────────────────────
header "Step 3: Build specworkflow"

BINARY_NAME="specworkflow"
BINARY_DEST="${INSTALL_DIR}/${BINARY_NAME}"

info "Building from $REPO_DIR..."
run bash -c "cd '$REPO_DIR' && go build -o '$BINARY_NAME' ./cmd/specworkflow"

if ! $DRY_RUN && [[ ! -f "${REPO_DIR}/${BINARY_NAME}" ]]; then
  die "Build failed — binary not found at ${REPO_DIR}/${BINARY_NAME}"
fi

# Install binary
if [[ "$INSTALL_DIR" != "." ]] && [[ "$INSTALL_DIR" != "$(pwd)" ]]; then
  info "Installing binary to $BINARY_DEST..."
  if [[ -w "$INSTALL_DIR" ]]; then
    run mv "${REPO_DIR}/${BINARY_NAME}" "$BINARY_DEST"
  else
    info "Need sudo to write to $INSTALL_DIR..."
    run sudo mv "${REPO_DIR}/${BINARY_NAME}" "$BINARY_DEST"
  fi
  ok "Installed: $BINARY_DEST"
else
  BINARY_DEST="${REPO_DIR}/${BINARY_NAME}"
  ok "Binary at: $BINARY_DEST (not added to PATH — run from $REPO_DIR)"
fi

# ─────────────────────────────────────────────
# Step 4: Claude CLI
# ─────────────────────────────────────────────
header "Step 4: Claude CLI"

if $SKIP_CLAUDE; then
  warn "Skipping Claude CLI (--skip-claude)"
elif has claude; then
  CLAUDE_VERSION="$(claude --version 2>/dev/null | head -1 || echo "unknown")"
  ok "Claude CLI already installed: $CLAUDE_VERSION"
else
  info "Installing Claude CLI..."
  if [[ "$PLATFORM" == "macos" ]]; then
    if has brew; then
      run brew install --cask claude-code
    else
      run bash -c 'curl -fsSL https://claude.ai/install.sh | bash'
    fi
  else
    run bash -c 'curl -fsSL https://claude.ai/install.sh | bash'
  fi
  if ! $DRY_RUN && ! has claude; then
    warn "Claude CLI installation may require opening a new shell. Run 'claude auth login' to authenticate."
  else
    ok "Claude CLI installed"
  fi
fi

# ─────────────────────────────────────────────
# Step 5: bd (Beads)
# ─────────────────────────────────────────────
header "Step 5: bd (Beads) — issue tracking [optional]"

if $SKIP_BEADS; then
  warn "Skipping bd (--skip-beads)"
elif has bd; then
  BD_VERSION="$(bd --version 2>/dev/null | head -1 || echo "unknown")"
  ok "bd already installed: $BD_VERSION"
else
  info "Installing bd (Beads)..."
  info "Repo: https://github.com/gastownhall/beads"

  # Beads uses CGO — ensure a C compiler is available
  if [[ "$PLATFORM" == "macos" ]]; then
    if ! has cc; then
      warn "Xcode Command Line Tools not found. Installing..."
      run xcode-select --install 2>/dev/null || true
    fi
    # Install via npm (no CGO required for the npm package)
    if has npm; then
      run npm install -g @beads/bd
    elif has brew; then
      run brew install beads
    else
      run bash -c 'curl -fsSL https://raw.githubusercontent.com/steveyegge/beads/main/scripts/install.sh | bash'
    fi
  else
    # Linux
    if has npm; then
      run npm install -g @beads/bd
    elif has apt-get; then
      info "Installing build dependencies for Beads..."
      run sudo apt-get install -y build-essential
      run bash -c 'curl -fsSL https://raw.githubusercontent.com/steveyegge/beads/main/scripts/install.sh | bash'
    elif has dnf; then
      run sudo dnf install -y gcc gcc-c++
      run bash -c 'curl -fsSL https://raw.githubusercontent.com/steveyegge/beads/main/scripts/install.sh | bash'
    else
      run bash -c 'curl -fsSL https://raw.githubusercontent.com/steveyegge/beads/main/scripts/install.sh | bash'
    fi
  fi

  if ! $DRY_RUN && ! has bd; then
    warn "bd not found in PATH after install. You may need to open a new shell."
    warn "To install manually: https://github.com/gastownhall/beads"
  else
    ok "bd installed"
  fi
fi

# ─────────────────────────────────────────────
# Step 6: taskval
# ─────────────────────────────────────────────
header "Step 6: taskval — task graph validation [optional]"

if $SKIP_TASKVAL; then
  warn "Skipping taskval (--skip-taskval)"
elif has taskval; then
  TASKVAL_VERSION="$(taskval --version 2>/dev/null | head -1 || echo "unknown")"
  ok "taskval already installed: $TASKVAL_VERSION"
else
  info "Installing taskval..."
  info "Repo: https://github.com/nixlim/task_templating"
  run go install github.com/nixlim/task_templating/cmd/taskval@latest
  if ! $DRY_RUN && ! has taskval; then
    # go install puts binaries in $(go env GOPATH)/bin — check if it's on PATH
    GOPATH_BIN="$(go env GOPATH)/bin"
    if [[ -f "${GOPATH_BIN}/taskval" ]]; then
      warn "taskval installed to $GOPATH_BIN but that directory is not in PATH."
      warn "Add this to your shell profile:"
      warn "  export PATH=\"\$PATH:$GOPATH_BIN\""
    else
      warn "taskval not found after install. Check: go install github.com/nixlim/task_templating/cmd/taskval@latest"
    fi
  else
    ok "taskval installed"
  fi
fi

# ─────────────────────────────────────────────
# Step 7: Codex CLI (prompt only)
# ─────────────────────────────────────────────
header "Step 7: Codex CLI — dual-provider mode [optional]"

if $SKIP_CODEX; then
  warn "Skipping Codex CLI (--skip-codex)"
elif has codex; then
  ok "Codex CLI already installed"
else
  warn "Codex CLI not found. Dual-provider mode (Claude + GPT in parallel) will be disabled."
  info "To install: follow instructions at https://github.com/openai/codex"
fi

# ─────────────────────────────────────────────
# Step 8: Skill directories
# ─────────────────────────────────────────────
header "Step 8: Skill directories"

SKILLS_DEST="${HOME}/.claude/skills"
PLAN_SPEC_SRC="${REPO_DIR}/.claude/skills/plan-spec"
GRILL_SPEC_SRC="${REPO_DIR}/.claude/skills/grill-spec"

PLAN_SPEC_DEST="${SKILLS_DEST}/plan-spec"
GRILL_SPEC_DEST="${SKILLS_DEST}/grill-spec"

if [[ -d "$PLAN_SPEC_DEST" ]]; then
  ok "plan-spec already at $PLAN_SPEC_DEST"
else
  if [[ -d "$PLAN_SPEC_SRC" ]]; then
    run mkdir -p "$SKILLS_DEST"
    run cp -r "$PLAN_SPEC_SRC" "$PLAN_SPEC_DEST"
    ok "Installed plan-spec → $PLAN_SPEC_DEST"
  else
    warn "plan-spec not found in repo at $PLAN_SPEC_SRC"
    warn "You will need to set skill_paths.plan_spec in config.yaml manually"
  fi
fi

if [[ -d "$GRILL_SPEC_DEST" ]]; then
  ok "grill-spec already at $GRILL_SPEC_DEST"
else
  if [[ -d "$GRILL_SPEC_SRC" ]]; then
    run mkdir -p "$SKILLS_DEST"
    run cp -r "$GRILL_SPEC_SRC" "$GRILL_SPEC_DEST"
    ok "Installed grill-spec → $GRILL_SPEC_DEST"
  else
    warn "grill-spec not found in repo at $GRILL_SPEC_SRC"
    warn "You will need to set skill_paths.grill_spec in config.yaml manually"
  fi
fi

# ─────────────────────────────────────────────
# Step 9: Workspace and config
# ─────────────────────────────────────────────
header "Step 9: Workspace and config"

CONFIG_FILE="./config.yaml"
run mkdir -p "$WORKSPACE_DIR"
ok "Workspace: $WORKSPACE_DIR"

if [[ -f "$CONFIG_FILE" ]]; then
  ok "config.yaml already exists — not overwriting"
else
  info "Writing default config.yaml..."
  if ! $DRY_RUN; then
    cat > "$CONFIG_FILE" <<YAML
# Adversarial Spec System — Configuration
# Full reference: see docs/operations-manual.md

# ─────────────────────────────────────────────
# Skill directories (auto-discovered from ~/.claude/skills if not set)
# ─────────────────────────────────────────────
skill_paths:
  plan_spec: "${HOME}/.claude/skills/plan-spec"
  grill_spec: "${HOME}/.claude/skills/grill-spec"

# ─────────────────────────────────────────────
# Review loop limits
# ─────────────────────────────────────────────
max_rounds: 5
min_rounds: 2
max_total_findings: 60
staleness_threshold: 2

# ─────────────────────────────────────────────
# Budget limits
# ─────────────────────────────────────────────
max_wall_clock_minutes: 60
max_cost_usd: 50.0

# ─────────────────────────────────────────────
# Agent reliability
# ─────────────────────────────────────────────
max_retries: 2
agent_timeout_seconds: 300
reviewer_timeout_seconds: 300
holdout_timeout_seconds: 300

# ─────────────────────────────────────────────
# Dual-provider (requires codex on PATH)
# ─────────────────────────────────────────────
enable_codex_reviewers: true
enable_codex_discovery: false
enable_codex_drafting: false
codex_model: "gpt-5.4"

# ─────────────────────────────────────────────
# Task decomposition
# ─────────────────────────────────────────────
taskify_max_retries: 3
task_review_max_rounds: 3

# ─────────────────────────────────────────────
# Beads integration (requires bd on PATH)
# ─────────────────────────────────────────────
beads_gate_poll_interval: 5s
beads_gate_timeout: 24h

# ─────────────────────────────────────────────
# Code review workflow
# ─────────────────────────────────────────────
code_review:
  max_rounds: 3
  max_cost_usd: 50.0
  max_wall_clock_minutes: 120
  fixer_timeout_seconds: 600
  commit_mode: branch_per_round
  staleness_threshold: 2
  max_retries: 2
  reviewer_timeout_seconds: 300
YAML
  fi
  ok "config.yaml written"
fi

# ─────────────────────────────────────────────
# Step 10: Initialise Beads workspace
# ─────────────────────────────────────────────
header "Step 10: Beads workspace init"

if $SKIP_BEADS; then
  warn "Skipping Beads init (--skip-beads)"
elif ! has bd; then
  warn "bd not on PATH — skipping Beads workspace init"
  warn "Run 'bd ready' in your project directory after installing bd"
elif [[ -d ".beads" ]]; then
  ok ".beads workspace already exists"
else
  info "Initialising Beads workspace in current directory..."
  run bd ready
  ok "Beads workspace initialised"
fi

# ─────────────────────────────────────────────
# Summary
# ─────────────────────────────────────────────
header "Installation complete"
echo

echo -e "${bold}What was installed:${reset}"

# Binary
if $DRY_RUN; then
  echo "  specworkflow  → $BINARY_DEST (dry-run)"
elif has specworkflow || [[ -f "$BINARY_DEST" ]]; then
  echo -e "  ${green}✓${reset} specworkflow  → $BINARY_DEST"
else
  echo -e "  ${yellow}?${reset} specworkflow  → $BINARY_DEST (check manually)"
fi

# Claude
if has claude; then
  echo -e "  ${green}✓${reset} claude CLI"
elif $SKIP_CLAUDE; then
  echo -e "  ${yellow}-${reset} claude CLI   (skipped)"
else
  echo -e "  ${red}✗${reset} claude CLI   (not found — install from https://claude.ai/install.sh)"
fi

# bd
if has bd; then
  echo -e "  ${green}✓${reset} bd (Beads)"
elif $SKIP_BEADS; then
  echo -e "  ${yellow}-${reset} bd (Beads)   (skipped)"
else
  echo -e "  ${yellow}!${reset} bd (Beads)   (not found — may need a new shell, or install from https://github.com/gastownhall/beads)"
fi

# taskval
if has taskval; then
  echo -e "  ${green}✓${reset} taskval"
elif $SKIP_TASKVAL; then
  echo -e "  ${yellow}-${reset} taskval      (skipped)"
else
  echo -e "  ${yellow}!${reset} taskval      (not found — check \$GOPATH/bin is on PATH)"
fi

# codex
if has codex; then
  echo -e "  ${green}✓${reset} codex CLI"
elif $SKIP_CODEX; then
  echo -e "  ${yellow}-${reset} codex CLI    (skipped)"
else
  echo -e "  ${yellow}-${reset} codex CLI    (not installed — dual-provider mode disabled)"
fi

echo
echo -e "${bold}Next steps:${reset}"
STEP=1

# Auth
if ! has claude || ! claude auth status &>/dev/null 2>&1; then
  echo "  $STEP. Authenticate with Claude:"
  echo "       claude auth login"
  STEP=$((STEP+1))
fi

echo "  $STEP. Start the server:"
echo "       specworkflow --config config.yaml --workspace $WORKSPACE_DIR"
STEP=$((STEP+1))

echo "  $STEP. Open the dashboard:"
echo "       http://localhost:8080"
STEP=$((STEP+1))

echo "  $STEP. Read the operations guide:"
echo "       $REPO_DIR/docs/operations-manual.md"

echo
