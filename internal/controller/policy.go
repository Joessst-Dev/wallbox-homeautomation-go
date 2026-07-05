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

// effectiveProfile returns the runtime auto-charge profile, falling back to
// the configured default when unset.
func effectiveProfile(in Inputs, cfg config.Control) string {
	if in.AutoProfile != "" {
		return in.AutoProfile
	}
	return cfg.AutoProfile
}

// effectiveStartThreshold returns the runtime start-threshold override for the
// active profile, falling back to cfg.StartThresholdW when unset.
func effectiveStartThreshold(in Inputs, cfg config.Control) float64 {
	if in.StartThresholdW != 0 {
		return in.StartThresholdW
	}
	return cfg.StartThresholdW
}

// effectiveTargetSoC returns the runtime target-SoC override for the active
// profile, falling back to cfg.SoCCap when unset.
func effectiveTargetSoC(in Inputs, cfg config.Control) int {
	if in.TargetSoC != 0 {
		return in.TargetSoC
	}
	return cfg.SoCCap
}

// LimitSoCTarget is the SoC limit wha asks evcc to enforce as its dead-man
// backstop. Precedence: (1) an explicit "charge past the cap" ForceOn+CapBypass
// override always wins, lifting the target to the operator-chosen value (or
// cfg.SoCMax when unspecified), clamped to [SoCCap+1, SoCMax]; (2) an active
// latch profile (power_latch/surplus_latch) sets the target to its effective
// target SoC, unclamped; (3) otherwise the configured cap.
func LimitSoCTarget(now time.Time, in Inputs, cfg config.Control) int {
	if in.Override == OverrideForceOn && in.OverrideCapBypass && overrideActive(now, in) {
		if in.OverrideCapBypassSoC > 0 {
			target := in.OverrideCapBypassSoC
			if target < cfg.SoCCap+1 {
				target = cfg.SoCCap + 1
			}
			if target > cfg.SoCMax {
				target = cfg.SoCMax
			}
			return target
		}
		return cfg.SoCMax
	}
	if effectiveProfile(in, cfg) != config.AutoProfileSurplus {
		return effectiveTargetSoC(in, cfg)
	}
	return cfg.SoCCap
}

// resetTimers returns Timers with both dwell markers cleared.
func resetTimers() Timers {
	return Timers{}
}

// newDecision builds a Decision, deriving the human Reason from code via
// reasonText so ReasonCode is the single source of truth: a branch supplies
// only the code and the audit-log prose follows, so the two cannot drift.
func newDecision(state State, timers Timers, mode string, code ReasonCode) Decision {
	return Decision{
		State:       state,
		Timers:      timers,
		DesiredMode: mode,
		Reason:      reasonText[code],
		ReasonCode:  code,
	}
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
		return newDecision(StateFailSafe, resetTimers(), ModeOff, ReasonStaleOff)
	}

	// 2. Manual override (respecting expiry). Evaluated before the readiness
	// gate so a manual ForceOn can kick-start a fresh install: evcc only polls
	// vehicle SoC while charging, so an auto-mode controller that waits for SoC
	// before charging would deadlock on first run. ForceOff is likewise honored
	// here because turning off is always safe.
	if overrideActive(now, in) {
		switch in.Override {
		case OverrideForceOff:
			return newDecision(StateIdle, resetTimers(), ModeOff, ReasonOverrideOff)
		case OverrideForceOn:
			return newDecision(StateCharging, resetTimers(), effectiveMode(in, cfg), ReasonOverrideOn)
		}
	}

	// 3. Not ready: we have not yet seen all required metrics.
	if !in.Ready {
		return newDecision(StateIdle, resetTimers(), ModeOff, ReasonAwaitingData)
	}

	// 4. No vehicle connected: never command charging under automatic control.
	// An active override was already handled above and returned early, so
	// reaching here means automatic control is in effect.
	if !in.Connected {
		return newDecision(StateIdle, resetTimers(), ModeOff, ReasonNoVehicle)
	}

	// 5. SoC cap (latched). target is cfg.SoCCap for the default surplus profile
	// (effectiveTargetSoC falls back to it), so the resume boundary reduces to
	// cfg.SoCResumeBelow exactly as before; a latch profile's target shifts both
	// bounds together, keeping the resume band's width constant.
	target := effectiveTargetSoC(in, cfg)
	resumeBelow := target - (cfg.SoCCap - cfg.SoCResumeBelow)
	if in.VehicleSoC >= target {
		return newDecision(StateSocReached, resetTimers(), ModeOff, ReasonSoCCap)
	}
	if st == StateSocReached && in.VehicleSoC >= resumeBelow {
		return newDecision(StateSocReached, resetTimers(), ModeOff, ReasonSoCLatch)
	}

	// 6. Auto-charge profile: the default surplus hysteresis, or a "charge to
	// completion" latch profile.
	if profile := effectiveProfile(in, cfg); profile != config.AutoProfileSurplus {
		return decideLatch(now, in, st, timers, cfg, profile)
	}

	s := Surplus(in)
	enable := effectiveMode(in, cfg)

	charging := st == StateCharging || st == StateStopPending
	if charging {
		return decideFromCharging(now, s, timers, cfg, enable)
	}
	return decideFromIdle(now, s, timers, cfg, enable)
}

