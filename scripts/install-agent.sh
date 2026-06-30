#!/usr/bin/env bash
# install-agent.sh — swarm-agent bootstrap installer
#
# Usage (one-liner):
#   curl -fsSL https://raw.githubusercontent.com/pradeep-av/agent-swarm/main/scripts/install-agent.sh | bash -
#
# Or download and run:
#   curl -fsSL https://raw.githubusercontent.com/pradeep-av/agent-swarm/main/scripts/install-agent.sh -o install-agent.sh
#   bash install-agent.sh
#
# Flags:
#   --uninstall   Remove the binary and service instead of installing

set -euo pipefail
IFS=$'\n\t'

# ── Colours ───────────────────────────────────────────────────────────────────
if [[ -t 1 ]] || [[ -t 2 ]]; then
  RED='\033[0;31m' GREEN='\033[0;32m' YELLOW='\033[1;33m'
  BLUE='\033[0;34m' BOLD='\033[1m' RESET='\033[0m'
else
  RED='' GREEN='' YELLOW='' BLUE='' BOLD='' RESET=''
fi

info()   { printf "${BLUE}→${RESET} %s\n" "$*"; }
ok()     { printf "${GREEN}✓${RESET} %s\n" "$*"; }
warn()   { printf "${YELLOW}⚠${RESET} %s\n" "$*" >&2; }
die()    { printf "${RED}✗${RESET} %s\n" "$*" >&2; exit 1; }
header() { printf "\n${BOLD}%s${RESET}\n" "$*"; }
hr()     { printf '%0.s─' $(seq 1 60); echo; }

REPO="pradeep-av/agent-swarm"
GITHUB_BASE="https://github.com/${REPO}/releases/latest/download"

# ── Platform detection ────────────────────────────────────────────────────────
detect_platform() {
  OS=$(uname -s | tr '[:upper:]' '[:lower:]')
  case "$OS" in
    linux|darwin) ;;
    *) die "Unsupported OS: $OS" ;;
  esac

  ARCH_RAW=$(uname -m)
  case "$ARCH_RAW" in
    x86_64)        ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) die "Unsupported architecture: $ARCH_RAW" ;;
  esac
}

# ── Interactive prompts (safe for curl | bash) ────────────────────────────────
# All reads go through /dev/tty so they work even when stdin is the pipe.
ask() {
  # ask <prompt> <default> <varname>
  local prompt="$1" default="${2:-}" varname="$3"
  local display_default=""
  [[ -n "$default" ]] && display_default=" [${default}]"
  printf "${BOLD}%s${RESET}%s: " "$prompt" "$display_default" >/dev/tty
  local reply
  IFS= read -r reply </dev/tty
  [[ -z "$reply" ]] && reply="$default"
  printf -v "$varname" '%s' "$reply"
}

ask_secret() {
  # ask_secret <prompt> <varname>
  local prompt="$1" varname="$2"
  printf "${BOLD}%s${RESET}: " "$prompt" >/dev/tty
  local reply
  IFS= read -rs reply </dev/tty
  echo >/dev/tty
  [[ -z "$reply" ]] && die "Token cannot be empty."
  printf -v "$varname" '%s' "$reply"
}

ask_yn() {
  # ask_yn <prompt> <default y|n> → returns 0 for yes, 1 for no
  local prompt="$1" default="${2:-y}"
  local options="[Y/n]"; [[ "$default" == "n" ]] && options="[y/N]"
  printf "${BOLD}%s${RESET} %s: " "$prompt" "$options" >/dev/tty
  local reply
  IFS= read -r reply </dev/tty
  [[ -z "$reply" ]] && reply="$default"
  [[ "$reply" =~ ^[Yy] ]]
}

# ── XML escape for plist values ───────────────────────────────────────────────
xml_escape() {
  local val="$1"
  val="${val//&/&amp;}"
  val="${val//</&lt;}"
  val="${val//>/&gt;}"
  val="${val//\"/&quot;}"
  echo "$val"
}

# ── plist helper: one arg per <string> element ────────────────────────────────
plist_arg() {
  # plist_arg <flag> <value>  → emits two <string> lines (skipped if value empty)
  local flag="$1" value="$2"
  [[ -z "$value" ]] && return
  printf '        <string>%s</string>\n        <string>%s</string>\n' \
    "$(xml_escape "$flag")" "$(xml_escape "$value")"
}

# ── Sudo helper ───────────────────────────────────────────────────────────────
need_sudo() { [[ ! -w "$1" ]] && [[ "$EUID" -ne 0 ]]; }

run_privileged() {
  if [[ "$EUID" -eq 0 ]]; then
    "$@"
  else
    sudo "$@"
  fi
}

