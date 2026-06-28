# wha — Wallbox Home Automation

A small Go service that runs PV-surplus EV charging on top of [evcc](https://evcc.io).
It reads live state from evcc over MQTT, applies its own decision policy, and toggles
the loadpoint — with a web dashboard and history.

**Two rules:**
- **Surplus detected → start charging** (sustained PV surplus over a threshold).
- **Vehicle SoC > 80% → stop charging** (hard cap, latched).

## Hardware

| Role    | Device                                   | evcc template     |
| ------- | ---------------------------------------- | ----------------- |
| Inverter (grid+PV+battery) | Sungrow SH8.0 RT hybrid   | `sungrow-hybrid`  |
| Wallbox | Easee Home                               | `easee`           |
| Vehicle | Renault Twingo Electric                  | `renault`         |

## Architecture

```
Sungrow / Easee / Renault ──drivers──► evcc ──MQTT(Mosquitto)──► wha
                                                                  ├─ controller (policy)
                                                                  ├─ Fiber web dashboard + JSON API
                                                                  └─ SQLite (sessions / events / samples)
```

evcc owns all hardware. wha owns the *decision*: when surplus is sustained it sets the
loadpoint to `pv` (evcc then modulates current to the actual surplus, avoiding grid
import); on SoC cap, fail-safe, or manual override it sets `off`. It also publishes
`limitSoc=80` to evcc once at startup as a dead-man backstop.

### Decision policy (the core)
`surplus = chargePower − gridPower − max(0, batteryPower)`, with hysteresis (separate
start/stop thresholds) and dwell timers (sustained for minutes) to protect the Easee
contactor from cloud-latency churn. State machine: `Idle → SurplusPending → Charging →
StopPending → Idle`, with `SocReached` (latched) and `FailSafe` overrides. See
`internal/controller/policy.go` — it's a pure function with a full Ginkgo suite.

## Layout

```
cmd/wha            entrypoint
internal/config    config model + validation + viper loader (WHA_* env > file > default)
internal/store     SQLite (pure-Go modernc) + golang-migrate embedded migrations
internal/controller policy (pure Decide) + the control loop
internal/evcc      paho MQTT client + thread-safe state store
internal/web       Fiber + htmx + Tailwind dashboard and JSON API
internal/app       composition root (errgroup, graceful shutdown)
```

## Run

### Docker Compose (Raspberry Pi)
```sh
cp evcc.example.yaml evcc.yaml      # fill in Sungrow IP, Easee + Renault creds
# (optional) tune thresholds in config.yaml
docker compose up -d
```
- wha dashboard: `http://<pi>:8080`
- evcc UI: `http://<pi>:7070`

### Local development
```sh
make test           # all Ginkgo suites
make run            # against a broker on localhost:1883
make css            # recompile Tailwind after editing templates
make build-arm64    # static binary for the Pi
```

## Configuration

`wha` is a single binary with no subcommands: on startup it applies database migrations,
then runs the control loop and web server until signalled (SIGINT/SIGTERM).

Configuration is loaded by viper from (highest precedence first): `WHA_*` environment
variables, a YAML config file, then built-in defaults. The file path comes from
`WHA_CONFIG`, or `config.yaml` is searched for in `/etc/wha` and the working directory.
Env keys are `WHA_<SECTION>_<KEY>`, with nested camelCase keys uppercased fully — e.g.
`control.startThresholdW` → `WHA_CONTROL_STARTTHRESHOLDW`, `mqtt.broker` → `WHA_MQTT_BROKER`.

Key tuning (all in `config.yaml`):

| Key | Default | Notes |
| --- | --- | --- |
| `control.startThresholdW` / `stopThresholdW` | 1400 / 0 | hysteresis band |
| `control.startDwell` / `stopDwell` | 2m / 3m | anti-flap; don't go too low (Easee cloud latency) |
| `control.socCap` / `socResumeBelow` | 80 / 78 | stop + resume latch |
| `control.staleTimeout` | 60s | fail-safe for fast power metrics (vehicle SoC is not stale-gated) |

## ⚠️ Security

evcc does **not** authenticate MQTT set-topics — anyone who can publish to the broker
can control your charging. Lock down Mosquitto (`mosquitto/config/mosquitto.conf`) with
credentials + ACLs before exposing anything, and keep the broker on your home network.

## Notes & known limits
- Vehicle SoC is coarse (Renault cloud, ~hourly, only while charging), so the 80% cap
  can overshoot by up to a poll interval — the evcc `limitSoc` backstop bounds it.
- In `pv` mode there's an unavoidable minimum charge power (~1.4 kW, single-phase), so
  brief grid draw can occur as surplus dips before evcc pauses.
- Verify your evcc version publishes the expected topics with MQTT Explorer on first run.
```
