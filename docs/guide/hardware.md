# Hardware

`wha` is developed and tested against a specific hardware stack. evcc supports many more devices; the table below lists what this setup uses.

## Supported hardware

| Role | Device | evcc template |
|------|--------|---------------|
| Inverter (grid + PV + battery) | Sungrow SH8.0 RT hybrid | `sungrow-hybrid` |
| Wallbox | Easee Home | `easee` |
| Vehicle | Renault Twingo Electric | `renault` |

evcc's charger/meter/vehicle templates handle the actual device communication. `wha` only cares that evcc is publishing the correct MQTT topics — it doesn't know or care what hardware is behind evcc.

## Requirements

- A host running Docker with the Compose plugin (Raspberry Pi 4 or later recommended).
- Network connectivity between the host and your inverter (LAN, Modbus TCP on port 502).
- Easee account credentials and your charger's serial number.
- Renault MY account credentials (and optionally the VIN).
- A working internet connection for pulling images and polling the Renault cloud for SoC.

## Sungrow specifics

The Sungrow SH8.0 RT communicates via **Modbus TCP on port 502** over the local LAN through the WiNet-S Wi-Fi/Ethernet dongle. Two requirements:

1. **Recent WiNet-S firmware.** Older firmware doesn't expose all power registers over Modbus. Update via the WiNet-S local web UI (`http://<winet-ip>`) if values show as zero or missing in evcc.
2. **Modbus TCP must be enabled.** In the WiNet-S web UI, go to *Advanced → Communication → Modbus* and ensure TCP is enabled on port 502.

## Easee specifics

The Easee Home integration in evcc uses the Easee cloud API. It requires:
- Your Easee account email and password.
- The **charger serial number** (printed on the unit and visible in the Easee app).

The Easee integration is cloud-dependent — local-only operation is not currently supported by evcc's Easee template.

## Renault specifics

evcc polls the Renault MY cloud for vehicle SoC. This polling only happens **while a charge session is active**, roughly once per hour. Consequences:

- SoC may be unknown on a fresh install until the first charge.
- SoC updates are coarse; the cap can overshoot by up to one poll interval.
- The `limitSoc` backstop in evcc bounds overshoot even if `wha` doesn't react in time.

Use the **Charge now** button on the dashboard once to kick-start the first session; after that, auto mode works as expected.

::: tip Other hardware
If you have different hardware, `wha` will work with any setup that evcc supports, as long as the MQTT topics match the contract in [Reference → MQTT](/reference/mqtt). The evcc template handles the device; `wha` only reads the topics.
:::
