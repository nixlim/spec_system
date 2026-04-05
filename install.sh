#!/usr/bin/env bash
# Adversarial Spec System — Installer
# Usage: curl -fsSL https://raw.githubusercontent.com/nixlim/spec_system/main/install.sh | bash
# Or:    ./install.sh [options]
#
# Prerequisites (install these yourself before running):
#   - Claude CLI  https://claude.ai/install  (required — runs all AI agents)
#   - Codex CLI   https://github.com/openai/codex  (optional — dual-provider mode)
#
# Options:
#   --dir DIR          Install binary to DIR (default: /usr/local/bin)
#   --workspace DIR    Create workspace at DIR (default: ./workspace)
#   --skip-beads       Skip bd (Beads) installation
#   --skip-taskval     Skip taskval installation
#   --no-color         Disable coloured output
#   --dry-run          Print what would be done without doing it
#   -h, --help         Show this help

set -euo pipefail

# ─────────────────────────────────────────────
# Colour helpers
# ─────────────────────────────────────────────
USE_COLOR=true
if [[ "${NO_COLOR:-}" != "" ]] || [[ ! -t 1 ]]; then
  USE_COLOR=false
fi

bold="" green="" yellow="" red="" cyan="" reset=""
if $USE_COLOR; then
  bold="\033[1m"; green="\033[0;32m"; yellow="\033[0;33m"
  red="\033[0;31m"; cyan="\033[0;36m"; reset="\033[0m"
fi

info()   { echo -e "${cyan}[info]${reset}  $*"; }
ok()     { echo -e "${green}[ok]${reset}    $*"; }
warn()   { echo -e "${yellow}[warn]${reset}  $*"; }
error()  { echo -e "${red}[error]${reset} $*" >&2; }
header() { echo -e "\n${bold}$*${reset}"; }
die()    { error "$*"; exit 1; }

# ─────────────────────────────────────────────
# Argument parsing
# ─────────────────────────────────────────────
INSTALL_DIR="/usr/local/bin"
WORKSPACE_DIR="./workspace"
SKIP_BEADS=false
SKIP_TASKVAL=false
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dir)         INSTALL_DIR="$2"; shift 2 ;;
    --workspace)   WORKSPACE_DIR="$2"; shift 2 ;;
    --skip-beads)  SKIP_BEADS=true; shift ;;
    --skip-taskval) SKIP_TASKVAL=true; shift ;;
    --no-color)    USE_COLOR=false; shift ;;
    --dry-run)     DRY_RUN=true; shift ;;
    -h|--help)
      cat <<EOF
Adversarial Spec System Installer

PREREQUISITES (install before running this script):
  Claude CLI   https://claude.ai/install        (required)
  Codex CLI    https://github.com/openai/codex  (optional, dual-provider mode)

Usage: $0 [options]
  --dir DIR          Install binary to DIR (default: /usr/local/bin)
  --workspace DIR    Create workspace at DIR (default: ./workspace)
  --skip-beads       Skip bd (Beads) install
  --skip-taskval     Skip taskval install
  --no-color         Disable colour output
  --dry-run          Show what would happen without doing it
  -h, --help         Show this help
EOF
      exit 0 ;;
    *) die "Unknown option: $1  (run '$0 --help' for usage)" ;;
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
  Darwin) PLATFORM="darwin" ;;
  *)      die "Unsupported OS: $OS. This installer supports macOS and Linux." ;;
esac
case "$ARCH" in
  x86_64|amd64)   GOARCH="amd64" ;;
  arm64|aarch64)  GOARCH="arm64" ;;
  *)              GOARCH="$ARCH" ;;
esac

has() { command -v "$1" &>/dev/null; }

GITHUB_REPO="nixlim/spec_system"

echo -e "${bold}Adversarial Spec System — Installer${reset}"
echo "Platform: $OS ($ARCH)"
echo

# ─────────────────────────────────────────────
# Check prerequisites
# ─────────────────────────────────────────────
header "Prerequisites"

if has claude; then
  CLAUDE_VERSION="$(claude --version 2>/dev/null | head -1 || echo "unknown")"
  ok "Claude CLI: $CLAUDE_VERSION"
