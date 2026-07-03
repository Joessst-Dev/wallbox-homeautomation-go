# Hardware

`wha` wird gegen einen bestimmten Hardware-Stack entwickelt und getestet. evcc unterstützt deutlich mehr Geräte; die Tabelle unten listet auf, was in diesem Setup verwendet wird.

## Unterstützte Hardware

| Rolle | Gerät | evcc-Template |
|------|--------|---------------|
| Wechselrichter (Netz + PV + Batterie) | Sungrow SH8.0 RT Hybrid | `sungrow-hybrid` |
| Wallbox | Easee Home | `easee` |
| Fahrzeug | Renault Twingo Electric | `renault` |

evccs Charger-/Meter-/Vehicle-Templates übernehmen die eigentliche Gerätekommunikation. `wha` interessiert sich nur dafür, dass evcc die korrekten MQTT-Topics veröffentlicht – welche Hardware hinter evcc steckt, ist ihm egal.

## Voraussetzungen

- Ein Host mit Docker und dem Compose-Plugin (empfohlen: Raspberry Pi 4 oder neuer).
- Netzwerkverbindung zwischen dem Host und deinem Wechselrichter (LAN, Modbus TCP auf Port 502).
- Easee-Kontodaten und die Seriennummer deiner Ladestation.
- Renault-MY-Kontodaten (optional die Fahrgestellnummer/VIN).
- Eine funktionierende Internetverbindung zum Ziehen der Images und zum Abfragen der Renault-Cloud für den SoC.

## Sungrow-Besonderheiten

Der Sungrow SH8.0 RT kommuniziert über **Modbus TCP auf Port 502** im lokalen LAN über den WiNet-S-WLAN-/Ethernet-Dongle. Zwei Anforderungen:

1. **Aktuelle WiNet-S-Firmware.** Ältere Firmware gibt nicht alle Leistungsregister über Modbus frei. Aktualisiere über die lokale WiNet-S-Weboberfläche (`http://<winet-ip>`), falls Werte in evcc als Null oder fehlend angezeigt werden.
2. **Modbus TCP muss aktiviert sein.** Gehe in der WiNet-S-Weboberfläche zu *Erweitert → Kommunikation → Modbus* und stelle sicher, dass TCP auf Port 502 aktiviert ist.

## Easee-Besonderheiten

Die Easee-Home-Integration in evcc nutzt die Easee-Cloud-API. Sie benötigt:
- Deine Easee-Konto-E-Mail und dein Passwort.
- Die **Seriennummer der Ladestation** (auf dem Gerät aufgedruckt und in der Easee-App sichtbar).

Die Easee-Integration ist cloud-abhängig – ein reiner Lokalbetrieb wird vom Easee-Template von evcc derzeit nicht unterstützt.

## Renault-Besonderheiten

evcc fragt die Renault-MY-Cloud für den Fahrzeug-SoC ab. Diese Abfrage passiert nur, **während eine Ladesitzung aktiv ist**, etwa einmal pro Stunde. Konsequenzen:

- Der SoC kann bei einer frischen Installation bis zur ersten Ladung unbekannt sein.
- SoC-Updates sind grob; der Deckel kann um bis zu ein Abfrageintervall überschießen.
- Der `limitSoc`-Backstop in evcc begrenzt das Überschießen, selbst wenn `wha` nicht rechtzeitig reagiert.

Nutze einmalig den **Jetzt laden**-Button im Dashboard, um die erste Sitzung anzustoßen; danach funktioniert der Auto-Modus wie erwartet.

::: tip Andere Hardware
Bei anderer Hardware funktioniert `wha` mit jedem Setup, das evcc unterstützt, solange die MQTT-Topics dem Vertrag in [Referenz → MQTT](/de/reference/mqtt) entsprechen. Das evcc-Template übernimmt das Gerät; `wha` liest nur die Topics.
:::
