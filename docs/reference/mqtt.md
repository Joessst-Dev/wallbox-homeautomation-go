# MQTT Topic Contract

`wha` talks to a running evcc instance entirely over MQTT (a shared Mosquitto broker). evcc must be configured to publish — add an `mqtt:` block to `evcc.yaml` pointing at the same broker (see `evcc.example.yaml`). The default topic prefix is `evcc`; `<id>` is the 1-based loadpoint ID (`evcc.loadpointID`, default `1`).

All payloads are **UTF-8 strings** (no JSON wrapper on leaf topics). Read topics are published **retained**, so `wha` re-seeds its state from the last known values on every (re)connect.

## Read topics (wha subscribes)

| Topic | Type | Notes |
|-------|------|-------|
| `evcc/site/grid/power` | float | **+ = import**, − = export/feed-in |
| `evcc/site/pvPower` | float | + = generation |
| `evcc/site/homePower` | float | + = consumption |
| `evcc/site/battery/soc` | float `0..100` | Aggregate battery SoC |
| `evcc/site/battery/power` | float | **+ = discharge**, − = charge |
| `evcc/loadpoints/<id>/chargePower` | float | Current charge power (W) |
| `evcc/loadpoints/<id>/charging` | `true`/`false` | Literal bool strings, **not** `1`/`0` |
| `evcc/loadpoints/<id>/connected` | `true`/`false` | Vehicle plugged in |
| `evcc/loadpoints/<id>/enabled` | `true`/`false` | evcc has the loadpoint enabled |
| `evcc/loadpoints/<id>/vehicleSoc` | float `0..100` | evcc's **estimate** (energy × 0.85 / vehicle `capacity`), re-synced only when the Renault API returns a changed value (polled only while charging by default) |
| `evcc/loadpoints/<id>/mode` | `off`/`smart`/`now` (evcc ≥ 0.316); `off`/`now`/`minpv`/`pv` (older) | Current mode. evcc ≥ 0.316 reports `smart` where older versions reported `pv`, and never `minpv`. `wha` only stores the read-back value; it never compares it with what it sent |
| `evcc/loadpoints/<id>/alwaysCharge` | `off`/`on`/`once` | evcc ≥ 0.316; replaces `minpv`. Informational — `wha` does not use it |
| `evcc/loadpoints/<id>/limitSoc` | int | Current SoC limit |
| `evcc/loadpoints/<id>/effectiveMinCurrent` | float (A) | Minimum current evcc actually uses (loadpoint/vehicle/charger combined). Drives the start-threshold floor |
| `evcc/loadpoints/<id>/phasesConfigured` | int `0`/`1`/`3` | Configured phases; `0` = evcc auto-switches 1p/3p (`wha`'s start floor then counts 1 phase) |
| `evcc/loadpoints/<id>/phasesActive` | int `1`/`3` | Phases currently in use |
| `evcc/loadpoints/<id>/maxCurrent` | float (A) | Current maximum charge current (evcc persists changes made via `maxCurrent/set`) |
| `evcc/loadpoints/<id>/chargerStatusReason` | string | Charger-reported reason when not charging; shown in the stall banner (content depends on the charger; may be empty) |
| `evcc/status` | `online`/`offline` | evcc's MQTT LWT; drives fail-safe |

`effectiveMinCurrent`, `phasesConfigured`, `phasesActive`, `maxCurrent` and `chargerStatusReason` are optional: `wha` uses `control.minCurrentA` / `control.phases` for the start floor until (or unless) evcc publishes `effectiveMinCurrent` / `phasesConfigured`, so an older evcc does not break the controller. Whether your evcc version publishes each of them should be verified with MQTT Explorer.

Parse everything numeric as `float64` (evcc trims trailing decimals, e.g. `1234.000` → `1234`). Booleans are exactly `true`/`false`.

## Command topics (wha publishes)

Published **non-retained**, QoS 1. Casing matters — the set segment is the **same** as the read key:

| Topic | Payload | Used for |
|-------|---------|---------|
| `evcc/loadpoints/<id>/mode/set` | `off` \| `now` \| `pv` \| `minpv` (also `smart` on evcc ≥ 0.316) | Enable/disable charging (wha sends `pv`, `now` or `off`). evcc ≥ 0.316 still accepts `pv`/`minpv`; sending `pv` also forces `alwaysCharge` to `off` |
| `evcc/loadpoints/<id>/limitSoc/set` | whole number (percent, e.g. `80`; evcc does no range check) | The dead-man SoC backstop (= `socCap` normally, `socMax` when charging past the cap) |
| `evcc/loadpoints/<id>/maxCurrent/set` | float (A), decimal payload | The dashboard **Max charge current** setting. Clamped by `wha` to `control.maxCurrentCeilingA`. evcc rejects values below the loadpoint `minCurrent` |

::: warning camelCase matters
`limitSoc/set` and `maxCurrent/set` are camelCase. Lowercase variants (`limitsoc/set`, `maxcurrent/set`) are silently ignored by evcc.
:::

::: warning maxCurrent has no upper bound in evcc
evcc rejects anything **below** the loadpoint's `minCurrent` (visible only as an `ERROR max current must be greater or equal than min current` line in evcc's log, not on MQTT), and does not cap the value at your circuit rating. `wha` therefore clamps to `control.maxCurrentCeilingA` before publishing, and re-publishes it on broker reconnect, on the evcc online edge, and on the `control.republish` cadence.
:::

## Behavior notes

- **Persistence:** evcc persists the values set through `mode/set`, `limitSoc/set` and `maxCurrent/set` across its own restarts. `wha` still re-asserts them (see Republish below).
- **Auth:** evcc does *not* authenticate set-topics — broker ACLs are the only protection. Anyone who can publish can control charging.
- **SoC is an estimate:** `vehicleSoc` is evcc's estimate, only re-synced when the Renault API returns a changed value. Between polls it can drift from the car's own display — see [Troubleshooting → SoC differs from the car](/reference/troubleshooting#soc-differs-from-the-car-estimate).
- **Stale vehicle SoC:** because evcc only polls SoC while charging (default `soc.poll.mode: charging`), a fresh install may have no SoC until the first charge. `wha` does not stale-gate `vehicleSoc`; use **Charge now** once to kick-start, and rely on the `limitSoc` backstop to bound overshoot.
- **Republish:** set-topics are not retained, so `wha` re-asserts `mode` and `limitSoc` on broker reconnect, on the `evcc/status` online edge (evcc restart), and on the `control.republish` cadence.
- **Version drift:** topic shapes can differ across evcc versions. Verify against your instance with MQTT Explorer on first run; mismatched topic names show up as a permanently-stale metric (fail-safe) or a never-ready controller.
