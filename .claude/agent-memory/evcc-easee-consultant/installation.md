---
name: installation
description: Hardware/topology of this evcc+Easee installation (inverter, charger, vehicle, broker, controller)
metadata:
  type: project
---

This installation (project dir `wallbox-homeautomation-go`):

- Inverter/battery: Sungrow SH8.0 RT hybrid → provides grid + PV + battery meters to evcc.
- Charger: Easee Home (cloud API integration in evcc; built-in `easee` template).
- Vehicle: Renault Twingo Electric (Renault/Kamereon cloud API for SoC; single-phase onboard charger).
- Broker: Mosquitto MQTT.
- Controller: a separate Go app `wha` sits ON TOP of a running evcc and controls ONE loadpoint over MQTT. evcc owns all hardware.

**Why:** wha is the user's own surplus/SoC supervisory layer; evcc remains the device controller.
**How to apply:** Treat evcc as the source of truth for device control; wha only sends mode/limitSoc commands and reads state over MQTT. Single-phase assumptions apply (Twingo charges 1p → minCurrent 6A ≈ ~1.4 kW PV floor).

Open/unconfirmed: evcc version, deployment method, sponsor token presence, Easee circuit/charger IDs, fuse rating, configured loadpoint phases. Confirm before giving safety-critical current limits.
