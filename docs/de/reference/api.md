# API-Referenz

`wha` stellt eine kleine HTTP-API unter `/api/*` bereit, dazu zwei Probe-Endpunkte
auf der Wurzelebene. Die htmx-Bedienelemente des Dashboards sind einfach Clients
dieser selben Endpunkte – alles, was du per Klick tun kannst, kannst du auch mit
`curl` tun.

Alle Endpunkte laufen auf `web.bindAddr:web.port` (Standard `0.0.0.0:8080`),
unauthentifiziert – siehe [Sicherer Zugriff](/de/guide/secure-access), bevor du
das Dashboard über dein Heimnetzwerk hinaus zugänglich machst.

## Konventionen

- **Request-Body.** Die `POST`-Endpunkte akzeptieren sowohl einen formularkodierten
  Body (`Content-Type: application/x-www-form-urlencoded`, wie ihn die
  Dashboard-Formulare senden) als auch JSON (`Content-Type: application/json`).
  Die Feldnamen sind in beiden Fällen identisch.
- **htmx- vs. reine JSON-Antworten.** Jeder `POST`-Endpunkt prüft den
  `HX-Request`-Header (den htmx automatisch setzt). Ist er gesetzt, ist die
  Antwort das frisch gerenderte HTML-Fragment `partials/controls` – dasselbe
  Markup, das das Dashboard einsetzt. Andernfalls ist die Antwort `{"ok": true}`
  als JSON. Validierungsfehler liefern in beiden Fällen `400` mit einer
  Klartext-Fehlermeldung.

## Schutz vor Cross-Site-Schreibzugriffen

