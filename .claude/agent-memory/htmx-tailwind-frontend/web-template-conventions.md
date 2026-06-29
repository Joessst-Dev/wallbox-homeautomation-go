---
name: web-template-conventions
description: Fiber html/v2 template structure and the substrings the web tests assert
metadata:
  type: project
---

Web UI is Fiber + gofiber/template/html/v2. Template name = path under `internal/web/templates/` minus `.html`.

- `dashboard.html` renders THROUGH `layout.html` via `{{embed}}`. Partials render STANDALONE.
- `partials/status.html` is the htmx poll target (`hx-get="/partials/status" hx-trigger="every 5s"`, swaps `#status` innerHTML). View model is `StatusVM` (see `internal/web/dto.go` — all values pre-formatted, no logic in templates).
- The override `<form>` (POST `/api/override`, vars `mode`=auto|on|off + optional `hours`) lives INSIDE the status partial and targets `#status`, so the active-override highlight stays fresh on every 5s poll. **Why:** if it lived in dashboard.html it would go stale after a swap.
- `partials/sessions.html` loaded via htmx `hx-get="/partials/sessions"`; VM is `[]SessionVM`. `StartVehicleSoC`/`EndVehicleSoC` are `*int` — guard with `{{if}}` (templates auto-deref for printing).

**Tests in `server_test.go` assert these substrings — keep them:** full page must contain `Surplus`, `Vehicle SoC`, `Override`, `hx-get="/partials/status"`, `<!DOCTYPE html>`. Status partial must contain `Surplus` and `charging` (the `{{.State}}` value) and NOT contain `<!DOCTYPE html>`.

See [[tailwind-build-workflow]] for recompiling CSS after template edits.