# ── Download binary ───────────────────────────────────────────────────────────
TMPDIR_WORK=""
cleanup() { [[ -n "$TMPDIR_WORK" ]] && rm -rf "$TMPDIR_WORK"; }
trap cleanup EXIT

download_binary() {
  local tarball="swarm-agent-${OS}-${ARCH}.tar.gz"
  local url="${GITHUB_BASE}/${tarball}"
  TMPDIR_WORK=$(mktemp -d)

  info "Fetching ${url}"
  if command -v curl &>/dev/null; then
    curl -fsSL --progress-bar "$url" -o "${TMPDIR_WORK}/${tarball}"
  elif command -v wget &>/dev/null; then
    wget -q --show-progress -O "${TMPDIR_WORK}/${tarball}" "$url"
  else
    die "curl or wget is required. Install either and re-run."
  fi

  tar -xzf "${TMPDIR_WORK}/${tarball}" -C "$TMPDIR_WORK"
  BINARY_SRC="${TMPDIR_WORK}/swarm-agent-${OS}-${ARCH}"
  [[ -f "$BINARY_SRC" ]] || die "Archive did not contain expected binary: ${BINARY_SRC}"
  chmod +x "$BINARY_SRC"
}

install_binary() {
  local dest="$1"
  local dest_dir; dest_dir=$(dirname "$dest")

  mkdir -p "$dest_dir" 2>/dev/null || run_privileged mkdir -p "$dest_dir"

  if need_sudo "$dest_dir"; then
    info "Writing to ${dest} (requires sudo)"
    run_privileged cp "$BINARY_SRC" "$dest"
    run_privileged chmod +x "$dest"
  else
    cp "$BINARY_SRC" "$dest"
    chmod +x "$dest"
  fi
  ok "Binary installed → ${dest}"
}

# ── systemd (Linux) ───────────────────────────────────────────────────────────
install_systemd() {
  local bin_path="$1" env_file="$2"
  local use_system=false
  local unit_file

  if [[ "$EUID" -eq 0 ]] || sudo -n true 2>/dev/null; then
    use_system=true
    unit_file="/etc/systemd/system/swarm-agent.service"
  else
    unit_file="${HOME}/.config/systemd/user/swarm-agent.service"
    mkdir -p "$(dirname "$unit_file")"
  fi

  # Write env file — secrets live here, not in the world-readable unit file.
  local env_dir; env_dir=$(dirname "$env_file")
  if need_sudo "$env_dir"; then
    run_privileged mkdir -p "$env_dir"
    printf 'SWARM_SWARMD_URL=%s\nSWARM_WORKER_ID=%s\nSWARM_TOKEN=%s\nSWARM_CAPABILITIES=%s\nSWARM_MODELS=%s\nSWARM_LABELS=%s\nSWARM_OPENCODE=%s\n' \
      "$SWARMD_URL" "$WORKER_ID" "$TOKEN" "$CAPABILITIES" "$MODELS" "$LABELS" "$OPENCODE_BIN" \
      | run_privileged tee "$env_file" >/dev/null
    run_privileged chmod 600 "$env_file"
  else
    mkdir -p "$env_dir"
    printf 'SWARM_SWARMD_URL=%s\nSWARM_WORKER_ID=%s\nSWARM_TOKEN=%s\nSWARM_CAPABILITIES=%s\nSWARM_MODELS=%s\nSWARM_LABELS=%s\nSWARM_OPENCODE=%s\n' \
      "$SWARMD_URL" "$WORKER_ID" "$TOKEN" "$CAPABILITIES" "$MODELS" "$LABELS" "$OPENCODE_BIN" \
      > "$env_file"
    chmod 600 "$env_file"
  fi
  ok "Env file written → ${env_file} (mode 600)"

  # ExecStart uses env vars — no secrets in the unit file itself.
  local unit_content
  unit_content=$(cat <<EOF
[Unit]
Description=swarm-agent — agent-swarm worker daemon
Documentation=https://github.com/${REPO}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=${env_file}
ExecStart=${bin_path} -swarmd \${SWARM_SWARMD_URL} -id \${SWARM_WORKER_ID} -token \${SWARM_TOKEN} -capabilities \${SWARM_CAPABILITIES} -models \${SWARM_MODELS} -labels \${SWARM_LABELS} -opencode \${SWARM_OPENCODE}
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=swarm-agent

[Install]
WantedBy=multi-user.target
EOF
)

  if $use_system; then
    echo "$unit_content" | run_privileged tee "$unit_file" >/dev/null
    run_privileged systemctl daemon-reload
    run_privileged systemctl enable --now swarm-agent.service
    ok "systemd system service installed and started"
    info "Logs  : journalctl -u swarm-agent -f"
    info "Config: ${env_file}"
  else
    echo "$unit_content" > "$unit_file"
    systemctl --user daemon-reload
    systemctl --user enable --now swarm-agent.service
    ok "systemd user service installed and started"
    warn "To survive logout, run: loginctl enable-linger $(whoami)"
    info "Logs  : journalctl --user -u swarm-agent -f"
    info "Config: ${env_file}"
  fi
}

