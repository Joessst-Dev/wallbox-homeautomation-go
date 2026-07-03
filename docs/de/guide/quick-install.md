# Schnellinstallation

Der Ein-Befehl-Installer richtet den kompletten PV-Überschuss-Lade-Stack (Mosquitto + evcc + wha) auf einem Raspberry Pi oder jedem beliebigen Docker-Host ein.

## Voraussetzungen

- Docker mit installiertem Compose-Plugin und laufendem Daemon.
- `curl` verfügbar (auf Raspberry Pi OS Standard).
- Deine Hardware-Zugangsdaten bereit (Sungrow-IP, Easee-Login + Seriennummer, Renault-Login).

## Installer ausführen

```sh
curl -fsSL https://raw.githubusercontent.com/Joessst-Dev/wallbox-homeautomation-go/main/scripts/install.sh | bash
```

Der Installer ist interaktiv: Er fragt nach Zugangsdaten, wählt eine Image-Version, erzeugt Konfigurationsdateien, validiert das evcc-Setup und startet den Stack.

::: tip Vorschau vor der Ausführung
Füge `--dry-run` hinzu, um Konfigurationsdateien zu erzeugen und zu prüfen, ohne etwas zu starten:
```sh
curl -fsSL https://raw.githubusercontent.com/Joessst-Dev/wallbox-homeautomation-go/main/scripts/install.sh | bash -s -- --dry-run
```
:::

Nach einer erfolgreichen Installation:
- **wha-Dashboard:** `http://<dein-pi>:8080`
- **evcc-Oberfläche:** `http://<dein-pi>:7070`

## Was der Installer tut

1. **Voraussetzungsprüfung** – prüft, ob `curl` und `docker` (mit Compose-Plugin) vorhanden sind und der Docker-Daemon erreichbar ist.
2. **Erkennung vorhandener Konfiguration** – existiert bereits `~/wha/evcc.yaml`, wird gefragt, ob sie beibehalten oder von Grund auf neu konfiguriert werden soll (siehe [Erneutes Ausführen](#erneutes-ausfuhren)).
3. **Versionsauswahl** – fragt GHCR nach veröffentlichten Tags ab und lässt dich zwischen `latest`, `edge` oder einer bestimmten `vX.Y.Z` wählen.
4. **Zugangsdatenabfrage** – erfasst die IP deines Sungrow-Wechselrichters, Easee-Kontodaten und Ladestation-Seriennummer, Renault-MY-Kontodaten und optional die VIN, sowie nutzbare Batteriekapazität, maximalen Ladestrom und Energietarife.
5. **Dateierzeugung** – schreibt `evcc.yaml` mit der gesamten Gerätekonfiguration, trägt in `config.yaml` die generierten MQTT-Zugangsdaten ein und sichert Mosquitto mit Auth + ACL. Dateien mit Geheimnissen erhalten `chmod 600`.
6. **evcc-Konfigurationsvalidierung** – führt `evcc checkconfig` in einem Wegwerf-Container aus, um Schemafehler zu erkennen, bevor der Stack startet. Verbindungsfehler zu deinem Wechselrichter sind an dieser Stelle erwartbar und nicht fatal.
7. **Stack-Start + Health-Poll** – zieht Images, startet den Stack mit `docker compose up -d` und pollt bis zu 60 Sekunden lang `http://localhost:8080/healthz`.

## Optionen

| Variable / Flag | Zweck |
|----------------|---------|
| `--dry-run` oder `WHA_SKIP_START=1` | Erzeugt Konfigurationsdateien, zieht aber keine Images und startet den Stack nicht. |
| `WHA_DIR=<pfad>` | Installationsverzeichnis (Standard: `~/wha`). |
| `WHA_IMAGE_TAG=<tag>` | Pinnt einen bestimmten Image-Tag und überspringt die Versionsabfrage (`latest`, `edge` oder `vX.Y.Z`). |

## Erzeugte Dateien

Nach einem erfolgreichen Lauf enthält das Installationsverzeichnis (standardmäßig `~/wha`):

```
~/wha/
├── docker-compose.yml          # Image-Tag auf deine Wahl gepinnt
├── config.yaml                 # wha-Konfiguration mit MQTT-Zugangsdaten  (chmod 600)
├── evcc.yaml                   # evcc-Konfiguration mit Gerätezugangsdaten (chmod 600)
└── mosquitto/
    └── config/
        ├── mosquitto.conf      # Listener + Persistenz + Auth-Referenzen
        ├── passwd              # bcrypt-gehashte Broker-Zugangsdaten   (chmod 600)
        └── acl                 # Themen-ACL pro Benutzer
```

`config.yaml` und `evcc.yaml` enthalten Klartext-Zugangsdaten – halte sie aus der Versionskontrolle und aus Backups heraus, die du nicht selbst kontrollierst.

## Image-Versionen

| Tag | Was er verfolgt |
|-----|----------------|
| `latest` | Neuestes stabiles Release. Existiert erst, sobald ein `v*`-Tag gepusht wurde. |
| `vX.Y.Z` | Ein bestimmtes Release. Gepinnt – bewegt sich nie. |
| `edge` | Aus jedem Push auf `main` gebaut. Immer verfügbar; kann weniger stabil sein. |

Existiert noch kein `latest`-Tag (vor dem ersten Release), greift der Installer standardmäßig auf `edge` zurück.

## Erneutes Ausführen

Der Installer kann gefahrlos erneut ausgeführt werden. Existiert `evcc.yaml` bereits, fragt er:

- **Beibehalten** (Standard) – überspringt alle Abfragen und geht direkt zum Ziehen und Starten des Stacks. Nutze das, um nach einer manuellen Konfigurationsänderung neu zu starten.
- **Neu konfigurieren** – alle Abfragen laufen erneut und erzeugte Dateien werden überschrieben.

## Kein Terminal verfügbar

Unter `curl … | bash` ist die stdin der Shell das Skript selbst, daher öffnet der Installer `/dev/tty` für interaktive Abfragen. Falls du ohne Terminal arbeitest (SSH ohne PTY, ein CI-Job), lade das Skript zuerst herunter und führe es direkt aus, oder liefere die Werte vorab über Umgebungsvariablen:

```sh
curl -fsSL https://raw.githubusercontent.com/Joessst-Dev/wallbox-homeautomation-go/main/scripts/install.sh \
  -o install.sh
WHA_DIR=/opt/wha WHA_IMAGE_TAG=edge bash install.sh
```
