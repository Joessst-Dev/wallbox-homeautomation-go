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
```

The default start threshold (1 400 W) matches the minimum charge power for a single-phase Renault Twingo. If your vehicle or wallbox has a different minimum, adjust accordingly.

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
