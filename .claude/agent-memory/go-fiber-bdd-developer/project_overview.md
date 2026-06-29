---
name: project-overview
description: What wallbox-homeautomation-go (wha) is and how its packages are laid out
metadata:
  type: project
---

wha = "wallbox home automation": a PV-surplus EV charging controller. It consumes evcc metrics over MQTT and republishes a desired evcc mode for an Easee Home wallbox.

Module path: `github.com/Joessst-Dev/wallbox-homeautomation-go` (note the capital J and "Joessst" — easy to mistype). Go 1.25.5.

**Why:** The controller decides whether to charge based on solar surplus, with hysteresis + dwell timers and SoC capping. It is safety-critical.

**How to apply:** Internal packages under `internal/`: `app`, `cli`, `config`, `controller`, `evcc`, `store`, `web`. The `controller` package holds the PURE decision engine (no side effects) — see [[controller-decision-engine]]. Config tuning lives in `config.Control` (defaults in `config.Default()`): StartThresholdW 1400, StopThresholdW 0, StartDwell 60s, StopDwell 120s, SoCCap 80, SoCResumeBelow 78, EnableMode "pv".
