# Einführung

`wha` (Wallbox Home Automation) ist ein kleiner Dienst, der **PV-Überschuss-Laden für dein E-Auto, aufgesetzt auf [evcc](https://evcc.io)**, realisiert. Er spricht **nicht** direkt mit der Hardware – evcc besitzt die Gerätetreiber (Wechselrichter, Wallbox, Fahrzeug). `wha` liest den Live-Zustand von evcc über MQTT, wendet seine eigene Entscheidungslogik an und schaltet den evcc-Loadpoint-Modus um.

## Die zwei Produktregeln

1. **Überschuss erkannt → Ladung starten.** Wenn die PV-Erzeugung den Hausverbrauch (plus Batterieladung) um mehr als einen Schwellenwert übersteigt und dieser Überschuss lange genug anhält, um Fehlstarts zu vermeiden, setzt `wha` den Loadpoint in den `pv`-Modus. evcc moduliert dann den Ladestrom passend zum tatsächlichen Überschuss.

2. **Fahrzeug-SoC > 80 % → Ladung stoppen.** Erreicht das Fahrzeug den konfigurierten Deckel (Standard 80 %), stoppt die Ladung. Der Deckel ist verriegelt: Er löst sich erst wieder, wenn der SoC unter eine Wiederaufnahme-Schwelle fällt – das verhindert ein Flattern an der Grenze.

Das war's schon. Alles andere – Hardwaretreiber, Lastprognosen, Tarifoptimierung – ist Aufgabe von evcc. `wha` ist die dünne Policy-Schicht obendrauf.

## Was wha nicht ist

- **Kein Hardwaretreiber.** Er spricht nie direkt mit Wechselrichter, Wallbox oder Fahrzeug. Unterstützt evcc deine Hardware nicht, kann `wha` daran nichts ändern.
- **Kein vollständiges HEMS.** Er trifft genau eine Entscheidung: laden oder nicht laden. Es gibt kein Tarif-Switching, keinen Batterie-Dispatch, kein Lastverschieben.
- **Kein Ersatz für evcc.** Die evcc-Weboberfläche (Port 7070) steht weiterhin für Diagnose, Einstellungen und alles zur Verfügung, was über `wha` hinausgeht.

## Architektur in einfachen Worten

```
Sungrow-Wechselrichter ──┐
Easee-Wallbox        ──┤── evcc ──(MQTT)──► wha ──► evcc-Loadpoint-Modus
Renault Twingo        ──┘
```

evcc fragt die Hardware ab und veröffentlicht den Live-Zustand an Mosquitto (einen MQTT-Broker). `wha` abonniert diese Topics, führt seine Überschuss-/SoC-Entscheidung alle 15 Sekunden aus und veröffentlicht zurück an evccs Kommando-Topics, um das Laden ein- oder auszuschalten.

Das `wha`-Web-Dashboard (Port 8080) zeigt den Live-Zustand und erlaubt es dir, die automatische Entscheidung manuell zu übersteuern.

## Verhältnis zu evcc

evcc hat einen eigenen, eingebauten PV-Modus (`pv`). `wha` existiert, weil der Repo-Betreiber eine strengere, nachvollziehbare Policy-Schicht mit persistenter Historie und einem zweckgebundenen Dashboard wollte. Wenn du das Ganze von Grund auf einrichtest, lies sowohl die [evcc-Dokumentation](https://docs.evcc.io) als auch dieses Handbuch – evcc muss konfiguriert sein und funktionieren, bevor `wha` einen Mehrwert bringt.
