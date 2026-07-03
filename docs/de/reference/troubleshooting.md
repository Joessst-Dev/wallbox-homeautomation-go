# Fehlerbehebung

## Installer-Probleme

### Kein Terminal für Abfragen verfügbar

Unter `curl … | bash` ist die stdin der Shell das Skript selbst, daher öffnet der Installer `/dev/tty` für interaktive Abfragen. Falls du ohne Terminal arbeitest (ein CI-Job, eine SSH-Sitzung ohne PTY), lade das Skript zuerst herunter und führe es direkt aus, oder liefere die Werte vorab über Umgebungsvariablen:

```sh
curl -fsSL https://raw.githubusercontent.com/Joessst-Dev/wallbox-homeautomation-go/main/scripts/install.sh \
  -o install.sh
WHA_DIR=/opt/wha WHA_IMAGE_TAG=edge bash install.sh
```

### evcc-checkconfig-Fehler

Checkconfig kann während der Installation deinen Wechselrichter nicht erreichen (er ist ein LAN-Gerät, kein Container), daher sind Verbindungsfehler erwartbar und das Skript fragt, ob fortgefahren werden soll. Schemafehler (fehlende oder falsch geschriebene Felder) bedeuten, dass `evcc.yaml` bearbeitet werden muss. Behebe das und führe den Installer erneut aus, oder bearbeite die Datei und führe checkconfig manuell aus:

```sh
docker run --rm \
  -v ~/wha/evcc.yaml:/etc/evcc.yaml:ro \
  evcc/evcc evcc -c /etc/evcc.yaml checkconfig
```

Nutze den evcc-Image-Tag, den deine `~/wha/docker-compose.yml` pinnt, damit das Schema zu dem passt, was der Stack tatsächlich ausführt.

## Laufzeitprobleme

### wha kann seine Datenbank nicht öffnen (SQLITE_CANTOPEN)

```
open store: ... unable to open database file (14)
```

Der Container läuft als Non-Root-Benutzer; ein bereits vorhandenes, root-eigenes `wha-data`-Volume blockiert die Erstellung der Datenbank. Erzeuge es neu:

```sh
docker compose -f ~/wha/docker-compose.yml down -v
docker compose -f ~/wha/docker-compose.yml up -d
```

::: warning Datenverlust
`down -v` entfernt alle Volumes einschließlich `wha-data` (Ladehistorie, Einstellungen). Möchtest du Daten erhalten, ermittle und entferne nur das `*_wha-data`-Volume manuell.
:::

### Keine Daten im Dashboard / readyz liefert 503

`/healthz` bestätigt, dass der wha-Prozess gestartet ist; `/readyz` bestätigt, dass er mit MQTT verbunden ist und Daten von evcc empfängt. Ein 503 von `/readyz` bedeutet eines von:

- `wha` kann Mosquitto nicht erreichen.
- evcc veröffentlicht nicht an MQTT.
- Die MQTT-Zugangsdaten stimmen zwischen `evcc.yaml` und `config.yaml` nicht überein.

Prüfe:

```sh
docker compose -f ~/wha/docker-compose.yml ps
docker compose -f ~/wha/docker-compose.yml logs evcc
docker compose -f ~/wha/docker-compose.yml logs wha
```

Untersuche die Live-Topics mit MQTT Explorer. Topic-Namen und Groß-/Kleinschreibung müssen exakt übereinstimmen – siehe [Referenz → MQTT](/de/reference/mqtt) für den vollständigen Vertrag.

### Ladung startet bei frischem Setup nicht

evcc fragt den Fahrzeug-SoC nur ab, während eine Ladesitzung aktiv ist, daher kann der SoC anfangs unbekannt sein. Nutze einmalig den **Jetzt laden**-Button im Dashboard, um eine Sitzung anzustoßen; der Auto-Modus (Überschuss) funktioniert danach bei nachfolgenden Läufen.

### Sungrow-Werte fehlen oder sind Null

Der WiNet-S-Dongle benötigt:
1. Aktuelle Firmware – aktualisiere über die lokale WiNet-S-Weboberfläche (`http://<winet-ip>`).
2. Aktiviertes Modbus TCP – gehe in der WiNet-S-Weboberfläche zu *Erweitert → Kommunikation → Modbus* und aktiviere TCP auf Port 502.

Ältere Firmware gibt nicht alle Leistungs-/SoC-Register über Modbus frei.

### 80-%-SoC-Überschießen

Der Renault-SoC ist grob (~stündliche Abfrage, nur während der Ladung). Der Deckel kann um bis zu ein Abfrageintervall überschießen. Der evcc-`limitSoc`-Backstop begrenzt das; `wha` kann die Abfrageauflösung nicht verbessern.

### Kurzzeitiger Netzbezug im pv-Modus

Es gibt eine unvermeidbare Mindestladeleistung (~1,4 kW einphasig beim Renault Twingo). Fällt der Überschuss knapp unter diesen Schwellenwert, kann evcc kurz aus dem Netz beziehen, bevor pausiert wird. Die Einstellungen `stopThresholdW` und `stopDwell` steuern, wann `wha` die Sitzung stoppt.

### Warnbanner: „Live-Daten sind veraltet“

`wha` hat innerhalb von `staleTimeout` (Standard 60 Sekunden) kein frisches MQTT-Update erhalten. Das erzwingt den Loadpoint-Zustand off. Prüfe:

- Läuft evcc? `docker compose logs evcc`
- Ist evcc mit einem `mqtt:`-Block konfiguriert?
- Sind die MQTT-Zugangsdaten korrekt?
- Läuft der Mosquitto-Container?

### Warnbanner: „MQTT-Broker getrennt“ oder „evcc ist offline“

- **MQTT-Broker getrennt:** `wha` hat die Verbindung zu Mosquitto verloren. Prüfe, ob der Mosquitto-Container läuft und die Zugangsdaten übereinstimmen.
- **evcc ist offline:** evccs MQTT Last Will hat ausgelöst (evcc wurde gestoppt oder ist abgestürzt). Prüfe `docker compose logs evcc`.

Beide Bedingungen erzwingen den Loadpoint-Zustand off, bis die Verbindung sich erholt.