else
  warn "Claude CLI not found — the server won't be able to run AI agents."
  warn "Install it first: curl -fsSL https://claude.ai/install.sh | bash"
  warn "Then re-run this installer or continue and install Claude CLI manually."
  echo
fi

if has codex; then
  ok "Codex CLI found — dual-provider mode available"
else
  info "Codex CLI not found — dual-provider mode will be disabled (optional)"
  info "Install from: https://github.com/openai/codex"
fi

# ─────────────────────────────────────────────
# Step 1: Get the specworkflow binary
# ─────────────────────────────────────────────
header "Step 1: specworkflow binary"

BINARY_NAME="specworkflow"
BINARY_DEST="${INSTALL_DIR}/${BINARY_NAME}"

# First try to download a pre-built release binary (no Go required).
# Falls back to building from source if no release asset is found.
DOWNLOADED=false

download_release() {
  local tag
  # Fetch latest release tag from GitHub API
  if has curl; then
    tag="$(curl -fsSL "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" \
           2>/dev/null | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
  elif has wget; then
    tag="$(wget -qO- "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" \
           2>/dev/null | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
  fi

  [[ -z "$tag" ]] && return 1

  local asset_name="${BINARY_NAME}_${PLATFORM}_${GOARCH}"
  local url="https://github.com/${GITHUB_REPO}/releases/download/${tag}/${asset_name}"

  info "Downloading $tag release for ${PLATFORM}/${GOARCH}..."

  local tmp
  tmp="$(mktemp)"
  if has curl; then
    curl -fsSL "$url" -o "$tmp" 2>/dev/null || { rm -f "$tmp"; return 1; }
  elif has wget; then
    wget -qO "$tmp" "$url" 2>/dev/null || { rm -f "$tmp"; return 1; }
  else
    rm -f "$tmp"; return 1
  fi

  # Sanity-check: must be an ELF/Mach-O binary, not an HTML error page
  local magic
  magic="$(head -c 4 "$tmp" | od -An -tx1 | tr -d ' \n')"
  if [[ "$magic" == "7f454c46"* ]] || [[ "$magic" == "cffaedfe"* ]] || [[ "$magic" == "feedfacf"* ]] || [[ "$magic" == "cafebabe"* ]]; then
    chmod +x "$tmp"
    if [[ -w "$INSTALL_DIR" ]]; then
      mv "$tmp" "$BINARY_DEST"
    else
      sudo mv "$tmp" "$BINARY_DEST"
    fi
    return 0
  else
    rm -f "$tmp"
    return 1
  fi
}

if $DRY_RUN; then
  echo -e "${yellow}[dry-run]${reset} Would attempt to download pre-built binary from GitHub releases"
  echo -e "${yellow}[dry-run]${reset} Fallback: build from source if Go available"
  DOWNLOADED=true  # Simulate success for dry-run flow
elif download_release; then
  ok "Downloaded pre-built binary → $BINARY_DEST"
  DOWNLOADED=true
else
  info "No pre-built release found — will build from source"
fi

if ! $DOWNLOADED; then
  # ── Build from source ──────────────────────────────────────────────────────
  if ! has go; then
    die "$(cat <<MSG
No pre-built binary available and Go is not installed.

To get the binary, choose one of:
  A) Install Go $MIN_GO and build from source:
       https://golang.org/dl/
       Then re-run: ./install.sh

  B) Download a binary manually from:
       https://github.com/${GITHUB_REPO}/releases

  C) Wait for a release to be published and re-run this installer.
