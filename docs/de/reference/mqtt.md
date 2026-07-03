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
| `evcc/loadpoints/<id>/vehicleSoc` | float `0..100` | **Grob** – aktualisiert ~stündlich, nur während der Ladung |
| `evcc/loadpoints/<id>/mode` | `off`/`now`/`minpv`/`pv` | Aktueller Modus |
| `evcc/loadpoints/<id>/limitSoc` | int | Aktuelles SoC-Limit |
| `evcc/status` | `online`/`offline` | evccs MQTT-LWT; steuert Fail-safe |

Parse alles Numerische als `float64` (evcc kürzt überflüssige Nachkommastellen, z. B. `1234.000` → `1234`). Booleans sind exakt `true`/`false`.

## Kommando-Topics (wha veröffentlicht)

Veröffentlicht **nicht retained**, QoS 1. Groß-/Kleinschreibung ist wichtig – das Set-Segment ist **identisch** zum Read-Schlüssel:

| Topic | Payload | Verwendet für |
|-------|---------|---------|
| `evcc/loadpoints/<id>/mode/set` | `off` \| `now` \| `minpv` \| `pv` | Laden aktivieren/deaktivieren (wha schaltet zwischen `pv` ↔ `off` um) |
| `evcc/loadpoints/<id>/limitSoc/set` | int (Prozent) | Der Totmann-SoC-Backstop (= normalerweise `socCap`, `socMax` beim Laden über den Deckel hinaus) |

::: warning camelCase ist wichtig
`limitSoc/set` ist camelCase. Ein kleingeschriebenes `limitsoc/set` wird von evcc stillschweigend ignoriert.
:::

## Hinweise zum Verhalten

- **Auth:** evcc authentifiziert Set-Topics *nicht* – Broker-ACLs sind der einzige Schutz. Jeder, der veröffentlichen kann, kann die Ladung steuern.
- **Veralteter Fahrzeug-SoC:** Da evcc den SoC nur während der Ladung abfragt, kann eine frische Installation bis zur ersten Ladung keinen SoC haben. `wha` prüft `vehicleSoc` nicht auf Veralterung; nutze **Jetzt laden** einmalig zum Anstoßen und verlasse dich auf den `limitSoc`-Backstop, um ein Überschießen zu begrenzen.
- **Republish:** Set-Topics sind nicht retained, daher setzt `wha` `mode` und `limitSoc` erneut durch – bei einem Broker-Reconnect, bei der Online-Flanke von `evcc/status` (evcc-Neustart) und im `control.republish`-Takt.
- **Versionsabweichungen:** Topic-Formen können sich zwischen evcc-Versionen unterscheiden. Prüfe bei der ersten Ausführung mit MQTT Explorer gegen deine Instanz; falsch benannte Topics zeigen sich als dauerhaft veraltete Metrik (Fail-safe) oder als nie bereiter Controller.
