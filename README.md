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
Sungrow / Easee / Renault
        │  (hardware)
        ▼
      evcc  ──── MQTT (Mosquitto) ────►  wha
                                          │
                                    ┌─────┴──────┐
                            evcc.Store       Controller
                         (MQTT snapshot)   │
                                           ▼
                                         tick
                                    ┌─────┴──────┐
                              InputsFromSnapshot  │
                                    │             │
                                    ▼             │
                                  Decide          │
                                (pure fn)         │
                                    │             │
                                    ▼             ▼
                              publish mode    SQLite
                             (MQTT set-topic) (sessions /
                                              events /
                                              samples)
                                          │
                                          ▼
                                   Fiber web dashboard
                                    + JSON API
```

evcc owns all hardware. wha owns the *decision*: when surplus is sustained it sets the
loadpoint to `pv` (evcc then modulates current to the actual surplus, avoiding grid
import); on SoC cap, fail-safe, or manual override it sets `off`. It also publishes
`limitSoc=80` to evcc once at startup as a dead-man backstop.

### Decision policy (the core)

`surplus = chargePower − gridPower − max(0, batteryPower)`, with hysteresis (separate
start/stop thresholds) and dwell timers (sustained for minutes) to protect the Easee
contactor from cloud-latency churn.

State machine: `Idle → SurplusPending → Charging → StopPending → Idle`, with
`SocReached` (latched) and `FailSafe` overrides.

**Fail-safe priority order** (first match wins):

1. `Stale` data or disconnected broker → **off**
2. Active manual override → forced on/off
3. Not `Ready` (initial data not yet seen) → **off**
4. No vehicle `Connected` → **off**
5. `VehicleSoC ≥ socCap` (latched until SoC drops below `socResumeBelow`) → **off**
6. Surplus hysteresis + dwell → normal path

See `internal/controller/policy.go` — it is a pure function with a full Ginkgo suite.

## Layout

```
cmd/wha            entrypoint (single binary, no subcommands)
internal/config    config model + validation + viper loader (WHA_* env > file > default)
internal/store     SQLite (pure-Go modernc) + golang-migrate embedded migrations
internal/controller policy (pure Decide) + the control loop
internal/evcc      paho MQTT client + thread-safe state store
internal/web       Fiber + htmx + Tailwind dashboard and JSON API
internal/app       composition root (errgroup, graceful shutdown)
```

## Raspberry Pi setup (Docker Compose)

### Prerequisites

- Raspberry Pi (64-bit OS recommended) with Docker and Docker Compose installed.
- Your Sungrow WiNet-S dongle reachable over the local network (Modbus TCP on port 502).
- Easee Home charger credentials (email + password + charger serial).
- MY Renault account credentials + vehicle VIN.

### First run

```sh
# 1. Clone (or copy) the repo to the Pi
git clone https://github.com/Joessst-Dev/wallbox-homeautomation-go.git
cd wallbox-homeautomation-go

# 2. Create your evcc config from the template
cp evcc.example.yaml evcc.yaml
# Fill in all CHANGE_ME values (Sungrow IP, Easee creds, Renault creds)
# Then validate it:
#   docker compose run --rm evcc evcc --config /etc/evcc.yaml checkconfig

# 3. (Optional) tune wha thresholds
#   edit config.yaml — or leave it and use WHA_* env vars in docker-compose.yml

# 4. Start everything
docker compose up -d
```

### Using the published image

To use the pre-built multi-arch image from GitHub Container Registry instead of
building locally, change the `wha` service in `docker-compose.yml`:

```yaml
  wha:
    image: ghcr.io/joessst-dev/wha:edge   # latest main-branch build
    # image: ghcr.io/joessst-dev/wha:latest  # latest release tag
    # image: ghcr.io/joessst-dev/wha:0.2.0   # pinned release
    restart: unless-stopped
    ...
```

The `edge` tag is rebuilt on every push to `main` for both `linux/amd64` and
`linux/arm64`. Release tags (`latest`, `v0.x.y`) are built by GoReleaser on
tag push.

### Check logs and status

```sh
# All services
docker compose logs -f

# Just wha
docker compose logs -f wha

# evcc UI (hardware + charging state)
open http://<pi-ip>:7070

