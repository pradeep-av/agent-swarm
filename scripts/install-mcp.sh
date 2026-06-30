#!/usr/bin/env bash
# install-mcp.sh — swarmd MCP server installer
#
# Downloads the swarmd binary and wires it into your MCP provider config so that
# OpenCode (and other supported providers) can spawn it as a local MCP server.
#
# Usage (one-liner):
#   curl -fsSL https://raw.githubusercontent.com/pradeep-av/agent-swarm/main/scripts/install-mcp.sh | bash -
#
# Or download and run:
#   curl -fsSL https://raw.githubusercontent.com/pradeep-av/agent-swarm/main/scripts/install-mcp.sh -o install-mcp.sh
#   bash install-mcp.sh
#
# Flags:
#   --uninstall   Remove the binary and MCP config entry

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
  case "$OS" in linux|darwin) ;; *) die "Unsupported OS: $OS" ;; esac

  ARCH_RAW=$(uname -m)
  case "$ARCH_RAW" in
    x86_64)        ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) die "Unsupported architecture: $ARCH_RAW" ;;
  esac
}

# ── Interactive prompts (safe for curl | bash) ────────────────────────────────
ask() {
  local prompt="$1" default="${2:-}" varname="$3"
  local display_default=""
  [[ -n "$default" ]] && display_default=" [${default}]"
  printf "${BOLD}%s${RESET}%s: " "$prompt" "$display_default" >/dev/tty
  local reply; IFS= read -r reply </dev/tty
  [[ -z "$reply" ]] && reply="$default"
  printf -v "$varname" '%s' "$reply"
}

ask_secret() {
  local prompt="$1" varname="$2"
  printf "${BOLD}%s${RESET}: " "$prompt" >/dev/tty
  local reply; IFS= read -rs reply </dev/tty; echo >/dev/tty
  [[ -z "$reply" ]] && die "Token cannot be empty."
  printf -v "$varname" '%s' "$reply"
}

ask_yn() {
  local prompt="$1" default="${2:-y}"
  local options="[Y/n]"; [[ "$default" == "n" ]] && options="[y/N]"
  printf "${BOLD}%s${RESET} %s: " "$prompt" "$options" >/dev/tty
  local reply; IFS= read -r reply </dev/tty
  [[ -z "$reply" ]] && reply="$default"
  [[ "$reply" =~ ^[Yy] ]]
}

# ── sudo helper ───────────────────────────────────────────────────────────────
need_sudo() { [[ ! -w "$1" ]] && [[ "$EUID" -ne 0 ]]; }
run_privileged() { [[ "$EUID" -eq 0 ]] && "$@" || sudo "$@"; }

# ── Download binary ───────────────────────────────────────────────────────────
TMPDIR_WORK=""
cleanup() { [[ -n "$TMPDIR_WORK" ]] && rm -rf "$TMPDIR_WORK"; }
trap cleanup EXIT

