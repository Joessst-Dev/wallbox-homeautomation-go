#!/usr/bin/env bash
#
# wha installer — set up the PV-surplus EV charging stack (Mosquitto + evcc + wha)
# on a Raspberry Pi (or any Docker host) with a single command:
#
#   curl -fsSL https://raw.githubusercontent.com/Joessst-Dev/wallbox-homeautomation-go/main/scripts/install.sh | bash
#
# It downloads the published compose stack, prompts for your evcc credentials,
# generates a secured Mosquitto broker config, and brings everything up.
#
# Environment overrides (handy for headless / re-runs):
#   WHA_DIR=/opt/wha          install directory          (default: ~/wha)
#   WHA_IMAGE_TAG=latest      wha image tag, skip prompt  (latest|edge|vX.Y.Z)
#   WHA_SKIP_START=1          generate files but don't pull/start (same as --dry-run)
#
set -euo pipefail

# --- constants ---------------------------------------------------------------
REPO="Joessst-Dev/wallbox-homeautomation-go"
RAW_BASE="${WHA_RAW_BASE:-https://raw.githubusercontent.com/${REPO}/main}"
REPO_PKG="joessst-dev/wha"          # GHCR package path (forces lowercase)
IMAGE="ghcr.io/${REPO_PKG}"
TTY="${WHA_TTY:-/dev/tty}"          # override only for automated testing

INSTALL_DIR="${WHA_DIR:-$HOME/wha}"
IMAGE_TAG="${WHA_IMAGE_TAG:-}"
DRY_RUN="${WHA_SKIP_START:-}"

# --- output helpers ----------------------------------------------------------
if [ -t 2 ]; then
  B=$(printf '\033[1m'); R=$(printf '\033[0m')
  GRN=$(printf '\033[32m'); YLW=$(printf '\033[33m'); RED=$(printf '\033[31m'); CYN=$(printf '\033[36m')
else
  B=""; R=""; GRN=""; YLW=""; RED=""; CYN=""
fi
log()  { printf '%s==>%s %s\n' "$CYN" "$R" "$*" >&2; }
ok()   { printf '%s ✓ %s%s\n' "$GRN" "$*" "$R" >&2; }
warn() { printf '%s ! %s%s\n' "$YLW" "$*" "$R" >&2; }
die()  { printf '%s ✗ %s%s\n' "$RED" "$*" "$R" >&2; exit 1; }

usage() {
  cat <<'USAGE'
wha installer — set up the PV-surplus EV charging stack (Mosquitto + evcc + wha)
on a Raspberry Pi (or any Docker host) with a single command:

  curl -fsSL https://raw.githubusercontent.com/Joessst-Dev/wallbox-homeautomation-go/main/scripts/install.sh | bash

Options:
  --dry-run   Generate config files without pulling or starting the stack.
  --help      Show this message.

Environment overrides:
  WHA_DIR=<path>        Install directory (default: ~/wha)
  WHA_IMAGE_TAG=<tag>   wha image tag (latest|edge|vX.Y.Z), skips the version prompt
  WHA_SKIP_START=1      Same as --dry-run
USAGE
  exit 0
}

# --- arg parsing -------------------------------------------------------------
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    -h|--help) usage ;;
    *) die "Unknown argument: $arg (try --help)" ;;
  esac
done

# --- interactive I/O (must read from the terminal, not stdin) ----------------
# Under `curl ... | bash`, stdin IS the script, so prompts must read from the
# terminal. We open it once on fd 3 so sequential reads share one offset.
require_tty() {
  if [ ! -e "$TTY" ]; then
    die "No terminal available for prompts. Download and run the script directly:
    curl -fsSL ${RAW_BASE}/scripts/install.sh -o install.sh && bash install.sh"
  fi
  # Open the terminal on fd 3 for all prompts. (Note: do NOT add other
  # redirections here — `exec` makes them permanent for the whole script.)
  exec 3<"$TTY"
}

prompt() { # prompt_text [default] -> echoes answer on stdout
  local text="$1" def="${2:-}" reply
  if [ -n "$def" ]; then
    read -r -u 3 -p "$text [$def]: " reply || true
    printf '%s' "${reply:-$def}"
  else
    read -r -u 3 -p "$text: " reply || true
    printf '%s' "$reply"
  fi
}