# ── launchd (macOS) ───────────────────────────────────────────────────────────
install_launchd() {
  local bin_path="$1"
  local plist_dir="${HOME}/Library/LaunchAgents"
  local plist_file="${plist_dir}/io.github.agent-swarm.plist"
  local log_file="${HOME}/Library/Logs/swarm-agent.log"

  mkdir -p "$plist_dir"

  # Build argument list — only include flags with non-empty values
  local args
  args=$(cat <<EOF
        <string>$(xml_escape "$bin_path")</string>
$(plist_arg "-swarmd"       "$SWARMD_URL")
$(plist_arg "-id"           "$WORKER_ID")
$(plist_arg "-token"        "$TOKEN")
$(plist_arg "-capabilities" "$CAPABILITIES")
$(plist_arg "-models"       "$MODELS")
$(plist_arg "-labels"       "$LABELS")
$(plist_arg "-opencode"     "$OPENCODE_BIN")
EOF
)

  cat >"$plist_file" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
    "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>io.github.agent-swarm</string>

    <key>ProgramArguments</key>
    <array>
${args}
    </array>

    <key>KeepAlive</key><true/>
    <key>RunAtLoad</key><true/>
    <key>ThrottleInterval</key><integer>5</integer>
    <key>StandardOutPath</key><string>$(xml_escape "$log_file")</string>
    <key>StandardErrorPath</key><string>$(xml_escape "$log_file")</string>
</dict>
</plist>
EOF

  # Reload (idempotent)
  launchctl unload "$plist_file" 2>/dev/null || true
  launchctl load   "$plist_file"
  ok "launchd agent installed and started"
  info "Logs: tail -f ${log_file}"
}

# ── Uninstall ─────────────────────────────────────────────────────────────────
uninstall() {
  detect_platform
  header "Uninstalling swarm-agent"

  if [[ "$OS" == "linux" ]]; then
    if systemctl is-active --quiet swarm-agent 2>/dev/null; then
      run_privileged systemctl stop    swarm-agent || true
      run_privileged systemctl disable swarm-agent || true
    fi
    if systemctl --user is-active --quiet swarm-agent 2>/dev/null; then
      systemctl --user stop    swarm-agent || true
      systemctl --user disable swarm-agent || true
    fi
    run_privileged rm -f /etc/systemd/system/swarm-agent.service
    rm -f "${HOME}/.config/systemd/user/swarm-agent.service"
    # Remove env files from default locations (user may have customised the path)
    run_privileged rm -f /etc/swarm-agent/swarm-agent.env
    rm -f "${HOME}/.config/swarm-agent/swarm-agent.env"
    run_privileged systemctl daemon-reload 2>/dev/null || true
    systemctl --user daemon-reload         2>/dev/null || true
  elif [[ "$OS" == "darwin" ]]; then
    local plist="${HOME}/Library/LaunchAgents/io.github.agent-swarm.plist"
    launchctl unload "$plist" 2>/dev/null || true
    rm -f "$plist"
  fi

  # Ask which binary to remove
  local default_bin="/usr/local/bin/swarm-agent"
  ask "Binary path to remove" "$default_bin" INSTALL_PATH
  if [[ -f "$INSTALL_PATH" ]]; then
    if need_sudo "$(dirname "$INSTALL_PATH")"; then
      run_privileged rm -f "$INSTALL_PATH"
    else
      rm -f "$INSTALL_PATH"
    fi
    ok "Removed ${INSTALL_PATH}"
  else
    warn "Binary not found at ${INSTALL_PATH} — skipping."
  fi

  ok "swarm-agent uninstalled."
}