download_binary() {
  local tarball="swarmd-${OS}-${ARCH}.tar.gz"
  local url="${GITHUB_BASE}/${tarball}"
  TMPDIR_WORK=$(mktemp -d)

  info "Fetching ${url}"
  if command -v curl &>/dev/null; then
    curl -fsSL --progress-bar "$url" -o "${TMPDIR_WORK}/${tarball}"
  elif command -v wget &>/dev/null; then
    wget -q --show-progress -O "${TMPDIR_WORK}/${tarball}" "$url"
  else
    die "curl or wget is required."
  fi

  tar -xzf "${TMPDIR_WORK}/${tarball}" -C "$TMPDIR_WORK"
  BINARY_SRC="${TMPDIR_WORK}/swarmd-${OS}-${ARCH}"
  [[ -f "$BINARY_SRC" ]] || die "Archive did not contain expected binary."
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

# ── jq check ─────────────────────────────────────────────────────────────────
require_jq() {
  if ! command -v jq &>/dev/null; then
    warn "jq is not installed — cannot auto-update the config file."
    warn "Install jq and re-run, or add the snippet below manually."
    return 1
  fi
}

# ── Provider config paths ─────────────────────────────────────────────────────
opencode_config_path() {
  # Respect XDG, fall back to ~/.config/opencode/opencode.json
  local xdg="${XDG_CONFIG_HOME:-${HOME}/.config}"
  echo "${xdg}/opencode/opencode.json"
}

# ── Print the manual config snippet ──────────────────────────────────────────
print_snippet() {
  local bin_path="$1" addr="$2" token="$3"
  cat <<EOF

  Add this to your MCP provider config:

  ── opencode  (~/.config/opencode/opencode.json) ──────────────────────────
  {
    "\$schema": "https://opencode.ai/config.json",
    "mcp": {
      "agent-swarm": {
        "type":    "local",
        "command": ["${bin_path}", "-addr", "${addr}", "-token", "${token}"],
        "enabled": true
      }
    }
  }
  ──────────────────────────────────────────────────────────────────────────

  Also copy the agent spec to your OpenCode agents directory:

    mkdir -p ~/.config/opencode/agents
    curl -fsSL https://raw.githubusercontent.com/${REPO}/main/.opencode/agents/swarm.md \
      -o ~/.config/opencode/agents/swarm.md

EOF
}

# ── Configure opencode ────────────────────────────────────────────────────────
configure_opencode() {
  local bin_path="$1" addr="$2" token="$3" cfg="$4"
  local cfg_dir; cfg_dir=$(dirname "$cfg")

  mkdir -p "$cfg_dir"

  # Build the command array as a JSON string
  local cmd_json
  cmd_json=$(jq -n \
    --arg bin "$bin_path" \
    --arg addr "$addr" \
    --arg tok "$token" \
    '[$bin, "-addr", $addr, "-token", $tok]')

  if [[ -f "$cfg" ]]; then
    cp "$cfg" "${cfg}.bak"
    jq \
      --argjson cmd "$cmd_json" \
      '
        .["$schema"] //= "https://opencode.ai/config.json" |
        .mcp["agent-swarm"] = {
          "type":    "local",
          "command": $cmd,
          "enabled": true
        }
      ' "$cfg" > "${cfg}.tmp" && mv "${cfg}.tmp" "$cfg"
  else
    jq -n \
      --argjson cmd "$cmd_json" \
      '{
        "$schema": "https://opencode.ai/config.json",
        "mcp": {
          "agent-swarm": {
            "type":    "local",
            "command": $cmd,
            "enabled": true
          }
        }
      }' > "$cfg"
  fi

  ok "opencode config updated → ${cfg}"
}

# ── Install agent markdown spec ───────────────────────────────────────────────
install_agent_file() {
  local agents_dir="$1"
  mkdir -p "$agents_dir"
  local dest="${agents_dir}/swarm.md"
  local url="https://raw.githubusercontent.com/${REPO}/main/.opencode/agents/swarm.md"

  info "Fetching agent spec ${url}"
  if command -v curl &>/dev/null; then
    curl -fsSL "$url" -o "$dest"
  elif command -v wget &>/dev/null; then
    wget -q -O "$dest" "$url"
  else
    warn "curl or wget not found — skipping agent spec install."
    warn "Download manually: ${url}"
    return
  fi
  ok "Agent spec installed → ${dest}"
}