prompt_required() { # prompt_text -> non-empty answer
  local text="$1" reply=""
  while [ -z "$reply" ]; do
    reply="$(prompt "$text")"
    [ -z "$reply" ] && warn "A value is required."
  done
  printf '%s' "$reply"
}

prompt_secret() { # prompt_text -> non-empty answer, no echo
  local text="$1" reply=""
  while [ -z "$reply" ]; do
    read -rs -u 3 -p "$text: " reply || true
    printf '\n' >&2
    [ -z "$reply" ] && warn "A value is required."
  done
  printf '%s' "$reply"
}

prompt_number() { # prompt_text default -> numeric answer (re-prompts on bad input)
  local text="$1" def="$2" reply
  while :; do
    reply="$(prompt "$text" "$def")"
    case "$reply" in
      *[!0-9.]* | '' | *.*.*) warn "Expected a number, got: $reply" ;;
      *) printf '%s' "$reply"; return ;;
    esac
  done
}

confirm() { # prompt_text [default y|n] -> 0 if yes
  local text="$1" def="${2:-y}" reply hint
  [ "$def" = y ] && hint="Y/n" || hint="y/N"
  read -r -u 3 -p "$text [$hint]: " reply || true
  reply="${reply:-$def}"
  case "$reply" in [yY]*) return 0 ;; *) return 1 ;; esac
}

# Single-quote an arbitrary string as a YAML scalar (escapes embedded quotes).
yaml_quote() { local s="${1//\'/\'\'}"; printf "'%s'" "$s"; }

# --- prerequisites -----------------------------------------------------------
command -v curl >/dev/null 2>&1 || die "curl is required but not installed."

check_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    local msg="Docker is not installed. Install it, then re-run this script:
    curl -fsSL https://get.docker.com | sh
    sudo usermod -aG docker \$USER   # then log out / back in"
    [ -n "$DRY_RUN" ] && { warn "$msg"; return; }
    die "$msg"
  fi
  if ! docker compose version >/dev/null 2>&1; then
    local msg="The Docker Compose plugin is missing. Install 'docker-compose-plugin' (or a recent Docker Desktop / Engine)."
    [ -n "$DRY_RUN" ] && { warn "$msg"; return; }
    die "$msg"
  fi
  if ! docker info >/dev/null 2>&1; then
    warn "Cannot talk to the Docker daemon. You may need to add yourself to the 'docker' group (sudo usermod -aG docker \$USER) and re-login, or run with sudo."
  fi
}

# --- start -------------------------------------------------------------------
printf '\n%swha installer%s — PV-surplus EV charging on evcc\n\n' "$B" "$R" >&2
require_tty
check_docker

# --- install dir + existing-config handling ----------------------------------
mkdir -p "$INSTALL_DIR/mosquitto/config"
chmod 700 "$INSTALL_DIR"
log "Install directory: $INSTALL_DIR"

REUSE=""
if [ -f "$INSTALL_DIR/evcc.yaml" ]; then
  warn "An existing config was found in $INSTALL_DIR."
  if confirm "Keep the existing configuration (answer No to reconfigure from scratch)?" y; then
    REUSE=1
    ok "Keeping existing configuration."
  fi
fi

fetch() { # remote_path local_file
  curl -fsSL "${RAW_BASE}/$1" -o "$INSTALL_DIR/$2" \
    || die "Failed to download $1 from $RAW_BASE"
}

# --- pick the wha version (rewrites the compose image tag) --------------------
# Tags come from the public GHCR registry (works even while the GitHub repo is
# private), so we always offer exactly what can actually be pulled.
ghcr_tags() {
  local tok
  tok="$(curl -fsSL "https://ghcr.io/token?scope=repository:${REPO_PKG}:pull&service=ghcr.io" 2>/dev/null \
    | grep -o '"token":"[^"]*"' | head -n1 | sed 's/.*:"//; s/"$//')"
  [ -z "$tok" ] && return 1
  curl -fsSL -H "Authorization: Bearer $tok" \
    "https://ghcr.io/v2/${REPO_PKG}/tags/list" 2>/dev/null \
    | grep -o '"tags":\[[^]]*\]' | grep -o '"[^"]*"' | sed 's/"//g' | grep -v '^tags$'
}

