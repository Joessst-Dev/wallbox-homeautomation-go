// Package controller holds the pure decision engine for the PV-surplus EV
// charging controller. The functions in this package are deliberately free of
// side effects: given a snapshot of the world (Inputs), the current State, the
// pending Timers, and the tuning Config, they compute the next Decision. This
// makes the safety-critical logic exhaustively testable without a broker, a
// clock, or a network.
package controller

import "time"

// State is the controller's finite-state-machine position.
type State string

const (
	// StateIdle means we are not charging and surplus is insufficient.
	StateIdle State = "idle"
	// StateSurplusPending means surplus reached the start threshold and we are
	// waiting out the start dwell before enabling charging.
	StateSurplusPending State = "surplus_pending"
	// StateCharging means charging is enabled.
	StateCharging State = "charging"
	// StateStopPending means surplus dropped below the stop threshold and we are
	// waiting out the stop dwell before disabling charging (keeps charging
	// meanwhile to avoid flapping).
	StateStopPending State = "stop_pending"
	// StateSocReached means the vehicle reached the SoC cap; charging is latched
	// off until SoC falls below the resume boundary.
	StateSocReached State = "soc_reached"
	// StateFailSafe means required data is stale or the broker is disconnected;
	// charging is forced off.
	StateFailSafe State = "failsafe"
)

// Override is the manual operator override.
type Override string

const (
	// OverrideAuto defers to the automatic surplus logic.
	OverrideAuto Override = "auto"
	// OverrideForceOn forces charging on regardless of surplus.
	OverrideForceOn Override = "on"
	// OverrideForceOff forces charging off regardless of surplus.
	OverrideForceOff Override = "off"
)

// ModeOff is the desired evcc mode meaning "do not charge".
const ModeOff = "off"

// ControlState is the operator-settable runtime state layered on top of the
// static config. It is the bundle the loop passes through the pure boundary
// (InputsFromSnapshot) each tick.
type ControlState struct {
	// Override is the manual override (auto|on|off) and its optional expiry.
	Override      Override
	OverrideUntil time.Time
	// CapBypass, meaningful only for a ForceOn override, lifts the evcc limitSoc
	// backstop from SoCCap to the operator-chosen target so the user can deliberately
	// charge past the cap. It expires together with the override.
	CapBypass bool
	// CapBypassSoC is the target SoC percentage for cap-bypass charging (1–100).
	// When 0, LimitSoCTarget falls back to cfg.SoCMax. Clamped to [SoCCap+1, SoCMax]
	// by LimitSoCTarget.
	CapBypassSoC int
	// ChargePower is the effective evcc charge mode used whenever charging is
	// enabled (config.EnableModePV or config.EnableModeNow). It is a persistent,
	// global operator setting, not tied to the override.
	ChargePower string
}

// Inputs is a snapshot of all metrics and flags the decision engine consumes.
type Inputs struct {
	GridW      float64 // + = import from grid
	PVW        float64
	HomeW      float64
	BatteryW   float64 // + = battery discharging
	ChargeW    float64 // current charge power of the loadpoint
	BatterySoC int
	VehicleSoC int
	// VehicleSoCKnown is true once a vehicle SoC has been received at least once,
	// so persistence can distinguish a genuine 0% from "never seen". Decide must
	// not use this; the SoC cap is already gated by the Ready/Seen path.
	VehicleSoCKnown bool
	Charging        bool
	Connected       bool // true = a vehicle is plugged into the loadpoint
	Ready           bool // all required metrics seen fresh at least once
	Stale           bool // a required metric is stale OR the broker is disconnected
	Override        Override
	OverrideUntil   time.Time // zero value = no expiry
	// OverrideCapBypass lifts the SoC cap while a ForceOn override is active.
	OverrideCapBypass bool
	// OverrideCapBypassSoC is the operator-chosen target SoC for cap-bypass charging.
	// When 0, LimitSoCTarget falls back to cfg.SoCMax.
	OverrideCapBypassSoC int
	// ChargePower is the effective evcc charge mode (pv|now) when charging is on.
	ChargePower string
}

// Timers carries the dwell bookkeeping between decisions.
type Timers struct {
	CrossedUpAt   time.Time // when surplus first reached startThreshold (zero = not pending up)
	CrossedDownAt time.Time // when surplus first dropped below stopThreshold (zero = not pending down)
}

// Decision is the engine's output for a single tick.
type Decision struct {
	State       State
	Timers      Timers
	DesiredMode string // config.EnableModePV, config.EnableModeNow, or ModeOff
	Reason      string // short human-readable explanation for the audit log
}
