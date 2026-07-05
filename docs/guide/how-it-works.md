# How It Works

This page explains `wha`'s safety model and decision logic in plain language, without Go internals.

## Safety model

Safety is the highest priority. Every rule in this section beats surplus detection.

### Fail-safe to off

Any of the following conditions immediately force the wallbox to stop charging, regardless of available surplus:

| Condition | What it means |
|-----------|--------------|
| **Stale data** | Power metrics (grid, PV, home, battery) haven't updated in 60 seconds (configurable). |
| **Broker disconnect** | `wha` lost its MQTT connection to Mosquitto. |
| **evcc offline** | evcc stopped publishing (its MQTT Last Will fired). |
| **No vehicle connected** | Nothing is plugged into the wallbox. |
| **SoC cap reached** | Vehicle hit the configured cap (see below). |

When any of these clears, `wha` resumes automatic surplus evaluation on the next control tick.

### SoC cap (latched)

Once the vehicle SoC reaches `socCap` (default 80%), charging stops and the state **latches**: it won't resume until SoC drops back below `socResumeBelow` (default 78%). This prevents oscillation at the boundary.

The cap applies to all automatic charging. A **Charge now** override also respects the cap — unless you explicitly tick **Charge past the cap** (see [Dashboard](/guide/dashboard#charge-past-the-cap)), in which case charging continues up to `socMax` (default 100%).

While a **latch** auto-charge profile is active (see below), its own **Charge to**
target replaces `socCap` as the stop point — both the safety latch and the
`limitSoc` backstop track it — but the resume band keeps the same width, i.e.
resuming happens once SoC drops `socCap − socResumeBelow` percentage points
below the latch target. The default **Surplus** profile is unaffected: it
always stops at `socCap` regardless of any leftover latch target from a
previous session.

### Dead-man backstop

`wha` keeps evcc's own `limitSoc` set to `socCap` at all times. This is re-asserted:
- On every MQTT broker reconnect.
- When evcc comes online (via the LWT topic).
- On the `republish` cadence (default every 5 minutes), since set-topics are not retained.

This means even if `wha` crashes or the broker blips, evcc itself enforces the stop at `socCap`. The backstop is the last line of defence.

::: tip Charge past the cap
When you enable **Charge past the cap**, `wha` temporarily raises the `limitSoc` backstop to `socMax`. As soon as the override ends, it immediately drops it back to `socCap`.
:::

### Vehicle SoC is never stale-gated

evcc polls the Renault cloud roughly once per hour and only while a charge session is active. `wha` uses the last known SoC value regardless of age — it is never declared "stale". The `limitSoc` backstop is what bounds any overshoot between polls.

## Surplus calculation

Every 15 seconds (configurable), `wha` calculates:

```
surplus = chargePower − gridPower − max(0, batteryPower)
```

- `chargePower`: current wallbox charge power.
- `gridPower`: positive = import, negative = export.
- `batteryPower`: positive = discharge, negative = charge.

The battery term prevents solar power that's going to charge the home battery from being counted as surplus available for the car.

## Auto-charge profiles

Automatic control (i.e. no manual override active) runs one of three
profiles, selectable from the dashboard (see [Dashboard → Auto-charge profile](/guide/dashboard#auto-charge-profile)):

- **Surplus** (default) — the hysteresis behavior described above: starts and
  stops repeatedly as surplus crosses the start/stop thresholds, each guarded
  by a dwell timer.
- **PV power** (`power_latch`) — a "charge to completion" profile. Once raw
  PV production (not surplus) reaches its **Start threshold** and the start
  dwell elapses, charging begins at full power and keeps going even if PV
  production later drops — it only stops once the vehicle reaches its
  **Charge to** target SoC (or a fail-safe/override intervenes).
- **Surplus (latch)** (`surplus_latch`) — the same "charge to completion"
  behavior, but triggered by the calculated **surplus** instead of raw PV
  production.

Both latch profiles use the same start-dwell anti-flap timer as the default
profile, but never evaluate a stop threshold or stop dwell once charging has
begun — the only way out is the SoC target, or a fail-safe/manual override.
Each latch profile's Start threshold and Charge to target are stored
independently, so switching between them does not mix up their settings.

## Hysteresis and dwell

To avoid starting and stopping on transient fluctuations, two timers govern mode transitions:

- **Start dwell** (default 2 minutes): surplus must stay above `startThresholdW` continuously before charging begins.
- **Stop dwell** (default 3 minutes): surplus must stay below `stopThresholdW` continuously before charging stops.

This protects the Easee contactor from cloud-latency churn and rapid PV variations.

## Priority order

`wha`'s decision follows a strict priority order — the first matching condition wins:

1. **Stale data** → off
2. **Active override** (Force on / Force off)
3. **Not ready** (evcc loadpoint not enabled) → off
4. **Not connected** (no vehicle) → off
5. **SoC cap latched** → off (tracking the active latch profile's Charge to target, if any)
6. **Active auto-charge profile** — Surplus hysteresis/dwell, or a latch profile charging to its target SoC

Override sits above the surplus logic but below the stale/safety check — a stale signal forces off even if you have a manual override.

## Security

evcc does **not** authenticate MQTT set-topics. Anyone who can publish to the broker can change your charging mode. The one-command installer secures Mosquitto automatically:

- Generates a random 48-character credential.
- Hashes it with `mosquitto_passwd`.
- Writes an ACL granting the `wha` user `readwrite evcc/#`.

In a manual setup, configure authentication and ACLs yourself before exposing anything. Keep the broker on your home network — **never expose MQTT to the internet**.