select_version() {
  if [ -n "$IMAGE_TAG" ]; then
    log "Using wha version from WHA_IMAGE_TAG: $IMAGE_TAG"
    return
  fi

  local tags semver has_latest default n
  tags="$(ghcr_tags || true)"
  semver="$(printf '%s\n' "$tags" | grep -E '^v?[0-9]+\.[0-9]+\.[0-9]+$' | { sort -rV 2>/dev/null || sort -r; } || true)"
  has_latest="$(printf '%s\n' "$tags" | grep -qx latest && echo 1 || true)"

  if [ -n "$has_latest" ]; then default="latest"; else default="edge"; fi

  printf '\nSelect the wha version to install:\n' >&2
  n=1
  if [ -n "$has_latest" ]; then
    printf '  %s%s)%s latest  — newest stable release (recommended)\n' "$B" "$n" "$R" >&2; n=$((n+1))
  fi
  printf '  %s%s)%s edge    — bleeding edge from main\n' "$B" "$n" "$R" >&2
  if [ -n "$semver" ]; then
    printf '  or type an exact version, e.g. %s\n' "$(printf '%s' "$semver" | head -n1)" >&2
  fi
  [ -z "$has_latest" ] && warn "No stable release published yet — 'edge' is the only rolling build."

  local choice
  choice="$(prompt "Choice (number or tag)" "$default")"
  case "$choice" in
    1) if [ -n "$has_latest" ]; then IMAGE_TAG="latest"; else IMAGE_TAG="edge"; fi ;;
    2) IMAGE_TAG="edge"
       [ -z "$has_latest" ] && warn "Only 'edge' is offered; using it." ;;
    latest|edge) IMAGE_TAG="$choice" ;;
    v[0-9]*|[0-9]*) IMAGE_TAG="$choice" ;;     # explicit version tag
    *) IMAGE_TAG="$default"; warn "Unrecognized choice, using '$default'." ;;
  esac

  # Sanity-check the chosen tag exists on the registry (non-fatal).
  if [ -n "$tags" ] && ! printf '%s\n' "$tags" | grep -qx "$IMAGE_TAG"; then
    warn "Tag '$IMAGE_TAG' was not found in the registry tag list — the pull may fail."
  fi
  log "Selected wha version: $IMAGE_TAG"
}

apply_image_tag() {
  # Replace the hardcoded `ghcr.io/joessst-dev/wha:<tag>` in the downloaded compose file.
  sed -i.bak -E "s#(image:[[:space:]]*${IMAGE}):[^[:space:]]+#\1:${IMAGE_TAG}#" \
    "$INSTALL_DIR/docker-compose.yml"
  rm -f "$INSTALL_DIR/docker-compose.yml.bak"
  ok "Pinned image: ${IMAGE}:${IMAGE_TAG}"
}

if [ -z "$REUSE" ]; then
  log "Downloading stack files…"
  fetch "docker-compose.yml" "docker-compose.yml"
  fetch "config.yaml" "config.yaml"
  # The wha-updater sidecar (in-UI software update) runs this script.
  mkdir -p "$INSTALL_DIR/scripts/updater"
  fetch "scripts/updater/run.sh" "scripts/updater/run.sh"
  chmod +x "$INSTALL_DIR/scripts/updater/run.sh"
  select_version
  apply_image_tag

  # --- collect evcc credentials ---------------------------------------------
  printf '\n%sevcc setup%s — your inverter, wallbox and vehicle\n' "$B" "$R" >&2
  INVERTER_IP="$(prompt_required "Sungrow inverter / WiNet-S IP address")"
  EASEE_USER="$(prompt_required "Easee account email")"
  EASEE_PASS="$(prompt_secret  "Easee account password")"
  EASEE_SERIAL="$(prompt_required "Easee charger serial (e.g. EH000000)")"
  RENAULT_USER="$(prompt_required "MY Renault account email")"
  RENAULT_PASS="$(prompt_secret  "MY Renault account password")"
  RENAULT_VIN="$(prompt "Vehicle VIN (optional, starts with VF…)")"
  VEH_CAPACITY="$(prompt_number "Vehicle usable battery capacity (kWh)" "22")"
  VEH_MAXCURRENT="$(prompt_number "Charger max current (A)" "16")"
  PRICE_GRID="$(prompt_number "Grid import price (EUR/kWh)" "0.29")"
  PRICE_FEEDIN="$(prompt_number "Feed-in price (EUR/kWh)" "0.10")"

  # --- broker credentials ----------------------------------------------------
  # Hex (no special chars) keeps YAML/sed substitution trivial and safe.
  MQTT_USER="wha"
  MQTT_PASS="$(openssl rand -hex 24 2>/dev/null || head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')"

  # --- generate evcc.yaml ----------------------------------------------------
  log "Writing evcc.yaml…"
  vin_line="    # vin: not provided"
  [ -n "$RENAULT_VIN" ] && vin_line="    vin: $(yaml_quote "$RENAULT_VIN")"

  cat >"$INSTALL_DIR/evcc.yaml" <<EOF
