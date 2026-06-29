# Introduction

`wha` (Wallbox Home Automation) is a small service that runs **PV-surplus EV charging on top of [evcc](https://evcc.io)**. It does **not** talk to hardware directly — evcc owns the device drivers (inverter, wallbox, vehicle). `wha` reads live state from evcc over MQTT, applies its own decision policy, and toggles the evcc loadpoint mode.

## The two product rules

1. **Surplus detected → start charging.** When PV generation exceeds home consumption (plus battery charging) by more than a threshold, and that surplus is sustained long enough to avoid false starts, `wha` sets the loadpoint to `pv` mode. evcc then modulates the charge current to match the actual surplus.

2. **Vehicle SoC > 80% → stop charging.** When the vehicle reaches the configured cap (default 80%), charging stops. The cap is latched: it won't resume until SoC drops back below a resume threshold, preventing flapping at the boundary.

That's it. Everything else — hardware drivers, load forecasting, tariff optimisation — is evcc's job. `wha` is the thin policy layer on top.

## What wha is not

- **Not a hardware driver.** It never talks to the inverter, wallbox, or vehicle directly. If evcc doesn't support your hardware, `wha` can't help with that.
- **Not a full HEMS.** It makes one decision: charge or don't charge. There's no tariff switching, no battery dispatch, no load shifting.
- **Not a replacement for evcc.** The evcc web UI is still available (port 7070) for diagnostics, settings, and anything beyond what `wha` exposes.

## Architecture in plain language

```
Sungrow inverter ──┐
Easee wallbox    ──┤── evcc ──(MQTT)──► wha ──► evcc loadpoint mode
Renault Twingo   ──┘
```

evcc polls the hardware and publishes live state to Mosquitto (an MQTT broker). `wha` subscribes to those topics, runs its surplus/SoC decision every 15 seconds, and publishes back to evcc's command topics to enable or disable charging.

The `wha` web dashboard (port 8080) shows the live state and lets you override the automatic decision manually.

## Relation to evcc

evcc has its own built-in PV mode (`pv`). `wha` exists because the repo owner wanted a stricter, auditable policy layer with persistent history and a purpose-built dashboard. If you're setting this up from scratch, read both the [evcc docs](https://docs.evcc.io) and this manual — evcc must be configured and working before `wha` adds value.