# ── Uninstall ─────────────────────────────────────────────────────────────────
uninstall() {
  detect_platform
  header "Uninstalling swarmd MCP"

  local default_bin="/usr/local/bin/swarmd"
  ask "Binary path to remove" "$default_bin" INSTALL_PATH
  if [[ -f "$INSTALL_PATH" ]]; then
    need_sudo "$(dirname "$INSTALL_PATH")" && run_privileged rm -f "$INSTALL_PATH" || rm -f "$INSTALL_PATH"
    ok "Removed ${INSTALL_PATH}"
  else
    warn "Binary not found at ${INSTALL_PATH} — skipping."
  fi

  if command -v jq &>/dev/null; then
    local cfg; cfg=$(opencode_config_path)
    if [[ -f "$cfg" ]]; then
      cp "$cfg" "${cfg}.bak"
      jq 'del(.mcp["agent-swarm"])' "$cfg" > "${cfg}.tmp" && mv "${cfg}.tmp" "$cfg"
      ok "Removed agent-swarm entry from ${cfg}"
    fi
  else
    warn "jq not found — remove the 'agent-swarm' MCP entry from your provider config manually."
  fi

  local xdg="${XDG_CONFIG_HOME:-${HOME}/.config}"
  local agent_file="${xdg}/opencode/agents/swarm.md"
  if [[ -f "$agent_file" ]]; then
    rm -f "$agent_file"
    ok "Removed agent spec ${agent_file}"
  fi
}

# ── Main ──────────────────────────────────────────────────────────────────────
main() {
  for arg in "$@"; do
    [[ "$arg" == "--uninstall" ]] && { uninstall; exit 0; }
  done

  detect_platform

  hr
  printf "${BOLD}  swarmd MCP installer${RESET}  (${OS}/${ARCH})\n"
  printf "  https://github.com/%s\n" "$REPO"
  hr

  header "Step 1 of 3 — Configuration"

  local default_bin="/usr/local/bin/swarmd"
  ask "Install binary to"    "$default_bin"  INSTALL_PATH
  ask "swarmd listen address" ":8080"         ADDR
  ask_secret "Auth token (pre-shared key)"    TOKEN

  # Provider selection (opencode is the only one for now)
  echo ""
  info "Supported MCP providers: opencode"
  ask "MCP provider" "opencode" PROVIDER
  case "$PROVIDER" in
    opencode) ;;
    *) die "Unsupported provider '${PROVIDER}'. Only 'opencode' is supported right now." ;;
  esac

  # Config path
  local default_cfg
  case "$PROVIDER" in
    opencode) default_cfg=$(opencode_config_path) ;;
  esac
  ask "Provider config file" "$default_cfg" CONFIG_PATH

  # Summary
  echo ""
  header "Summary"
  printf "  %-20s %s\n" "OS / arch:"       "${OS}/${ARCH}"
  printf "  %-20s %s\n" "Binary path:"     "$INSTALL_PATH"
  printf "  %-20s %s\n" "Listen address:"  "$ADDR"
  printf "  %-20s %s\n" "Token:"           "$(head -c4 <<<"$TOKEN")****"
  printf "  %-20s %s\n" "MCP provider:"    "$PROVIDER"
  printf "  %-20s %s\n" "Config file:"     "$CONFIG_PATH"
  echo ""

  ask_yn "Proceed with installation?" "y" || { info "Aborted."; exit 0; }

  header "Step 2 of 3 — Downloading binary"
  download_binary
  install_binary "$INSTALL_PATH"

  header "Step 3 of 3 — Configuring MCP provider"

  local xdg="${XDG_CONFIG_HOME:-${HOME}/.config}"
  if require_jq; then
    configure_opencode "$INSTALL_PATH" "$ADDR" "$TOKEN" "$CONFIG_PATH"
    install_agent_file "${xdg}/opencode/agents"
  else
    print_snippet "$INSTALL_PATH" "$ADDR" "$TOKEN"
  fi

  echo ""
  hr
  ok "swarmd is installed and wired into ${PROVIDER}."
  hr
  echo ""
  printf "  Restart OpenCode (or reload its config) and you should see\n"
  printf "  the 'agent-swarm' MCP server available.\n"
  echo ""
  printf "  ${BOLD}Quick test:${RESET}\n"
  printf "    %s -addr %s -token \"\$TOKEN\"\n" "$INSTALL_PATH" "$ADDR"
  echo ""
  printf "  ${BOLD}Uninstall:${RESET}\n"
  printf "    bash <(curl -fsSL https://raw.githubusercontent.com/%s/main/scripts/install-mcp.sh) --uninstall\n" "$REPO"
  echo ""
}

main "$@"
