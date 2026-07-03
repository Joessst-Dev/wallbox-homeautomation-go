# Manuelle Installation

Diese Seite beschreibt, wie du den Stack manuell mit Docker Compose betreibst, ohne den Ein-Befehl-Installer. Nutze das, wenn du volle Kontrolle über die Konfigurationsdateien willst, lieber aus dem Quellcode baust oder das Setup an andere Hardware anpasst.

## Docker Compose (veröffentlichtes Image ziehen)

Die `docker-compose.yml` im Repo zieht das veröffentlichte Multi-Arch-Image von GHCR – kein lokaler Build nötig:

```sh
# 1. Beispiel-evcc-Konfiguration kopieren und deine Hardware-Zugangsdaten eintragen
cp evcc.example.yaml evcc.yaml

# 2. (Optional) wha-Schwellenwerte in config.yaml anpassen

# 3. Stack starten
docker compose up -d
```

Prüfe, ob alles hochgekommen ist:

```sh
docker compose logs -f wha        # erwartet: "store opened" + "web server listening"
curl -s localhost:8080/healthz    # → ok
```

Die veröffentlichten Image-Tags sind:

| Tag | Verfolgt |
|-----|--------|
| `ghcr.io/joessst-dev/wha:edge` | Neuester Build von `main` |
| `ghcr.io/joessst-dev/wha:latest` | Neuestes stabiles Release |
| `ghcr.io/joessst-dev/wha:vX.Y.Z` | Bestimmtes Release |

Die Standard-`docker-compose.yml` nutzt `:edge`.

## Aus dem Quellcode bauen

Um `wha` aus dem lokalen Quellbaum statt durch Ziehen des Images zu bauen, verwende die lokale Override-Datei:

```sh
docker compose -f docker-compose.yml -f docker-compose.local.yml up -d --build
```

Das Override taggt den Build als `:local`, damit er das gezogene `:edge`-Image nie überschreibt. Ein einfaches `docker compose up` auf dem Pi nutzt weiterhin das veröffentlichte Image.

Du kannst das Binary für die lokale Entwicklung auch direkt ohne Docker ausführen:

```sh
make run     # läuft gegen einen Broker auf localhost:1883 (WHA_*-Umgebungsvariablen setzen)
```

## Erforderliche Konfiguration: evcc.yaml

evcc muss mit deiner Hardware konfiguriert werden. Beginne mit dem Beispiel:

```sh
cp evcc.example.yaml evcc.yaml
```

Bearbeite `evcc.yaml` und trage ein:
- Die LAN-IP des Sungrow-Wechselrichters im Block `meters:`.
- Easee-Konto-E-Mail, Passwort und Ladestation-Seriennummer unter `chargers:`.
- Renault-MY-Konto-E-Mail, Passwort und optional die VIN unter `vehicles:`.
- Den Block `mqtt:`, der auf `mosquitto:1883` mit den Zugangsdaten aus `config.yaml` zeigt.

::: warning MQTT-Zugangsdaten
Die MQTT-Zugangsdaten in `evcc.yaml` müssen mit denen in `config.yaml` übereinstimmen. Der Ein-Befehl-Installer erzeugt und verdrahtet sie automatisch; bei einem manuellen Setup wählst du sie selbst.
:::

## Optionale Konfiguration: config.yaml

`wha` läuft von Haus aus mit sinnvollen Standardwerten. Kopiere und bearbeite `config.yaml`, wenn du Schwellenwerte anpassen willst:

```sh
cp config.yaml /etc/wha/config.yaml   # oder im Arbeitsverzeichnis ablegen
```

Siehe [Anleitung → Konfiguration](/de/guide/configuration) für die am häufigsten angepassten Stellschrauben und [Referenz → Konfiguration](/de/reference/configuration) für die vollständige Tabelle.

## Mosquitto-Setup

Der Stack enthält einen Mosquitto-Container. Bei einem manuellen Setup musst du Auth- und ACL-Konfiguration selbst bereitstellen – siehe den Hinweis ⚠️ [Sicherheit](/de/guide/how-it-works#sicherheit). Der Ein-Befehl-Installer automatisiert dies; erstelle für ein manuelles Setup `mosquitto/config/mosquitto.conf` mit aktivierter Authentifizierung und einer ACL, die den `wha`-Benutzer auf `evcc/#` beschränkt.

## Zugriff auf die Dienste

| Dienst | URL | Hinweise |
|---------|-----|-------|
| wha-Dashboard | `http://<host>:8080` | Live-Status, Übersteuerungen, Historie |
| evcc-Oberfläche | `http://<host>:7070` | Vollständige evcc-Steuerung und Diagnose |
| Health-Check | `http://<host>:8080/healthz` | Liefert `ok`, wenn der wha-Prozess läuft |
| Ready-Check | `http://<host>:8080/readyz` | Liefert 200 nur, wenn mit MQTT verbunden und Daten empfangen werden |
