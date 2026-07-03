# Secure access

The wha dashboard can be [installed as an app](/guide/dashboard#install-as-an-app)
and works offline once installed. Both require a **secure context** — HTTPS or
`localhost` — because browsers only allow a Progressive Web App's service
worker (which powers offline caching *and* the install prompt) to register
over a secure connection. Plain `http://<pi-ip>:8080` is not a secure context
on any platform, including iOS.

## The Caddy internal CA model

The stack ships a [Caddy](https://caddyserver.com/) reverse proxy in front of
wha, configured with `tls internal`: Caddy generates and manages its own
self-signed certificate authority, with no public domain or DNS required.
Caddy terminates HTTPS and forwards requests to wha over the internal Docker
network.

The one-time setup cost is trusting that CA on each device you use — a
website (or `wha` itself) **cannot silently install a root certificate into a
device's trust store**. Every modern OS and browser forbids it, because
otherwise any site could man-in-the-middle you. What wha *does* provide is a
guided setup page and, where a device can be scripted, a one-liner.

Local development (`http://localhost:8080`) is already a secure context, so
you can test the full PWA experience without Caddy at all.

## Guided setup

Open **`https://<your-wha-address>/setup`** from the dashboard (or the
**Secure access & install as app** link). The page:

- Downloads the root certificate, or an iOS/macOS configuration profile that
  packages it for a guided install.
- Lists exact steps for iOS, Android, macOS, Windows, and Linux.
- Links the desktop helper scripts described below.

If no CA root is configured for this deployment (for example, local dev
without Caddy), the page explains that and skips straight to the
"Install as an app" step.

## Per-device steps

| Device | Approach |
|--------|----------|
| iOS / iPadOS | Install the configuration profile from `/setup`, then enable full trust under **Settings → General → About → Certificate Trust Settings**. Manual taps required — this cannot be scripted. |
| Android | Install the downloaded root certificate as a **CA certificate** under **Settings → Security**. Manual taps required. |
| macOS | Run `scripts/trust-ca.sh <url-or-path>`, or install manually via Keychain Access and set **Always Trust**. |
| Windows | Run `scripts/trust-ca.ps1 <url-or-path>` as Administrator, or import manually into **Local Machine → Trusted Root Certification Authorities**. |
| Linux (Debian/Ubuntu) | Run `scripts/trust-ca.sh <url-or-path>` (copies to `/usr/local/share/ca-certificates` and runs `update-ca-certificates`). |

Desktop/Linux machines can be scripted because they expose a local trust
store an admin process can write to; phones deliberately don't allow that from
an unprivileged script or web page.

## Deployment

`scripts/install.sh` prompts for the hostname or IP you'll browse to
(`WHA_SITE_ADDRESS`), writes it to `.env`, and starts Caddy alongside the rest
of the stack. So wha can serve `/setup/root.crt` and
`/setup/wha-ca.mobileconfig`, a small `caddy-cert-export` helper copies **only**
Caddy's public root certificate out of the `caddy-data` volume into a dedicated
`caddy-cacert` volume — the only certificate path mounted into wha. wha never
gets a filesystem path into `caddy-data` itself, which also holds the CA's
**private key**.

::: warning Keep the Caddy data volume
The `caddy-data` volume holds the internal CA's private key. If it's lost,
Caddy generates a *new* CA on next start and every previously trusted device
shows a certificate warning again until it re-trusts the new root.
:::

For a manual (non-`install.sh`) setup, set `WHA_SITE_ADDRESS` yourself (an
`.env` file next to `docker-compose.yml` works) before `docker compose up -d`.
