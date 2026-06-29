# Contributing

## Prerequisites

- Go 1.23+ (`go version`)
- [golangci-lint](https://golangci-lint.run/welcome/install/) v2 (`golangci-lint --version`)
- [Tailwind CSS standalone CLI](https://tailwindcss.com/blog/standalone-cli) — only needed when editing HTML templates (`make css`)

## Build and test

```sh
go build ./...                       # build all packages
go test ./...                        # run all Ginkgo suites
go test ./internal/controller/...    # one package
go test ./internal/controller/ -run TestController -args -ginkgo.focus="surplus"  # one spec

go vet ./...
gofmt -l .                           # must print nothing (CI gate)
golangci-lint run                    # v2, config in .golangci.yml
```

The `make` targets are thin wrappers:

```sh
make test           # go test ./...
make run            # run locally with WHA_MQTT_BROKER=tcp://localhost:1883
make build-arm64    # static arm64 binary (CGO_ENABLED=0 GOOS=linux GOARCH=arm64)
make css            # recompile Tailwind → internal/web/static/app.css
```

## Cross-compile

The runtime binary must always build without cgo. Verify the Pi target still compiles:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...
```

The multi-arch Docker image:

```sh
docker buildx build --platform linux/amd64,linux/arm64 .
```

## cgo-free constraint

**Never import `mattn/go-sqlite3`** or any other cgo dependency. SQLite is provided by
`modernc.org/sqlite` (driver name `"sqlite"`, not `"sqlite3"`). One cgo import silently
breaks the static `linux/arm64` build because cgo cross-compilation requires a C
cross-toolchain that is not present in CI.

To verify the cgo-free invariant locally:

```sh
CGO_ENABLED=0 go build ./...    # must succeed with no errors
```

## Testing approach

- Ginkgo/Gomega throughout — write specs in `*_test.go` files alongside the package.
- The pure `Decide` state machine is the primary test target; see
  `internal/controller/policy_test.go`.
- Inject the `Clock` interface to make time-dependent tests deterministic — no
  `time.Sleep` in tests.
- Store tests run real migrations against a temp SQLite file (see
  `internal/store/*_test.go`).
- Web tests drive the Fiber app via `Server.App().Test(...)` with fake
  controller/store implementations.

## Commit messages

Follow [Conventional Commits](https://www.conventionalcommits.org/). GoReleaser
uses commit messages to build the changelog:

| Prefix | Changelog section |
|--------|-------------------|
| `feat:` | Features |
| `fix:` | Bug Fixes |
| `docs:`, `test:`, `chore:`, `ci:` | *filtered out* |

Examples:

```
feat: add configurable log level
fix: prevent double-start on broker reconnect
docs: expand Pi setup section in README
```

Breaking changes: append `!` after the type (`feat!:`) and add a `BREAKING CHANGE:`
footer.

## Pull requests

- Keep PRs focused: one feature or fix per PR.
- All CI gates must be green: `gofmt`, `go vet`, `golangci-lint`, `go test ./...`,
  and the `CGO_ENABLED=0 GOARCH=arm64` cross-compile check.
- The PR description should explain *what* changed and *why*, plus a brief test plan.
