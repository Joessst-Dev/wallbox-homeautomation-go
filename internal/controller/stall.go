package controller

import (
	"time"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
)

// StallChargeThresholdW is the charge power below which the loadpoint counts as
// delivering nothing for stall detection.
const StallChargeThresholdW = 100.0

// StallState is the stall-detection bookkeeping the loop carries between ticks
// (like Timers for Decide). The zero value means "nothing observed".
//
// A stall is the loadpoint looking like it should charge but not doing it: wha
// wants charging, evcc has enabled the charger, a vehicle is plugged in and
// below the active SoC target, yet evcc reports not charging and (almost) no
// power. Typical causes are a car-side fault or a charger paused outside evcc.
// Re-sending the same evcc mode does nothing (evcc ignores a mode it is already
// in), so the loop spends one short "off" pulse per connected session to make
// evcc re-run its enable sequence.
type StallState struct {
	// Since is when the stall condition was first observed in its current
	// uninterrupted run; zero when the condition is not holding.
	Since time.Time
	// Stalled is latched once the condition has held for cfg.StallTimeout. It
	// clears only on positive evidence: power flowing, the vehicle reaching the
	// target, the desired mode going off, or the vehicle being unplugged. A
	// transient "evcc not enabled" (e.g. right after the recovery pulse) does
	// not clear it, so a failed recovery stays flagged.
	Stalled bool
	// StalledSince is when the run that latched Stalled began (for display).
	StalledSince time.Time
	// RecoveryUsed is true once the single recovery attempt for the current
	// connected session has been spent. Reset only once the vehicle has been
	// seen unplugged on two consecutive ticks (see Unplugged).
	RecoveryUsed bool
	// Unplugged is true when the previous tick saw no vehicle connected. A
	// single connected=false tick (an MQTT/evcc blip) must not count as a new
	// connected session, so the recovery budget resets only on the second
	// consecutive unplugged tick.
	Unplugged bool
	// RecoveryUntil is the end of the recovery "off" pulse; zero when no pulse
	// is in force.
	RecoveryUntil time.Time
}

// RecoveryActive reports whether the recovery "off" pulse is in force at now,
// i.e. whether the loop must publish off instead of Decide's desired mode.
func (s StallState) RecoveryActive(now time.Time) bool {
	return !s.RecoveryUntil.IsZero() && now.Before(s.RecoveryUntil)
}

// stallCondition reports whether, at this instant, the loadpoint looks stalled.
func stallCondition(in Inputs, desiredMode string, targetSoC int) bool {
	return desiredMode != ModeOff &&
		in.Connected &&
		in.Enabled &&
		!in.Charging &&
		in.ChargeW < StallChargeThresholdW &&
		in.VehicleSoC < targetSoC
}

// powerFlowing is the positive evidence that clears a latched stall.
func powerFlowing(in Inputs) bool {
	return in.Charging || in.ChargeW >= StallChargeThresholdW
}

// StepStall is the pure stall detector and recovery scheduler. Given the time,
// the tick's Inputs, Decide's desired mode, the active SoC target (the evcc
// limitSoc backstop target), and the previous StallState, it returns the next
// StallState. The caller publishes ModeOff instead of desiredMode while
// RecoveryActive(now) holds on the result.
//
// The recovery can only ever turn charging off for cfg.StallRecoveryOff and
// then hand control back to Decide; it never raises power. Fail-safes keep
// priority: an unplugged vehicle cancels any pulse and flag at once (and, if
// still unplugged on the next tick, resets the one-attempt budget), and a stale
// snapshot or an off desired mode cancels any pulse.
func StepStall(now time.Time, in Inputs, desiredMode string, targetSoC int, prev StallState, cfg config.Control) StallState {
	if !in.Connected {
		if prev.Unplugged {
			// Unplugged for two consecutive ticks: the connected session is over,
			// so the recovery budget resets.
			return StallState{Unplugged: true}
		}
		// First unplugged tick: nothing can be stalled without a vehicle, but keep
		// the spent budget in case this is only a blip.
		return StallState{RecoveryUsed: prev.RecoveryUsed, Unplugged: true}
	}
	// Decide wants off (fail-safe, SoC cap, override, no surplus) or the data is
	// stale: there is nothing to be stalled on. Keep the spent budget — it is
	// still the same connected session.
	if desiredMode == ModeOff || in.Stale {
		return StallState{RecoveryUsed: prev.RecoveryUsed}
	}

	// Off pulse in progress: hold everything; the loop keeps publishing off.
	if prev.RecoveryActive(now) {
		return prev
	}

	next := prev
	next.Unplugged = false
	if !next.RecoveryUntil.IsZero() {
		// The pulse just ended: control returns to Decide's mode this tick. Restart
		// the timer so evcc gets a full StallTimeout to bring the charger back; the
		// latch itself stays until positive evidence clears it.
		next.RecoveryUntil = time.Time{}
		next.Since = time.Time{}
	}

	if stallCondition(in, desiredMode, targetSoC) {
		if next.Since.IsZero() {
			next.Since = now
		}
		if !next.Stalled && now.Sub(next.Since) >= cfg.StallTimeout {
			next.Stalled = true
			next.StalledSince = next.Since
		}
	} else {
		next.Since = time.Time{}
		if powerFlowing(in) || in.VehicleSoC >= targetSoC {
			next.Stalled = false
			next.StalledSince = time.Time{}
		}
	}

	// Spend the single recovery attempt for this connected session.
	if next.Stalled && !next.RecoveryUsed {
		next.RecoveryUsed = true
		next.RecoveryUntil = now.Add(cfg.StallRecoveryOff)
	}
	return next
}
