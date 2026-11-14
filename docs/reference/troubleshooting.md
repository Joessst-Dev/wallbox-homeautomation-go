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

The Renault SoC is coarse (polled roughly hourly, only while charging) and evcc fills the gaps with an estimate. The cap can overshoot by up to one poll interval. The evcc `limitSoc` backstop bounds this; `wha` cannot fix the polling resolution. If the shown SoC is far from the car's own display, see [SoC differs from the car](#soc-differs-from-the-car-estimate).

### Brief grid import in pv mode

There's an unavoidable minimum charge power: minimum current × 230 V × phases (about 1.4 kW at 6 A single-phase, about 5.5 kW at 8 A on three phases). As surplus dips just below that, evcc may briefly import from the grid before pausing. The `stopThresholdW` and `stopDwell` settings control when `wha` stops the session.

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

## Charging behaviour

These come from the first field report (Sungrow SH8.0 RT + Easee Home + Renault Twingo Electric on a 16 A circuit, charger limited to 8 A in the Easee app). The causes below are the likely diagnosis; the evcc behaviours marked *observed/likely* are not verified against every evcc version.

### Charging starts and stops repeatedly / red lamp

Symptom: the loadpoint toggles between charging and paused every few minutes and the car shows a red lamp.

::: warning Single-phase install that no longer starts at 1400 W?
The start threshold is now floored at `(effectiveMinCurrent, else control.minCurrentA) × 230 V × phases + control.startMarginW` for the surplus and `surplus_latch` profiles (not `power_latch`). With the defaults (8 A, 3 phases, 200 W) that is **5720 W** until evcc publishes its values. If your charger is single-phase, either wait until evcc reports `effectiveMinCurrent`/`phasesConfigured`, or set `control.phases: 1` and `control.minCurrentA: 6` (floor then 1580 W). If evcc publishes `phasesConfigured: 0` (automatic 1p/3p), the floor counts 1 phase.
:::

Likely causes, in order of how often they bite:

1. **The start threshold is below evcc's minimum charge power.** evcc only enables once export reaches the loadpoint's minimum charge power (`minCurrent` × 230 V × phases) — *not* "as soon as any surplus exists", even with `enable.threshold: 0`. At 3 phases and 8 A that is about 5.5 kW. A `startThresholdW` of 1400 only fits 6 A on a single phase. `wha` now floors `startThresholdW` at `(effectiveMinCurrent, else minCurrentA) × 230 V × phases + startMarginW` (live values from evcc; `control.minCurrentA`/`control.phases` only until evcc has published them), but set `control.startThresholdW`, `control.minCurrentA` and `control.phases` to match your setup anyway.
2. **Two gates in parallel.** evcc's own `enable`/`disable` delays run alongside `wha`'s start/stop dwells; neither replaces the other. If you see frequent cycling, increase `control.startDwell`/`control.stopDwell` and keep evcc's `disable.delay` at about 3 minutes.
3. **Phase auto-switching.** With `phases: 0` (auto) evcc switches between 1 and 3 phases, and each switch **pauses the Easee**. Some cars react badly (red lamp, aborted session). Use a fixed phase count in `evcc.yaml`.
4. **minCurrent equals the Easee app limit.** If the Easee app caps the charger at 8 A and evcc's `minCurrent` is 8 A, evcc can only offer exactly 8 A and cannot follow the surplus. Use `minCurrent: 6` (evcc then raises the Easee start current to 7 A, *observed/likely*) or raise the app limit.

### wha says charging but the wallbox is idle (stalled)

`wha` reports **charging** from the evcc loadpoint state, but the charger is enabled and not delivering power (`chargePower` ≈ 0).

After the charger has been enabled but not charging for `control.stallTimeout` (default 3 minutes), the dashboard shows a **stalled** banner (with `chargerStatusReason` when evcc provides one). `wha` then tries **one** recovery per plug-in session: it sets the loadpoint `off` for `control.stallRecoveryOff` (default 90 s) and resumes. If that doesn't help it only keeps flagging the stall; it does not retry in a loop.

