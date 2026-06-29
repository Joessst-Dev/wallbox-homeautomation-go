---
name: controller-decision-engine
description: The pure decision engine in internal/controller — API, priority rules, and test conventions
metadata:
  type: project
---

`internal/controller` is the safety-critical PURE decision engine. No I/O, no clock, no network — pure functions over a snapshot so it is exhaustively testable.

**Why:** Charging the wrong way (importing grid power, overcharging the battery, ignoring stale data) has real cost/safety consequences, so the core logic is isolated and heavily spec'd.

**How to apply:**
- Key types in `state.go`: `State`, `Override`, `Inputs`, `Timers`, `Decision`, const `ModeOff = "off"`. Other packages depend on these exact signatures — do NOT rename.
- `clock.go`: `Clock` interface, `RealClock`, and `FakeClock` (with `NewFakeClock(t)`, `Now`, `Set`, `Advance`). FakeClock is deliberately in a non-test file so other packages reuse it.
- `policy.go`: `Surplus(in)` = `ChargeW - GridW - max(0, BatteryW)` (battery discharge never counts as solar; battery charging i.e. negative BatteryW is ignored). `Decide(now, in, st, timers, cfg)` evaluates in strict priority order, first match wins, ALWAYS returns updated Timers: 1) Stale→FailSafe/off (must stay first), 2) active Override (respects OverrideUntil expiry; ForceOff→off, ForceOn→enableMode), 3) !Ready→Idle/off, 4) `!Connected`→Idle/off ("no vehicle connected"; automatic control only), 5) SoC cap latch (latched in SocReached until VehicleSoC < SoCResumeBelow), 6) surplus hysteresis + dwell state machine.
- Domain rule (from evcc): evcc must NOT be commanded to charge when no vehicle is physically plugged in. `Inputs.Connected bool` gates this.
- First-run deadlock fix: override is checked BEFORE the !Ready gate (was after). evcc only polls vehicle SoC while charging, so on a fresh install SoC is never seen → readiness never satisfied → never charges → SoC never polled. A manual ForceOn must be able to break out, so override precedes readiness. Stale still beats override.
- Package now also contains (added by other agents): `controller.go` (runtime loop) and `inputs.go`/`inputs_test.go` (input aggregation). Note: `config.Control.VehicleSoCStaleTimeout` was removed from config.
- Dwell: from idle, surplus>=StartThresholdW arms CrossedUpAt→SurplusPending, then after StartDwell→Charging. From charging, surplus<StopThresholdW arms CrossedDownAt→StopPending (keeps charging), then after StopDwell→Idle. Surplus recovery cancels a pending stop (anti-flap).

**Tests:** Ginkgo v2 + Gomega, run via `go test ./internal/controller/...`. Suite bootstrap in `controller_suite_test.go` (`TestController`). Black-box package `controller_test`. Helpers `baseConfig()` / `baseInputs()` in `policy_test.go`. Use `FakeClock` + `Advance` for dwell tests, `DescribeTable`/`Entry` for the Surplus formula.
