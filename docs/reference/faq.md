# FAQ

## Does "Charge now" still stop at 80%?

**Yes** — by default, **Charge now** still stops at the `socCap` (default 80%), via both `wha`'s own logic and the evcc `limitSoc` backstop.

If you want to charge past the cap for a specific session, tick the **Charge now → charge past the SoC cap** checkbox on the dashboard. This lifts the stop to `socMax` (default 100%) for the duration of the override only. When the override ends, the cap is immediately restored.

## How do I charge at full power in winter?

Set **Charge power → Full power** on the dashboard. This switches `wha` to `now` mode (evcc charges at maximum current regardless of surplus), while still applying the SoC cap. The setting persists across restarts.

When you want to return to surplus-only charging in spring, switch it back to **Surplus**.

## Do my settings survive a restart or update?

| Setting | Persists |
|---------|---------|
| **Charge power** (Surplus / Full power) | ✅ Yes — stored in the database |
| **Auto** / **Charge now** / **Stop** override | ❌ No — resets to Auto on restart |
| Configuration in `config.yaml` | ✅ Yes — it's a file |

After a `docker compose down && up`, the charge power setting is restored from the database and `wha` resumes in the same charge-power mode as before the restart.

## Is the MQTT broker safe to expose?

**No — keep the broker on your home network only.**

evcc does not authenticate MQTT set-topics. Anyone who can publish to `evcc/loadpoints/<id>/mode/set` can control your charging. The one-command installer secures Mosquitto with a randomly generated credential and an ACL that restricts `wha` to `evcc/#`. Even so, do not expose port 1883 to the internet or to untrusted networks.

## Why does wha need its own policy on top of evcc?

evcc has a built-in `pv` mode that also charges on surplus. `wha` adds:
- A stricter, auditable policy with documented priority order.
- Persistent charge history in SQLite.
- A purpose-built dashboard focused on the operator view (surplus, SoC, override).
- The dead-man `limitSoc` backstop re-assertion logic.
- The charge-power toggle and cap-bypass features.

If evcc's built-in PV mode is sufficient for you, `wha` is optional.

## What happens if wha crashes?

The evcc `limitSoc` backstop is the last line of defence. Even if `wha` crashes:
- evcc's own `limitSoc` setting (last asserted by `wha`) remains in effect, capping the charge at `socCap`.
- evcc's `pv` mode (last set by `wha`) continues to run on evcc's internal logic.
- `wha` does **not** set the mode to `off` on shutdown; it relies on the `limitSoc` backstop.

On restart, `wha` re-asserts the mode and `limitSoc` on the first control tick.

## Why is vehicle SoC not updated in real time?

The Renault MY API is a cloud polling service — evcc can only request an update when a charge session is active, and then roughly once per hour. This is a limitation of the Renault integration, not `wha`.

Implications:
- SoC may be unknown (showing 0%) on a fresh setup until the first charge.
- SoC can lag reality by up to an hour.
- The `limitSoc` backstop compensates: even if `wha` doesn't see the SoC hit 80%, evcc's own limit stops the charge.

## How do I check if everything is working?

1. Open the dashboard at `http://<your-pi>:8080`.
2. The **Health** card should show MQTT broker and evcc both green.
3. The **Power flow** card should show live power values (updating every ~5 seconds).
4. The **Vehicle** card should show a connected vehicle.

If you see stale data or disconnected health indicators, see [Troubleshooting](/reference/troubleshooting).

## Can I run wha without the Raspberry Pi installer?

Yes. See [Guide → Manual Install](/guide/manual-install) for Docker Compose and local source build options. The one-command installer is just convenience; the stack runs anywhere Docker is available.