The stall flag clears only when power flows, the target SoC is reached, `wha`'s decision goes to `off`, or the car is unplugged; a failed recovery stays flagged. The one-recovery budget resets only after the car has been seen unplugged on two consecutive ticks (about 30 s at the default `decisionInterval`). The one-time `off` pulse (`stallRecoveryOff`) also fires during a manual **Charge now**. The audit log (`/api/events`) records `stalled` (detected/cleared) and `stall_recovery` (off/resume); `/api/status` exposes `stalled`, `stalledSince`, `stalledAgo`, `stallRecoveryActive` and `chargerStatusReason`.

If it keeps happening, check the items under [Check these Easee/Renault settings](#check-these-easee-renault-settings), unplug and re-plug the car, and look at the evcc log for the charger status.

### Charge now ignores my current limit

In `now` mode evcc writes the Easee **dynamic** current from its own effective max current (16 A by default). That overrides any *temporary* limit set in the Easee app; only the Easee **static** "max charger current" setting holds (*observed/likely*).

Fix it in one of two ways:

- Use the dashboard **Max charge current** setting ([Dashboard](/guide/dashboard#max-charge-current)); `wha` publishes it to evcc as `maxCurrent/set` and re-asserts it on reconnect. It is clamped to `control.maxCurrentCeilingA`; evcc silently rejects values below `minCurrent`.
- Or set `maxCurrent` on the vehicle or loadpoint in `evcc.yaml`, consistent with your circuit (see `evcc.example.yaml`).

Also set `control.maxCurrentCeilingA` to your circuit/charger rating — evcc has no upper bound of its own.

### Wallbox stays enabled after a session

After `off`, or after the `limitSoc` is reached, the Easee is only **paused**: evcc never *disables* it and no MQTT command can. This is working as designed — the car is simply not receiving power. Disable the charger in the Easee app if you need it fully off.

Related: evcc ignores a mode it is already in, so `wha` re-sending the same mode is harmless and not a sign of a problem.

### SoC differs from the car (estimate)

Example: `wha` shows 82 % while the car's own display says 72 %.

The `vehicleSoc` topic is **evcc's estimate** (charged energy × 0.85 ÷ vehicle `capacity`), only re-synced when the Renault API returns a *changed* value. By default evcc polls Renault only while charging (`soc.poll.mode: charging`), and car data is only reachable while the car is plugged in. Causes of a mismatch:

- **`capacity` too small** in `evcc.yaml` → the estimate overstates SoC and charging stops early. Use the usable kWh of your car (27.5 kWh is the example value for the 2026 Twingo Electric (LFP) — verify yours).
- The Renault cloud returning stale values.

The dashboard labels SoC as *estimated by evcc* while charging and the value has not changed for more than 5 minutes. To get fresher values while plugged in, you can set `soc.poll.mode: connected` with an `interval` such as `30m` on the loadpoint (see `evcc.example.yaml`); don't poll aggressively, the Renault cloud rate-limits.

### Check these Easee/Renault settings

Settings outside `wha` that commonly produce idle or flapping sessions:

- **Easee smart charging / schedules: off.** An app schedule or smart-charging profile can pause the charger while evcc thinks it is charging.
- **Easee access control / RFID: off, or authorize the session.** An unauthorized charger stays idle although evcc has enabled it.
- **Easee phase mode** matches `phases` in `evcc.yaml` (1-phase vs. 3-phase). A mismatch leads to failed starts.
- **Easee max charger current** (static) is at or above evcc's `minCurrent` and at or below your circuit.
- **Renault-side charge schedule / delayed charging: off.** A car-side schedule can refuse to start charging outside its window and produces the red lamp.
- Do **not** set `onIdentify: mode` on the evcc vehicle — it fights `wha`'s mode control.
