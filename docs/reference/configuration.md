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
| `control.enableMode` | `pv` | Initial charge mode when charging is enabled (`pv` = surplus only, `now` = full power). Overridden by the dashboard **Charge power** toggle which persists across restarts. |
| `control.startThresholdW` | `1400` | Surplus (W) required to start charging |
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

## Web

| Key | Default | Meaning |
|-----|---------|---------|
| `web.bindAddr` | `0.0.0.0` | Dashboard + API bind address |
| `web.port` | `8080` | Dashboard + API port |

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
  startThresholdW: 1400
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