# wha dashboard (surplus controller state + session history)
open http://<pi-ip>:8080
```

### Managing volumes

Data is kept in named Docker volumes (`mosquitto-data`, `evcc-data`, `wha-data`).
To reset everything and start fresh:

```sh
docker compose down -v   # removes all named volumes — DELETES ALL DATA
docker compose up -d
```

To reset only the wha database (keeps evcc history and MQTT persistence):

```sh
docker compose stop wha
docker compose run --rm wha sh -c 'rm -f /data/wha.db'
docker compose start wha
```

## Configuration

`wha` is a single binary with no subcommands: on startup it applies database migrations,
then runs the control loop and web server until signalled (SIGINT/SIGTERM).

### Config file

Configuration is loaded from `config.yaml` searched in `/etc/wha` then the working
directory, or from the path given by `WHA_CONFIG`. The file in this repo is a
commented example covering every key with its default.

### Environment variables

Every config key can be set via a `WHA_` environment variable. The key is constructed
as `WHA_<SECTION>_<KEY>` with all letters uppercased. Nested camelCase keys are
uppercased in full — there is no separator between the camelCase words:

```
mqtt.broker              → WHA_MQTT_BROKER
mqtt.clientID            → WHA_MQTT_CLIENTID
control.startThresholdW  → WHA_CONTROL_STARTTHRESHOLDW
control.startDwell       → WHA_CONTROL_STARTDWELL
```

To override the config file path: `WHA_CONFIG=/path/to/config.yaml`

### Full config reference

All durations accept Go syntax: `"60s"`, `"2m"`, `"6h"`.

#### `mqtt`

| Key | Default | Notes |
|-----|---------|-------|
| `mqtt.broker` | `tcp://localhost:1883` | MQTT broker URL. Use `tcp://mosquitto:1883` in Docker Compose. |
| `mqtt.clientID` | `wha` | MQTT client identifier. |
| `mqtt.username` | *(empty)* | Broker username; leave empty if `allow_anonymous true`. |
| `mqtt.password` | *(empty)* | Broker password. |
| `mqtt.topicPrefix` | `evcc` | Must match `mqtt.topic` in `evcc.yaml`. |

#### `evcc`

| Key | Default | Notes |
|-----|---------|-------|
| `evcc.loadpointID` | `1` | Which evcc loadpoint to control (1-based). |

#### `control`

| Key | Default | Notes |
|-----|---------|-------|
| `control.enableMode` | `pv` | evcc mode when charging is enabled. `pv` = surplus only (no grid import); `now` = full power. |
| `control.startThresholdW` | `1400` | Surplus must reach this (watts) to start charging. ~min charge power for single-phase Twingo. |
| `control.stopThresholdW` | `0` | Surplus must drop below this (watts) before the stop dwell begins. |
| `control.startDwell` | `2m` | Surplus must be sustained this long before charging starts. Protects the Easee contactor from cloud-latency churn. |
| `control.stopDwell` | `3m` | Low surplus must be sustained this long before charging stops. Matches evcc's own `disableDelay`. |
| `control.socCap` | `80` | Stop charging at this vehicle SoC (%). |
| `control.socResumeBelow` | `78` | After a SoC-cap stop, only allow resuming once SoC drops below this (%). Prevents rapid re-stop. |
| `control.decisionInterval` | `15s` | How often the control loop evaluates. |
| `control.staleTimeout` | `60s` | Power metrics (grid/pv/battery/charge) older than this trigger the fail-safe. **Not applied to vehicle SoC** — evcc polls the Renault cloud ~hourly; the last known value is always used. |
| `control.republish` | `5m` | Re-send mode and `limitSoc` backstop periodically. MQTT set-topics are not retained; this ensures they survive a broker restart. |

#### `web`

| Key | Default | Notes |
|-----|---------|-------|
| `web.bindAddr` | `0.0.0.0` | IP address the HTTP server listens on. |
| `web.port` | `8080` | TCP port for the web dashboard and JSON API. |

#### `db`

| Key | Default | Notes |
|-----|---------|-------|
| `db.path` | `/data/wha.db` | SQLite database file path. The directory must be writable. |

#### `log`

| Key | Default | Notes |
|-----|---------|-------|
| `log.level` | `info` | Log verbosity: `debug`, `info`, `warn`, or `error`. |

## Safety model

wha uses **fail-safe-to-off** as its fundamental design principle. Charging is only
enabled when *all* conditions are actively met; any absence of data or connectivity
forces the mode to `off`.

### Stale data → off

Grid power and PV power must be seen (via MQTT) within `control.staleTimeout` (default
60 s). If either goes stale — or if the broker connection drops — `Stale` is set and
the controller immediately publishes `mode=off`. The next tick re-evaluates and will
re-enable charging as soon as fresh data arrives.

### Vehicle SoC is not stale-gated

evcc polls the Renault cloud for vehicle SoC at most ~hourly, and *only while charging*.
Applying a short stale timeout would mean wha never starts charging on a fresh vehicle
connection. The last known SoC is therefore always used for the cap check. The evcc
`limitSoc` backstop (see below) bounds any overshoot.

