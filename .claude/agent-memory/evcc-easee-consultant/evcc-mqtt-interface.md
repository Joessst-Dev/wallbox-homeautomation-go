---
name: evcc-mqtt-interface
description: Validated evcc MQTT topic/payload facts (checked against evcc master source, June 2026)
metadata:
  type: reference
---

Validated against evcc `master` source (core/keys/*.go, server/mqtt.go, plugin/mqtt/client.go, core/types/types.go, core/loadpoint.go) on 2026-06-28. Re-verify against the user's installed evcc version before acting — older versions had extra scalar keys (`gridPower`, `batterySoc`, `batteryPower`) that current master does NOT publish.

**Defaults / encoding (server/mqtt.go `encode`):**
- Default root prefix: `evcc`. Loadpoint index is 1-based: `evcc/loadpoints/1/...`.
- All state published with retain=true. On evcc start AND shutdown it Cleanup-wipes retained topics under root, then republishes.
- LWT/liveness topic: `evcc/status` → `online`/`offline`. wha must treat `offline` (or stale) as fail-safe.
- float64 → `%.3f` with trailing zeros+dot trimmed (e.g. `1234`, `1234.5`). int → `%v` (`80`). bool → `%v` = `true`/`false` (NOT 1/0, no quotes). string → as-is. time.Duration → integer seconds. time.Time → unix epoch seconds. nil/empty → empty payload (erases/resets value).
- Publish cadence = each site/loadpoint update cycle (the `interval` setting); event-driven values publish on change. No per-value dedup in mqtt.go.

**Read topics (current master — all CONFIRMED correct):**
- `evcc/site/grid/power` (Measurement struct; +import / -export)
- `evcc/site/pvPower` (+generation)
- `evcc/site/homePower` (+consumption)
- `evcc/site/battery/soc`, `evcc/site/battery/power` (BatteryState struct; power +discharge / -charge)
- `evcc/loadpoints/1/chargePower`, `/charging` (true/false), `/vehicleSoc`, `/mode`, `/limitSoc`
- also useful: `/connected` (car plugged), `/enabled` (control-enabled)
- NOTE: `grid` and `battery` are structs flattened to subtopics; there are no `gridPower`/`batterySoc`/`batteryPower` scalar keys in master.

**Write topics — append `/set`, camelCase preserved:**
- `evcc/loadpoints/1/mode/set` ← `off`|`now`|`minpv`|`pv`
- `evcc/loadpoints/1/limitSoc/set` ← integer percent. CASING IS `limitSoc` (camelCase) for BOTH read and set — NOT lowercase `limitsoc`.
- Other setters: phasesConfigured, priority, minCurrent, maxCurrent, limitEnergy, enableThreshold, disableThreshold, enableDelay, disableDelay, smartCostLimit, planEnergy, vehicle, etc.
- There is NO enable/disable setter — enable/disable is done via `mode` (off = disabled).

**Auth:** MQTT setters require NO evcc auth. evcc's password protects web UI/REST only. Control is gated solely by broker (Mosquitto) ACLs — lock down write access to `/set` topics.

**Vehicle poll defaults (core/loadpoint.go):** `pollInterval = 60m`, default `Poll.Mode = charging` (polls vehicle cloud SoC only while charging; connected/always emit a risk warning). So vehicleSoc refreshes at most ~hourly and NOT at all while paused → a 60s staleness check on vehicleSoc is wrong; tolerate hours. Enable default delay 1m / threshold 0W; Disable default delay 3m / threshold 0W.

**limitSoc persistence:** loadpoint limitSoc persists across restart (bug #11635 reset-to-100 fixed in #11637). It's the correct backstop key (independent of which vehicle); enforcement needs a valid SoC reading.
