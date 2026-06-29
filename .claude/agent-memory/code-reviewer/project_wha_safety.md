---
name: project-wha-safety
description: wha (wallbox-homeautomation-go) safety contract and review focus areas
metadata:
  type: project
---

wha is a greenfield PV-surplus EV charging controller sitting on top of evcc over MQTT (paho). Pure decision engine in internal/controller (policy.go/state.go/inputs.go), evcc store in internal/evcc, persistence in internal/store (modernc sqlite, single conn), Fiber web UI in internal/web.

**Safety contract to verify on every review:**
- Fail-safe to OFF must win over everything when data stale or broker disconnected.
- Hard latched STOP at vehicle SoC >= cap (default 80).
- Never charge with no vehicle connected (under automatic control).
- The evcc `limitSoc` command is the dead-man backstop — it MUST actually reach evcc and be refreshed; commands are non-retained so a single startup publish is not enough.

**Why:** owner treats this as safety-critical home automation controlling real hardware (Easee wallbox).

**How to apply:** prioritize any path that could fail to stop charging (SoC cap, stale, disconnect, lost backstop) as Critical/High. Owner explicitly does not want style nits.
