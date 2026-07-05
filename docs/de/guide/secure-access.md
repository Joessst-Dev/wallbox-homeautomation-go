# Sicherer Zugriff

Das wha-Dashboard lässt sich [als App installieren](/de/guide/dashboard#als-app-installieren)
und funktioniert nach der Installation auch offline. Beides setzt einen
**sicheren Kontext** voraus — HTTPS oder `localhost` —, denn Browser erlauben
den Service Worker einer Progressive Web App (der sowohl das Offline-Caching
*als auch* die Installationsaufforderung ermöglicht) nur über eine sichere
Verbindung. Einfaches `http://<pi-ip>:8080` ist auf keiner Plattform ein
sicherer Kontext, auch nicht unter iOS.

## Das Modell der internen Caddy-CA

Der Stack liefert einen [Caddy](https://caddyserver.com/)-Reverse-Proxy vor
wha aus, konfiguriert mit `tls internal`: Caddy erzeugt und verwaltet seine
eigene selbstsignierte Zertifizierungsstelle (CA), ganz ohne öffentliche
Domain oder DNS. Caddy terminiert HTTPS und leitet die Anfragen über das
interne Docker-Netzwerk an wha weiter.

Der einmalige Aufwand besteht darin, dieser CA auf jedem Gerät zu vertrauen,
das du nutzt — eine Website (oder `wha` selbst) **kann kein Stammzertifikat
unbemerkt in den Vertrauensspeicher eines Geräts installieren**. Jedes moderne
Betriebssystem und jeder Browser verbietet das, weil dich sonst jede Seite per
Man-in-the-Middle abhören könnte. Was wha *bereitstellt*, ist eine geführte
Setup-Seite und, wo sich ein Gerät skripten lässt, ein Einzeiler.

Die lokale Entwicklung (`http://localhost:8080`) ist bereits ein sicherer
Kontext, sodass du das volle PWA-Erlebnis ganz ohne Caddy testen kannst.

## Geführtes Setup

Öffne **`https://<deine-wha-adresse>/setup`** vom Dashboard aus (oder über den
Link **Sicherer Zugriff & als App installieren**). Die Seite:

- Lädt das Stammzertifikat herunter oder ein iOS/macOS-Konfigurationsprofil,
  das es für eine geführte Installation verpackt.
- Listet die genauen Schritte für iOS, Android, macOS, Windows und Linux auf.
- Verlinkt die weiter unten beschriebenen Desktop-Hilfsskripte.

Ist für diese Installation keine CA-Wurzel konfiguriert (zum Beispiel bei
lokaler Entwicklung ohne Caddy), erklärt die Seite das und springt direkt zum
Schritt „Als App installieren“.

## Schritte je Gerät

| Gerät | Vorgehen |
|-------|----------|
| iOS / iPadOS | Installiere das Konfigurationsprofil aus `/setup` und aktiviere anschließend das volle Vertrauen unter **Einstellungen → Allgemein → Info → Vertrauenseinstellungen für Zertifikate**. Manuelle Tipps erforderlich — das lässt sich nicht skripten. |
| Android | Installiere das heruntergeladene Stammzertifikat als **CA-Zertifikat** unter **Einstellungen → Sicherheit**. Manuelle Tipps erforderlich. |
| macOS | Führe `scripts/trust-ca.sh <url-oder-pfad>` aus oder installiere es manuell über die Schlüsselbundverwaltung und setze **Immer vertrauen**. |
| Windows | Führe `scripts/trust-ca.ps1 <url-oder-pfad>` als Administrator aus oder importiere es manuell in **Lokaler Computer → Vertrauenswürdige Stammzertifizierungsstellen**. |
| Linux (Debian/Ubuntu) | Führe `scripts/trust-ca.sh <url-oder-pfad>` aus (kopiert nach `/usr/local/share/ca-certificates` und ruft `update-ca-certificates` auf). |

Desktop-/Linux-Rechner lassen sich skripten, weil sie einen lokalen
Vertrauensspeicher bereitstellen, in den ein Admin-Prozess schreiben kann;
Telefone erlauben das bewusst nicht aus einem unprivilegierten Skript oder von
einer Webseite aus.

## Bereitstellung

`scripts/install.sh` fragt den Hostnamen oder die IP ab, unter der du das
Dashboard aufrufst (`WHA_SITE_ADDRESS`), schreibt sie in `.env` und startet
Caddy zusammen mit dem Rest des Stacks. Damit wha `/setup/root.crt` und
`/setup/wha-ca.mobileconfig` ausliefern kann, kopiert ein kleiner
`caddy-cert-export`-Helfer **nur** Caddys öffentliches Stammzertifikat aus dem
`caddy-data`-Volume in ein eigenes `caddy-cacert`-Volume — der einzige
Zertifikatspfad, der in wha eingebunden wird. wha erhält niemals einen
Dateisystempfad in `caddy-data` selbst, das auch den **privaten Schlüssel** der
CA enthält.

::: warning Das Caddy-Daten-Volume behalten
Das `caddy-data`-Volume enthält den privaten Schlüssel der internen CA. Geht es
verloren, erzeugt Caddy beim nächsten Start eine *neue* CA, und jedes zuvor
vertrauende Gerät zeigt wieder eine Zertifikatswarnung, bis es der neuen Wurzel
erneut vertraut.
:::

Für ein manuelles Setup (ohne `install.sh`) setzt du `WHA_SITE_ADDRESS` selbst
(eine `.env`-Datei neben `docker-compose.yml` genügt), bevor du
`docker compose up -d` ausführst.
