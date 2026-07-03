# Konfigurationsreferenz

Vollständige Tabelle jedes Konfigurationsschlüssels, seines Standardwerts und seiner Bedeutung.

## Ladereihenfolge

(Höchste Priorität zuerst)

1. `WHA_*`-Umgebungsvariablen
2. YAML-Konfigurationsdatei (Pfad über `WHA_CONFIG`, oder `config.yaml` in `/etc/wha` oder im Arbeitsverzeichnis)
3. Eingebaute Standardwerte

Env-Schlüsselformat: `WHA_<SECTION>_<KEY>` mit verschachtelten camelCase-Schlüsseln, vollständig großgeschrieben. Zeitdauern akzeptieren die Go-Syntax: `60s`, `2m`, `6h`.

## MQTT

| Schlüssel | Standard | Bedeutung |
|-----|---------|---------|
| `mqtt.broker` | `tcp://localhost:1883` | Mosquitto-Broker-URL |
| `mqtt.clientID` | `wha` | MQTT-Client-ID (muss auf dem Broker eindeutig sein) |
| `mqtt.username` | _(leer)_ | Broker-Benutzername (setze diesen – siehe Sicherheit) |
| `mqtt.password` | _(leer)_ | Broker-Passwort |
| `mqtt.topicPrefix` | `evcc` | MQTT-Topic-Präfix von evcc |

## evcc

| Schlüssel | Standard | Bedeutung |
|-----|---------|---------|
| `evcc.loadpointID` | `1` | Welcher evcc-Loadpoint gesteuert wird (1-basiert) |

## Control

| Schlüssel | Standard | Bedeutung |
|-----|---------|---------|
| `control.enableMode` | `pv` | Anfänglicher Lademodus, wenn die Ladung aktiviert ist (`pv` = nur Überschuss, `now` = volle Leistung). Wird vom Dashboard-Schalter **Ladeleistung** überschrieben, der über Neustarts hinweg gespeichert bleibt. |
| `control.startThresholdW` | `1400` | Zum Starten der Ladung erforderlicher Überschuss (W) |
| `control.stopThresholdW` | `0` | Überschuss (W), unterhalb dessen die Ladung stoppt |
| `control.startDwell` | `2m` | Der Überschuss muss so lange anhalten, bevor gestartet wird (Anti-Flattern) |
| `control.stopDwell` | `3m` | Ein niedriger Überschuss muss so lange anhalten, bevor gestoppt wird |
| `control.socCap` | `80` | Ladung bei diesem Fahrzeug-SoC (%) stoppen |
| `control.socResumeBelow` | `78` | Erst fortsetzen, wenn der SoC darunter fällt (Verriegelung) |
| `control.socMax` | `100` | Maximaler SoC, wenn das Laden über den Deckel hinaus aktiviert ist (muss > `socCap` und ≤ 100 sein) |
| `control.decisionInterval` | `15s` | Wie oft die Steuerungsschleife auswertet |
| `control.staleTimeout` | `60s` | Leistungsmesswerte, die älter sind → Fail-safe (off) |
| `control.republish` | `5m` | Modus + `limitSoc`-Backstop periodisch erneut senden (Set-Topics sind nicht retained) |
| `control.retentionWindow` | `2160h` (90 Tage) | Samples/Ereignisse, die älter sind, entfernen; `0` deaktiviert das Aufräumen |
| `control.retentionInterval` | `6h` | Wie oft der Aufräum-Job läuft |

## Web

| Schlüssel | Standard | Bedeutung |
|-----|---------|---------|
| `web.bindAddr` | `0.0.0.0` | Bind-Adresse für Dashboard + API |
| `web.port` | `8080` | Port für Dashboard + API |

## Datenbank

| Schlüssel | Standard | Bedeutung |
|-----|---------|---------|
| `db.path` | `/data/wha.db` | Pfad zur SQLite-Datenbank |

## Logging

| Schlüssel | Standard | Bedeutung |
|-----|---------|---------|
| `log.level` | `info` | `debug` \| `info` \| `warn` \| `error` |

## Update

| Schlüssel | Standard | Bedeutung |
|-----|---------|---------|
| `update.enabled` | `false` | Aktiviert den In-UI-Software-Update-Mechanismus (erfordert den `wha-updater`-Sidecar) |
| `update.repository` | `joessst-dev/wha` | GHCR-Paketpfad, der auf neuere Releases geprüft wird |
| `update.requestDir` | `/run/update` | Gemeinsames Volume, das der Sidecar auf Update-Anfragen überwacht |
| `update.checkTTL` | `1h` | Wie lange ein GHCR-Prüfergebnis zwischengespeichert wird, bevor erneut abgefragt wird |

## Beispiel config.yaml

```yaml
mqtt:
  broker: tcp://mosquitto:1883
  clientID: wha
  username: wha
  password: <vom-installer-generiert>
  topicPrefix: evcc

evcc:
  loadpointID: "1"

control:
  enableMode: pv
  startThresholdW: 1400
  stopThresholdW: 0
  startDwell: 2m
  stopDwell: 3m
  socCap: 80
  socResumeBelow: 78
  decisionInterval: 15s
  staleTimeout: 60s
  republish: 5m

web:
  bindAddr: 0.0.0.0
  port: 8080

db:
  path: /data/wha.db

log:
  level: info
```
