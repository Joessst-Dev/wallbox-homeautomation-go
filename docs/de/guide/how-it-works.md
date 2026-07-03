# Funktionsweise

Diese Seite erklärt das Sicherheitsmodell und die Entscheidungslogik von `wha` in einfachen Worten, ohne Go-Interna.

## Sicherheitsmodell

Sicherheit hat oberste Priorität. Jede Regel in diesem Abschnitt schlägt die Überschusserkennung.

### Fail-safe zu off

Jede der folgenden Bedingungen erzwingt sofort das Stoppen der Wallbox-Ladung, unabhängig vom verfügbaren Überschuss:

| Bedingung | Was sie bedeutet |
|-----------|--------------|
| **Veraltete Daten** | Leistungsmesswerte (Netz, PV, Haus, Batterie) wurden seit 60 Sekunden (konfigurierbar) nicht aktualisiert. |
| **Broker-Verbindungsabbruch** | `wha` hat seine MQTT-Verbindung zu Mosquitto verloren. |
| **evcc offline** | evcc hat aufgehört zu veröffentlichen (sein MQTT Last Will hat ausgelöst). |
| **Kein Fahrzeug verbunden** | Nichts ist an die Wallbox angesteckt. |
| **SoC-Deckel erreicht** | Fahrzeug hat den konfigurierten Deckel erreicht (siehe unten). |

Löst sich eine dieser Bedingungen, nimmt `wha` beim nächsten Steuerungstakt die automatische Überschussauswertung wieder auf.

### SoC-Deckel (verriegelt)

Erreicht der Fahrzeug-SoC `socCap` (Standard 80 %), stoppt die Ladung und der Zustand **verriegelt**: Er löst sich erst wieder, wenn der SoC unter `socResumeBelow` (Standard 78 %) fällt. Das verhindert Oszillation an der Grenze.

Der Deckel gilt für alle automatischen Ladevorgänge. Auch eine **Jetzt laden**-Übersteuerung respektiert den Deckel – es sei denn, du aktivierst explizit **Über den Deckel hinaus laden** (siehe [Dashboard](/de/guide/dashboard#uber-den-deckel-hinaus-laden)); dann läuft die Ladung bis `socMax` (Standard 100 %) weiter.

### Totmann-Backstop

`wha` hält evccs eigenes `limitSoc` jederzeit auf `socCap` gesetzt. Das wird erneut durchgesetzt:
- Bei jedem Reconnect des MQTT-Brokers.
- Wenn evcc online kommt (über das LWT-Topic).
- Im `republish`-Takt (Standard alle 5 Minuten), da Set-Topics nicht retained sind.

Das bedeutet: Selbst wenn `wha` abstürzt oder der Broker kurz aussetzt, erzwingt evcc selbst den Stopp bei `socCap`. Der Backstop ist die letzte Verteidigungslinie.

::: tip Über den Deckel hinaus laden
Aktivierst du **Über den Deckel hinaus laden**, hebt `wha` den `limitSoc`-Backstop vorübergehend auf `socMax` an. Sobald die Übersteuerung endet, setzt er ihn sofort wieder auf `socCap` zurück.
:::

### Fahrzeug-SoC wird nie auf Veralterung geprüft

evcc fragt die Renault-Cloud etwa einmal pro Stunde ab, und nur während eine Ladesitzung aktiv ist. `wha` nutzt den zuletzt bekannten SoC-Wert unabhängig von seinem Alter – er wird nie als „veraltet“ eingestuft. Der `limitSoc`-Backstop begrenzt ein etwaiges Überschießen zwischen den Abfragen.

## Überschussberechnung

Alle 15 Sekunden (konfigurierbar) berechnet `wha`:

```
Überschuss = Ladeleistung − Netzleistung − max(0, Batterieleistung)
```

- `Ladeleistung`: aktuelle Wallbox-Ladeleistung.
- `Netzleistung`: positiv = Bezug, negativ = Einspeisung.
- `Batterieleistung`: positiv = Entladung, negativ = Ladung.

Der Batterieterm verhindert, dass Solarstrom, der die Hausbatterie lädt, als für das Auto verfügbarer Überschuss gezählt wird.

## Hysterese und Verweilzeit

Um Starts und Stopps bei kurzzeitigen Schwankungen zu vermeiden, steuern zwei Timer die Moduswechsel:

- **Start-Verweilzeit** (Standard 2 Minuten): Der Überschuss muss durchgehend über `startThresholdW` bleiben, bevor die Ladung beginnt.
- **Stopp-Verweilzeit** (Standard 3 Minuten): Der Überschuss muss durchgehend unter `stopThresholdW` bleiben, bevor die Ladung stoppt.

Das schützt das Easee-Schütz vor Churn durch Cloud-Latenz und schnelle PV-Schwankungen.

## Prioritätsreihenfolge

Die Entscheidung von `wha` folgt einer strikten Prioritätsreihenfolge – die erste zutreffende Bedingung gewinnt:

1. **Veraltete Daten** → off
2. **Aktive Übersteuerung** (Erzwungen ein / Erzwungen aus)
3. **Nicht bereit** (evcc-Loadpoint nicht aktiviert) → off
4. **Nicht verbunden** (kein Fahrzeug) → off
5. **SoC-Deckel verriegelt** → off
6. **Überschuss-Hysterese / Verweilzeit** (die normale Auto-Logik)

Die Übersteuerung steht über der Überschusslogik, aber unter der Veraltet-/Sicherheitsprüfung – ein veraltetes Signal erzwingt off, selbst wenn du eine manuelle Übersteuerung gesetzt hast.

## Sicherheit

evcc authentifiziert MQTT-Set-Topics **nicht**. Jeder, der auf den Broker veröffentlichen kann, kann deinen Lademodus ändern. Der Ein-Befehl-Installer sichert Mosquitto automatisch:

- Erzeugt ein zufälliges 48-Zeichen-Zugangsdatum.
- Hasht es mit `mosquitto_passwd`.
- Schreibt eine ACL, die dem `wha`-Benutzer `readwrite evcc/#` gewährt.

Konfiguriere bei einem manuellen Setup Authentifizierung und ACLs selbst, bevor du irgendetwas exponierst. Halte den Broker in deinem Heimnetzwerk – **exponiere MQTT niemals ins Internet**.
