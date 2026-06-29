# Multi-stage, fully static (CGO_ENABLED=0) build. The SQLite driver is pure-Go
# (modernc), so this cross-compiles to arm64 with no toolchain/QEMU and runs on
# distroless. Web assets (templates, compiled app.css, htmx.min.js) are embedded
# via go:embed, so no Node/Tailwind is needed at image-build time.

FROM --platform=$BUILDPLATFORM golang:1.25 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# BUILDPLATFORM pins this stage to the runner's native arch, so the Go toolchain
# never runs under emulation. TARGETOS/TARGETARCH are injected by buildx per
# requested --platform, and Go cross-compiles to each one. No QEMU needed.
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /wha ./cmd/wha

# Pre-create the data dir so the named volume inherits nonroot ownership on first
# mount (otherwise the volume is root-owned and the nonroot process cannot create
# /data/wha.db → SQLITE_CANTOPEN).
RUN mkdir -p /data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /wha /wha
# uid:gid 65532 is the distroless "nonroot" user.
COPY --from=build --chown=65532:65532 /data /data
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/wha"]
