---
layout: home

hero:
  name: wha
  text: PV-Überschuss-Laden für dein E-Auto, aufgesetzt auf evcc
  tagline: Zwei Regeln. Überschuss erkannt → Ladung starten. Fahrzeug-SoC > 80 % → stoppen. Läuft auf einem Raspberry Pi und mischt sich sonst nicht ein.
  actions:
    - theme: brand
      text: Schnellinstallation
      link: /de/guide/quick-install
    - theme: alt
      text: Einführung
      link: /de/guide/introduction

features:
  - icon: ☀️
    title: PV-Überschussladen
    details: Überwacht den PV-Überschuss über MQTT und startet die Ladung automatisch, sobald anhaltend genug Überschuss vorhanden ist. Stoppt sauber, wenn der Überschuss abfällt.
  - icon: 🔋
    title: SoC-Deckel mit Backstop
    details: Stoppt beim 80-%-Deckel und setzt evccs limitSoc bei jedem Reconnect erneut durch – damit die Ladung auch stoppt, wenn wha abstürzt.
  - icon: 🛡️
    title: Fail-safe per Konstruktion
    details: Veraltete Daten, Broker-Verbindungsabbruch, evcc offline oder kein Fahrzeug erzwingen alle den Loadpoint-Zustand „off“. Sicherheit schlägt jede andere Regel.
  - icon: 🖥️
    title: Web-Dashboard
    details: Live-Energiefluss, Fahrzeug-SoC, Verbindungsstatus, manuelle Übersteuerungen und die letzten Ladevorgänge – alles im Browser.
---
