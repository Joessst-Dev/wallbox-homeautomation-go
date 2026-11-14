# Configuration Reference

Full table of every configuration key, its default, and its meaning.

## Loading order

(Highest precedence first)

1. `WHA_*` environment variables
2. YAML config file (`WHA_CONFIG` path, or `config.yaml` in `/etc/wha` or the working directory)
3. Built-in defaults

Env key format: `WHA_<SECTION>_<KEY>` with nested camelCase keys uppercased fully. Durations accept Go syntax: `60s`, `2m`, `6h`.

## MQTT

| Key | Default | Meaning |
|-----|---------|---------|
| `mqtt.broker` | `tcp://localhost:1883` | Mosquitto broker URL |
| `mqtt.clientID` | `wha` | MQTT client ID (must be unique on the broker) |
| `mqtt.username` | _(empty)_ | Broker username (set this — see Security) |
| `mqtt.password` | _(empty)_ | Broker password |
| `mqtt.topicPrefix` | `evcc` | evcc's MQTT topic prefix |

## evcc

| Key | Default | Meaning |
|-----|---------|---------|
| `evcc.loadpointID` | `1` | Which evcc loadpoint to control (1-based) |

## Control

| Key | Default | Meaning |
|-----|---------|---------|
| `control.enableMode` | `pv` | Initial charge mode when charging is enabled (`pv` = surplus only — evcc ≥ 0.316 calls it `smart`, and `wha` still sends `pv`, which evcc accepts; `now` = full power). Overridden by the dashboard **Charge power** toggle which persists across restarts. |
| `control.autoProfile` | `surplus` | Initial auto-charge profile (`surplus`, `power_latch`, or `surplus_latch`). This is only the **startup default** — the runtime profile, and each latch profile's own start-threshold/target-SoC, are operator settings set from the dashboard's **Auto-charge profile** control and persisted in the database, not read from env/YAML after first boot. See [Dashboard → Auto-charge profile](/guide/dashboard#auto-charge-profile). |
| `control.startThresholdW` | `1400` | Surplus (W) required to start charging. Set it to roughly the minimum charge power for **your** phases × `minCurrent` (3 phases × 8 A × 230 V ≈ 5.5 kW; 1 phase × 6 A ≈ 1.4 kW). `wha` **auto-floors** it at `(effectiveMinCurrent, else minCurrentA)` × 230 V × phases + `startMarginW` (surplus and `surplus_latch` profiles; **not** `power_latch`), so a value that is too low cannot cause start/stop flapping. See the note below. |
| `control.minCurrentA` | `8` | Minimum charge current (A) for the start floor. Used only while evcc has not published `effectiveMinCurrent`. Also the lower bound accepted by `POST /api/max-current`. |
| `control.phases` | `3` | Phase count (`1` or `3`) for the start floor. Used **only** when evcc has not published `phasesConfigured` (or published a negative/garbage value). If evcc publishes `phasesConfigured` > 0 that value wins; if it publishes `0` (automatic 1p/3p switching) the floor counts **1 phase**, because a start is single-phase — `control.phases` is *not* used then. |
| `control.startMarginW` | `200` | Extra watts added on top of the computed minimum charge power for the start floor. |
| `control.maxCurrentCeilingA` | `16` | Circuit/charger rating in A. Clearing the dashboard max-current limit sets evcc's `maxCurrent` to this value. Lowering it clamps a saved limit above it at startup (warning logged); a saved limit below `minCurrentA` is clamped up. Set it to what your circuit and charger can carry — `wha` never sends a max-current above it (the dashboard **Max charge current** setting is clamped to it). |
| `control.stallTimeout` | `3m` | Charger enabled but not charging for this long → dashboard "stalled" banner and one recovery attempt per plug-in session. |
| `control.stallRecoveryOff` | `90s` | How long the single recovery attempt keeps the loadpoint `off` before resuming. |
| `control.stopThresholdW` | `0` | Surplus (W) below which charging stops |
| `control.startDwell` | `2m` | Surplus must hold this long before starting (anti-flap) |
| `control.stopDwell` | `3m` | Low surplus must hold this long before stopping |
| `control.socCap` | `80` | Stop charging at this vehicle SoC (%) |
| `control.socResumeBelow` | `78` | Only resume once SoC drops below this (latch) |
| `control.socMax` | `100` | Maximum SoC when charging past the cap is enabled (must be > `socCap` and ≤ 100) |
| `control.decisionInterval` | `15s` | How often the control loop evaluates |
| `control.staleTimeout` | `60s` | Power metrics older than this → fail-safe (off) |
| `control.republish` | `5m` | Re-send mode + limitSoc backstop periodically (set-topics aren't retained) |
| `control.retentionWindow` | `2160h` (90 days) | Prune samples/events older than this; `0` disables pruning |
| `control.retentionInterval` | `6h` | How often the pruning janitor runs |

::: warning Behaviour change: the start floor (single-phase installs)
The effective start threshold for the **surplus** and **surplus_latch** profiles (not `power_latch`) is now
`max(startThresholdW, floor)` where `floor = (effectiveMinCurrent if evcc publishes it, else control.minCurrentA) × 230 V × phases + control.startMarginW`.

With the defaults (8 A, 3 phases, 200 W margin) the floor is **5720 W** until evcc publishes its own values. A single-phase install that used to start at 1400 W **will not start** until evcc reports `effectiveMinCurrent` / `phasesConfigured`, or, if your charger is single-phase **and your car allows 6 A**, you set `control.phases: 1` and `control.minCurrentA: 6` (floor then 1580 W). A 3-phase Renault Twingo needs 8 A, so its floor stays at 5720 W. The dashboard shows both the configured and the effective (floored) value — see `configuredStartThresholdW`, `minChargePowerW` and `startThresholdFloored` in the [API](/reference/api#get-api-status).
:::

## Web

| Key | Default | Meaning |
|-----|---------|---------|
| `web.bindAddr` | `0.0.0.0` | Dashboard + API bind address |
| `web.port` | `8080` | Dashboard + API port |
| `web.caRootPath` | _(empty)_ | Path to a reverse proxy's CA root certificate (e.g. Caddy's internal CA root), served for download from `/setup`. Empty disables those routes (they 404, never 500) — see [Secure access](/guide/secure-access). |

## Database

| Key | Default | Meaning |
|-----|---------|---------|
| `db.path` | `/data/wha.db` | SQLite database path |

## Logging

| Key | Default | Meaning |
|-----|---------|---------|
| `log.level` | `info` | `debug` \| `info` \| `warn` \| `error` |

## Update

| Key | Default | Meaning |
|-----|---------|---------|
| `update.enabled` | `false` | Enable the in-UI software update mechanism (requires the `wha-updater` sidecar) |
| `update.repository` | `joessst-dev/wha` | GHCR package path checked for newer releases |
| `update.requestDir` | `/run/update` | Shared volume the sidecar watches for update requests |
| `update.checkTTL` | `1h` | How long a GHCR check result is cached before re-querying |

## Example config.yaml

```yaml
mqtt:
  broker: tcp://mosquitto:1883
  clientID: wha
  username: wha
  password: <generated-by-installer>
  topicPrefix: evcc

evcc:
  loadpointID: "1"

control:
  enableMode: pv
  autoProfile: surplus
  startThresholdW: 1400   # floored automatically at minCurrentA x 230 V x phases + startMarginW
  minCurrentA: 8
  phases: 3
  startMarginW: 200
  maxCurrentCeilingA: 16  # your circuit/charger rating
  stallTimeout: 3m
  stallRecoveryOff: 90s
  stopThresholdW: 0
  startDwell: 2m
  stopDwell: 3m
  socCap: 80
  socResumeBelow: 78
  decisionInterval: 15s
  staleTimeout: 60s
  republish: 5m

web:
  bindAddr: 0.0.0.0
  port: 8080

db:
  path: /data/wha.db

log:
  level: info
```
