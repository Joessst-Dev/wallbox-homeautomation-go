# Installer guide

The one-command installer sets up the full PV-surplus EV charging stack
(Mosquitto + evcc + wha) on a Raspberry Pi (or any Docker host):

```sh
curl -fsSL https://raw.githubusercontent.com/Joessst-Dev/wallbox-homeautomation-go/main/scripts/install.sh | bash
```

The script is self-contained (`scripts/install.sh` in this repo). You can also
clone the repo and run `make install`, or download and inspect the script before
executing it:

```sh
curl -fsSL https://raw.githubusercontent.com/Joessst-Dev/wallbox-homeautomation-go/main/scripts/install.sh \
  -o install.sh
# review install.sh, then:
bash install.sh
```

## What the script does

1. **Prerequisite checks** — verifies `curl` and `docker` (with the Compose
   plugin) are present and that the daemon is reachable.
2. **Existing-config detection** — if `$INSTALL_DIR/evcc.yaml` already exists,
   prompts whether to keep it or reconfigure from scratch
   (see [Re-running / reconfiguring](#re-running--reconfiguring)).
3. **Version selection** — queries GHCR for published tags and lets you pick
   `latest` (recommended when a stable release exists), `edge` (bleeding-edge
   from `main`), or an explicit `vX.Y.Z` (see [Versions](#versions)).
4. **Credential prompts** — collects your Sungrow inverter IP, Easee account
   credentials and charger serial, Renault MY account credentials and optional
   VIN, plus usable battery capacity, charger max current, and energy tariffs.
5. **File generation** — writes `evcc.yaml` with all device config, fills in
   `config.yaml` with the generated MQTT credentials, and secures Mosquitto
   with auth + ACL. Secret-bearing files are set to `chmod 600`
   (see [Generated files](#generated-files)).
6. **evcc config validation** — runs `evcc checkconfig` inside a throwaway
   container to catch schema errors before the stack starts. Connection errors
   to your inverter are expected at this stage and are non-fatal.
7. **Stack start + health poll** — pulls images, starts the stack with
   `docker compose up -d`, and polls `http://localhost:8080/healthz` for up
   to 60 s.

## Options and environment overrides

| Variable / flag | Purpose |
| --------------- | ------- |
| `--dry-run` / `WHA_SKIP_START=1` | Generate config files but do not pull images or start the stack. Use this to preview what will be written before committing. |
| `WHA_DIR=<path>` | Install directory (default: `~/wha`). |
| `WHA_IMAGE_TAG=<tag>` | Pin a specific wha image tag and skip the interactive version prompt (`latest`, `edge`, or `vX.Y.Z`). |

> **Test-only hooks:** `WHA_TTY` and `WHA_RAW_BASE` are internal overrides used
> by the automated test suite. Do not set them in production.

`--dry-run` is the safest way to inspect what the installer will produce before
making live changes:

```sh
bash install.sh --dry-run
# equivalent:
WHA_SKIP_START=1 bash install.sh
```

Generated files are written to the install directory but no images are pulled
and the stack is never started. Inspect `~/wha/evcc.yaml` and
`~/wha/config.yaml`, edit as needed, then start manually:

```sh
docker compose -f ~/wha/docker-compose.yml up -d
```

## Generated files

After a successful run, `$INSTALL_DIR` (default `~/wha`) contains:

```
~/wha/
├── docker-compose.yml          # pulled from repo; image tag pinned to your choice
├── config.yaml                 # wha config with MQTT credentials wired in  (chmod 600)
├── evcc.yaml                   # evcc config with all device credentials    (chmod 600)
└── mosquitto/
    └── config/
        ├── mosquitto.conf      # listener + persistence + auth references
        ├── passwd              # bcrypt-hashed broker credentials            (chmod 600)
        └── acl                 # per-user topic ACL
```

`config.yaml` and `evcc.yaml` contain plaintext credentials and are set to
`chmod 600`. The Mosquitto `passwd` file stores a bcrypt hash and is also
`chmod 600`. The install directory itself is `chmod 700`.

## Re-running / reconfiguring

The installer is safe to re-run. If `evcc.yaml` already exists in
`$INSTALL_DIR`, the script asks:

- **Keep** (default) — skips all credential prompts, version selection, and
  file generation and goes straight to pulling and starting the stack. Use this
  to restart after a manual config edit or a failed first start.
- **Reconfigure** — all prompts run again and generated files are overwritten.

## Versions

Published wha images live at `ghcr.io/joessst-dev/wha` (public GHCR).

| Tag | What it tracks |
| --- | -------------- |
| `latest` | Newest stable GoReleaser release. Only exists once a `v*` tag has been pushed. |
| `vX.Y.Z` | A specific release. Pinned — never moves. |
| `edge` | Built from every push to `main`. Always available; may be less stable. |

If no `latest` tag exists yet (the project hasn't published a release), the
installer defaults to `edge` and shows a warning.

`WHA_IMAGE_TAG` lets you skip the interactive prompt and pin a tag in
unattended or headless runs:

```sh
WHA_IMAGE_TAG=edge bash install.sh
```

## Updating

To pull a newer image and restart:

```sh
docker compose -f ~/wha/docker-compose.yml pull
docker compose -f ~/wha/docker-compose.yml up -d
```

To switch to a different tag, edit `~/wha/docker-compose.yml`, change the
image line to the desired tag (e.g. `ghcr.io/joessst-dev/wha:latest`), then
run the two commands above.

## Security

The installer secures Mosquitto automatically: it generates a random 48-character
hex password, hashes it with `mosquitto_passwd` (or via a throwaway Docker
container if `mosquitto_passwd` is not installed locally), and writes an ACL
that grants the single `wha` user `readwrite evcc/#`.

One shared credential covers both evcc (which publishes state and subscribes to
its own set-topics) and wha (which reads state and publishes to
`evcc/loadpoints/<id>/.../set`). A finer two-user split — evcc with broad
access, wha with write access scoped only to `evcc/loadpoints/<id>/.../set` —
is possible future hardening. For most home installations the single shared
credential is sufficient; the broker must not be reachable from outside your
home network.

> **Why this matters:** evcc does not authenticate MQTT set-topics. Anyone who
> can publish to the broker can change your charging mode. Broker auth + ACL
> are the only protection — see the [Security section](../README.md#%EF%B8%8F-security)
> in the README.

## Troubleshooting

### No terminal available for prompts

Under `curl … | bash` the shell's stdin is the script itself, so the installer
opens `/dev/tty` for interactive prompts. If you're running without a terminal
(a CI job, an SSH session with no PTY), download the script first and run it
directly, or pre-supply all values via environment variables:

```sh
curl -fsSL https://raw.githubusercontent.com/Joessst-Dev/wallbox-homeautomation-go/main/scripts/install.sh \
  -o install.sh
WHA_DIR=/opt/wha WHA_IMAGE_TAG=edge bash install.sh
```

### wha can't open its database (SQLITE_CANTOPEN)

The container runs as a non-root user; a pre-existing root-owned `wha-data`
volume blocks DB creation. Recreate it:

```sh
docker compose -f ~/wha/docker-compose.yml down -v
docker compose -f ~/wha/docker-compose.yml up -d
```

### No data on the dashboard / `readyz` returns 503

`/healthz` confirms the wha process started (so the installer prints "wha is
up!"); `/readyz` confirms it has connected to MQTT and is receiving state from
evcc. If the dashboard is empty, `curl http://localhost:8080/readyz` — a 503
means wha can't reach the broker or evcc isn't publishing. Check that `evcc.yaml`
has an `mqtt:` block pointing at `mosquitto:1883`, that the MQTT credentials
match what's in `config.yaml`, and that both containers are running:

```sh
docker compose -f ~/wha/docker-compose.yml ps
docker compose -f ~/wha/docker-compose.yml logs evcc
```

Inspect live topics with MQTT Explorer. Topic names and casing must match
exactly — see [docs/mqtt.md](mqtt.md) for the full topic contract.

### evcc checkconfig errors

Checkconfig cannot reach your inverter during install (it's a LAN device, not a
container), so connection errors are expected and the script asks whether to
continue. Schema errors (missing or misspelled fields) mean `evcc.yaml` needs
editing — fix it and re-run the installer, or edit the file and run (swap
`evcc/evcc` for whichever evcc image your `~/wha/docker-compose.yml` pins, so
the schema matches what the stack actually runs):

```sh
docker run --rm \
  -v ~/wha/evcc.yaml:/etc/evcc.yaml:ro \
  evcc/evcc evcc -c /etc/evcc.yaml checkconfig
```

### Charging won't start on a fresh setup

evcc only polls vehicle SoC while a charge session is active, so the SoC may
be unknown at first. Use the wha dashboard's **Charge now** (ForceOn) once to
kick-start a session; auto (surplus) mode then takes over on subsequent runs.