### `limitSoc` dead-man backstop

On every broker connect (and periodically via `control.republish`), wha publishes
`evcc/loadpoints/1/limitSoc/set = 80` to evcc. Even if wha dies or loses connectivity,
evcc will stop charging at that SoC independently. The backstop topic uses camelCase
(`limitSoc/set`, not `limitsoc/set`) — evcc silently ignores the lowercase form.

### MQTT security

evcc does **not** authenticate MQTT set-topics — anyone who can publish to the broker
can control your charging. See the **Security** section below.

## ⚠️ Security

Lock down Mosquitto before exposing anything outside your home LAN:

1. Edit `mosquitto/config/mosquitto.conf` and set:
   ```
   allow_anonymous false
   password_file /mosquitto/config/passwd
   ```
2. Create a password file:
   ```sh
   docker compose exec mosquitto mosquitto_passwd -c /mosquitto/config/passwd wha
   ```
3. Set credentials in `config.yaml` (or via `WHA_MQTT_USERNAME` / `WHA_MQTT_PASSWORD`)
   and in `evcc.yaml` under `mqtt.username`/`mqtt.password`.
4. Optionally add an ACL file restricting `wha` to only its required topics:
   - Subscribe: `evcc/#`
   - Publish: `evcc/loadpoints/1/mode/set`, `evcc/loadpoints/1/limitSoc/set`

## Troubleshooting

### `SQLITE_CANTOPEN` on first run

The wha container writes to `/data/wha.db`. The Docker image creates that directory
owned by `nonroot` (UID 65532). If the host volume is owned by root (as it is the first
time Docker creates it), the process cannot write there.

**Fix:** let Docker create the volume before starting the stack, then the `chown` in the
image's entrypoint will set ownership correctly. If you see this after copying in an old
database file:

```sh
docker compose run --rm --entrypoint sh wha -c 'chown -R 65532:65532 /data'
```

### evcc topics not arriving

1. Verify Mosquitto is reachable: `docker compose exec wha mosquitto_pub -h mosquitto -t test -m hello`
2. Inspect live topics with MQTT Explorer (`mqtt-explorer.com`) — subscribe to `evcc/#`.
3. Check that `mqtt.topic` in `evcc.yaml` matches `mqtt.topicPrefix` in `config.yaml` (default `evcc`).
4. If using broker auth, confirm the same credentials appear in both `evcc.yaml` and `config.yaml`.

### Sungrow WiNet-S not reachable

- The WiNet-S dongle must be on the **same LAN segment** as the Pi (or routed).
- Confirm Modbus TCP is enabled in the SolarInfo Bank app (≥ firmware V3.x is recommended).
- Test connectivity: `nc -zv <sungrow-ip> 502`
- Some Sungrow firmware versions disable Modbus TCP after a UI upgrade — re-enable it
  in the SolarInfo Bank app under *Advanced Settings → Communication → Modbus TCP*.

### Renault SoC is always 0 % or stale

evcc polls the Renault cloud **only while charging** and at most ~hourly. This is normal:
wha uses the last known value for the SoC cap check, and the evcc `limitSoc` backstop
bounds overshoot. If you never see SoC update:

- Confirm the MY Renault account credentials and VIN are correct in `evcc.yaml`.
- Check `docker compose logs evcc` for Renault API errors.
- The first SoC reading only arrives after the car starts charging.

### wha starts charging immediately at startup

If `overrideActive` is set in the dashboard, wha is in manual ForceOn mode. Click
**Auto** in the dashboard to return to automatic surplus control.

### Verifying charging commands reach evcc

Use MQTT Explorer to monitor `evcc/loadpoints/1/mode` and `evcc/loadpoints/1/mode/set`.
When wha detects surplus and publishes `mode=pv`, the read topic should update. If the
read topic stays at `off`, check evcc logs for ACL/auth errors.

## Local development

```sh
make test           # all Ginkgo suites
make run            # against a broker on localhost:1883 (WHA_MQTT_BROKER overrides)
make css            # recompile Tailwind after editing templates
make build-arm64    # static CGO-free binary for the Pi
```

See `CONTRIBUTING.md` for contribution guidelines.

## Notes & known limits

- Vehicle SoC is coarse (Renault cloud, ~hourly, only while charging), so the 80% cap
  can overshoot by up to a poll interval — the evcc `limitSoc` backstop bounds it.
- In `pv` mode there's an unavoidable minimum charge power (~1.4 kW, single-phase), so
  brief grid draw can occur as surplus dips before evcc pauses.
- Verify your evcc version publishes the expected topics with MQTT Explorer on first run.
