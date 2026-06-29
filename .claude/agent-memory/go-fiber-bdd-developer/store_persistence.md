---
name: store-persistence
description: internal/store SQLite layer — modernc driver, timestamp encoding gotcha, single-writer pool, public API
metadata:
  type: project
---

The `internal/store` package is the SQLite persistence layer for wha (charge sessions, audit events, time-series samples). See [[project-overview]].

Key facts and design decisions:
- Driver is pure-Go `modernc.org/sqlite` (registers driver name `"sqlite"`). NEVER import `mattn/go-sqlite3` (cgo) — the arm64 build is static (verified with `CGO_ENABLED=0 go build`).
- Migrations: golang-migrate v4 with `database/sqlite` driver (which itself imports modernc — confirmed cgo-free) + `source/iofs` over a `//go:embed migrations/*.sql`. Package funcs: `MigrateUp/MigrateDown/MigrateVersion(path)`; `Open(path)` runs migrate up treating `migrate.ErrNoChange` as success. `MigrateVersion` maps `migrate.ErrNilVersion` -> (0,false,nil).
- Single writer: `db.SetMaxOpenConns(1)` to avoid "database is locked". PRAGMAs after open: journal_mode=WAL, busy_timeout=5000, foreign_keys=ON.

**Timestamp encoding gotcha (important):** times are stored as TEXT in TIMESTAMP-typed columns using a fixed-width UTC layout `2006-01-02T15:04:05.000000000Z07:00` (so SQL ORDER BY / range BETWEEN stay correct). BUT the modernc driver NORMALIZES values read from columns whose declared type contains TIMESTAMP/DATETIME back to RFC3339 (dropping zero fractional seconds). So storage uses the fixed-width layout, while `parseTime` reads with `time.RFC3339Nano` to accept both forms. All times stored/returned in UTC.

**Why:** A first test run failed with `cannot parse "Z" as ".000000000"` — modernc stripped the zero nanos on read. How to apply: when adding timestamp columns/queries here, format with the fixed-width layout for writes and parse with RFC3339Nano on reads; don't assume verbatim string round-trip through TIMESTAMP columns.

Nullable fields use `*int`/`*time.Time` in structs, mapped via `sql.NullInt64`/`sql.NullString` on read and a `nullInt` helper (nil -> NULL) on write. Bool (`Sample.Charging`) stored as INTEGER 0/1.

Tests: black-box `store_test` package, Ginkgo suite in `store_suite_test.go` with a `newTempStore()` helper using `GinkgoT().TempDir()` + `DeferCleanup`. Run: `go test ./internal/store/...`.
