# Agent Memory Index

- [Project Overview](project_overview.md) — wha = PV-surplus EV charging controller; module path & package layout
- [Controller Decision Engine](controller_engine.md) — internal/controller pure engine: API, priority rules, dwell/hysteresis, test setup
- [Store Persistence Layer](store_persistence.md) — internal/store SQLite: modernc driver, migrate wiring, timestamp encoding gotcha, single-writer pool
- [Web Layer](web_layer.md) — internal/web Fiber v2 + htmx: public API, html template engine naming/layout/partials, go:embed partials pitfall, fake-based tests
