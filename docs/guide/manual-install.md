# Manual Install

This page covers running the stack manually with Docker Compose, without the one-command installer. Use this if you want full control over the config files, prefer to build from source, or are adapting the setup for different hardware.

## Docker Compose (pull published image)

The `docker-compose.yml` in the repo pulls the published multi-arch image from GHCR — no local build needed:

```sh
# 1. Copy the example evcc config and fill in your hardware credentials
cp evcc.example.yaml evcc.yaml

# 2. (Optional) tune wha thresholds in config.yaml

# 3. Start the stack
docker compose up -d
```

Check that everything came up:

```sh
docker compose logs -f wha        # expect "store opened" + "web server listening"
curl -s localhost:8080/healthz    # → ok
```

The published image tags are:

| Tag | Tracks |
|-----|--------|
| `ghcr.io/joessst-dev/wha:edge` | Latest build from `main` |
| `ghcr.io/joessst-dev/wha:latest` | Latest stable release |
| `ghcr.io/joessst-dev/wha:vX.Y.Z` | Specific release |

The default `docker-compose.yml` uses `:edge`.

## Build from source

To build `wha` from the local source tree instead of pulling the image, use the local override file:

```sh
docker compose -f docker-compose.yml -f docker-compose.local.yml up -d --build
```

The override tags the build `:local` so it never clobbers the pulled `:edge` image. A plain `docker compose up` on the Pi continues using the published image.

You can also run the binary directly without Docker for local development:

```sh
make run     # runs against a broker on localhost:1883 (set WHA_* env vars)
```

## Required config: evcc.yaml

evcc must be configured with your hardware. Start from the example:

```sh
cp evcc.example.yaml evcc.yaml
```

Edit `evcc.yaml` and fill in:
- Sungrow inverter LAN IP under the `meters:` block.
- Easee account email, password, and charger serial under `chargers:`.
- Renault MY account email, password, and optionally the VIN under `vehicles:`.
- The `mqtt:` block pointing at `mosquitto:1883` with the credentials from `config.yaml`.

::: warning MQTT credentials
The MQTT credentials in `evcc.yaml` must match what's in `config.yaml`. The one-command installer generates and wires them automatically; in a manual setup you choose your own.
:::

## Optional config: config.yaml

`wha` runs with sensible defaults out of the box. Copy and edit `config.yaml` if you want to tune thresholds:

```sh
cp config.yaml /etc/wha/config.yaml   # or place it in the working directory
```

See [Guide → Configuration](/guide/configuration) for the most-tuned knobs, and [Reference → Configuration](/reference/configuration) for the full table.

## Mosquitto setup

The stack includes a Mosquitto container. In a manual setup you must provide auth + ACL config yourself — see the ⚠️ [Security](/guide/how-it-works#security) note. The one-command installer automates this; for a manual setup, create `mosquitto/config/mosquitto.conf` with authentication enabled and an ACL restricting the `wha` user to `evcc/#`.

## Accessing the services

| Service | URL | Notes |
|---------|-----|-------|
| wha dashboard | `http://<host>:8080` | Live status, overrides, history |
| evcc UI | `http://<host>:7070` | Full evcc controls and diagnostics |
| Health check | `http://<host>:8080/healthz` | Returns `ok` if wha process is up |
| Ready check | `http://<host>:8080/readyz` | Returns 200 only when connected to MQTT and receiving data |