Alle schreibenden Anfragen unter `/api/*` (`POST`, `PUT`, `PATCH`, `DELETE`) werden mit `403` und einem JSON-Body abgelehnt – `{"ok":false,"error":"cross-site request rejected"}` bzw. `{"ok":false,"error":"cross-origin request rejected"}` –, wenn `Sec-Fetch-Site` den Wert `cross-site` hat oder der Host des `Origin`-Headers nicht zu `Host` (oder dem ersten `X-Forwarded-Host`) passt. `Origin: null` wird abgelehnt. Clients ohne `Origin`-Header (`curl`, Skripte) sind nicht betroffen, ebenso `GET`-Anfragen. Hinter einem Reverse-Proxy muss `Host` (oder `X-Forwarded-Host`) unverändert bei `wha` ankommen – siehe [Sicherer Zugriff](/de/guide/secure-access#reverse-proxy-und-host-header).

## Probes

### `GET /healthz`

Liveness-Probe. Meldet immer `200 ok`, sobald der Prozess läuft – prüft weder
Broker noch evcc.

### `GET /readyz`

Readiness-Probe. Meldet `200 ok`, sobald `wha` eine bestehende MQTT-Verbindung
zum Broker hat, sonst `503 not ready`. Nutze diesen Endpunkt (nicht `/healthz`)
für Readiness-Checks in Orchestrierung/Reverse-Proxy, da ein laufender Prozess
ohne Broker-Verbindung keine Daten liefern kann.

## Status

### `GET /api/status`

Liefert das vollständige Status-View-Model als JSON – dieselben Daten, die die
Live-Status-Karte, die Energiefluss-Karte, die Fahrzeug-Karte und die
Bedienelemente des Dashboards speisen. Wichtige Felder:

| Feld | Typ | Bedeutung |
|-------|------|---------|
| `state` | string | Controller-Zustand (`idle`, `surplus_pending`, `charging`, `stop_pending`, `soc_reached`, `failsafe`) |
| `reason` / `reasonCode` | string | Lesbarer Grund und dessen stabiler Maschinencode für die letzte Entscheidung |
| `desiredMode` | string | evcc-Loadpoint-Modus, den `wha` zuletzt gesetzt hat (`pv`, `now`, `off`) |
| `override` | string | Manuelle Übersteuerung (`auto`, `on`, `off`) |
| `overrideUntil` | string | RFC3339-Zeitpunkt des automatischen Zurücksetzens, fehlt bei keinem Ablauf |
| `overrideActive` | bool | `true`, wenn die Übersteuerung etwas anderes als `auto` ist |
| `capBypass` / `capBypassSoC` / `capBypassTarget` | bool / int / int | Zustand von „Über den Deckel hinaus laden" – siehe [Dashboard → Über den Deckel hinaus laden](/de/guide/dashboard#uber-den-deckel-hinaus-laden) |
| `chargePower` / `chargePowerNow` | string / bool | Dauerhafter Ladeleistungs-Modus (`pv`\|`now`) |
| `autoProfile` | string | Effektives Automatik-Profil (`surplus`, `power_latch`, `surplus_latch`) – siehe [Dashboard → Automatik-Profil](/de/guide/dashboard#automatik-profil) |
| `autoProfilePowerLatch` / `autoProfileSurplusLatch` / `autoProfileIsLatch` | bool | Das obige Profil, vorreduziert |
| `startThresholdW` | int | Effektive fest-Startschwelle (W) für das aktive Profil |
| `targetSoC` | int | Effektives Stoppziel (%) für das aktive Profil |
| `socCap` / `socMax` / `socCapPlusOne` | int | Konfigurierter SoC-Deckel, Obergrenze und niedrigstes akzeptiertes Deckel-Bypass-Ziel |
| `surplusW` / `surplusKW`, `gridW`/`gridKW`, `pvW`/`pvKW`, `homeW`/`homeKW`, `batteryW`/`batteryKW`, `chargeW`/`chargeKW` | int / string | Aktuelle Energiefluss-Messwerte, in Watt und vorformatiert in kW |
| `batterySoC` / `vehicleSoC` | int | Ladezustand in Prozent |
| `charging` / `connected` / `ready` / `stale` | bool | Flags für Loadpoint/Fahrzeug/Datenaktualität |
| `online` / `brokerConnected` | bool | Health-Flags für evcc-online (LWT) und MQTT-Broker-Verbindung |
| `updatedAt` / `updatedAgo` / `hasUpdated` | string / string / bool | Wann `wha` zuletzt frische Daten von evcc erhalten hat |
| `maxCurrentA` | number | Der maximale Ladestrom (A), den `wha` aktuell durchsetzt; `0`, wenn `wha` nie ein Limit gesetzt hat, nach dem Aufheben gleich der Obergrenze |
| `maxCurrentLimited` | bool | `true` nur, wenn `0 < maxCurrentA < maxCurrentCeilingA`; `false`, wenn kein Limit gesetzt ist oder nach dem Aufheben (das Limit entspricht dann der Obergrenze) |
| `minCurrentA` | number | Minimaler Ladestrom für die Start-Untergrenze (evcc `effectiveMinCurrent`, sonst `control.minCurrentA`); zugleich der niedrigste von `/api/max-current` akzeptierte Wert |
| `maxCurrentCeilingA` | number | `control.maxCurrentCeilingA` – der höchste Strom, den `wha` sendet |
| `evccMaxCurrentA` | number | Das `maxCurrent`, das evcc selbst meldet |
| `stalled` / `stalledSince` / `stalledAgo` | bool / string / string | Wallbox aktiviert, lädt aber seit `control.stallTimeout` nicht; seit wann und wie lange |
| `stallRecoveryActive` | bool | `true`, solange die einmalige Aus/Fortsetzen-Wiederherstellung läuft |
| `chargerStatusReason` | string | evccs `chargerStatusReason`, sofern veröffentlicht |
| `vehicleSoCChangedAt` / `vehicleSoCChangedAgo` | string / string | Wann sich der Fahrzeug-SoC zuletzt geändert hat |
| `vehicleSoCEstimated` | bool | `true`, solange geladen wird und sich der SoC länger als 5 Minuten nicht geändert hat (angezeigt als *von evcc geschätzt*) |
| `configuredStartThresholdW` | int | Der Startschwellenwert wie konfiguriert/eingegeben, vor der Anhebung |
| `minChargePowerW` | int | Die berechnete Start-Untergrenze: Mindeststrom × 230 V × Phasen + `control.startMarginW` |
| `startThresholdFloored` | bool | `true`, wenn der effektive Startschwellenwert auf `minChargePowerW` angehoben wurde (nur `surplus` und `surplus_latch`; `power_latch` wird nie angehoben) |

Vergleiche `configuredStartThresholdW` mit `minChargePowerW` (oder prüfe `startThresholdFloored`), um zu sehen, ob angehoben wurde.

## Übersteuerungen und Lade-Bedienelemente

### `POST /api/override`

Setzt die manuelle Übersteuerung.

| Feld | Typ | Werte | Hinweise |
|-------|------|--------|-------|
| `mode` | string | `auto` \| `on` \| `off` | Erforderlich. `auto` kehrt zur automatischen Überschusssteuerung zurück; `on` erzwingt die Ladung; `off` erzwingt das Abschalten. |
| `hours` | number | `> 0` | Optionale Verzögerung bis zum automatischen Zurücksetzen. `0` oder weggelassen bedeutet keinen Ablauf. |
| `capBypass` | bool | — | Nur relevant mit `mode=on`; hebt den SoC-Stopp von `socCap` auf `socMax` (oder `capBypassSoC`, falls angegeben). Wird bei anderen Modi ignoriert. |
| `capBypassSoC` | int | `socCap+1`–`socMax` | Ziel-SoC für das Laden über den Deckel hinaus. `0`/weggelassen fällt auf `socMax` zurück. |

### `POST /api/charge-power`

Setzt den dauerhaften Ladeleistungs-Modus, der verwendet wird, sobald die Ladung
aktiviert ist (automatisch oder „Jetzt laden").

| Feld | Typ | Werte |
|-------|------|--------|
| `power` | string | `pv` \| `now` |

### `POST /api/max-current`

Setzt den dauerhaften **maximalen Ladestrom** (A). `wha` veröffentlicht ihn als
`loadpoints/<id>/maxCurrent/set` an evcc. Der Wert wird in der Tabelle
`settings` gespeichert, beim ersten Start, bei Änderung, bei Broker-Reconnect
und im `control.republish`-Takt veröffentlicht und immer auf
`control.maxCurrentCeilingA` begrenzt.

| Feld | Typ | Werte |
|-------|------|--------|
| `amps` | number | **Pflicht.** `control.minCurrentA` – `control.maxCurrentCeilingA`; `0` oder ein vorhandenes, aber leeres Feld **hebt** das Limit **auf** |

Formularfeld `amps` oder JSON `{"amps": 8}`. Eine Anfrage ohne den Schlüssel `amps` liefert `400` (`amps is required (0 or empty clears the limit)`).

- **Aufheben** (`amps=0` oder vorhanden, aber leer): Das Limit wird auf `control.maxCurrentCeilingA` gesetzt und gespeichert, und `wha` veröffentlicht diesen Wert ab dann weiter (erster Takt nach einem Neustart, bei Änderung, bei Reconnect, bei der Online-Flanke von evcc und im Republish-Takt). `/api/status` zeigt dann `maxCurrentA` gleich der Obergrenze und `maxCurrentLimited: false`. Hat `wha` nie ein Limit gesetzt, passiert beim Aufheben nichts und `maxCurrentA` bleibt `0`.
- Aufheben setzt evccs `maxCurrent` also auf die Obergrenze, die sich von dem unterscheiden kann, was evcc hatte, bevor `wha` es erstmals berührte – setze `control.maxCurrentCeilingA` auf den Nennstrom deines Stromkreises/deiner Wallbox.
- Senkst du `control.maxCurrentCeilingA` später, wird ein gespeichertes Limit oberhalb der neuen Obergrenze beim Start darauf begrenzt (eine Warnung wird protokolliert) und erneut veröffentlicht; ein gespeichertes Limit unter `control.minCurrentA` wird nach oben begrenzt.
- `400` bei nicht numerischem oder außerhalb des Bereichs liegendem Wert (unter `control.minCurrentA` oder über `control.maxCurrentCeilingA`); `500`, wenn das Speichern der Einstellung fehlschlägt.

```sh
curl -X POST -d amps=8 http://<dein-pi>:8080/api/max-current
curl -X POST -H 'Content-Type: application/json' -d '{"amps":8}' http://<dein-pi>:8080/api/max-current
curl -X POST -d amps=0 http://<dein-pi>:8080/api/max-current   # aufheben
```

### `POST /api/auto-profile`

Setzt das dauerhafte Automatik-Profil.

| Feld | Typ | Werte |
|-------|------|--------|
| `profile` | string | `surplus` \| `power_latch` \| `surplus_latch` |

### `POST /api/start-threshold`

Setzt die dauerhafte Startschwellen-Übersteuerung (Watt, 100-W-Schritte, muss
`control.stopThresholdW` überschreiten) für das **aktuell aktive** fest-Profil.
`400`, wenn das aktive Profil `surplus` ist – die Schwelle gilt nur für ein
fest-Profil.

| Feld | Typ | Werte |
|-------|------|--------|
| `startThresholdW` | number | positives Vielfaches von 100 |

### `POST /api/target-soc`

Setzt die dauerhafte Ziel-SoC-Übersteuerung (10-%-Schritte, 50–100), die als
Stoppziel für das **aktuell aktive** fest-Profil verwendet wird. `400`, wenn
das aktive Profil `surplus` ist.

| Feld | Typ | Werte |
|-------|------|--------|
| `targetSoC` | int | Vielfaches von 10, 50–100 |

::: tip Pro-Profil-Einstellungen
`start-threshold` und `target-soc` werden pro Automatik-Profil gespeichert:
Wechselst du von `power_latch` zu `surplus_latch`, merkt sich jedes Profil
seine eigenen Werte. Siehe [Konfigurationsreferenz → control.autoProfile](/de/reference/configuration#control).
:::

## Historie

### `GET /api/sessions`

Liefert die letzten Ladesitzungen als JSON. Query-Parameter: `limit` (Standard 20).

### `GET /api/events`

Liefert die letzten Controller-/Kommando-Audit-Ereignisse als JSON. Query-Parameter:
`limit` (Standard 50).

Stillstands- und Maximalstrom-Ereignisse: `stalled` (erkannt/aufgehoben),
`stall_recovery` (`off`/`resume`), `max_current:N`, `max_current:clear` und
`set_max_current:N`.

### `GET /api/history`

Liefert die letzten Energiefluss-Messwerte als JSON. Query-Parameter:
`from`/`to` (RFC3339) zur Eingrenzung des Zeitfensters, Standard die letzten
24 Stunden, begrenzt auf 7 Tage. Ein ungültiger Wert oder `to` vor `from`
liefert `400`.

## Software-Update

### `POST /api/update/check`

Erzwingt eine erneute GHCR-Prüfung auf ein neueres Release. Liefert die
Update-Info als JSON (oder das aktualisierte `partials/update`-Fragment bei
`HX-Request`).

### `POST /api/update/apply`

Fordert ein In-Place-Update auf die angegebene Version an (erfordert den
`wha-updater`-Sidecar, siehe [Dashboard → Software-Update](/de/guide/dashboard#software-update)).
`400`, wenn Updates deaktiviert sind oder `version` nicht exakt der Version
entspricht, die die letzte `/api/update/check`-Prüfung gemeldet hat.

| Feld | Typ | Werte |
|-------|------|--------|
| `version` | string | muss dem letzten geprüften Release entsprechen, z. B. `1.4.0` |
