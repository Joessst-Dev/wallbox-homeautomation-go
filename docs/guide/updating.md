# Updating

There are two ways to update `wha`: in-UI (recommended) and manually from the Pi.

## In-UI update

The **Software** card on the dashboard handles updates without SSH. This requires the `wha-updater` sidecar, which the one-command installer sets up automatically.

1. Open the dashboard at `http://<your-pi>:8080`.
2. In the **Software** card, click **Check** to query GHCR for a newer release.
3. If an update is available, an **Update to vX.Y.Z** button appears. Click it.
4. `wha` writes the chosen version to the shared update volume. The `wha-updater` sidecar pulls the new image and restarts the `wha` container.
5. The dashboard reconnects automatically after a few seconds.

Your data (sessions, events, settings) is stored in the `wha-data` volume and survives the container swap.

### How the updater works

Because `wha` runs as a distroless non-root container with no Docker socket access, it cannot replace its own image. The `wha-updater` sidecar owns the Docker socket and does that on its behalf:

1. `wha` validates and writes the chosen version tag to the shared `wha-update` volume.
2. `wha-updater` pins that tag in `docker-compose.yml` and runs `docker compose up -d wha`, recreating only the `wha` service.

The Compose stack enables this with `WHA_UPDATE_ENABLED=true`; it's off by default in other configurations since it requires the sidecar.

## Manual update (from the Pi)

To update without the in-UI mechanism — or to switch image tags:

```sh
cd ~/wha
docker compose pull wha
docker compose up -d
```

::: warning Pull first
A plain `docker compose up -d` does **not** pull a newer image if the tag is already cached locally. Always run `docker compose pull wha` first.
:::

## Switching image tags

To switch to a different channel (e.g. from `edge` to `latest` after the first release):

```sh
# Edit docker-compose.yml and change the wha image tag, then:
docker compose pull wha && docker compose up -d
```

Or set `WHA_IMAGE_TAG` and re-run the installer (it will regenerate `docker-compose.yml` with the new tag).

## After an update

Check the logs to confirm the new version came up cleanly:

```sh
docker compose logs -f wha
```

The log output includes the version on startup. If the update failed or the container didn't restart, check the `wha-updater` logs:

```sh
docker compose logs wha-updater
```