// decideLatch implements the power_latch/surplus_latch profiles: once the
// trigger (PVW for power_latch, Surplus for surplus_latch) crosses the
// effective start threshold and the start dwell elapses, charging begins at
// full power (config.EnableModeNow) and continues even if the trigger later
// drops — StopThreshold/StopDwell are never evaluated. A stray StateStopPending
// (e.g. left over from a mid-flight switch away from the surplus profile) is
// treated as already-charging so it re-latches instead of stopping.
func decideLatch(now time.Time, in Inputs, st State, timers Timers, cfg config.Control, profile string) Decision {
	if st == StateCharging || st == StateStopPending {
		return newDecision(StateCharging, resetTimers(), config.EnableModeNow, ReasonLatchCharging)
	}

	trigger := Surplus(in)
	if profile == config.AutoProfilePowerLatch {
		trigger = in.PVW
	}
	threshold := effectiveStartThreshold(in, cfg)

	if trigger >= threshold {
		if timers.CrossedUpAt.IsZero() {
			return newDecision(StateSurplusPending, Timers{CrossedUpAt: now}, ModeOff, ReasonLatchStart)
		}
		if now.Sub(timers.CrossedUpAt) >= cfg.StartDwell {
			return newDecision(StateCharging, resetTimers(), config.EnableModeNow, ReasonLatchCharging)
		}
		return newDecision(StateSurplusPending, Timers{CrossedUpAt: timers.CrossedUpAt}, ModeOff, ReasonSurplusDwellPending)
	}
	return newDecision(StateIdle, resetTimers(), ModeOff, ReasonLatchBelowStart)
}

// decideFromIdle handles ticks where we are not currently charging.
func decideFromIdle(now time.Time, s float64, timers Timers, cfg config.Control, enable string) Decision {
	if s >= cfg.StartThresholdW {
		if timers.CrossedUpAt.IsZero() {
			return newDecision(StateSurplusPending, Timers{CrossedUpAt: now}, ModeOff, ReasonSurplusDwellStart)
		}
		if now.Sub(timers.CrossedUpAt) >= cfg.StartDwell {
			return newDecision(StateCharging, resetTimers(), enable, ReasonSurplusCharging)
		}
		return newDecision(StateSurplusPending, Timers{CrossedUpAt: timers.CrossedUpAt}, ModeOff, ReasonSurplusDwellPending)
	}
	return newDecision(StateIdle, resetTimers(), ModeOff, ReasonSurplusBelowStart)
}

// decideFromCharging handles ticks where we are currently charging (or pending
// a stop). A surplus recovery cancels any pending stop to avoid flapping.
func decideFromCharging(now time.Time, s float64, timers Timers, cfg config.Control, enable string) Decision {
	if s < cfg.StopThresholdW {
		if timers.CrossedDownAt.IsZero() {
			return newDecision(StateStopPending, Timers{CrossedDownAt: now}, enable, ReasonStopDwellStart)
		}
		if now.Sub(timers.CrossedDownAt) >= cfg.StopDwell {
			return newDecision(StateIdle, resetTimers(), ModeOff, ReasonStopLow)
		}
		return newDecision(StateStopPending, Timers{CrossedDownAt: timers.CrossedDownAt}, enable, ReasonStopDwellPending)
	}
	return newDecision(StateCharging, resetTimers(), enable, ReasonCharging)
}
