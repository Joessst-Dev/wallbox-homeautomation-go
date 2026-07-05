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

// ReasonCode is a stable, machine-readable identifier for the branch of Decide
// that produced a Decision, one per priority rule. Unlike Decision.Reason
// (free-form English prose meant for logs), ReasonCode is a fixed enum the web
// layer can map through its i18n catalog without matching on English text.
type ReasonCode string

const (
	// ReasonStaleOff: stale data or disconnected broker forces off.
	ReasonStaleOff ReasonCode = "stale_off"
	// ReasonOverrideOff: manual override forces off.
	ReasonOverrideOff ReasonCode = "override_off"
	// ReasonOverrideOn: manual override forces on.
	ReasonOverrideOn ReasonCode = "override_on"
	// ReasonAwaitingData: not yet ready (required metrics not all seen fresh).
	ReasonAwaitingData ReasonCode = "awaiting_data"
	// ReasonNoVehicle: no vehicle connected under automatic control.
	ReasonNoVehicle ReasonCode = "no_vehicle"
	// ReasonSoCCap: vehicle SoC reached the configured cap.
	ReasonSoCCap ReasonCode = "soc_cap"
	// ReasonSoCLatch: SoC cap latch holding off until resumeBelow.
	ReasonSoCLatch ReasonCode = "soc_latch"
	// ReasonSurplusDwellStart: surplus just crossed the start threshold; dwell armed.
	ReasonSurplusDwellStart ReasonCode = "surplus_dwell_start"
	// ReasonSurplusCharging: start dwell elapsed; charging begins.
	ReasonSurplusCharging ReasonCode = "surplus_charging"
	// ReasonSurplusDwellPending: start dwell still counting down.
	ReasonSurplusDwellPending ReasonCode = "surplus_dwell_pending"
	// ReasonSurplusBelowStart: surplus below the start threshold; staying idle.
	ReasonSurplusBelowStart ReasonCode = "surplus_below_start"
	// ReasonStopDwellStart: surplus just dropped below the stop threshold; dwell armed.
	ReasonStopDwellStart ReasonCode = "stop_dwell_start"
	// ReasonStopLow: stop dwell elapsed; charging stops.
	ReasonStopLow ReasonCode = "stop_low"
	// ReasonStopDwellPending: stop dwell still counting down (still charging).
	ReasonStopDwellPending ReasonCode = "stop_dwell_pending"
	// ReasonCharging: surplus remains within the hysteresis band; charging continues.
	ReasonCharging ReasonCode = "charging"
)

// reasonText maps each ReasonCode to its human-readable English audit-log prose.
// Holding the prose here — instead of duplicating it at every Decide branch —
// makes ReasonCode the single source of truth: a branch sets only the code (via
// newDecision) and the Reason string follows, so the two cannot drift as
// branches are added or edited. A completeness test asserts every declared code
// has a non-empty entry.
var reasonText = map[ReasonCode]string{
	ReasonStaleOff:            "stale data / disconnected → off",
	ReasonOverrideOff:         "manual override: off",
	ReasonOverrideOn:          "manual override: on",
	ReasonAwaitingData:        "waiting for initial data",
	ReasonNoVehicle:           "no vehicle connected",
	ReasonSoCCap:              "SoC >= cap",
	ReasonSoCLatch:            "SoC latch (>= resumeBelow)",
	ReasonSurplusDwellStart:   "surplus rising, dwell started",
	ReasonSurplusCharging:     "surplus sustained → charging",
	ReasonSurplusDwellPending: "surplus dwell pending",
	ReasonSurplusBelowStart:   "surplus below start",
	ReasonStopDwellStart:      "surplus dropping, stop dwell started",
	ReasonStopLow:             "surplus low sustained → stop",
	ReasonStopDwellPending:    "stop dwell pending",
	ReasonCharging:            "charging",
}

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
	DesiredMode string     // config.EnableModePV, config.EnableModeNow, or ModeOff
	Reason      string     // short human-readable explanation for the audit log
	ReasonCode  ReasonCode // stable identifier for the branch that decided this
}
