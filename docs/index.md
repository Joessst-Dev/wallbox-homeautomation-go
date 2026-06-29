---
layout: home

hero:
  name: wha
  text: PV-surplus EV charging on top of evcc
  tagline: Two rules. Surplus detected → start charging. Vehicle SoC > 80% → stop. Runs on a Raspberry Pi and stays out of the way.
  actions:
    - theme: brand
      text: Quick Install
      link: /guide/quick-install
    - theme: alt
      text: Introduction
      link: /guide/introduction

features:
  - icon: ☀️
    title: Solar-surplus charging
    details: Monitors PV surplus over MQTT and starts charging automatically when excess power is sustained. Stops cleanly when surplus drops.
  - icon: 🔋
    title: SoC cap with backstop
    details: Stops at the 80% cap and re-asserts evcc's limitSoc on every reconnect — so charging stops even if wha crashes.
  - icon: 🛡️
    title: Fail-safe by design
    details: Stale data, broker disconnect, evcc offline, or no vehicle all force the loadpoint to off. Safety wins over every other rule.
  - icon: 🖥️
    title: Web dashboard
    details: Live power flow, vehicle SoC, connection health, manual overrides, and recent charge history — all in a browser.
---
