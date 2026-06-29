---
name: project-cicd-facts
description: Core CI/CD and containerization facts for the wha (wallbox-homeautomation-go) service
metadata:
  type: project
---

CI/CD setup for `github.com/Joessst-Dev/wallbox-homeautomation-go` (owner `Joessst-Dev`, default branch `main`).

**Why:** First-time CI introduction (no prior workflows). Designed 2026-06-28.
**How to apply:** Use these facts when extending/auditing the pipeline so the no-QEMU and CGO split assumptions stay intact.

- Binary `wha`, entrypoint `./cmd/wha`. Go 1.25 (go.mod `go 1.25.5`).
- Production binary is pure-Go, `CGO_ENABLED=0` (SQLite = modernc, no cgo). Cross-compiles linux/{amd64,arm64} with NO QEMU.
- Tests are Ginkgo/Gomega via `go test ./...` across internal/{config,controller,evcc,store,web}. store/web tests touch a temp SQLite file + embedded Fiber app, no network. Race detector needs `CGO_ENABLED=1` (tests only) — ubuntu runner has gcc.
- Web assets (templates, compiled app.css, htmx.min.js) are committed and go:embed'd → image build needs NO Node/Tailwind.
- Deploy target: Raspberry Pi (arm64) via docker compose; amd64 image also wanted for dev.
- Registry: GHCR `ghcr.io/joessst-dev/wha` (owner must be lowercased for GHCR).
- Image split decision: docker.yml (main branch) publishes dev images (`edge` + sha); GoReleaser (tags `v*`) owns release images (`latest` + semver) and the GitHub Release. No double-build because triggers never overlap. GoReleaser builds tag images from prebuilt binaries (COPY-only Dockerfile.goreleaser) so no second `go build` and no QEMU.
- Multi-arch no-QEMU trick: Dockerfile build stage `FROM --platform=$BUILDPLATFORM golang:1.25` + `ARG TARGETOS TARGETARCH` + `GOOS/GOARCH` so buildx cross-compiles natively per platform.
