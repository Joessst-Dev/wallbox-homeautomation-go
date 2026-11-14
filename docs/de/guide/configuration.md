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
  minCurrentA: 8          # gilt, bis evcc effectiveMinCurrent veröffentlicht
  phases: 3               # gilt nur, wenn evcc phasesConfigured nicht veröffentlicht hat
  startMarginW: 200       # Reserve auf die Mindestladeleistung
```

evcc lädt nicht unterhalb seiner **Mindestladeleistung**: minimaler Strom × 230 V × Phasenzahl. Bei 3 Phasen und 8 A sind das etwa 5,5 kW; der Standardwert von 1400 W passt nur zu einer einphasigen Wallbox mit 6 A. Setze `startThresholdW` auf ungefähr dieses Minimum für **dein** Setup.

Es muss nicht auf den Watt genau stimmen: `wha` hebt den effektiven Startschwellenwert auf mindestens `minCurrentA × 230 V × phases + startMarginW` an. Bevorzugt werden die Live-Werte, die evcc veröffentlicht (`effectiveMinCurrent`, `phasesConfigured`); `minCurrentA` gilt nur, bis evcc `effectiveMinCurrent` veröffentlicht hat; `phases` nur, wenn evcc `phasesConfigured` nicht veröffentlicht hat. Veröffentlicht evcc `phasesConfigured: 0` (automatische 1p/3p-Umschaltung), zählt die Untergrenze **eine** Phase, da ein Start einphasig erfolgt. Die Untergrenze gilt für die Profile **Überschuss** und **Überschuss (fest)**, nicht für **PV-Leistung**. Ein Schwellenwert unterhalb dieser Untergrenze kann so keinen Start anfordern, den evcc sofort verweigern würde – genau das verursachte das Start/Stopp-Flattern.

::: warning Verhaltensänderung für einphasige Installationen
Mit den Standardwerten (8 A, 3 Phasen, 200 W Reserve) beträgt die Untergrenze **5720 W**, bis evcc seine Werte veröffentlicht. Hast du dich bei einer einphasigen Wallbox auf den Start bei 1400 W verlassen, setze `phases: 1` und `minCurrentA: 6` (Untergrenze 1580 W) oder warte, bis evcc seine Werte meldet.
:::

### Stromobergrenze

```yaml
control:
  maxCurrentCeilingA: 16  # Nennstrom deines Stromkreises/deiner Wallbox in A
```

`maxCurrentCeilingA` ist der höchste Strom, den `wha` je an evcc sendet (über die Dashboard-Einstellung **Maximaler Ladestrom**). Setze ihn auf den Nennstrom deines Stromkreises/deiner Wallbox – evcc selbst kennt keine Obergrenze. Das Aufheben dieses Limits setzt evccs `maxCurrent` auf die Obergrenze (die sich von dem unterscheiden kann, was evcc hatte, bevor `wha` es berührte) – setze sie also auf deinen tatsächlichen Nennstrom. Senkst du sie später, wird ein gespeichertes Limit darüber beim Start begrenzt (Warnung im Log). Siehe [Dashboard → Maximaler Ladestrom](/de/guide/dashboard#maximaler-ladestrom).

### Stillstandserkennung

```yaml
control:
  stallTimeout: 3m        # aktiviert, aber nicht ladend → Banner + ein Wiederherstellungsversuch
  stallRecoveryOff: 90s   # wie lange der Versuch den Loadpoint ausgeschaltet lässt
```

Siehe [Fehlerbehebung → wha meldet Laden, aber die Wallbox ist inaktiv](/de/reference/troubleshooting#wha-meldet-laden-aber-die-wallbox-ist-inaktiv-stillstand).

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
