# Contributing

Thanks for working on **wha**. This is a small, safety-relevant Go service; the bar is
"correct, tested, and boring to operate."

> **Deploying, not developing?** If you want to run wha on a Raspberry Pi rather than
> contribute to the code, the one-command installer in [docs/install.md](docs/install.md)
> is the faster path.

## Prerequisites

- Go 1.25+ (the module pins the version in `go.mod`).
- [`golangci-lint`](https://golangci-lint.run) v2 (`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`).
- Optional: the standalone Tailwind CLI (for `make css`) and GoReleaser (for release dry-runs).

## Build, test, lint

```sh
make test                      # all Ginkgo suites
go test ./internal/controller/ -run TestController -args -ginkgo.focus="surplus"  # one spec
go vet ./...
gofmt -l .                     # must print nothing
golangci-lint run              # config in .golangci.yml
make build-arm64               # prove the cgo-free arm64 target still builds
```

All five must pass before a PR is mergeable (CI enforces them). The web/store tests use a
temp SQLite DB and an embedded Fiber app — no network required.

## Conventions

- **Conventional Commits.** Commit subjects use `feat:`, `fix:`, `docs:`, `test:`, `chore:`,
  `ci:` … The `feat:`/`fix:` lines are surfaced in the GoReleaser changelog; `docs/test/chore/ci`
  are filtered out. Reference issues in the body (`Closes #N`).
- **No AI/self-attribution** in commits or PRs (no `Co-Authored-By: Claude`, no generated-with
  footer).
- **Tests are first-class.** Add/extend Ginkgo specs for behavior changes. Keep them
  deterministic: inject the `Clock`, no `time.Sleep`, no real network. The pure `Decide`
  state machine is the primary target for control-logic changes.
- **Match the surrounding style** and run `gofmt`/`goimports` (the linter's `goimports`
  local-prefixes the module path).

## Hard constraints (don't break these)

- **cgo-free.** SQLite is `modernc.org/sqlite` (driver name `sqlite`). Never import
  `mattn/go-sqlite3` — one cgo import breaks the static arm64 build.
- **Fail-safe priority.** In `Decide`, `Stale → off` must win over everything; never let a
  refactor reorder it below the surplus logic.
- **Lock discipline.** MQTT publishes and DB I/O must not run while holding the controller
  mutex (`c.mu`); `tick` releases the lock before any I/O.
- **evcc MQTT casing.** Command set-topics are camelCase (`.../limitSoc/set`); see
  [docs/mqtt.md](docs/mqtt.md).

## Workflow

1. Branch from `main` (`feat/...` or `fix/...`).
2. Make the change with tests; get all gates green locally.
3. Open a PR (`Closes #N`). CI runs lint/test/cross-compile; the review bot comments.
4. Address findings, then squash-merge. Tag `vX.Y.Z` to cut a release.

## Docs

Keep `README.md` and `CLAUDE.md` in sync with behavior changes (new config keys, new
invariants). Update [docs/mqtt.md](docs/mqtt.md) if the evcc topic contract changes.
If you change `scripts/install.sh`, update [docs/install.md](docs/install.md) to match —
the two document the same flow from different perspectives (user guide vs. script source).