MSG
)"
  fi

  MIN_GO="1.21"
  GO_VERSION_FULL="$(go version | awk '{print $3}' | sed 's/go//')"
  GO_MAJOR="$(echo "$GO_VERSION_FULL" | cut -d. -f1)"
  GO_MINOR="$(echo "$GO_VERSION_FULL" | cut -d. -f2)"
  if [[ "$GO_MAJOR" -lt 1 ]] || ( [[ "$GO_MAJOR" -eq 1 ]] && [[ "$GO_MINOR" -lt 21 ]] ); then
    die "Go $GO_VERSION_FULL is too old (need $MIN_GO+). Download from https://golang.org/dl/"
  fi
  ok "Go $GO_VERSION_FULL"

  # Locate or clone the repository
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || echo "")"
  REPO_DIR=""
  if [[ -f "${SCRIPT_DIR}/go.mod" ]] && grep -q "adversarial-spec-system" "${SCRIPT_DIR}/go.mod" 2>/dev/null; then
    REPO_DIR="$SCRIPT_DIR"
    ok "Building from: $REPO_DIR"
  elif [[ -d "./adversarial-spec-system" ]]; then
    REPO_DIR="$(pwd)/adversarial-spec-system"
    ok "Found existing clone: $REPO_DIR"
  else
    has git || die "git is required to clone the repository. Install git and re-run."
    info "Cloning repository..."
    run git clone "https://github.com/${GITHUB_REPO}.git" adversarial-spec-system
    REPO_DIR="$(pwd)/adversarial-spec-system"
    ok "Cloned to $REPO_DIR"
  fi

  info "Building specworkflow..."
  run bash -c "cd '$REPO_DIR' && go build -o '$BINARY_NAME' ./cmd/specworkflow"

  if ! $DRY_RUN && [[ ! -f "${REPO_DIR}/${BINARY_NAME}" ]]; then
    die "Build failed — binary not found at ${REPO_DIR}/${BINARY_NAME}"
  fi

  if [[ -w "$INSTALL_DIR" ]]; then
    run mv "${REPO_DIR}/${BINARY_NAME}" "$BINARY_DEST"
  else
    run sudo mv "${REPO_DIR}/${BINARY_NAME}" "$BINARY_DEST"
  fi
  ok "Built and installed → $BINARY_DEST"
fi

# ─────────────────────────────────────────────
# Step 2: bd (Beads) — issue tracking
# ─────────────────────────────────────────────
header "Step 2: bd (Beads) — issue tracking [optional]"
info "https://github.com/gastownhall/beads"

if $SKIP_BEADS; then
  warn "Skipping (--skip-beads)"
elif has bd; then
  BD_VERSION="$(bd --version 2>/dev/null | head -1 || echo "unknown")"
  ok "Already installed: $BD_VERSION"
else
  if [[ "$PLATFORM" == "darwin" ]]; then
    if has brew; then
      run brew install beads
    elif has npm; then
      run npm install -g @beads/bd
    else
      run bash -c 'curl -fsSL https://raw.githubusercontent.com/steveyegge/beads/main/scripts/install.sh | bash'
    fi
  else
    # Linux — prefer npm (no CGO), otherwise curl installer
    if has npm; then
      run npm install -g @beads/bd
    elif has apt-get; then
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
    warn "bd not in PATH after install — you may need to open a new shell."
  else
    ok "bd installed"
  fi
fi

# ─────────────────────────────────────────────
# Step 3: taskval — task graph validation
# ─────────────────────────────────────────────
header "Step 3: taskval — task graph validation [optional]"
info "https://github.com/nixlim/task_templating"

if $SKIP_TASKVAL; then
  warn "Skipping (--skip-taskval)"
elif has taskval; then
  TASKVAL_VERSION="$(taskval --version 2>/dev/null | head -1 || echo "unknown")"
  ok "Already installed: $TASKVAL_VERSION"
elif has go; then
  run go install github.com/nixlim/task_templating/cmd/taskval@latest
  GOPATH_BIN="$(go env GOPATH)/bin"
  if ! $DRY_RUN && ! has taskval && [[ -f "${GOPATH_BIN}/taskval" ]]; then
    warn "taskval installed to $GOPATH_BIN — add to PATH:"
    warn "  export PATH=\"\$PATH:$GOPATH_BIN\""
  else
    ok "taskval installed"
  fi
else
  warn "Go not found — cannot install taskval automatically."
  warn "To install: go install github.com/nixlim/task_templating/cmd/taskval@latest"
  warn "Or download from: https://github.com/nixlim/task_templating/releases"
fi

# ─────────────────────────────────────────────
# Step 4: Skill directories
# ─────────────────────────────────────────────
header "Step 4: Skill directories"

SKILLS_DEST="${HOME}/.claude/skills"

# When running via curl, SCRIPT_DIR won't contain the repo; we need REPO_DIR.
# Fall back gracefully if repo is not local.
SKILLS_SRC="${REPO_DIR:-}/.claude/skills"

