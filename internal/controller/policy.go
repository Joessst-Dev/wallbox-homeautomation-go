package controller

import (
	"time"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
)

// Surplus computes the PV power available to the car, in watts.
//
// It is the power the car already draws (ChargeW) plus current export (-GridW),
// minus any battery discharge (max(0, BatteryW)) so that battery power never
// masquerades as solar surplus. Battery charging (negative BatteryW) is ignored
// because it is already reflected in the grid/PV balance.
func Surplus(in Inputs) float64 {
	batteryDischarge := in.BatteryW
	if batteryDischarge < 0 {
		batteryDischarge = 0
	}
	return in.ChargeW - in.GridW - batteryDischarge
}

// effectiveMode returns the evcc mode used when charging is enabled: the
// operator's runtime charge-power setting (pv|now), falling back to the
// configured default when unset.
func effectiveMode(in Inputs, cfg config.Control) string {
	if in.ChargePower != "" {
		return in.ChargePower
	}
	return cfg.EnableMode
}

// LimitSoCTarget is the SoC limit wha asks evcc to enforce as its dead-man
// backstop. It is the configured cap, lifted to SoCMax only while an explicit
// "charge past the cap" ForceOn override is active.
func LimitSoCTarget(now time.Time, in Inputs, cfg config.Control) int {
	if in.Override == OverrideForceOn && in.OverrideCapBypass && overrideActive(now, in) {
		return cfg.SoCMax
	}
	return cfg.SoCCap
}

// resetTimers returns Timers with both dwell markers cleared.
func resetTimers() Timers {
	return Timers{}
}

// overrideActive reports whether a non-auto override is currently in force,
// honoring its expiry. A zero OverrideUntil means the override never expires.
func overrideActive(now time.Time, in Inputs) bool {
	if in.Override == OverrideAuto {
		return false
	}
	return in.OverrideUntil.IsZero() || now.Before(in.OverrideUntil)
}

// Decide is the pure decision engine. Given the current time, a snapshot of the
// world, the prior State and Timers, and the tuning config, it returns the next
// Decision. Rules are evaluated in strict priority order; the first match wins.
// Decide always returns a fully-populated Timers field.
func Decide(now time.Time, in Inputs, st State, timers Timers, cfg config.Control) Decision {
	// 1. Fail-safe: stale data or disconnected broker forces off.
	if in.Stale {
		return Decision{
			State:       StateFailSafe,
			Timers:      resetTimers(),
			DesiredMode: ModeOff,
			Reason:      "stale data / disconnected → off",
		}
	}

	// 2. Manual override (respecting expiry). Evaluated before the readiness
	// gate so a manual ForceOn can kick-start a fresh install: evcc only polls
	// vehicle SoC while charging, so an auto-mode controller that waits for SoC
	// before charging would deadlock on first run. ForceOff is likewise honored
	// here because turning off is always safe.
	if overrideActive(now, in) {
		switch in.Override {
		case OverrideForceOff:
			return Decision{
				State:       StateIdle,
				Timers:      resetTimers(),
				DesiredMode: ModeOff,
				Reason:      "manual override: off",
			}
		case OverrideForceOn:
			return Decision{
				State:       StateCharging,
				Timers:      resetTimers(),
				DesiredMode: effectiveMode(in, cfg),
				Reason:      "manual override: on",
			}
		}
	}

	// 3. Not ready: we have not yet seen all required metrics.
	if !in.Ready {
		return Decision{
			State:       StateIdle,
			Timers:      resetTimers(),
			DesiredMode: ModeOff,
			Reason:      "waiting for initial data",
		}
	}

	// 4. No vehicle connected: never command charging under automatic control.
	// An active override was already handled above and returned early, so
	// reaching here means automatic control is in effect.
	if !in.Connected {
		return Decision{
			State:       StateIdle,
			Timers:      resetTimers(),
			DesiredMode: ModeOff,
			Reason:      "no vehicle connected",
		}
	}

	// 5. SoC cap (latched).
	if in.VehicleSoC >= cfg.SoCCap {
		return Decision{
			State:       StateSocReached,
			Timers:      resetTimers(),
			DesiredMode: ModeOff,
			Reason:      "SoC >= cap",
		}
	}
	if st == StateSocReached && in.VehicleSoC >= cfg.SoCResumeBelow {
		return Decision{
			State:       StateSocReached,
			Timers:      resetTimers(),
			DesiredMode: ModeOff,
			Reason:      "SoC latch (>= resumeBelow)",
		}
	}

	// 6. Surplus hysteresis + dwell (the normal path).
	s := Surplus(in)
	enable := effectiveMode(in, cfg)

	charging := st == StateCharging || st == StateStopPending
	if charging {
		return decideFromCharging(now, s, timers, cfg, enable)
	}
	return decideFromIdle(now, s, timers, cfg, enable)
}

// decideFromIdle handles ticks where we are not currently charging.
func decideFromIdle(now time.Time, s float64, timers Timers, cfg config.Control, enable string) Decision {
	if s >= cfg.StartThresholdW {
		if timers.CrossedUpAt.IsZero() {
			return Decision{
				State:       StateSurplusPending,
				Timers:      Timers{CrossedUpAt: now},
				DesiredMode: ModeOff,
				Reason:      "surplus rising, dwell started",
			}
		}
		if now.Sub(timers.CrossedUpAt) >= cfg.StartDwell {
			return Decision{
				State:       StateCharging,
				Timers:      resetTimers(),
				DesiredMode: enable,
				Reason:      "surplus sustained → charging",
			}
		}
		return Decision{
			State:       StateSurplusPending,
			Timers:      Timers{CrossedUpAt: timers.CrossedUpAt},
			DesiredMode: ModeOff,
			Reason:      "surplus dwell pending",
		}
	}
	return Decision{
		State:       StateIdle,
		Timers:      resetTimers(),
		DesiredMode: ModeOff,
		Reason:      "surplus below start",
	}
}

// decideFromCharging handles ticks where we are currently charging (or pending
// a stop). A surplus recovery cancels any pending stop to avoid flapping.
func decideFromCharging(now time.Time, s float64, timers Timers, cfg config.Control, enable string) Decision {
	if s < cfg.StopThresholdW {
		if timers.CrossedDownAt.IsZero() {
			return Decision{
				State:       StateStopPending,
				Timers:      Timers{CrossedDownAt: now},
				DesiredMode: enable,
				Reason:      "surplus dropping, stop dwell started",
			}
		}
		if now.Sub(timers.CrossedDownAt) >= cfg.StopDwell {
			return Decision{
				State:       StateIdle,
				Timers:      resetTimers(),
				DesiredMode: ModeOff,
				Reason:      "surplus low sustained → stop",
			}
		}
		return Decision{
			State:       StateStopPending,
			Timers:      Timers{CrossedDownAt: timers.CrossedDownAt},
			DesiredMode: enable,
			Reason:      "stop dwell pending",
		}
	}
	return Decision{
		State:       StateCharging,
		Timers:      resetTimers(),
		DesiredMode: enable,
		Reason:      "charging",
	}
}
