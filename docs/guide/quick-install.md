# Quick Install

The one-command installer sets up the full PV-surplus EV charging stack (Mosquitto + evcc + wha) on a Raspberry Pi or any Docker host.

## Prerequisites

- Docker with the Compose plugin installed and the daemon running.
- `curl` available (standard on Raspberry Pi OS).
- Your hardware credentials ready (Sungrow IP, Easee login + serial, Renault login).

## Run the installer

```sh
curl -fsSL https://raw.githubusercontent.com/Joessst-Dev/wallbox-homeautomation-go/main/scripts/install.sh | bash
```

The installer is interactive: it prompts for credentials, picks an image version, generates config files, validates the evcc setup, and starts the stack.

::: tip Preview before committing
Add `--dry-run` to generate and inspect config files without starting anything:
```sh
curl -fsSL https://raw.githubusercontent.com/Joessst-Dev/wallbox-homeautomation-go/main/scripts/install.sh | bash -s -- --dry-run
```
:::

After a successful install:
- **wha dashboard:** `http://<your-pi>:8080`
- **evcc UI:** `http://<your-pi>:7070`

## What the installer does

1. **Prerequisite checks** — verifies `curl` and `docker` (with the Compose plugin) are present and that the Docker daemon is reachable.
2. **Existing-config detection** — if `~/wha/evcc.yaml` already exists, asks whether to keep it or reconfigure from scratch (see [Re-running](#re-running)).
3. **Version selection** — queries GHCR for published tags and lets you pick `latest`, `edge`, or a specific `vX.Y.Z`.
4. **Credential prompts** — collects your Sungrow inverter IP, Easee account credentials and charger serial, Renault MY account credentials and optional VIN, plus usable battery capacity, charger max current, and energy tariffs.
5. **File generation** — writes `evcc.yaml` with all device config, fills in `config.yaml` with the generated MQTT credentials, and secures Mosquitto with auth + ACL. Secret-bearing files are set to `chmod 600`.
6. **evcc config validation** — runs `evcc checkconfig` inside a throwaway container to catch schema errors before the stack starts. Connection errors to your inverter are expected at this stage and are non-fatal.
7. **Stack start + health poll** — pulls images, starts the stack with `docker compose up -d`, and polls `http://localhost:8080/healthz` for up to 60 seconds.

## Options

| Variable / flag | Purpose |
|----------------|---------|
| `--dry-run` or `WHA_SKIP_START=1` | Generate config files but do not pull images or start the stack. |
| `WHA_DIR=<path>` | Install directory (default: `~/wha`). |
| `WHA_IMAGE_TAG=<tag>` | Pin a specific image tag and skip the version prompt (`latest`, `edge`, or `vX.Y.Z`). |

## Generated files

After a successful run, the install directory (`~/wha` by default) contains:

```
~/wha/
├── docker-compose.yml          # image tag pinned to your choice
├── config.yaml                 # wha config with MQTT credentials  (chmod 600)
├── evcc.yaml                   # evcc config with device credentials (chmod 600)
└── mosquitto/
    └── config/
        ├── mosquitto.conf      # listener + persistence + auth references
        ├── passwd              # bcrypt-hashed broker credentials   (chmod 600)
        └── acl                 # per-user topic ACL
```

`config.yaml` and `evcc.yaml` contain plaintext credentials — keep them out of version control and backups you don't control.

## Image versions

| Tag | What it tracks |
|-----|----------------|
| `latest` | Newest stable release. Only exists once a `v*` tag has been pushed. |
| `vX.Y.Z` | A specific release. Pinned — never moves. |
| `edge` | Built from every push to `main`. Always available; may be less stable. |

If no `latest` tag exists yet (before the first release), the installer defaults to `edge`.

## Re-running

The installer is safe to re-run. If `evcc.yaml` already exists, it asks:

- **Keep** (default) — skips all prompts and goes straight to pulling and starting the stack. Use this to restart after a manual config edit.
- **Reconfigure** — all prompts run again and generated files are overwritten.

## No terminal available

Under `curl … | bash`, the shell's stdin is the script itself, so the installer opens `/dev/tty` for interactive prompts. If you're running without a terminal (SSH without a PTY, a CI job), download the script first and run it directly, or pre-supply values via environment variables:

```sh
curl -fsSL https://raw.githubusercontent.com/Joessst-Dev/wallbox-homeautomation-go/main/scripts/install.sh \
  -o install.sh
WHA_DIR=/opt/wha WHA_IMAGE_TAG=edge bash install.sh
```