install_skill() {
  local name="$1"
  local dest="${SKILLS_DEST}/${name}"
  local src="${SKILLS_SRC}/${name}"
  if [[ -d "$dest" ]]; then
    ok "$name already at $dest"
  elif [[ -d "$src" ]]; then
    run mkdir -p "$SKILLS_DEST"
    run cp -r "$src" "$dest"
    ok "Installed $name → $dest"
  else
    warn "$name skill not found in repo — set skill_paths.${name//-/_} in config.yaml manually"
  fi
}

install_skill "plan-spec"
install_skill "grill-spec"

# ─────────────────────────────────────────────
# Step 5: Workspace and config.yaml
# ─────────────────────────────────────────────
header "Step 5: Workspace and config"

run mkdir -p "$WORKSPACE_DIR"
ok "Workspace: $WORKSPACE_DIR"

CONFIG_FILE="./config.yaml"
if [[ -f "$CONFIG_FILE" ]]; then
  ok "config.yaml already exists — not overwriting"
else
  info "Writing default config.yaml..."
  if ! $DRY_RUN; then
    cat > "$CONFIG_FILE" <<YAML
# Adversarial Spec System — Configuration
# Full reference: docs/operations-manual.md

skill_paths:
  plan_spec: "${HOME}/.claude/skills/plan-spec"
  grill_spec: "${HOME}/.claude/skills/grill-spec"

max_rounds: 5
min_rounds: 2
max_total_findings: 60
staleness_threshold: 2
max_wall_clock_minutes: 60
max_cost_usd: 50.0
max_retries: 2
agent_timeout_seconds: 300
reviewer_timeout_seconds: 300
holdout_timeout_seconds: 300

enable_codex_reviewers: true
enable_codex_discovery: false
enable_codex_drafting: false
codex_model: "gpt-5.4"

taskify_max_retries: 3
task_review_max_rounds: 3

beads_gate_poll_interval: 5s
beads_gate_timeout: 24h

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
# Step 6: Initialise Beads workspace
# ─────────────────────────────────────────────
header "Step 6: Beads workspace"

if $SKIP_BEADS; then
  warn "Skipping (--skip-beads)"
elif ! has bd; then
  warn "bd not on PATH — run 'bd ready' in your project directory after installing"
elif [[ -d ".beads" ]]; then
  ok ".beads workspace already exists"
else
  run bd ready
  ok "Beads workspace initialised"
fi

# ─────────────────────────────────────────────
# Summary
# ─────────────────────────────────────────────
header "Done"
echo

check() {
  local label="$1" cmd="$2" skip="$3" note="${4:-}"
  if $skip; then
    echo -e "  ${yellow}-${reset}  $label  (skipped)"
  elif $DRY_RUN; then
    echo -e "  ${cyan}?${reset}  $label  (dry-run)"
  elif has "$cmd"; then
    echo -e "  ${green}✓${reset}  $label"
  else
    echo -e "  ${red}✗${reset}  $label  ${note}"
  fi
}

check "specworkflow" "specworkflow" false
check "bd (Beads)"   "bd"           "$SKIP_BEADS"  "(open a new shell or check install)"
check "taskval"      "taskval"      "$SKIP_TASKVAL" "(check \$GOPATH/bin is on PATH)"

# Claude and Codex are the user's responsibility — just report status
if has claude; then
  echo -e "  ${green}✓${reset}  claude CLI"
else
  echo -e "  ${red}✗${reset}  claude CLI  — ${bold}install before starting:${reset} https://claude.ai/install.sh"
fi
if has codex; then
  echo -e "  ${green}✓${reset}  codex CLI"
else
  echo -e "  ${yellow}-${reset}  codex CLI  (optional, dual-provider mode disabled)"
fi

echo
echo -e "${bold}Next steps:${reset}"
N=1
if ! has claude 2>/dev/null; then
  echo "  $N. Install and authenticate Claude CLI:"
  echo "       curl -fsSL https://claude.ai/install.sh | bash"
  echo "       claude auth login"
  N=$((N+1))
fi
echo "  $N. Start the server:"
echo "       specworkflow --config config.yaml --workspace $WORKSPACE_DIR"
N=$((N+1))
echo "  $N. Open the dashboard: http://localhost:8080"
N=$((N+1))
echo "  $N. Read the guide: docs/operations-manual.md"
echo