# Generated by scripts/install.sh — evcc config for the wha stack.
# Sungrow SH8.0 RT + Easee Home + Renault Twingo. Validate with:
#   docker run --rm -v "\$PWD/evcc.yaml":/etc/evcc.yaml:ro evcc/evcc evcc -c /etc/evcc.yaml checkconfig

mqtt:
  broker: mosquitto:1883
  topic: evcc
  user: $(yaml_quote "$MQTT_USER")        # evcc uses 'user', not 'username'
  password: $(yaml_quote "$MQTT_PASS")

site:
  title: Home
  meters:
    grid: sungrow_grid
    pv:
      - sungrow_pv
    battery:
      - sungrow_battery

loadpoints:
  - title: Garage
    charger: easee_home
    vehicle: twingo
    phases: 3 # Easee Home is 3-phase; set to 1 if wired single-phase
    mode: pv # wha drives this between pv and off
    enable:
      delay: 1m
      threshold: 0 # wha already gates surplus
    disable:
      delay: 3m
      threshold: 0 # protects the contactor from rapid cycling

meters:
  - name: sungrow_grid
    type: template
    template: sungrow-hybrid
    usage: grid
    modbus: tcpip
    id: 1
    host: $(yaml_quote "$INVERTER_IP")
    port: 502
  - name: sungrow_pv
    type: template
    template: sungrow-hybrid
    usage: pv
    modbus: tcpip
    id: 1
    host: $(yaml_quote "$INVERTER_IP")
    port: 502
  - name: sungrow_battery
    type: template
    template: sungrow-hybrid
    usage: battery
    modbus: tcpip
    id: 1
    host: $(yaml_quote "$INVERTER_IP")
    port: 502

chargers:
  - name: easee_home
    type: template
    template: easee
    user: $(yaml_quote "$EASEE_USER")
    password: $(yaml_quote "$EASEE_PASS")
    charger: $(yaml_quote "$EASEE_SERIAL")

vehicles:
  - name: twingo
    type: template
    template: renault
    title: Renault Twingo Electric
    capacity: ${VEH_CAPACITY}
    user: $(yaml_quote "$RENAULT_USER")
    password: $(yaml_quote "$RENAULT_PASS")
${vin_line}
    minCurrent: 8
    maxCurrent: ${VEH_MAXCURRENT}

tariffs:
  currency: EUR
  grid:
    type: fixed
    price: ${PRICE_GRID}
  feedin:
    type: fixed
    price: ${PRICE_FEEDIN}
EOF

  # --- secure Mosquitto ------------------------------------------------------
  log "Securing Mosquitto (broker auth + ACL)…"
  cat >"$INSTALL_DIR/mosquitto/config/mosquitto.conf" <<'EOF'
# Generated by scripts/install.sh — broker locked down with auth + ACL.
listener 1883
persistence true
persistence_location /mosquitto/data/

allow_anonymous false
password_file /mosquitto/config/passwd
acl_file /mosquitto/config/acl
EOF

  # One shared credential covers evcc (publishes state, subscribes to its set-topics)
  # and wha (reads state, writes evcc/loadpoints/<id>/.../set). A finer two-user split
  # (evcc full, wha set-topics only) is possible future hardening.
  cat >"$INSTALL_DIR/mosquitto/config/acl" <<EOF
