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

Der Renault-SoC ist grob (etwa stündliche Abfrage, nur während der Ladung), und evcc füllt die Lücken mit einem Schätzwert. Der Deckel kann um bis zu ein Abfrageintervall überschießen. Der evcc-`limitSoc`-Backstop begrenzt das; `wha` kann die Abfrageauflösung nicht verbessern. Weicht der angezeigte SoC stark von der Fahrzeuganzeige ab, siehe [SoC weicht vom Fahrzeug ab](#soc-weicht-vom-fahrzeug-ab-schatzwert).

### Kurzzeitiger Netzbezug im pv-Modus

Es gibt eine unvermeidbare Mindestladeleistung: minimaler Strom × 230 V × Phasen (etwa 1,4 kW bei 6 A einphasig, etwa 5,5 kW bei 8 A auf drei Phasen). Fällt der Überschuss knapp darunter, kann evcc kurz aus dem Netz beziehen, bevor pausiert wird. Die Einstellungen `stopThresholdW` und `stopDwell` steuern, wann `wha` die Sitzung stoppt.

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

## Ladeverhalten

Diese Punkte stammen aus dem ersten Praxisbericht (Sungrow SH8.0 RT + Easee Home + Renault Twingo Electric an einem 16-A-Stromkreis, Wallbox in der Easee-App auf 8 A begrenzt). Die Ursachen unten sind die wahrscheinliche Diagnose; das beschriebene evcc-Verhalten wurde gegen die evcc-Doku und den Quellcode (evcc 0.319.0) geprüft, kann bei anderen Versionen aber abweichen.

### Laden startet und stoppt ständig / rote Lampe

Symptom: Der Loadpoint wechselt alle paar Minuten zwischen Laden und Pause, und das Fahrzeug zeigt eine rote Lampe.

::: warning Einphasige Installation, die nicht mehr bei 1400 W startet?
Der Startschwellenwert wird für die Profile Überschuss und `surplus_latch` (nicht `power_latch`) jetzt auf mindestens `(effectiveMinCurrent, sonst control.minCurrentA) × 230 V × Phasen + control.startMarginW` angehoben. Mit den Standardwerten (8 A, 3 Phasen, 200 W) sind das **5720 W**, bis evcc seine Werte veröffentlicht. Ist deine Wallbox einphasig, warte entweder, bis evcc `effectiveMinCurrent`/`phasesConfigured` meldet, oder setze, falls deine Wallbox einphasig ist **und dein Fahrzeug 6 A zulässt**, `control.phases: 1` und `control.minCurrentA: 6` (Untergrenze dann 1580 W). Ein dreiphasiger Renault Twingo braucht 8 A, die Untergrenze bleibt mit drei Phasen also bei 5720 W. Veröffentlicht evcc `phasesConfigured: 0` (automatische 1p/3p-Umschaltung), zählt die Untergrenze 1 Phase.
:::

Wahrscheinliche Ursachen, nach Häufigkeit:

1. **Der Startschwellenwert liegt unter evccs Mindestladeleistung.** evcc aktiviert erst, wenn die Einspeisung die Mindestladeleistung des Loadpoints erreicht (`minCurrent` × 230 V × Phasen) – *nicht* „sobald irgendein Überschuss da ist“. `enable.threshold` ist Netzleistung in W (negativ = Einspeisung): `0` bedeutet, die Einspeisung muss die Mindestladeleistung erreichen, ein negativer Wert verlangt zusätzliche Einspeisung. Bei 3 Phasen und 8 A sind das etwa 5,5 kW. Ein `startThresholdW` von 1400 passt nur zu 6 A auf einer Phase. `wha` hebt `startThresholdW` jetzt auf mindestens `(effectiveMinCurrent, sonst minCurrentA) × 230 V × phases + startMarginW` an (Live-Werte von evcc; `control.minCurrentA`/`control.phases` nur, bis evcc sie veröffentlicht hat); setze `control.startThresholdW`, `control.minCurrentA` und `control.phases` trotzdem passend zu deinem Setup.
2. **Zwei Tore parallel.** evccs eigene `enable`-/`disable`-Verzögerungen (Standard 1 Min / 3 Min) laufen neben den Start-/Stopp-Verweilzeiten von `wha`; keine ersetzt die andere. Bei häufigem Takten erhöhe `control.startDwell`/`control.stopDwell` und halte evccs `disable.delay` bei etwa 3 Minuten.
3. **Phasen-Automatik.** Mit `phases: 0` (Auto) schaltet evcc zwischen 1 und 3 Phasen um (bei der Easee Home unterstützt); jeder Wechsel schreibt den Phasenmodus und **pausiert dann die Easee**, und laut evcc-Doku betrifft er jede Wallbox am selben Easee-Stromkreis. Manche Fahrzeuge reagieren schlecht darauf (rote Lampe, abgebrochene Sitzung). Nutze in `evcc.yaml` eine feste Phasenzahl.
4. **minCurrent entspricht dem Easee-App-Limit.** Lass das `minCurrent` des evcc-Fahrzeugs beim echten Minimum des Fahrzeugs (die evcc-Renault-Vorlage dokumentiert 8 A auf drei Phasen, 10 A bei älteren Modellen). Begrenzt die Easee-App die Wallbox auf 8 A, hat evcc genau einen möglichen Strom und kann dem Überschuss nicht folgen. Erhöhe das Easee-App-Limit auf den Nennstrom deines Stromkreises (16 A) und nutze den **Maximalen Ladestrom** von `wha` für vorübergehende Begrenzungen, oder akzeptiere festes Laden mit 8 A. (Der Easee-Treiber hebt Startströme unter 7 A auf 7 A an.)

### wha meldet Laden, aber die Wallbox ist inaktiv (Stillstand)

`wha` meldet **Laden** anhand des evcc-Loadpoint-Zustands, aber die Wallbox ist aktiviert und liefert keine Leistung (`chargePower` ≈ 0).

Ist die Wallbox für `control.stallTimeout` (Standard 3 Minuten) aktiviert, lädt aber nicht, zeigt das Dashboard ein Banner **Stillstand** (mit `chargerStatusReason`, sofern evcc ihn liefert). `wha` versucht dann **eine** Wiederherstellung pro Ansteck-Sitzung: Es setzt den Loadpoint für `control.stallRecoveryOff` (Standard 90 s) auf `off` und setzt dann fort. Hilft das nicht, markiert es den Stillstand nur weiter und versucht es nicht in einer Schleife erneut.

Die Stillstandsmarkierung verschwindet erst, wenn Leistung fließt, der Ziel-SoC erreicht ist, die Entscheidung von `wha` auf `off` geht oder das Fahrzeug abgesteckt wird; eine fehlgeschlagene Wiederherstellung bleibt markiert. Das Budget von einer Wiederherstellung wird erst zurückgesetzt, nachdem das Fahrzeug in zwei aufeinanderfolgenden Takten als abgesteckt gesehen wurde (bei Standard-`decisionInterval` etwa 30 s). Der einmalige `off`-Impuls (`stallRecoveryOff`) feuert auch bei einem manuellen **Jetzt laden**. Das Audit-Log (`/api/events`) protokolliert `stalled` (erkannt/aufgehoben) und `stall_recovery` (off/resume); `/api/status` liefert `stalled`, `stalledSince`, `stalledAgo`, `stallRecoveryActive` und `chargerStatusReason`.

Tritt es weiter auf, prüfe die Punkte unter [Diese Easee-/Renault-Einstellungen prüfen](#diese-easee-renault-einstellungen-prufen), stecke das Fahrzeug ab und wieder an und sieh im evcc-Log den Wallbox-Status an.

### Jetzt laden ignoriert mein Stromlimit

Im `now`-Modus schreibt evcc den **dynamischen** Easee-Strom aus seinem eigenen effektiven Maximalstrom (Standard 16 A). Das überschreibt jedes *vorübergehende* oder dynamische Limit aus der Easee-App; `now` wird nur durch den **statischen** „maximalen Ladestrom“ der Easee-App begrenzt.

Zwei Lösungen:

- Nutze die Dashboard-Einstellung **Maximaler Ladestrom** ([Dashboard](/de/guide/dashboard#maximaler-ladestrom)); `wha` veröffentlicht sie als `maxCurrent/set` an evcc und setzt sie bei Reconnect erneut durch. Sie wird auf `control.maxCurrentCeilingA` begrenzt; evcc lehnt Werte unter `minCurrent` ab (nur sichtbar als `ERROR max current must be greater or equal than min current` im evcc-Log).
- Oder setze `maxCurrent` am Fahrzeug in `evcc.yaml`, passend zu deinem Stromkreis (siehe `evcc.example.yaml`). Ein `maxCurrent`-Schlüssel auf Loadpoint-Ebene ist auf docs.evcc.io nicht dokumentiert; bevorzuge die Einstellung von `wha` oder die evcc-Oberfläche.

Setze außerdem `control.maxCurrentCeilingA` auf den Nennstrom deines Stromkreises/deiner Wallbox – evcc hat keine eigene Obergrenze.

### Wallbox bleibt nach einer Sitzung aktiviert

Nach `off` oder nach Erreichen des `limitSoc` wird die Easee nur **pausiert**: evcc *deaktiviert* sie nie, und kein MQTT-Kommando kann das. Das ist wie vorgesehen – das Fahrzeug erhält schlicht keinen Strom. Deaktiviere die Wallbox in der Easee-App, wenn sie ganz aus sein soll.

Dazu: evcc ignoriert einen Modus, in dem es bereits ist; wenn `wha` denselben Modus erneut sendet, ist das harmlos und kein Problemzeichen.

### SoC weicht vom Fahrzeug ab (Schätzwert)

Beispiel: `wha` zeigt 82 %, die Anzeige im Fahrzeug 72 %.

Das Topic `vehicleSoc` ist **evccs Schätzwert** (geladene Energie × 0,85 ÷ Fahrzeug-`capacity`) und wird nur neu abgeglichen, wenn die Renault-API einen *geänderten* Wert liefert. Standardmäßig fragt evcc Renault nur während der Ladung ab (`soc.poll.mode: charging`), und Fahrzeugdaten sind nur erreichbar, solange das Fahrzeug angesteckt ist. Ursachen einer Abweichung:

- **`capacity` zu klein** in `evcc.yaml` → der Schätzwert überschätzt den SoC und die Ladung stoppt zu früh. Nutze die nutzbaren kWh deines Fahrzeugs (27,5 kWh ist der Beispielwert für den Twingo Electric 2026 (LFP) – prüfe deinen).
- Die Renault-Cloud liefert veraltete Werte.

Das Dashboard kennzeichnet den SoC als *von evcc geschätzt*, solange geladen wird und der Wert sich länger als 5 Minuten nicht geändert hat. Für frischere Werte im angesteckten Zustand kannst du am Loadpoint `soc.poll.mode: connected` mit einem `interval` wie `30m` im `soc:`-Block des **Loadpoints** setzen (der Schlüssel am Fahrzeug heißt `cache`; siehe `evcc.example.yaml`); frage nicht aggressiv ab, die Renault-Cloud begrenzt die Rate.

### Diese Easee-/Renault-Einstellungen prüfen

Einstellungen außerhalb von `wha`, die oft zu inaktiven oder flatternden Sitzungen führen:

- **Easee Smart Charging / Zeitpläne: aus.** Ein App-Zeitplan oder Smart-Charging-Profil kann die Wallbox pausieren, während evcc glaubt, dass geladen wird.
- **Easee-Zugriffskontrolle: aus oder die Sitzung autorisieren.** Eine nicht autorisierte Wallbox bleibt inaktiv, obwohl evcc sie aktiviert hat. Der Parameter der evcc-Easee-Vorlage dafür heißt `authorize`; laut evcc-Doku ist er nicht mit der RFID-Fahrzeugerkennung kompatibel.
- **Easee-Phasenmodus** passt zu `phases` in `evcc.yaml` (1- vs. 3-phasig). Eine Abweichung führt zu fehlgeschlagenen Starts.
- **Easee maximaler Ladestrom** (statisch) liegt auf oder über evccs `minCurrent` und auf oder unter deinem Stromkreis.
- **Renault-seitiger Ladeplan / verzögertes Laden: aus.** Ein fahrzeugseitiger Plan kann das Laden außerhalb seines Zeitfensters verweigern und führt zur roten Lampe.
- Setze am evcc-Fahrzeug **nicht** `onIdentify: mode` – es kämpft gegen die Modussteuerung von `wha`.
