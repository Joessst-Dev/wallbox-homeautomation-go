# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`wha` is a single Go binary that runs **PV-surplus EV charging on top of [evcc](https://evcc.io)**.
It does **not** talk to hardware directly — evcc owns the device drivers (Sungrow SH8.0 RT
inverter, Easee Home wallbox, Renault Twingo). `wha` reads evcc's state over MQTT, applies its
own decision policy (start on sustained surplus, stop at SoC cap, fail-safe to off), and toggles
the evcc loadpoint mode. The two product rules: surplus → start charging; vehicle SoC > 80% → stop.

## Commands

```sh
go build ./...                       # build everything
go test ./...                        # all tests (Ginkgo suites)
go test ./internal/controller/...    # one package
go test ./internal/controller/ -run TestController -args -ginkgo.focus="surplus"  # one spec by substring
go vet ./...
gofmt -l .                           # must be empty (CI gate)
golangci-lint run                    # v2 config in .golangci.yml

make run                             # run locally (WHA_MQTT_BROKER=tcp://localhost:1883)
make build-arm64                     # static arm64 binary for the Pi (CGO_ENABLED=0)
make css                             # recompile Tailwind → internal/web/static/app.css (see "Web assets")

CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...   # prove the Pi target still builds (cgo-free)
docker buildx build --platform linux/amd64,linux/arm64 .   # multi-arch image (no QEMU)
goreleaser release --snapshot --clean # dry-run release build (binaries + images)
```

There are **no CLI subcommands or flags**: the binary loads config and runs. Configuration is
viper-based — `WHA_*` env vars + an optional YAML file (`WHA_CONFIG`, or `config.yaml` in
`/etc/wha` or `.`). Env keys are `WHA_<SECTION>_<KEY>` with nested camelCase keys uppercased
fully, e.g. `control.startThresholdW` → `WHA_CONTROL_STARTTHRESHOLDW`.

## Architecture (the parts that span files)

Data flow each control tick:
```
evcc → MQTT(Mosquitto) → evcc.Store (snapshot) → controller.tick:
    InputsFromSnapshot → Decide (pure) → publish mode/off + limitSoc backstop → persist
```

- **`internal/controller` is split into a pure core and a stateful loop.** `policy.go`
  (`Decide`, `Surplus`) and `inputs.go` (`InputsFromSnapshot`) are **pure functions** — no I/O,
  no `time.Now()` (a `Clock` is injected, see `clock.go`). `controller.go` is the loop that wires
  them to MQTT + the DB. When changing behavior, change `Decide`/`InputsFromSnapshot` and test
  them directly; the loop just orchestrates.
- **Composition via small interfaces.** `controller.go` depends on `Commander`,
  `SnapshotProvider`, `Recorder` (defined there), satisfied by `*evcc.Client`, `*evcc.Store`,
  `*store.Store`. `internal/web` depends on its own `Controller`/`Store` interfaces. `internal/app`
  is the composition root that wires concrete types and runs MQTT + loop + web under an
  `errgroup` with signal-based graceful shutdown.
- **Migrations run on startup**, not via a command: `store.Open()` applies the embedded
  golang-migrate migrations (treats `ErrNoChange` as success). `main.go` → `config.Load()` →
  `app.Run()`.

## Invariants — verify these when editing (regressions are easy and high-impact)

- **Fail-safe priority order** in `Decide` (first match wins): `Stale → off` (1), active
  Override (2), `!Ready → off` (3), `!Connected → off` (4), SoC-cap latch (5), surplus
  hysteresis/dwell (6). Stale/disconnect/no-vehicle/SoC-cap must always force `off`.
- **Pure-Go / cgo-free is mandatory.** SQLite is `modernc.org/sqlite` (driver name `"sqlite"`,
  not `"sqlite3"`). Never import `mattn/go-sqlite3` — one cgo import breaks the static arm64 build.
- **evcc MQTT command topics are camelCase** (`evcc/loadpoints/<id>/limitSoc/set`); a lowercase
  `limitsoc` is silently ignored. Booleans arrive as the strings `true`/`false`. Command
  publishes are non-retained and bounded (`WaitTimeout`) and must **not** run while holding the
  controller mutex — `tick` does publishes/DB I/O after releasing `c.mu`.
- **Vehicle SoC is coarse/slow** (evcc polls the Renault cloud ~hourly, only while charging) and
  is deliberately **not** stale-gated; the last known value is always used, with the evcc
  `limitSoc` backstop bounding overshoot. Don't add a short stale timeout to it.
- The `limitSoc` dead-man backstop and the mode are re-published on broker reconnect and on the
  `Republish` cadence (set-topics aren't retained). The backstop target is normally `control.socCap`,
  but is lifted to `control.socMax` while an explicit "charge past the cap" force-on override is
  active (`LimitSoCTarget`), and re-published immediately when the target changes so clearing the
  override restores the cap at once.
- **The charge mode (`pv`/`now`) is a runtime, persisted setting**, not just `control.enableMode`.
  `Decide` uses `Inputs.ChargePower` (via `effectiveMode`) for both automatic and force-on charging;
  it is set with `SetChargePower`, stored in the `settings` KV table, and reloaded on startup, so it
  survives restarts (including the in-UI update). `control.enableMode` is only the initial default.

## Testing approach

Ginkgo/Gomega throughout. The pure `Decide` state machine is the primary test target
(`internal/controller/policy_test.go`, table-driven with `FakeClock` for dwell/hysteresis).
Store tests run real migrations against a temp SQLite file; web tests drive the Fiber app via
`Server.App().Test(...)` with fake controller/store. Keep tests deterministic — inject the clock,
no `time.Sleep`, no real network.

## Web assets (non-obvious)

Templates + `htmx.min.js` + **compiled** Tailwind `app.css` are embedded via `//go:embed` in
`internal/web/assets.go`. The embed directive lists `templates/partials/*` explicitly — a bare
`templates/*` does not recurse. Tailwind is compiled with the standalone CLI (no Node):
after editing templates, run `make css` so newly-used utility classes land in the committed
`internal/web/static/app.css` (it's embedded into the binary; the Docker build is Node-free).

## CI/CD

GitHub Actions: `ci.yml` (lint/test/cross-compile on PR + main), `docker.yml` (main → GHCR
multi-arch `:edge`/`:sha`), `release.yml` (tag `v*` → GoReleaser binaries + `:latest`/semver
images). Commit messages should follow Conventional Commits — they feed the GoReleaser changelog
(`feat:`/`fix:` are surfaced; `docs/test/chore/ci` are filtered out). The runtime binary is always
built `CGO_ENABLED=0`; the test job uses `CGO_ENABLED=1` only for the race detector.