user ${MQTT_USER}
topic readwrite evcc/#
EOF

  generate_passwd() {
    if command -v mosquitto_passwd >/dev/null 2>&1; then
      mosquitto_passwd -b -c "$INSTALL_DIR/mosquitto/config/passwd" "$MQTT_USER" "$MQTT_PASS"
    elif command -v docker >/dev/null 2>&1; then
      docker run --rm -v "$INSTALL_DIR/mosquitto/config":/c eclipse-mosquitto:2 \
        mosquitto_passwd -b -c /c/passwd "$MQTT_USER" "$MQTT_PASS" >/dev/null 2>&1
    else
      # Reachable only in a dry run (check_docker already exits otherwise). Guard
      # explicitly so a live run can never start Mosquitto with a plaintext passwd.
      [ -n "$DRY_RUN" ] || die "Neither mosquitto_passwd nor docker is available — cannot hash the broker password."
      warn "No mosquitto_passwd or docker available — writing a plaintext placeholder (dry-run only)."
      printf '%s:%s\n' "$MQTT_USER" "$MQTT_PASS" >"$INSTALL_DIR/mosquitto/config/passwd"
    fi
  }
  generate_passwd
  ok "Broker user '${MQTT_USER}' created."

  # --- wire broker creds into wha config.yaml --------------------------------
  sed -i.bak -E \
    -e "s|^([[:space:]]*username:)[[:space:]]*\"\"|\1 \"${MQTT_USER}\"|" \
    -e "s|^([[:space:]]*password:)[[:space:]]*\"\"|\1 \"${MQTT_PASS}\"|" \
    "$INSTALL_DIR/config.yaml"
  rm -f "$INSTALL_DIR/config.yaml.bak"

  # --- lock down files containing secrets ------------------------------------
  chmod 600 "$INSTALL_DIR/evcc.yaml" "$INSTALL_DIR/config.yaml" \
            "$INSTALL_DIR/mosquitto/config/passwd" 2>/dev/null || true
  ok "Configuration written to $INSTALL_DIR (broker password saved in evcc.yaml / config.yaml)."
fi

# --- dry run stops here ------------------------------------------------------
if [ -n "$DRY_RUN" ]; then
  warn "Dry run — generated files only, not starting the stack."
  log  "Inspect: $INSTALL_DIR  (then run without --dry-run / WHA_SKIP_START to launch)"
  exit 0
fi

# --- validate evcc config (best effort) --------------------------------------
if [ -z "$REUSE" ]; then
  log "Validating evcc configuration…"
  # Use the exact evcc image the stack will run, so checkconfig matches its schema.
  evcc_image="$(grep -E '^[[:space:]]*image:[[:space:]]*evcc/' "$INSTALL_DIR/docker-compose.yml" \
    | awk '{print $2}' | head -n1)"
  if docker run --rm -v "$INSTALL_DIR/evcc.yaml":/etc/evcc.yaml:ro "${evcc_image:-evcc/evcc:latest}" \
       evcc -c /etc/evcc.yaml checkconfig >/dev/null 2>&1; then
    ok "evcc config is valid."
  else
    warn "evcc checkconfig reported problems (it cannot reach the inverter yet, which is expected on first run)."
    confirm "Continue and start the stack anyway?" y || die "Aborted. Edit $INSTALL_DIR/evcc.yaml and re-run."
  fi
fi

# --- start + verify ----------------------------------------------------------
log "Pulling images…"
docker compose -f "$INSTALL_DIR/docker-compose.yml" pull
log "Starting the stack…"
docker compose -f "$INSTALL_DIR/docker-compose.yml" up -d

log "Waiting for wha to become healthy…"
healthy=""
i=0
while [ "$i" -lt 30 ]; do
  if curl -fsS "http://localhost:8080/healthz" >/dev/null 2>&1; then healthy=1; break; fi
  sleep 2
  i=$((i + 1))
done

ip="$(hostname -I 2>/dev/null | awk '{print $1}')"; ip="${ip:-localhost}"
printf '\n' >&2
if [ -n "$healthy" ]; then
  ok "wha is up!"
else
  warn "wha did not report healthy yet — check logs:  docker compose -f $INSTALL_DIR/docker-compose.yml logs -f wha"
fi
cat >&2 <<EOF

${B}All set.${R}
  wha dashboard : http://${ip}:8080
  evcc UI       : http://${ip}:7070

  Logs    : docker compose -f $INSTALL_DIR/docker-compose.yml logs -f wha
  Update  : from the dashboard — the "Software" card checks GHCR and upgrades in place.
            Or manually: docker compose -f $INSTALL_DIR/docker-compose.yml pull && \\
            docker compose -f $INSTALL_DIR/docker-compose.yml up -d
EOF
