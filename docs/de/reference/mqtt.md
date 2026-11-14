# MQTT-Topic-Vertrag

`wha` spricht ausschließlich über MQTT (einen gemeinsamen Mosquitto-Broker) mit einer laufenden evcc-Instanz. evcc muss zum Veröffentlichen konfiguriert sein – füge in `evcc.yaml` einen `mqtt:`-Block hinzu, der auf denselben Broker zeigt (siehe `evcc.example.yaml`). Das Standard-Topic-Präfix ist `evcc`; `<id>` ist die 1-basierte Loadpoint-ID (`evcc.loadpointID`, Standard `1`).

Alle Payloads sind **UTF-8-Strings** (kein JSON-Wrapper auf den Leaf-Topics). Read-Topics werden **retained** veröffentlicht, sodass `wha` seinen Zustand bei jedem (Re-)Connect aus den letzten bekannten Werten neu befüllt.

## Read-Topics (wha abonniert)

| Topic | Typ | Hinweise |
|-------|------|-------|
| `evcc/site/grid/power` | float | **+ = Bezug**, − = Einspeisung |
| `evcc/site/pvPower` | float | + = Erzeugung |
| `evcc/site/homePower` | float | + = Verbrauch |
| `evcc/site/battery/soc` | float `0..100` | Aggregierter Batterie-SoC |
| `evcc/site/battery/power` | float | **+ = Entladung**, − = Ladung |
| `evcc/loadpoints/<id>/chargePower` | float | Aktuelle Ladeleistung (W) |
| `evcc/loadpoints/<id>/charging` | `true`/`false` | Wörtliche bool-Strings, **nicht** `1`/`0` |
| `evcc/loadpoints/<id>/connected` | `true`/`false` | Fahrzeug angesteckt |
| `evcc/loadpoints/<id>/enabled` | `true`/`false` | evcc hat den Loadpoint aktiviert |
| `evcc/loadpoints/<id>/vehicleSoc` | float `0..100` | evccs **Schätzwert** (Energie × 0,85 / Fahrzeug-`capacity`), nur neu abgeglichen, wenn die Renault-API einen geänderten Wert liefert (standardmäßig nur während der Ladung abgefragt) |
| `evcc/loadpoints/<id>/mode` | `off`/`now`/`minpv`/`pv` | Aktueller Modus |
| `evcc/loadpoints/<id>/limitSoc` | int | Aktuelles SoC-Limit |
| `evcc/loadpoints/<id>/effectiveMinCurrent` | float (A) | Minimalstrom, den evcc tatsächlich nutzt (Loadpoint/Fahrzeug/Wallbox kombiniert). Bestimmt die Start-Untergrenze |
| `evcc/loadpoints/<id>/phasesConfigured` | int `0`/`1`/`3` | Konfigurierte Phasen; `0` = evcc schaltet 1p/3p automatisch (die Start-Untergrenze von `wha` zählt dann 1 Phase) |
| `evcc/loadpoints/<id>/phasesActive` | int `1`/`3` | Aktuell genutzte Phasen |
| `evcc/loadpoints/<id>/maxCurrent` | float (A) | Aktueller maximaler Ladestrom (evcc speichert Änderungen über `maxCurrent/set` dauerhaft) |
| `evcc/loadpoints/<id>/chargerStatusReason` | string | Von der Wallbox gemeldeter Grund, wenn nicht geladen wird; wird im Stillstands-Banner angezeigt (Inhalt hängt von der Wallbox ab; kann leer sein) |
| `evcc/status` | `online`/`offline` | evccs MQTT-LWT; steuert Fail-safe |

`effectiveMinCurrent`, `phasesConfigured`, `phasesActive`, `maxCurrent` und `chargerStatusReason` sind optional: Fehlen sie, nutzt `wha` für die Start-Untergrenze `control.minCurrentA` / `control.phases`, bis (oder falls) evcc `effectiveMinCurrent` / `phasesConfigured` veröffentlicht, ein älteres evcc bricht den Controller also nicht. Ob deine evcc-Version jedes davon veröffentlicht, solltest du mit MQTT Explorer prüfen.

Parse alles Numerische als `float64` (evcc kürzt überflüssige Nachkommastellen, z. B. `1234.000` → `1234`). Booleans sind exakt `true`/`false`.

## Kommando-Topics (wha veröffentlicht)

Veröffentlicht **nicht retained**, QoS 1. Groß-/Kleinschreibung ist wichtig – das Set-Segment ist **identisch** zum Read-Schlüssel:

| Topic | Payload | Verwendet für |
|-------|---------|---------|
| `evcc/loadpoints/<id>/mode/set` | `off` \| `now` \| `minpv` \| `pv` | Laden aktivieren/deaktivieren (wha schaltet zwischen `pv` ↔ `off` um) |
| `evcc/loadpoints/<id>/limitSoc/set` | int (Prozent) | Der Totmann-SoC-Backstop (= normalerweise `socCap`, `socMax` beim Laden über den Deckel hinaus) |
| `evcc/loadpoints/<id>/maxCurrent/set` | float (A), Dezimal-Payload | Die Dashboard-Einstellung **Maximaler Ladestrom**. Von `wha` auf `control.maxCurrentCeilingA` begrenzt |

::: warning camelCase ist wichtig
`limitSoc/set` und `maxCurrent/set` sind camelCase. Kleingeschriebene Varianten (`limitsoc/set`, `maxcurrent/set`) werden von evcc stillschweigend ignoriert.
:::

::: warning maxCurrent hat in evcc keine Obergrenze
evcc speichert den Wert und lehnt alles **unterhalb** von `minCurrent` des Loadpoints stillschweigend ab, begrenzt ihn aber nicht auf den Nennstrom deines Stromkreises. `wha` begrenzt deshalb vor dem Veröffentlichen auf `control.maxCurrentCeilingA` und sendet den Wert bei Broker-Reconnect, bei der Online-Flanke von evcc und im `control.republish`-Takt erneut.
:::

## Hinweise zum Verhalten

- **Auth:** evcc authentifiziert Set-Topics *nicht* – Broker-ACLs sind der einzige Schutz. Jeder, der veröffentlichen kann, kann die Ladung steuern.
- **SoC ist ein Schätzwert:** `vehicleSoc` ist evccs Schätzung und wird nur neu abgeglichen, wenn die Renault-API einen geänderten Wert liefert. Zwischen den Abfragen kann er von der Anzeige im Fahrzeug abweichen – siehe [Fehlerbehebung → SoC weicht vom Fahrzeug ab](/de/reference/troubleshooting#soc-weicht-vom-fahrzeug-ab-schatzwert).
- **Veralteter Fahrzeug-SoC:** Da evcc den SoC standardmäßig (`soc.poll.mode: charging`) nur während der Ladung abfragt, kann eine frische Installation bis zur ersten Ladung keinen SoC haben. `wha` prüft `vehicleSoc` nicht auf Veralterung; nutze **Jetzt laden** einmalig zum Anstoßen und verlasse dich auf den `limitSoc`-Backstop, um ein Überschießen zu begrenzen.
- **Republish:** Set-Topics sind nicht retained, daher setzt `wha` `mode` und `limitSoc` erneut durch – bei einem Broker-Reconnect, bei der Online-Flanke von `evcc/status` (evcc-Neustart) und im `control.republish`-Takt.
- **Versionsabweichungen:** Topic-Formen können sich zwischen evcc-Versionen unterscheiden. Prüfe bei der ersten Ausführung mit MQTT Explorer gegen deine Instanz; falsch benannte Topics zeigen sich als dauerhaft veraltete Metrik (Fail-safe) oder als nie bereiter Controller.
