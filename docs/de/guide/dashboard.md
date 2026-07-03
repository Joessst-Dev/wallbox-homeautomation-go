# Dashboard

Das wha-Dashboard ist unter `http://<dein-pi>:8080` erreichbar. Es zeigt den Live-Status und erlaubt es dir, die Ladung zu steuern, ohne die Konfigurationsdateien anzufassen.

## Statusübersicht

Der obere Bereich des Dashboards zeigt den Controller-Zustand auf einen Blick:

- **Controller-Karte** – aktueller Zustand (`charging`, `idle`, `force-on` usw.) mit einem Grund in Klartext (z. B. „Überschuss erkannt“, „SoC-Deckel erreicht“).
- **Gewünschter Modus** – der evcc-Loadpoint-Modus, den `wha` zuletzt gesetzt hat (`pv`, `off`, `now`).
- **Aktualisiert**-Zeitstempel – wie lange es her ist, dass `wha` zuletzt Daten von evcc erhalten hat. Ein pulsierender grüner Punkt bedeutet live; Bernstein bedeutet veraltet.

## Energiefluss

Die Karte **Energiefluss** zeigt das aktuelle Energiebild:

- **Überschuss** – der berechnete, für die Ladung verfügbare Überschuss (W und kW). Grün bedeutet positiven Überschuss; Grau bedeutet keinen Überschuss.
- **PV** – aktuelle Solarerzeugung (kW).
- **Haus** – aktueller Hausverbrauch (kW).
- **Netz** – aktueller Netzbezug/-einspeisung (W). Positiv = Bezug, negativ = Einspeisung.
- **Ladung** – aktuelle Wallbox-Ladeleistung (W).
- **Hausbatterie** – Batterieleistung (W) und Ladezustand (%), als Fortschrittsbalken dargestellt.

## Fahrzeug

Die Karte **Fahrzeug** zeigt:

- Den Fahrzeug-Ladezustand (%) als große Zahl und als Fortschrittsbalken. Der Fortschrittsbalken hat eine Markierung beim 80-%-Deckel.
- Status **Verbunden** / **Nicht angesteckt**.
- Status **Lädt** / **Inaktiv**.
- Ein **Bereit**-Abzeichen, wenn der Loadpoint aktiviert und ein Fahrzeug angesteckt ist.

## Statusanzeigen

Die Karte **Status** zeigt den Verbindungsstatus von:

- **MQTT-Broker** – ob `wha` mit Mosquitto verbunden ist.
- **evcc** – ob evcc als online meldet (über das MQTT Last Will and Testament).

Beide müssen für den Normalbetrieb grün sein. Am oberen Rand des Statusbereichs erscheint ein Warnbanner, wenn Daten veraltet sind oder eine Verbindung verloren geht.

## Übersteuerungen

Der Bereich **Übersteuerung** erlaubt es dir, den Lademodus manuell zu setzen.

| Button | Wirkung |
|--------|--------|
| **Auto** | Zurück zur automatischen, überschussbasierten Steuerung. |
| **Jetzt laden** | Ladung sofort erzwingen, unabhängig vom Überschuss. |
| **Stopp** | Ladung sofort erzwungen abschalten. |

Die aktive Übersteuerung wird hervorgehoben. Ein „Manuell“-Abzeichen zeigt an, wie lange die Übersteuerung noch läuft.

**Automatisches Zurücksetzen:** Trage im Feld **Automatisch zurücksetzen nach** eine Stundenzahl ein, und die Übersteuerung läuft automatisch ab und kehrt in den Auto-Modus zurück. Lass es leer für eine dauerhafte Übersteuerung.

::: tip
Setzt du **Jetzt laden** mit einem Zeitlimit, kehrt wha nach Ablauf der Zeit automatisch zu Auto zurück – praktisch für ein Boost-Laden vor einer Fahrt, ohne das Zurückschalten vergessen zu müssen.
:::

## Ladeleistung

Der Schalter **Ladeleistung** (im Übersteuerungsbereich) legt fest, ob die Wallbox mit **Überschuss** oder **Volle Leistung** lädt:

| Einstellung | evcc-Modus | Wann verwenden |
|---------|-----------|-------------|
| **Überschuss** (`pv`) | `pv` | Normalbetrieb – lädt nur, wenn PV-Überschuss verfügbar ist, vermeidet Netzbezug. |
| **Volle Leistung** (`now`) | `now` | Wintertage oder Tage mit wenig Überschuss – lädt mit maximalem Strom unabhängig vom Überschuss. |

