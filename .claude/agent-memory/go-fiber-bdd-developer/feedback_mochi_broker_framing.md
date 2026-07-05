---
name: mochi-broker-mis-framing
description: E2E MQTT testing pitfall — the embedded mochi broker mis-frames back-to-back small PUBLISH packets under rapid publish
metadata:
  type: feedback
---

The `internal/integration` E2E suite runs a real in-process `github.com/mochi-mqtt/server/v2`
broker (pure-Go, test-only, cgo-free) with real paho clients. Under the millisecond-scale
`Republish`/`DecisionInterval` timing the tests use, the broker occasionally **mis-frames
adjacent small MQTT PUBLISH packets**, bleeding bytes between them (e.g. `"off"`+`"now"`
delivered to a subscriber as `"onw"`). It is intermittent and NOT flagged by `-race` (the
bytes travel over a real loopback TCP socket, not shared memory), and it is a broker artifact
— wha's evcc client always publishes the exact command string.

**Why:** cost a real debugging loop on issue #43. Distinguish it from the separate,
genuine [[chargepower-string-aliasing]] in-memory corruption (that one is our code).

**How to apply:** in E2E observers, **filter captured command payloads to the well-formed set**
(valid modes / all-digit limitSoc) so torn frames are dropped; `Republish` re-emits the intended
value cleanly, so `Eventually` on the last valid payload is stable. Do NOT assert on raw
`last()` without filtering, and do NOT try to "fix" it with `-race` — it won't show. See the
`validMode`/`validLimit` filters in `internal/integration/e2e_test.go`.
