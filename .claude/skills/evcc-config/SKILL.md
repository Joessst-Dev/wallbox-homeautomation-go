---
name: evcc-config
description: "Write, validate, and troubleshoot evcc configuration (evcc.yaml). Use when creating or editing an evcc setup — site, loadpoints, chargers, meters, vehicles, tariffs — or any of the integrations (mqtt, influx, hems, eebus, messaging, modbusproxy). Also triggers for evcc.yaml syntax, checkconfig, template devices, or debugging meters/chargers/vehicles."
user-invocable: true
license: MIT
compatibility: Designed for Claude Code or similar AI coding agents working with evcc (https://evcc.io) installations.
metadata:
  version: "1.0.0"
  source: https://docs.evcc.io/de/installation/configuration/
allowed-tools: Read Edit Write Glob Grep Bash(evcc:*) AskUserQuestion
---

# evcc Configuration

evcc is an extensible EV charge controller and home energy management system. It is
configured either through its **web interface** (recommended; reachable at
`http://<host>:7070`) or through a single **`evcc.yaml`** file for advanced setups.

> Some newer features are only configurable via the web interface. When a user is
> unsure which to use, recommend the web UI for first-time setup and `evcc.yaml`
> for version-controlled, reproducible deployments.

## When to use this skill

- Creating a new `evcc.yaml` from scratch.
- Adding or editing a site, loadpoint, charger, meter, vehicle, or tariff.
- Wiring up an integration: MQTT, InfluxDB, HEMS, EEBus, messaging, Modbus proxy.
- Validating config or debugging a device that isn't reporting correctly.

## Core structure

A valid config is built from a small set of top-level sections. The four that matter
for almost every install are `meters`, `chargers`, `vehicles`, and `loadpoints`,
tied together by `site`. Devices are **defined** in their list sections with a unique
`name`, then **referenced** by that name from `site` and `loadpoints`.

```yaml
site:
  title: Home              # display name in the UI
  meters:
    grid: my_grid          # references a meter by name
    pv:
      - my_pv
    battery:
      - my_battery

loadpoints:
  - title: Garage
    charger: my_charger    # references a charger by name
    vehicle: my_car        # default vehicle by name

meters:
  - name: my_grid
    type: template
    template: demo-meter
    usage: grid
  - name: my_pv
    type: template
    template: demo-meter
    usage: pv
  - name: my_battery
    type: template
    template: demo-battery
    usage: battery
    soc: 50

chargers:
  - name: my_charger
    type: template
    template: demo-charger

vehicles:
  - name: my_car
    type: template
    template: offline
    title: blue e-Golf
    capacity: 50           # kWh

tariffs:
  currency: EUR
  grid:
    type: fixed
    price: 0.29            # EUR/kWh
  feedin:
    type: fixed
    price: 0.10            # EUR/kWh
```

A complete, copy-pasteable minimal example with inline comments is in
[reference/minimal-evcc.yaml](reference/minimal-evcc.yaml). A description of every
top-level section is in [reference/sections.md](reference/sections.md).

## Authoring rules

1. **Names are references.** Every entry in `meters`, `chargers`, `vehicles` needs a
   unique `name`. `site.meters.*` and `loadpoints[].charger/vehicle` must reference
   names that exist. A typo here is the most common config error — cross-check them.
2. **`usage` on meters is mandatory** and must be one of `grid`, `pv`, `battery`, or
   `charge`. The `site.meters` slot (`grid`/`pv`/`battery`) must match the meter's
   `usage`.
3. **Prefer `type: template`.** Most real hardware is supported via a named
   `template:`. Only drop to raw `type: modbus`/`mqtt`/`custom` when no template fits.
   Don't invent template names — confirm against the device docs.
4. **`pv` and `battery` are lists**, `grid` is a single meter.
5. **Keep secrets out of the repo** where possible — `sponsortoken`, API
   credentials, MQTT/InfluxDB passwords. Prefer environment variables or the web UI's
   encrypted store; flag any plaintext secret you're asked to commit.
6. Don't add sections the user didn't ask for. A working minimal config beats a large
   speculative one.

## Validate and debug

Always validate after editing. Run from the directory containing the file (or pass an
explicit path):

```bash
evcc -c evcc.yaml checkconfig          # parse + validate the whole config
evcc -c evcc.yaml -l debug meter       # probe configured meters, verbose
evcc -c evcc.yaml -l debug charger     # probe chargers
evcc -c evcc.yaml -l debug vehicle     # probe vehicles
evcc -c evcc.yaml                      # run normally; UI at http://<host>:7070
```

If `checkconfig` passes but a device reports wrong/zero values, run the matching
`-l debug` probe and inspect the raw readings before changing the config.

## Reference

- [reference/sections.md](reference/sections.md) — every top-level key explained.
- [reference/minimal-evcc.yaml](reference/minimal-evcc.yaml) — annotated starter file.
- [reference/easee-twingo-evcc.yaml](reference/easee-twingo-evcc.yaml) — Easee Home + Renault Twingo Electric + Sungrow SH8.0 RT example.
- Official docs (German): https://docs.evcc.io/de/installation/configuration/
- Full option reference: https://docs.evcc.io/de/reference/configuration
- All options in one file (`evcc.dist.yaml`):
  https://github.com/evcc-io/evcc/blob/master/evcc.dist.yaml
- Device docs: [meters](https://docs.evcc.io/docs/meters) ·
  [chargers](https://docs.evcc.io/docs/chargers) ·
  [vehicles](https://docs.evcc.io/docs/vehicles) ·
  [tariffs](https://docs.evcc.io/docs/tariffs)
