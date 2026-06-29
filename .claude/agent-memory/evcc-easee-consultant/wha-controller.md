---
name: wha-controller
description: Design of the `wha` Go controller and consultant-validated decisions about its evcc/MQTT control strategy
metadata:
  type: project
---

`wha` = Go controller supervising ONE evcc loadpoint (Easee Home) over Mosquitto MQTT. See [[installation]] and [[evcc-mqtt-interface]].

Strategy (validated as sound): wha decides WHEN charging is allowed (own surplus threshold + hysteresis + dwell + SoC cap) and toggles loadpoint between `mode=pv` (enabled, evcc modulates current to surplus) and `mode=off` (disabled). Hard stop at vehicleSoc ≥ 80 → `mode=off`. Also publishes `limitSoc/set=80` once at startup as a backstop if wha dies.

**Why:** keep grid import ~0 via PV-only charging while retaining an independent SoC ceiling.
**How to apply / caveats surfaced:**
- pv/off toggling is the correct enable/disable mechanism (no separate enable setter). mode=off does NOT disconnect vehicle or clear association.
- mode=pv does NOT require minSoc/limitSoc. Pure pv = no guaranteed minimum (use `minpv` only if a grid-fed floor is wanted). Keep vehicle minSoc=0 to truly avoid grid import.
- DOUBLE control: evcc's native pv mode ALSO has enable/disable thresholds+delays (default enableDelay 1m / disableDelay 3m, minCurrent 6A floor ≈ 1.4 kW on 1p Twingo). wha's dwell stacks on top. Decide whether wha gates (then relax evcc thresholds) or evcc gates (then wha only does SoC override). Avoid them fighting.
- minCurrent floor means PV charging can't go below ~1.4 kW (1p); some grid import during the disableDelay band is unavoidable — "zero grid import" is not strictly guaranteed at the low end.
- vehicleSoc is coarse (~hourly, only while charging) → risk of overshooting 80% by up to a poll interval; evcc's limitSoc backstop shares the same staleness. Twingo BMS won't self-stop at 80 unless set in-car.
- Easee: Smart charging / scheduling MUST be OFF in the Easee app or evcc loses control. Cloud API has latency (seconds+); avoid rapid pv/off cycling (use generous dwell, ≥ a few minutes) to limit session re-negotiation / contactor wear.
- Security: broker ACLs are the only gate on `/set` topics — restrict wha's credentials.
- On startup wha gets retained values immediately but they may be stale; check `evcc/status` LWT and value freshness.
