#!/bin/sh
# wha-updater: the privileged half of wha's in-UI software update.
#
# wha runs in a distroless, non-root container with no Docker socket, so it
# cannot replace its own image. Instead it writes the chosen target version into
# a shared request file; this sidecar (which owns the Docker socket) watches for
# that file and performs the actual pull + recreate of the wha service.
#
# Security: the target version is re-validated here against a strict X.Y.Z regex
# before it is ever used as an image tag. It is never interpolated into a shell
# command in a way that could inject arguments — a poisoned request file can at
# most name a (non-existent) image tag, which simply fails the pull.
set -eu

UPDATE_DIR="${WHA_UPDATE_DIR:-/run/update}"
COMPOSE_FILE="${WHA_COMPOSE_FILE:-/workspace/docker-compose.yml}"
IMAGE="${WHA_IMAGE:-ghcr.io/joessst-dev/wha}"
SERVICE="${WHA_SERVICE:-wha}"
POLL_INTERVAL="${WHA_UPDATE_POLL:-5}"
# wha runs as uid 65532 (distroless nonroot); make the shared dir writable for it.
WHA_UID="${WHA_UPDATE_UID:-65532}"

REQUEST="$UPDATE_DIR/request.json"
STATUS="$UPDATE_DIR/status.json"

# extract_version pulls "targetVersion" out of the request JSON without a JSON
# parser (busybox has no jq). The raw value is validated by is_semver before use.
extract_version() {
  sed -n 's/.*"targetVersion"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$REQUEST"
}

is_semver() {
  echo "$1" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'
}

# json_escape makes an arbitrary string safe to embed in a JSON string value:
# escape backslashes and double quotes, and drop newlines/carriage returns. Keep
# this in front of every field interpolated into write_status — message strings
# in particular may one day carry docker output.
json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g' | tr -d '\n\r'
}

write_status() { # state targetVersion message
  ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  cat > "$STATUS.tmp" <<EOF
{"state":"$(json_escape "$1")","targetVersion":"$(json_escape "$2")","message":"$(json_escape "$3")","updatedAt":"$ts"}
EOF
  mv "$STATUS.tmp" "$STATUS"
}

# detect_project finds the compose project name of the RUNNING wha container so
# we recreate the same stack regardless of the install directory's name. Falls
# back to COMPOSE_PROJECT_NAME, then to "wha".
detect_project() {
  if [ -n "${COMPOSE_PROJECT_NAME:-}" ]; then
    echo "$COMPOSE_PROJECT_NAME"
    return
  fi
  p="$(docker ps --filter "label=com.docker.compose.service=${SERVICE}" \
    --format '{{ index .Labels "com.docker.compose.project" }}' 2>/dev/null | head -n1)"
  echo "${p:-wha}"
}

apply_update() { # version
  version="$1"
  project="$(detect_project)"
  write_status applying "$version" "Pulling ${IMAGE}:${version}"
  echo "wha-updater: updating ${SERVICE} to ${version} (project: ${project})"

  # Pin the wha image tag in the compose file. The substitution is anchored to
  # the wha image reference only, so other services are untouched.
  if ! sed -i -E "s#(image:[[:space:]]*${IMAGE}):[^[:space:]\"']+#\1:${version}#" "$COMPOSE_FILE"; then
    write_status failed "$version" "Failed to rewrite compose file"
    return 1
  fi

  if ! docker compose -p "$project" -f "$COMPOSE_FILE" pull "$SERVICE"; then
    write_status failed "$version" "docker compose pull failed"
    return 1
  fi
  # Recreate ONLY the wha service so the updater never restarts itself.
  if ! docker compose -p "$project" -f "$COMPOSE_FILE" up -d "$SERVICE"; then
    write_status failed "$version" "docker compose up failed"
    return 1
  fi
  write_status done "$version" "Updated to ${version}"
  echo "wha-updater: ${SERVICE} updated to ${version}"
}

# selftest exercises the safety-critical bits (semver guard + tag rewrite)
# without needing the Docker socket. Run with: sh scripts/updater/run.sh _selftest
selftest() {
  fail=0
  for bad in "latest" "1.2" "1.2.3; rm -rf /" "../../etc" "1.2.3.4" ""; do
    if is_semver "$bad"; then echo "FAIL: accepted invalid version '$bad'"; fail=1; fi
  done
  for good in "1.2.3" "0.0.1" "10.20.30"; do
    if ! is_semver "$good"; then echo "FAIL: rejected valid version '$good'"; fail=1; fi
  done
  # Verify the tag-rewrite is anchored to the wha image only. Uses non-in-place
  # sed so the check is portable (BSD/macOS vs GNU/busybox); apply_update adds
  # only -i on top of this exact expression.
  sample="$(printf 'services:\n  evcc:\n    image: evcc/evcc:latest\n  wha:\n    image: %s:1.2.3\n' "$IMAGE")"
  rewritten="$(printf '%s\n' "$sample" | sed -E "s#(image:[[:space:]]*${IMAGE}):[^[:space:]\"']+#\1:9.9.9#")"
  printf '%s\n' "$rewritten" | grep -q "evcc/evcc:latest" || { echo "FAIL: tag rewrite touched the evcc image"; fail=1; }
  printf '%s\n' "$rewritten" | grep -q "${IMAGE}:9.9.9" || { echo "FAIL: tag rewrite did not pin the wha image"; fail=1; }
  [ "$fail" -eq 0 ] && echo "selftest OK"
  return "$fail"
}

if [ "${1:-}" = "_selftest" ]; then
  selftest
  exit $?
fi

mkdir -p "$UPDATE_DIR"
chown "$WHA_UID" "$UPDATE_DIR" 2>/dev/null || chmod 0777 "$UPDATE_DIR" 2>/dev/null || true

echo "wha-updater: watching $REQUEST (compose: $COMPOSE_FILE, service: $SERVICE)"
[ -f "$STATUS" ] || write_status idle "" ""

while true; do
  if [ -f "$REQUEST" ]; then
    version="$(extract_version || true)"
    # Consume the request immediately so a crash can't loop on a poisoned file.
    rm -f "$REQUEST"
    if is_semver "$version"; then
      apply_update "$version" || true
    else
      echo "wha-updater: rejecting invalid version '$version'"
      write_status failed "" "Rejected invalid version"
    fi
  fi
  sleep "$POLL_INTERVAL"
done
