# Troubleshooting

## Installer issues

### No terminal available for prompts

Under `curl … | bash` the shell's stdin is the script itself, so the installer opens `/dev/tty` for interactive prompts. If you're running without a terminal (a CI job, an SSH session with no PTY), download the script first and run it directly, or pre-supply values via environment variables:

```sh
curl -fsSL https://raw.githubusercontent.com/Joessst-Dev/wallbox-homeautomation-go/main/scripts/install.sh \
  -o install.sh
WHA_DIR=/opt/wha WHA_IMAGE_TAG=edge bash install.sh
```

### evcc checkconfig errors

Checkconfig cannot reach your inverter during install (it's a LAN device, not a container), so connection errors are expected and the script asks whether to continue. Schema errors (missing or misspelled fields) mean `evcc.yaml` needs editing. Fix it and re-run the installer, or edit the file and run checkconfig manually:

```sh
docker run --rm \
  -v ~/wha/evcc.yaml:/etc/evcc.yaml:ro \
  evcc/evcc evcc -c /etc/evcc.yaml checkconfig
```

Use whichever evcc image tag your `~/wha/docker-compose.yml` pins, so the schema matches what the stack runs.

## Runtime issues

### wha can't open its database (SQLITE_CANTOPEN)

```
open store: ... unable to open database file (14)
```

The container runs as a non-root user; a pre-existing root-owned `wha-data` volume blocks DB creation. Recreate it:

```sh
docker compose -f ~/wha/docker-compose.yml down -v
docker compose -f ~/wha/docker-compose.yml up -d
```

::: warning Data loss
`down -v` removes all volumes including `wha-data` (charge history, settings). If you want to preserve data, identify and remove only the `*_wha-data` volume manually.
:::

### No data on the dashboard / readyz returns 503

`/healthz` confirms the wha process started; `/readyz` confirms it has connected to MQTT and is receiving data from evcc. A 503 from `/readyz` means one of:

- `wha` can't reach Mosquitto.
- evcc isn't publishing to MQTT.
- MQTT credentials don't match between `evcc.yaml` and `config.yaml`.

Check:

```sh
docker compose -f ~/wha/docker-compose.yml ps
docker compose -f ~/wha/docker-compose.yml logs evcc
docker compose -f ~/wha/docker-compose.yml logs wha
```

Inspect live topics with MQTT Explorer. Topic names and casing must match exactly — see [Reference → MQTT](/reference/mqtt) for the full contract.

### Charging won't start on a fresh setup

evcc only polls vehicle SoC while a charge session is active, so the SoC may be unknown at first. Use the dashboard's **Charge now** button once to kick-start a session; auto (surplus) mode then works on subsequent runs.

### Sungrow values missing or zero

The WiNet-S dongle needs:
1. Recent firmware — update via the WiNet-S local web UI (`http://<winet-ip>`).
2. Modbus TCP enabled — in the WiNet-S web UI, go to *Advanced → Communication → Modbus* and enable TCP on port 502.

Older firmware doesn't expose all power/SoC registers over Modbus.

### 80% SoC overshoot

The Renault SoC is coarse (~hourly polling, only while charging). The cap can overshoot by up to one poll interval. The evcc `limitSoc` backstop bounds this; `wha` cannot fix the polling resolution.

### Brief grid import in pv mode

There's an unavoidable minimum charge power (~1.4 kW single-phase for the Renault Twingo). As surplus dips just below that threshold, evcc may briefly import from the grid before pausing. The `stopThresholdW` and `stopDwell` settings control when `wha` stops the session.

### Warning banner: "Live data is stale"

`wha` hasn't received a fresh MQTT update within `staleTimeout` (default 60 seconds). This forces the loadpoint to off. Check:

- Is evcc running? `docker compose logs evcc`
- Is evcc configured with an `mqtt:` block?
- Are the MQTT credentials correct?
- Is the Mosquitto container running?

### Warning banner: "MQTT broker disconnected" or "evcc is offline"

- **MQTT broker disconnected:** `wha` lost its connection to Mosquitto. Check that the Mosquitto container is running and that credentials match.
- **evcc is offline:** evcc's MQTT Last Will fired (evcc stopped or crashed). Check `docker compose logs evcc`.

Both conditions force the loadpoint to off until the connection recovers.