Diese Einstellung ist **dauerhaft**: Sie übersteht Neustarts und Updates. Sie gilt sowohl für automatisches überschussgetriggertes Laden als auch für **Jetzt laden**-Übersteuerungen.

::: warning Netzbezug
Der Modus **Volle Leistung** bezieht Strom aus dem Netz, wenn die PV-Erzeugung nicht ausreicht. Nutze ihn bewusst, wenn du unabhängig von den Solarbedingungen laden musst.
:::

## Über den Deckel hinaus laden

Wählst du **Jetzt laden**, erscheint eine Checkbox **Jetzt laden → über den SoC-Deckel hinaus laden**. Aktivierst du sie, wird der Ladestopp vom normalen `socCap` (Standard 80 %) auf `socMax` (Standard 100 %) angehoben, was ein vollständiges Auffüllen erlaubt.

Regeln für **Über den Deckel hinaus laden**:
- Nur während einer **Jetzt laden**-Übersteuerung aktiv – der Auto-Modus stoppt immer bei `socCap`.
- Läuft automatisch ab, sobald die Übersteuerung endet (entweder manuell auf Auto zurückgesetzt oder durch den Auto-Reset-Timer).
- Endet die Übersteuerung, setzt `wha` `limitSoc` sofort wieder auf `socCap` zurück.

::: danger Bewusst einsetzen
Regelmäßiges Laden über 80 % hinaus beschleunigt bei den meisten Lithium-Chemien die Batteriealterung. Nutze das nur, wenn du wirklich eine volle Ladung brauchst.
:::

## Letzte Ladevorgänge

Die Karte **Letzte Ladevorgänge** listet die letzten Ladesitzungen auf, jeweils mit:

- Startgrund (z. B. „Überschuss“, „Erzwungen ein“).
- Stoppgrund, falls die Sitzung beendet wurde.
- Startzeit.
- SoC bei Start → Ende (falls verfügbar).
- Durchschnittliche und Spitzen-Ladeleistung.
- Gesamt gelieferte Energie (kWh).

Klicke auf **Aktualisieren**, um die Liste neu zu laden.

## Software-Update

Die Karte **Software** erscheint am unteren Rand des Dashboards. Ist der In-UI-Updater aktiviert (wie vom Ein-Befehl-Installer eingerichtet), zeigt sie:

- Die aktuell laufende Version (`vX.Y.Z`).
- **Prüfen** – fragt GHCR nach dem neuesten veröffentlichten Semver-Tag ab.
- Den Button **Auf vX.Y.Z aktualisieren** – ist ein Update verfügbar, startet er das In-Place-Update (siehe [Aktualisieren](/de/guide/updating)).

Läuft der Updater-Sidecar nicht (z. B. bei einem lokalen Quellcode-Build), zeigt die Karte stattdessen den manuellen Update-Befehl.

## Als App installieren

Das wha-Dashboard ist eine Progressive Web App: Sie lässt sich auf dem
Startbildschirm eines Telefons oder Desktops installieren, startet im
Vollbild und behält eine einfache Offline-Hülle, falls die Verbindung kurz
abreißt. Das setzt einen **sicheren Kontext** voraus — HTTPS oder `localhost` —,
weil Browser den Offline-Service-Worker andernfalls nicht registrieren.

Öffne vom Dashboard aus den Link **Sicherer Zugriff & als App installieren**
(oder gehe direkt zu `/setup`) für eine geführte Anleitung je Gerät. Kurz
gefasst:

1. **Dem Zertifikat vertrauen** (überspringe das, wenn du auf
   `http://localhost` bist, das schon sicher ist): Lade das Stammzertifikat
   oder unter iOS/macOS das Konfigurationsprofil herunter und installiere es
   gemäß den Schritten deines Geräts.
2. **Lade das Dashboard neu** und nutze dann die Installationsaufforderung
   deines Browsers — oder unter iOS das Teilen-Menü von Safari → **Zum
   Home-Bildschirm** —, um wha als App zu installieren.

Siehe [Sicherer Zugriff](/de/guide/secure-access) für das vollständige
HTTPS-Setup (Caddys interne CA) und warum eine Website die Zertifikatsinstallation
nicht für dich übernehmen kann.
