# FAQ

## Stoppt „Jetzt laden“ trotzdem bei 80 %?

**Ja** – standardmäßig stoppt auch **Jetzt laden** beim `socCap` (Standard 80 %), sowohl durch `wha`s eigene Logik als auch durch den evcc-`limitSoc`-Backstop.

Möchtest du für eine bestimmte Sitzung über den Deckel hinaus laden, aktiviere im Dashboard die Checkbox **Jetzt laden → über den SoC-Deckel hinaus laden**. Das hebt den Stopp nur für die Dauer der Übersteuerung auf `socMax` (Standard 100 %) an. Endet die Übersteuerung, wird der Deckel sofort wiederhergestellt.

## Wie lade ich im Winter mit voller Leistung?

Setze im Dashboard **Ladeleistung → Volle Leistung**. Das schaltet `wha` in den `now`-Modus (evcc lädt mit maximalem Strom unabhängig vom Überschuss), wendet den SoC-Deckel aber weiterhin an. Die Einstellung bleibt über Neustarts hinweg erhalten.

Möchtest du im Frühling wieder auf reines Überschussladen zurück, schalte einfach auf **Überschuss** zurück.

## Überstehen meine Einstellungen einen Neustart oder ein Update?

| Einstellung | Bleibt erhalten |
|---------|---------|
| **Ladeleistung** (Überschuss / Volle Leistung) | ✅ Ja – in der Datenbank gespeichert |
| Übersteuerung **Auto** / **Jetzt laden** / **Stopp** | ❌ Nein – wird beim Neustart auf Auto zurückgesetzt |
| Konfiguration in `config.yaml` | ✅ Ja – es ist eine Datei |

Nach einem `docker compose down && up` wird die Ladeleistungs-Einstellung aus der Datenbank wiederhergestellt, und `wha` setzt im selben Ladeleistungsmodus fort wie vor dem Neustart.

## Ist es sicher, den MQTT-Broker zu exponieren?

**Nein – halte den Broker ausschließlich in deinem Heimnetzwerk.**

evcc authentifiziert MQTT-Set-Topics nicht. Jeder, der auf `evcc/loadpoints/<id>/mode/set` veröffentlichen kann, kann deine Ladung steuern. Der Ein-Befehl-Installer sichert Mosquitto mit einem zufällig generierten Zugangsdatum und einer ACL, die `wha` auf `evcc/#` beschränkt. Trotzdem: Exponiere Port 1883 niemals ins Internet oder in nicht vertrauenswürdige Netzwerke.

## Warum braucht wha eine eigene Policy zusätzlich zu evcc?

evcc hat einen eingebauten `pv`-Modus, der ebenfalls auf Überschuss lädt. `wha` ergänzt:
- Eine strengere, nachvollziehbare Policy mit dokumentierter Prioritätsreihenfolge.
- Persistente Ladehistorie in SQLite.
- Ein zweckgebundenes Dashboard mit Fokus auf die Betreibersicht (Überschuss, SoC, Übersteuerung).
- Die Totmann-`limitSoc`-Backstop-Logik zum erneuten Durchsetzen.
- Den Ladeleistungs-Schalter und die Deckel-Umgehungsfunktion.

Reicht dir evccs eingebauter PV-Modus aus, ist `wha` optional.

## Was passiert, wenn wha abstürzt?

Der evcc-`limitSoc`-Backstop ist die letzte Verteidigungslinie. Selbst wenn `wha` abstürzt:
- Bleibt evccs eigene `limitSoc`-Einstellung (zuletzt von `wha` gesetzt) wirksam und deckelt die Ladung bei `socCap`.
- Läuft evccs `pv`-Modus (zuletzt von `wha` gesetzt) auf evccs interner Logik weiter.
- Setzt `wha` den Modus beim Herunterfahren **nicht** auf `off`; es verlässt sich auf den `limitSoc`-Backstop.

Beim Neustart setzt `wha` beim ersten Steuerungstakt Modus und `limitSoc` erneut durch.

## Warum wird der Fahrzeug-SoC nicht in Echtzeit aktualisiert?

Die Renault-MY-API ist ein Cloud-Abfragedienst – evcc kann nur ein Update anfordern, während eine Ladesitzung aktiv ist, und dann etwa einmal pro Stunde. Das ist eine Einschränkung der Renault-Integration, nicht von `wha`.

Auswirkungen:
- Der SoC kann bei einem frischen Setup bis zur ersten Ladung unbekannt sein (zeigt 0 %).
- Der SoC kann der Realität um bis zu eine Stunde hinterherhinken.
- Der `limitSoc`-Backstop gleicht das aus: Selbst wenn `wha` nicht sieht, dass der SoC 80 % erreicht, stoppt evccs eigenes Limit die Ladung.

## Wie prüfe ich, ob alles funktioniert?

1. Öffne das Dashboard unter `http://<dein-pi>:8080`.
2. Die Karte **Status** sollte MQTT-Broker und evcc beide grün zeigen.
3. Die Karte **Energiefluss** sollte Live-Leistungswerte zeigen (aktualisiert alle ~5 Sekunden).
4. Die Karte **Fahrzeug** sollte ein verbundenes Fahrzeug zeigen.

Siehst du veraltete Daten oder getrennte Statusanzeigen, sieh in der [Fehlerbehebung](/de/reference/troubleshooting) nach.

## Kann ich wha ohne den Raspberry-Pi-Installer betreiben?

Ja. Siehe [Anleitung → Manuelle Installation](/de/guide/manual-install) für Docker-Compose- und lokale Quellcode-Build-Optionen. Der Ein-Befehl-Installer ist nur eine Annehmlichkeit; der Stack läuft überall, wo Docker verfügbar ist.
