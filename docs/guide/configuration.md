# Configuration

`wha` is a single binary with **no subcommands and no flags**. It loads configuration at startup from (highest precedence first):

1. `WHA_*` environment variables
2. A YAML config file (`WHA_CONFIG` path, or `config.yaml` searched in `/etc/wha` and the working directory)
3. Built-in defaults

## Environment variable naming

Env keys follow the pattern `WHA_<SECTION>_<KEY>`, with nested camelCase keys uppercased fully:

| Config key | Environment variable |
|-----------|---------------------|
| `mqtt.broker` | `WHA_MQTT_BROKER` |
| `control.startThresholdW` | `WHA_CONTROL_STARTTHRESHOLDW` |
| `control.socCap` | `WHA_CONTROL_SOCCAP` |

Durations accept Go syntax: `60s`, `2m`, `6h`.

## The most-tuned knobs

These are the settings most operators adjust. Copy `config.yaml` to `/etc/wha/config.yaml` and edit as needed.

### Surplus thresholds

```yaml
control:
  startThresholdW: 1400   # surplus must reach this to start charging
  stopThresholdW: 0       # surplus must drop below this to stop
  minCurrentA: 8          # used until evcc publishes effectiveMinCurrent
  phases: 3               # used only if evcc has not published phasesConfigured
  startMarginW: 200       # headroom on top of the minimum charge power
```

evcc will not charge below its **minimum charge power**: minimum current × 230 V × number of phases. At 3 phases and 8 A that is about 5.5 kW; the 1 400 W default only fits a single-phase charger at 6 A. Set `startThresholdW` to roughly that minimum for **your** setup.

You don't have to get it exactly right: `wha` floors the effective start threshold at `minCurrentA × 230 V × phases + startMarginW`. It prefers the live values evcc publishes (`effectiveMinCurrent`, `phasesConfigured`). The `minCurrentA` key is used only until evcc has published `effectiveMinCurrent`; `phases` is used only when evcc has not published `phasesConfigured`. If evcc publishes `phasesConfigured: 0` (automatic 1p/3p switching), the floor counts **one** phase, since a start is single-phase. The floor applies to the **surplus** and **surplus (latch)** profiles, not to **PV power**. A threshold below that floor can never start a charge that evcc would immediately refuse, which is what caused start/stop flapping.

::: warning Behaviour change for single-phase installs
With the defaults (8 A, 3 phases, 200 W margin) the floor is **5720 W** until evcc publishes its values. If you relied on starting at 1400 W on a single-phase charger and your car allows 6 A, set `phases: 1` and `minCurrentA: 6` (floor 1580 W); otherwise wait until evcc reports its values. A 3-phase Renault Twingo needs 8 A, so its floor stays at 5720 W.
:::

### Current ceiling

```yaml
control:
  maxCurrentCeilingA: 16  # your circuit/charger rating, in A
```

`maxCurrentCeilingA` is the highest current `wha` will ever send to evcc (via the dashboard **Max charge current** setting). Set it to your circuit/charger rating — evcc itself has no upper bound. Clearing that limit sets evcc's `maxCurrent` to the ceiling (which may differ from what evcc had before `wha` touched it), so set the ceiling to your actual rating. If you lower the ceiling later, a saved limit above it is clamped at startup (warning logged). See [Dashboard → Max charge current](/guide/dashboard#max-charge-current).

### Stall detection

```yaml
control:
  stallTimeout: 3m        # enabled but not charging for this long → banner + one recovery
  stallRecoveryOff: 90s   # how long the recovery attempt keeps the loadpoint off
```

See [Troubleshooting → wha says charging but the wallbox is idle](/reference/troubleshooting#wha-says-charging-but-the-wallbox-is-idle-stalled).

### Dwell timers (anti-flap)

```yaml
control:
  startDwell: 2m    # surplus must be sustained before starting
  stopDwell: 3m     # low surplus must be sustained before stopping
```

These prevent the contactor from cycling rapidly when surplus fluctuates. Increase them if you see frequent start/stop cycles; decrease if response feels sluggish.

### SoC cap

```yaml
control:
  socCap: 80        # stop charging at this vehicle SoC (%)
  socResumeBelow: 78  # only resume if SoC drops below this (latch)
  socMax: 100       # maximum SoC when charging past the cap is enabled
```

`socCap` is the normal daily limit. `socResumeBelow` prevents flapping at the boundary. `socMax` is only relevant when you explicitly use **Charge past the cap** (see [Dashboard → Charge past the cap](/guide/dashboard#charge-past-the-cap)).

### Charge mode default

```yaml
control:
  enableMode: pv    # pv = surplus only; now = full power
```

This sets the initial default. The dashboard's **Charge power** toggle overrides it at runtime and persists the choice across restarts — so after you toggle it on the dashboard, `enableMode` only applies on a fresh database (first run).

## Full reference

See [Reference → Configuration](/reference/configuration) for a complete table of every configuration key, its default, and its meaning.
