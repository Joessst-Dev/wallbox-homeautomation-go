# API Reference

`wha` exposes a small HTTP API under `/api/*`, plus two probe endpoints at the
root. The dashboard's htmx controls are just clients of these same endpoints —
anything you can do by clicking a button, you can also do with `curl`.

All endpoints are served on `web.bindAddr:web.port` (default `0.0.0.0:8080`),
unauthenticated — see [Secure access](/guide/secure-access) before exposing
the dashboard beyond your home network.

## Conventions

- **Request body.** The `POST` endpoints accept either a form-encoded body
  (`Content-Type: application/x-www-form-urlencoded`, as the dashboard forms
  send) or JSON (`Content-Type: application/json`). Field names are identical
  in both.
- **htmx vs. plain JSON responses.** Every `POST` endpoint checks for the
  `HX-Request` header (which htmx sets automatically). When present, the
  response is the freshly-rendered `partials/controls` HTML fragment — the
  same markup the dashboard swaps in. Otherwise the response is
  `{"ok": true}` as JSON. Validation failures return `400` with a plain-text
  error message in either case.

## Probes

### `GET /healthz`

Liveness probe. Always returns `200 ok` once the process is up — it does not
check the broker or evcc.

### `GET /readyz`

Readiness probe. Returns `200 ok` once `wha` has an established MQTT
connection to the broker, otherwise `503 not ready`. Use this (not `/healthz`)
for orchestration/reverse-proxy readiness checks, since a live process with no
broker connection has no data to serve.

## Status

### `GET /api/status`

Returns the full status view model as JSON — the same data backing the
dashboard's live-status card, power-flow card, vehicle card, and controls.
Key fields:

| Field | Type | Meaning |
|-------|------|---------|
| `state` | string | Controller FSM state (`idle`, `surplus_pending`, `charging`, `stop_pending`, `soc_reached`, `failsafe`) |
| `reason` / `reasonCode` | string | Human-readable reason and its stable machine code for the last decision |
| `desiredMode` | string | evcc loadpoint mode `wha` last set (`pv`, `now`, `off`) |
| `override` | string | Manual override (`auto`, `on`, `off`) |
| `overrideUntil` | string | RFC3339 auto-revert time, omitted when no expiry |
| `overrideActive` | bool | `true` when override is anything other than `auto` |
| `capBypass` / `capBypassSoC` / `capBypassTarget` | bool / int / int | "Charge past the cap" state — see [Dashboard → Charge past the cap](/guide/dashboard#charge-past-the-cap) |
| `chargePower` / `chargePowerNow` | string / bool | Persistent charge-power mode (`pv`\|`now`) |
| `autoProfile` | string | Effective auto-charge profile (`surplus`, `power_latch`, `surplus_latch`) — see [Dashboard → Auto-charge profile](/guide/dashboard#auto-charge-profile) |
| `autoProfilePowerLatch` / `autoProfileSurplusLatch` / `autoProfileIsLatch` | bool | The profile above, pre-reduced |
| `startThresholdW` | int | Effective latch start threshold (W) for the active profile |
| `targetSoC` | int | Effective stop target (%) for the active profile |
| `socCap` / `socMax` / `socCapPlusOne` | int | Configured SoC cap, ceiling, and the lowest cap-bypass target accepted |
| `surplusW` / `surplusKW`, `gridW`/`gridKW`, `pvW`/`pvKW`, `homeW`/`homeKW`, `batteryW`/`batteryKW`, `chargeW`/`chargeKW` | int / string | Current power-flow readings, in watts and pre-formatted kW |
| `batterySoC` / `vehicleSoC` | int | State of charge, in percent |
| `charging` / `connected` / `ready` / `stale` | bool | Loadpoint/vehicle/data-freshness flags |
| `online` / `brokerConnected` | bool | evcc-online (LWT) and MQTT-broker-connected health flags |
| `updatedAt` / `updatedAgo` / `hasUpdated` | string / string / bool | When `wha` last received fresh data from evcc |

## Overrides and charging controls

### `POST /api/override`

Sets the manual override.

| Field | Type | Values | Notes |
|-------|------|--------|-------|
| `mode` | string | `auto` \| `on` \| `off` | Required. `auto` returns to automatic surplus control; `on` forces charging; `off` forces it off. |
| `hours` | number | `> 0` | Optional auto-revert delay. `0` or omitted means no expiry. |
| `capBypass` | bool | — | Only meaningful with `mode=on`; lifts the SoC stop from `socCap` to `socMax` (or `capBypassSoC` if given). Ignored for other modes. |
| `capBypassSoC` | int | `socCap+1`–`socMax` | Target SoC for cap-bypass charging. `0`/omitted falls back to `socMax`. |

### `POST /api/charge-power`

Sets the persistent charge-power mode used whenever charging is enabled
(automatic or `Charge now`).

| Field | Type | Values |
|-------|------|--------|
| `power` | string | `pv` \| `now` |

### `POST /api/auto-profile`

Sets the persistent auto-charge profile.

| Field | Type | Values |
|-------|------|--------|
| `profile` | string | `surplus` \| `power_latch` \| `surplus_latch` |

### `POST /api/start-threshold`

Sets the persistent start-threshold override (watts, 100 W steps, must exceed
`control.stopThresholdW`) for the **currently active** latch profile. `400`
if the active profile is `surplus` — the threshold only applies to a latch
profile.

| Field | Type | Values |
|-------|------|--------|
| `startThresholdW` | number | positive multiple of 100 |

### `POST /api/target-soc`

Sets the persistent target-SoC override (10 % steps, 50–100) used as the stop
target for the **currently active** latch profile. `400` if the active
profile is `surplus`.

| Field | Type | Values |
|-------|------|--------|
| `targetSoC` | int | multiple of 10, 50–100 |

::: tip Per-profile settings
`start-threshold` and `target-soc` are stored per auto-charge profile: switch
from `power_latch` to `surplus_latch` and each profile remembers its own
values. See [Configuration reference → control.autoProfile](/reference/configuration#control).
:::

## History

### `GET /api/sessions`

Returns recent charge sessions as JSON. Query params: `limit` (default 20).

### `GET /api/events`

Returns recent controller/command audit events as JSON. Query params:
`limit` (default 50).

### `GET /api/history`

Returns recent power-flow samples as JSON. Query params: `from`/`to`
(RFC3339) bounding the window, default the last 24 hours, capped at 7 days.
An unparseable value, or `to` before `from`, returns `400`.

## Software update

### `POST /api/update/check`

Forces a GHCR re-check for a newer release. Returns the update-info JSON (or
the refreshed `partials/update` fragment for `HX-Request`).

### `POST /api/update/apply`

Requests an in-place update to the given version (requires the
`wha-updater` sidecar, see [Dashboard → Software update](/guide/dashboard#software-update)).
`400` if updates are disabled, or if `version` doesn't exactly match the
latest version the last `/api/update/check` reported.

| Field | Type | Values |
|-------|------|--------|
| `version` | string | must equal the latest checked release, e.g. `1.4.0` |
