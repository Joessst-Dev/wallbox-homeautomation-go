---
name: web-layer
description: internal/web Fiber v2 + htmx layer — public API, template engine quirks, go:embed pitfall, test approach
metadata:
  type: project
---

internal/web is the HTTP layer: Fiber v2 app serving an htmx dashboard + JSON API over the controller and store. Never touches MQTT.

Public API (the app composition root in internal/app calls these exactly): `web.New(cfg config.Web, ctrl Controller, st Store, log *slog.Logger) *Server`, plus `Start()`, `Shutdown(ctx)`, `App() *fiber.App`. Handlers depend on the local `Controller`/`Store` interfaces (not concrete types) so tests pass fakes.

**Why:** Lets specs drive everything through `Server.App().Test(req, -1)` with no broker, DB, or real port.

**How to apply:**
- Template engine: `html.NewFileSystem(http.FS(fs.Sub(embeddedFS,"templates")), ".html")`. Template names are paths relative to templates/ minus `.html` → `dashboard`, `layout`, `partials/status`, `partials/sessions`. Render full page with layout via `c.Render("dashboard", vm, "layout")`; layout.html embeds the child with `{{embed}}`. Render partials standalone (no layout) via `c.Render("partials/status", vm)`.
- go:embed pitfall: `//go:embed templates/*` does NOT include the partials subdir's files; assets.go must list `templates/partials/*` explicitly too. assets.go must sit next to templates/ and static/.
- View models: dto.go flattens controller.StatusView → `StatusVM` (whole-watt ints + pre-formatted kW strings + booleans) so templates carry zero logic. `newStatusVM(now, view)` takes an injected `now` for deterministic "ago" text.
- Override mapping in POST /api/override: form/JSON `mode` auto|on|off → controller.Override; `hours>0` sets until=now+hours. If `HX-Request` header present, returns the refreshed status partial; else `{"ok":true}`.
- See [[project-overview]], [[controller-decision-engine]], [[store-persistence-layer]].
