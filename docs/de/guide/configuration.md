# Konfiguration

`wha` ist ein einzelnes Binary **ohne Unterbefehle und ohne Flags**. Es lädt die Konfiguration beim Start aus (höchste Priorität zuerst):

1. `WHA_*`-Umgebungsvariablen
2. Einer YAML-Konfigurationsdatei (Pfad über `WHA_CONFIG`, oder `config.yaml`, gesucht in `/etc/wha` und im Arbeitsverzeichnis)
3. Eingebauten Standardwerten

## Benennung der Umgebungsvariablen

Env-Schlüssel folgen dem Muster `WHA_<SECTION>_<KEY>`, wobei verschachtelte camelCase-Schlüssel vollständig großgeschrieben werden:

| Konfigurationsschlüssel | Umgebungsvariable |
|-----------|---------------------|
| `mqtt.broker` | `WHA_MQTT_BROKER` |
| `control.startThresholdW` | `WHA_CONTROL_STARTTHRESHOLDW` |
| `control.socCap` | `WHA_CONTROL_SOCCAP` |

Zeitdauern akzeptieren die Go-Syntax: `60s`, `2m`, `6h`.

## Die am häufigsten angepassten Stellschrauben

Das sind die Einstellungen, die die meisten Betreiber anpassen. Kopiere `config.yaml` nach `/etc/wha/config.yaml` und bearbeite sie nach Bedarf.

### Überschuss-Schwellenwerte

```yaml
control:
  startThresholdW: 1400   # Überschuss muss dies erreichen, um die Ladung zu starten
  stopThresholdW: 0       # Überschuss muss darunter fallen, um zu stoppen
```

Der Standard-Startschwellenwert (1400 W) entspricht der minimalen Ladeleistung eines einphasigen Renault Twingo. Hat dein Fahrzeug oder deine Wallbox ein anderes Minimum, passe den Wert entsprechend an.

### Verweilzeiten (Anti-Flattern)

```yaml
control:
  startDwell: 2m    # Überschuss muss anhalten, bevor gestartet wird
  stopDwell: 3m     # niedriger Überschuss muss anhalten, bevor gestoppt wird
```

Diese verhindern, dass das Schütz bei schwankendem Überschuss schnell hintereinander schaltet. Erhöhe die Werte, wenn du häufige Start-/Stopp-Zyklen siehst; verringere sie, wenn die Reaktion träge wirkt.

### SoC-Deckel

```yaml
control:
  socCap: 80        # Ladung bei diesem Fahrzeug-SoC (%) stoppen
  socResumeBelow: 78  # nur fortsetzen, wenn der SoC darunter fällt (Verriegelung)
  socMax: 100       # maximaler SoC, wenn das Laden über den Deckel hinaus aktiviert ist
```

`socCap` ist das normale Tageslimit. `socResumeBelow` verhindert Flattern an der Grenze. `socMax` ist nur relevant, wenn du explizit **Über den Deckel hinaus laden** nutzt (siehe [Dashboard → Über den Deckel hinaus laden](/de/guide/dashboard#uber-den-deckel-hinaus-laden)).

### Standard-Lademodus

```yaml
control:
  enableMode: pv    # pv = nur Überschuss; now = volle Leistung
```

Das legt den anfänglichen Standardwert fest. Der Dashboard-Schalter **Ladeleistung** übersteuert ihn zur Laufzeit und speichert die Wahl über Neustarts hinweg – nachdem du ihn im Dashboard umgeschaltet hast, gilt `enableMode` also nur noch bei einer frischen Datenbank (erster Start).

## Vollständige Referenz

Siehe [Referenz → Konfiguration](/de/reference/configuration) für eine vollständige Tabelle jedes Konfigurationsschlüssels, seines Standardwerts und seiner Bedeutung.
