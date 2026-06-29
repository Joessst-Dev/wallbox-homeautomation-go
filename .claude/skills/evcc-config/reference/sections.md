# evcc.yaml top-level sections

Source: https://docs.evcc.io/de/reference/configuration

## Core (needed by almost every install)

| Key          | Purpose |
| ------------ | ------- |
| `site`       | The installation as a whole; assigns grid/pv/battery meters and distributes available power across loadpoints. |
| `loadpoints` | Charging points. Each combines a `charger` and a default `vehicle` plus charging behaviour. |
| `chargers`   | Wallbox/charger devices and how to communicate with them. |
| `meters`     | Devices measuring power flows. Each needs a `usage`: `grid`, `pv`, `battery`, or `charge`. |
| `vehicles`   | EVs and (optionally) online credentials, enabling SoC limits and monitoring. |
| `tariffs`    | Electricity pricing and forecast sources (e.g. `currency`, `grid`, `feedin`, `co2`). |

## Integrations & services

| Key           | Purpose |
| ------------- | ------- |
| `mqtt`        | MQTT broker connection for publishing/subscribing evcc state. |
| `influx`      | InfluxDB time-series storage for historical data. |
| `hems`        | Integrate with a Home Energy Management System (share loadpoint/current data). |
| `eebus`       | EEBus protocol settings (e.g. SMA/EEBus-based control). |
| `eebus-cert`  | Certificates for secure EEBus communication. |
| `messaging`   | Event notifications across messaging platforms (push, email, etc.). |
| `modbusproxy` | Expose/forward Modbus so multiple clients can share one device. |

## System & operations

| Key            | Purpose |
| -------------- | ------- |
| `network`      | URI/host/port settings for the evcc web server and how it advertises itself. |
| `interval`     | Control loop timing — how often evcc polls and acts. |
| `log`          | Global log level. |
| `levels`       | Per-component log levels (override `log` for specific areas). |
| `sponsortoken` | Token unlocking sponsor-only features. Keep secret. |
| `telemetry`    | Opt-in anonymous usage/telemetry reporting. |
| `database`     | (where applicable) storage backend for session/history data. |
| `circuits`     | Load-management circuits enforcing current/power limits across loadpoints. |

## Notes

- Lists (`meters`, `chargers`, `vehicles`, `loadpoints`) hold objects; each device
  object carries a `name` used as a cross-reference elsewhere.
- `site.meters` references meter `name`s: `grid` (single), `pv` (list), `battery` (list).
- For the exhaustive set of keys and every supported field, see the upstream
  `evcc.dist.yaml`: https://github.com/evcc-io/evcc/blob/master/evcc.dist.yaml
