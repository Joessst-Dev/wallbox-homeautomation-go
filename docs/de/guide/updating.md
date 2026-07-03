# Aktualisieren

Es gibt zwei Wege, `wha` zu aktualisieren: In-UI (empfohlen) und manuell vom Pi aus.

## In-UI-Update

Die Karte **Software** im Dashboard übernimmt Updates ohne SSH. Das erfordert den `wha-updater`-Sidecar, den der Ein-Befehl-Installer automatisch einrichtet.

1. Öffne das Dashboard unter `http://<dein-pi>:8080`.
2. Klicke in der Karte **Software** auf **Prüfen**, um GHCR nach einem neueren Release abzufragen.
3. Ist ein Update verfügbar, erscheint ein Button **Auf vX.Y.Z aktualisieren**. Klicke ihn an.
4. `wha` schreibt die gewählte Version in das gemeinsame Update-Volume. Der `wha-updater`-Sidecar zieht das neue Image und startet den `wha`-Container neu.
5. Das Dashboard verbindet sich nach ein paar Sekunden automatisch neu.

Deine Daten (Sitzungen, Ereignisse, Einstellungen) liegen im `wha-data`-Volume und überstehen den Container-Austausch.

### Wie der Updater funktioniert

Da `wha` als distroless Non-Root-Container ohne Zugriff auf den Docker-Socket läuft, kann er sein eigenes Image nicht ersetzen. Der `wha-updater`-Sidecar besitzt den Docker-Socket und erledigt das in seinem Auftrag:

1. `wha` validiert den gewählten Versions-Tag und schreibt ihn in das gemeinsame `wha-update`-Volume.
2. `wha-updater` pinnt diesen Tag in `docker-compose.yml` und führt `docker compose up -d wha` aus, wodurch nur der `wha`-Dienst neu erstellt wird.

Der Compose-Stack aktiviert dies mit `WHA_UPDATE_ENABLED=true`; standardmäßig ist es bei anderen Konfigurationen aus, da es den Sidecar voraussetzt.

## Manuelles Update (vom Pi aus)

Um ohne den In-UI-Mechanismus zu aktualisieren – oder um Image-Tags zu wechseln:

```sh
cd ~/wha
docker compose pull wha
docker compose up -d
```

::: warning Zuerst ziehen
Ein einfaches `docker compose up -d` zieht **kein** neueres Image, wenn der Tag bereits lokal zwischengespeichert ist. Führe immer zuerst `docker compose pull wha` aus.
:::

## Image-Tags wechseln

Um zu einem anderen Kanal zu wechseln (z. B. von `edge` zu `latest` nach dem ersten Release):

```sh
# docker-compose.yml bearbeiten und den wha-Image-Tag ändern, dann:
docker compose pull wha && docker compose up -d
```

Oder setze `WHA_IMAGE_TAG` und führe den Installer erneut aus (er erzeugt `docker-compose.yml` mit dem neuen Tag neu).

## Nach einem Update

Prüfe die Logs, um zu bestätigen, dass die neue Version sauber hochgekommen ist:

```sh
docker compose logs -f wha
```

Die Log-Ausgabe enthält die Version beim Start. Ist das Update fehlgeschlagen oder der Container nicht neu gestartet, prüfe die `wha-updater`-Logs:

```sh
docker compose logs wha-updater
```
