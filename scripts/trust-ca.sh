#!/usr/bin/env bash
#
# trust-ca.sh — trust wha's reverse-proxy internal CA root on macOS or
# Debian/Ubuntu Linux. Phones can't be scripted (a website/script cannot
# silently install a root CA into a mobile trust store); use the /setup page's
# per-device steps there instead.
#
# Usage:
#   scripts/trust-ca.sh https://<wha-address>/setup/root.crt   # download + trust
#   scripts/trust-ca.sh /path/to/root.crt                      # trust a local file
#
set -euo pipefail

SOURCE="${1:-}"
if [ -z "$SOURCE" ]; then
  echo "Usage: $0 <https-url-or-path-to-root.crt>" >&2
  exit 1
fi

TMP=""
cleanup() { [ -n "$TMP" ] && rm -f "$TMP"; }
trap cleanup EXIT

case "$SOURCE" in
  http://*|https://*)
    command -v curl >/dev/null 2>&1 || { echo "curl is required to fetch $SOURCE" >&2; exit 1; }
    TMP="$(mktemp)"
    # The proxy's cert is Caddy's own internal CA, not yet trusted — skip
    # verification for this one-time bootstrap fetch only.
    curl -fsSk "$SOURCE" -o "$TMP"
    CERT_PATH="$TMP"
    ;;
  *)
    CERT_PATH="$SOURCE"
    ;;
esac

[ -f "$CERT_PATH" ] || { echo "Certificate not found: $CERT_PATH" >&2; exit 1; }

case "$(uname -s)" in
  Darwin)
    echo "Adding to the macOS System keychain (you will be prompted for your password)..."
    sudo security add-trusted-cert -d -r trustRoot \
      -k /Library/Keychains/System.keychain "$CERT_PATH"
    echo "Done. Restart your browser to pick up the new trust."
    ;;
  Linux)
    if [ "$(id -u)" -ne 0 ]; then
      echo "This step needs root; re-running with sudo..." >&2
      exec sudo "$0" "$CERT_PATH"
    fi
    command -v update-ca-certificates >/dev/null 2>&1 || {
      echo "update-ca-certificates not found — this script supports Debian/Ubuntu-based systems." >&2
      exit 1
    }
    install -m 0644 "$CERT_PATH" /usr/local/share/ca-certificates/wha-root.crt
    update-ca-certificates
    echo "Done. Restart your browser to pick up the new trust."
    ;;
  *)
    echo "Unsupported OS: $(uname -s). Use the per-device steps on the /setup page instead." >&2
    exit 1
    ;;
esac
