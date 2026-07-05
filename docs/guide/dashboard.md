# Dashboard

The wha dashboard is available at `http://<your-pi>:8080`. It shows live status and lets you control charging without touching the config files.

## Language

The dashboard and the `/setup` page are available in **English** and **German**. Use
the **EN** / **DE** switch in the top-right corner of the header to change language;
your choice is saved in a cookie and remembered on future visits.

## Status overview

The top of the dashboard shows the controller state at a glance:

- **Controller card** — current state (`charging`, `idle`, `force-on`, etc.) with a plain-language reason (e.g. "Surplus detected", "SoC cap reached").
- **Desired mode** — the evcc loadpoint mode that `wha` last set (`pv`, `off`, `now`).
- **Updated** timestamp — how long ago `wha` last received data from evcc. A pulsing green dot means live; amber means stale.

## Power flow

The **Power flow** card shows the current energy picture:

- **Surplus** — the calculated surplus available for charging (W and kW). Green means positive surplus; grey means no surplus.
- **PV** — current solar generation (kW).
- **Home** — current home consumption (kW).
- **Grid** — current grid import/export (W). Positive = import, negative = export.
- **Charge** — current wallbox charge power (W).
- **Home battery** — battery power (W) and state of charge (%), shown as a progress bar.

## Vehicle

The **Vehicle** card shows:

- Vehicle state of charge (%) as a large number and a progress bar. The progress bar has a marker at the 80% cap.
- **Connected** / **Unplugged** status.
- **Charging** / **Idle** status.
- **Ready** badge when the loadpoint is enabled and a vehicle is connected.

## Health indicators

The **Health** card shows the connection status of:

- **MQTT broker** — whether `wha` is connected to Mosquitto.
- **evcc** — whether evcc is reporting as online (via MQTT Last Will and Testament).

Both must be green for normal operation. An alert banner appears at the top of the status section if data is stale or a connection is lost.

## Overrides

The **Override** section lets you set the charging mode manually.

| Button | Effect |
|--------|--------|
| **Auto** | Return to automatic surplus-based control. |
| **Charge now** | Force charging on immediately, regardless of surplus. |
| **Stop** | Force charging off immediately. |

The active override is highlighted. A "Manual" badge shows how long the override will run.

**Auto-revert:** enter a number of hours in the **Auto-revert after** field and the override will automatically expire and return to Auto mode. Leave it empty for a permanent override.

::: tip
If you set **Charge now** with a time limit, wha reverts to Auto when that time elapses — useful for a boost charge before a trip without forgetting to switch back.
:::

## Auto-charge profile

The **Auto-charge profile** toggle (in the Override section) selects the automatic
control strategy used whenever no manual override is active:

| Profile | Behavior |
|---------|----------|
| **Surplus** (`surplus`) | Default hysteresis behavior: starts and stops on sustained PV surplus (see [How It Works](/guide/how-it-works)). |
| **PV power** (`power_latch`) | Once PV production reaches its **Start threshold**, charging starts and continues **to completion** — it does not stop again if production later drops. |
| **Surplus (latch)** (`surplus_latch`) | Same as above, but the trigger is the calculated **surplus** (PV minus home/battery use) instead of raw PV production. |

Selecting **PV power** or **Surplus (latch)** reveals two extra inputs:

- **Start threshold** — the trigger level (W, 100 W steps) that arms charging for that profile.
- **Charge to** — the target vehicle SoC (%, 10% steps, 50–100) at which charging stops.

::: tip Per-profile, persistent
Each latch profile remembers its **own** Start threshold and Charge to values —
switching from **PV power** to **Surplus (latch)** and back does not mix them up.
All three settings (profile, threshold, target) persist across restarts.
:::

These two inputs only appear for a latch profile — the default **Surplus**
profile always uses the static `startThresholdW`/`stopThresholdW` hysteresis
from configuration instead (see [Configuration reference](/reference/configuration#control)).

The SoC cap safety latch (see [How It Works → SoC cap](/guide/how-it-works#soc-cap-latched))
and the evcc `limitSoc` dead-man backstop both track a latch profile's **Charge to**
target while it's active, instead of the static `socCap`.

## Charge power

The **Charge power** toggle (in the Override section) sets whether the wallbox charges at **Surplus** or **Full power**:

| Setting | evcc mode | When to use |
|---------|-----------|-------------|
| **Surplus** (`pv`) | `pv` | Normal operation — charges only when PV surplus is available, avoids grid import. |
| **Full power** (`now`) | `now` | Winter or low-surplus days — charges at maximum current regardless of surplus. |

This setting is **persistent**: it survives restarts and updates. It applies to both automatic surplus-triggered charging and **Charge now** overrides.

::: warning Grid import
**Full power** mode will import from the grid if PV generation is insufficient. Use it deliberately when you need to charge regardless of solar conditions.
:::

## Charge past the cap

When you select **Charge now**, a **Charge now → charge past the SoC cap** checkbox appears. Ticking it lifts the charging stop from the normal `socCap` (default 80%) to `socMax` (default 100%), allowing a full top-up.

Rules for **Charge past the cap**:
- Only active during a **Charge now** override — Auto mode always stops at `socCap`.
- Expires automatically when the override ends (either manually reverted to Auto, or the auto-revert timer fires).
- When the override ends, `wha` immediately re-asserts `limitSoc` back to `socCap`.

::: danger Use deliberately
Charging past 80% regularly accelerates battery degradation on most lithium chemistries. Use this only when you genuinely need a full charge.
:::

## Recent sessions

The **Recent sessions** card lists the latest charge sessions, each showing:

- Start reason (e.g. "Surplus", "Force on").
- Stop reason if the session ended.
- Start time.
- SoC at start → end (if available).
- Average and peak charge power.
- Total energy delivered (kWh).

Click **Refresh** to reload the list.

## Software update

The **Software** card appears at the bottom of the dashboard. When the in-UI updater is enabled (as set up by the one-command installer), it shows:

- The currently running version (`vX.Y.Z`).
- **Check** — queries GHCR for the newest published semver tag.
- **Update to vX.Y.Z** button — when an update is available, initiates the in-place update (see [Updating](/guide/updating)).

If the updater sidecar is not running (e.g. a local source build), the card instead shows the manual update command.

## Install as an app

wha's dashboard is a Progressive Web App: it can be installed to a phone or
desktop home screen, launches full-screen, and keeps a basic offline shell if
the connection briefly drops. This needs a **secure context** — HTTPS or
`localhost` — because browsers refuse to register the offline service worker
otherwise.

From the dashboard, open the **Secure access & install as app** link (or go
directly to `/setup`) for a guided, per-device walkthrough. In short:

1. **Trust the certificate** (skip this if you're on `http://localhost`, which
   is already secure): download the root certificate or, on iOS/macOS, the
   configuration profile, and install it following your device's steps.
2. **Reload the dashboard**, then use your browser's install prompt — or
   Safari's Share sheet → **Add to Home Screen** on iOS — to install wha as an
   app.

See [Secure access](/guide/secure-access) for the full HTTPS setup (Caddy's
internal CA) and why a website can't do the certificate install for you.
