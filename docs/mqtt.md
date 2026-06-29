# evcc MQTT topic contract

wha talks to a running evcc instance entirely over MQTT (a shared Mosquitto broker). evcc
must be configured to publish — add an `mqtt:` block to `evcc.yaml` pointing at the same
broker (see `evcc.example.yaml`). The default topic prefix is `evcc`; `<id>` is the 1-based
loadpoint id (`control.loadpointID`, default `1`).

All payloads are **UTF-8 strings** (no JSON wrapper on leaf topics). Read topics are
published **retained**, so wha re-seeds its state from the last known values on every
(re)connect.

## Read topics (wha subscribes)

| Topic | Type | Notes |
| ----- | ---- | ----- |
| `evcc/site/grid/power` | float | **+ = import**, − = export/feed-in |
| `evcc/site/pvPower` | float | + = generation |
| `evcc/site/homePower` | float | + = consumption |
| `evcc/site/battery/soc` | float `0..100` | aggregate battery SoC |
| `evcc/site/battery/power` | float | **+ = discharge**, − = charge |
| `evcc/loadpoints/<id>/chargePower` | float | current charge power (W) |
| `evcc/loadpoints/<id>/charging` | `true`/`false` | literal bool strings, **not** `1`/`0` |
| `evcc/loadpoints/<id>/connected` | `true`/`false` | vehicle plugged in |
| `evcc/loadpoints/<id>/enabled` | `true`/`false` | evcc has the loadpoint enabled |
| `evcc/loadpoints/<id>/vehicleSoc` | float `0..100` | **coarse** — updated ~hourly, only while charging |
| `evcc/loadpoints/<id>/mode` | `off`/`now`/`minpv`/`pv` | current mode |
| `evcc/loadpoints/<id>/limitSoc` | int | current SoC limit |
| `evcc/status` | `online`/`offline` | evcc's MQTT LWT; drives fail-safe |

Parse everything numeric as `float64` (evcc trims trailing decimals, e.g. `1234.000` →
`1234`). Booleans are exactly `true`/`false`.

## Command topics (wha publishes)

Published **non-retained**, QoS 1. Casing matters — the set segment is the **same** as the
read key:

| Topic | Payload | Used for |
| ----- | ------- | -------- |
| `evcc/loadpoints/<id>/mode/set` | `off` \| `now` \| `minpv` \| `pv` | enable/disable charging (wha toggles `pv` ↔ `off`) |
| `evcc/loadpoints/<id>/limitSoc/set` | int (percent) | the dead-man SoC backstop (`= socCap`) |

> ⚠️ **`limitSoc/set` is camelCase.** A lowercase `limitsoc/set` is silently ignored by evcc.

## Behavior notes

- **Auth:** evcc does *not* authenticate set-topics — broker ACLs are the only protection
  (see Security in the README). Anyone who can publish can control charging.
- **Stale vehicle SoC:** because evcc only polls SoC while charging, a fresh install may have
  no SoC until the first charge. wha does not stale-gate `vehicleSoc`; use ForceOn once to
  kick-start, and rely on the `limitSoc` backstop to bound overshoot.
- **Republish:** set-topics are not retained, so wha re-asserts `mode` and `limitSoc` on
  broker reconnect, on the `evcc/status` online edge (evcc restart), and on the
  `control.republish` cadence.
- **Version drift:** topic shapes can differ across evcc versions. Verify against your
  instance with MQTT Explorer on first run; mismatched names show up as a permanently-stale
  metric (fail-safe) or a never-ready controller.