# ── Main ──────────────────────────────────────────────────────────────────────
main() {
  # Handle --uninstall flag
  for arg in "$@"; do
    [[ "$arg" == "--uninstall" ]] && { uninstall; exit 0; }
  done

  detect_platform

  hr
  printf "${BOLD}  swarm-agent installer${RESET}  (${OS}/${ARCH})\n"
  printf "  https://github.com/%s\n" "$REPO"
  hr

  header "Step 1 of 3 — Configuration"

  ask "swarmd WebSocket URL"       "wss://your-swarmd-host/agents" SWARMD_URL
  ask "Worker ID"                  "$(hostname -s 2>/dev/null || hostname)" WORKER_ID
  ask_secret "Auth token (pre-shared key)"                                  TOKEN
  ask "Capabilities (comma-separated)" "coding"                             CAPABILITIES
  ask "Models (comma-separated)"   ""                                        MODELS
  ask "Labels (comma-separated)"   ""                                        LABELS

  # Auto-detect opencode binary
  local detected_oc=""
  if command -v opencode &>/dev/null; then
    detected_oc="$(command -v opencode)"
  else
    for candidate in \
      "${HOME}/.opencode/bin/opencode" \
      "/usr/local/bin/opencode" \
      "/opt/homebrew/bin/opencode" \
      "/home/linuxbrew/.linuxbrew/bin/opencode"; do
      if [[ -x "$candidate" ]]; then
        detected_oc="$candidate"
        break
      fi
    done
  fi
  ask "OpenCode binary path" "${detected_oc:-/usr/local/bin/opencode}" OPENCODE_BIN

  if [[ ! -x "$OPENCODE_BIN" ]]; then
    warn "'${OPENCODE_BIN}' not found or not executable — make sure it exists before the service starts."
  fi

  # Install path
  local default_bin="/usr/local/bin/swarm-agent"
  ask "Install binary to" "$default_bin" INSTALL_PATH

  # Env file path (Linux only — shown conditionally but stored regardless for simplicity)
  local default_env_file
  if [[ "$EUID" -eq 0 ]] || sudo -n true 2>/dev/null; then
    default_env_file="/etc/swarm-agent/swarm-agent.env"
  else
    default_env_file="${HOME}/.config/swarm-agent/swarm-agent.env"
  fi
  ENV_FILE="$default_env_file"
  if [[ "$OS" == "linux" ]]; then
    ask "Env file path (config + token stored here, mode 600)" "$default_env_file" ENV_FILE
  fi

  # Summary before proceeding
  echo ""
  header "Summary"
  printf "  %-18s %s\n" "OS / arch:"     "${OS}/${ARCH}"
  printf "  %-18s %s\n" "swarmd URL:"    "$SWARMD_URL"
  printf "  %-18s %s\n" "Worker ID:"     "$WORKER_ID"
  printf "  %-18s %s\n" "Token:"         "$(head -c4 <<<"$TOKEN")****"
  printf "  %-18s %s\n" "Capabilities:"  "$CAPABILITIES"
  [[ -n "$MODELS" ]] && printf "  %-18s %s\n" "Models:" "$MODELS"
  [[ -n "$LABELS" ]] && printf "  %-18s %s\n" "Labels:" "$LABELS"
  printf "  %-18s %s\n" "OpenCode:"      "$OPENCODE_BIN"
  printf "  %-18s %s\n" "Binary path:"   "$INSTALL_PATH"
  [[ "$OS" == "linux" ]] && printf "  %-18s %s\n" "Env file:" "$ENV_FILE"
  echo ""

  ask_yn "Proceed with installation?" "y" || { info "Aborted."; exit 0; }

  header "Step 2 of 3 — Downloading binary"
  download_binary
  install_binary "$INSTALL_PATH"

  header "Step 3 of 3 — Installing service"
  if [[ "$OS" == "linux" ]]; then
    command -v systemctl &>/dev/null || die "systemd not found. Run the binary manually:\n  ${INSTALL_PATH} -swarmd ${SWARMD_URL} -token \"\$TOKEN\" ..."
    install_systemd "$INSTALL_PATH" "$ENV_FILE"
  elif [[ "$OS" == "darwin" ]]; then
    install_launchd "$INSTALL_PATH"
  fi

  echo ""
  hr
  ok "Installation complete! swarm-agent is running."
  hr
  echo ""
  if [[ "$OS" == "linux" ]]; then
    printf "  Status  : sudo systemctl status swarm-agent\n"
    printf "  Logs    : journalctl -u swarm-agent -f\n"
    printf "  Restart : sudo systemctl restart swarm-agent\n"
    printf "  Remove  : bash <(curl -fsSL https://raw.githubusercontent.com/%s/main/scripts/install-agent.sh) --uninstall\n" "$REPO"
  else
    printf "  Logs    : tail -f ~/Library/Logs/swarm-agent.log\n"
    printf "  Restart : launchctl unload ~/Library/LaunchAgents/io.github.agent-swarm.plist && launchctl load ~/Library/LaunchAgents/io.github.agent-swarm.plist\n"
    printf "  Remove  : bash <(curl -fsSL https://raw.githubusercontent.com/%s/main/scripts/install-agent.sh) --uninstall\n" "$REPO"
  fi
  echo ""
}

main "$@"
