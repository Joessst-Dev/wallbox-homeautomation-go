---
name: chargepower-string-aliasing
description: Latent bug — SetChargePower stores a Fiber/fasthttp BodyParser-aliased string that later requests can corrupt
metadata:
  type: project
---

`web.handleChargePower` passes `req.Power` (from Fiber `c.BodyParser`) straight into
`controller.SetChargePower`, which keeps it in `ControlState.ChargePower` long-term.
Fiber's default (non-Immutable) `BodyParser` returns strings that **alias the pooled
fasthttp request buffer**; once that `RequestCtx` returns to the pool and is reused by a
later request, the bytes behind the stored `ChargePower` are overwritten (observed: `"now"`
scrambled to `"onw"`, then published to evcc and reflected in `Status()`).

**Why:** discovered while writing the issue-#43 integration suite — two back-to-back
`App().Test` POSTs (`/api/charge-power` then `/api/override`) corrupted the first request's
stored charge-power string. The DB write inside `SetChargePower` happens before the buffer
is reused, so `charge_power` in the `settings` table stays correct; only the in-memory value
(and thus the published mode until the next `loadChargePower`) corrupts.

**How to apply:** if asked to harden this, copy the string at the boundary — e.g.
`strings.Clone(req.Power)` in the handler, or enable Fiber's `Immutable: true`. The #43 E2E
test works around it (not fixes it) by ordering the charge-power POST **last** so its buffer
is never recycled; see `internal/integration/e2e_test.go`. Any test that does a charge-power
POST followed by another `App().Test` request will hit this. Related: [[mochi-broker-mis-framing]].
