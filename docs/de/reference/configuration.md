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
| `control.enableMode` | `pv` | Anfänglicher Lademodus, wenn die Ladung aktiviert ist (`pv` = nur Überschuss – evcc ≥ 0.316 nennt ihn `smart`, `wha` sendet weiterhin `pv`, was evcc akzeptiert; `now` = volle Leistung). Wird vom Dashboard-Schalter **Ladeleistung** überschrieben, der über Neustarts hinweg gespeichert bleibt. |
| `control.autoProfile` | `surplus` | Anfängliches Automatik-Profil beim Start (`surplus`, `power_latch` oder `surplus_latch`). Das ist nur der **Startwert** – das Laufzeitprofil sowie Startschwelle/Ziel-SoC jedes fest-Profils sind Bedienereinstellungen, die über den Dashboard-Schalter **Automatik-Profil** gesetzt und in der Datenbank gespeichert werden, nicht nach dem ersten Start aus Env/YAML gelesen. Siehe [Dashboard → Automatik-Profil](/de/guide/dashboard#automatik-profil). |
| `control.startThresholdW` | `1400` | Zum Starten der Ladung erforderlicher Überschuss (W). Setze ihn auf ungefähr die Mindestladeleistung **deiner** Phasen × `minCurrent` (3 Phasen × 8 A × 230 V ≈ 5,5 kW; 1 Phase × 6 A ≈ 1,4 kW). `wha` **hebt ihn automatisch** auf mindestens `(effectiveMinCurrent, sonst minCurrentA)` × 230 V × Phasen + `startMarginW` an (Profile Überschuss und `surplus_latch`; **nicht** `power_latch`), ein zu niedriger Wert kann also kein Start/Stopp-Flattern verursachen. Siehe den Hinweis unten. |
| `control.minCurrentA` | `8` | Minimaler Ladestrom (A) für die Start-Untergrenze. Nur genutzt, solange evcc `effectiveMinCurrent` nicht veröffentlicht hat. Zugleich die untere Grenze, die `POST /api/max-current` akzeptiert. |
| `control.phases` | `3` | Phasenzahl (`1` oder `3`) für die Start-Untergrenze. Wird **nur** genutzt, wenn evcc `phasesConfigured` nicht veröffentlicht hat (oder einen negativen/unsinnigen Wert). Veröffentlicht evcc `phasesConfigured` > 0, gilt dieser Wert; veröffentlicht es `0` (automatische 1p/3p-Umschaltung), zählt die Untergrenze **1 Phase**, weil ein Start einphasig erfolgt – `control.phases` wird dann *nicht* genutzt. |
| `control.startMarginW` | `200` | Zusätzliche Watt auf die berechnete Mindestladeleistung für die Start-Untergrenze. |
| `control.maxCurrentCeilingA` | `16` | Nennstrom von Stromkreis/Wallbox in A. Das Aufheben des Dashboard-Maximalstrom-Limits setzt evccs `maxCurrent` auf diesen Wert. Wird er gesenkt, wird ein gespeichertes Limit darüber beim Start begrenzt (Warnung im Log); ein gespeichertes Limit unter `minCurrentA` wird nach oben begrenzt. Setze ihn auf das, was dein Stromkreis und deine Wallbox tragen – `wha` sendet nie einen höheren Maximalstrom (die Dashboard-Einstellung **Maximaler Ladestrom** wird darauf begrenzt). |
| `control.stallTimeout` | `3m` | Wallbox aktiviert, aber so lange ohne Ladung → Banner „Stillstand“ im Dashboard und ein Wiederherstellungsversuch pro Ansteck-Sitzung. |
| `control.stallRecoveryOff` | `90s` | Wie lange der einzelne Wiederherstellungsversuch den Loadpoint auf `off` hält, bevor er fortsetzt. |
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

::: warning Verhaltensänderung: die Start-Untergrenze (einphasige Installationen)
Der effektive Startschwellenwert der Profile **Überschuss** und **surplus_latch** (nicht `power_latch`) ist jetzt `max(startThresholdW, Untergrenze)` mit `Untergrenze = (effectiveMinCurrent, falls evcc ihn veröffentlicht, sonst control.minCurrentA) × 230 V × Phasen + control.startMarginW`.

Mit den Standardwerten (8 A, 3 Phasen, 200 W Reserve) beträgt die Untergrenze **5720 W**, bis evcc seine eigenen Werte veröffentlicht. Eine einphasige Installation, die bisher bei 1400 W startete, **startet nicht**, bis evcc `effectiveMinCurrent` / `phasesConfigured` meldet oder, falls deine Wallbox einphasig ist **und dein Fahrzeug 6 A zulässt**, `control.phases: 1` und `control.minCurrentA: 6` setzt (Untergrenze dann 1580 W). Ein dreiphasiger Renault Twingo braucht 8 A, seine Untergrenze bleibt also bei 5720 W. Das Dashboard zeigt den konfigurierten und den effektiven (angehobenen) Wert – siehe `configuredStartThresholdW`, `minChargePowerW` und `startThresholdFloored` in der [API](/de/reference/api#get-api-status).
:::

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
  autoProfile: surplus
  startThresholdW: 1400   # wird automatisch auf minCurrentA x 230 V x phases + startMarginW angehoben
  minCurrentA: 8
  phases: 3
  startMarginW: 200
  maxCurrentCeilingA: 16  # Nennstrom deines Stromkreises/deiner Wallbox
  stallTimeout: 3m
  stallRecoveryOff: 90s
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
