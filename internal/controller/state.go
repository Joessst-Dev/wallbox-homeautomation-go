// Package controller holds the pure decision engine for the PV-surplus EV
// charging controller. The functions in this package are deliberately free of
// side effects: given a snapshot of the world (Inputs), the current State, the
// pending Timers, and the tuning Config, they compute the next Decision. This
// makes the safety-critical logic exhaustively testable without a broker, a
// clock, or a network.
package controller

import (
	"time"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
)

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
	// ReasonLatchStart: a latch profile's trigger just crossed the start
	// threshold; dwell armed.
	ReasonLatchStart ReasonCode = "latch_start"
	// ReasonLatchCharging: a latch profile is charging (either the start dwell
	// elapsed, or it is already latched and holding through a trigger dip).
	ReasonLatchCharging ReasonCode = "latch_charging"
	// ReasonLatchBelowStart: a latch profile's trigger is below the start
	// threshold; staying idle.
	ReasonLatchBelowStart ReasonCode = "latch_below_start"
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
	ReasonLatchStart:          "latch trigger rising, dwell started",
	ReasonLatchCharging:       "latch charging to target SoC",
	ReasonLatchBelowStart:     "latch trigger below start",
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
	// AutoProfile is the runtime auto-charge profile (config.AutoProfileSurplus,
	// AutoProfilePowerLatch, or AutoProfileSurplusLatch). Empty falls back to
	// cfg.AutoProfile.
	AutoProfile string
	// StartThresholdW holds each latch profile's own start-trigger override,
	// keyed by profile name. A missing/zero entry falls back to
	// cfg.StartThresholdW. Only latch profiles (AutoProfilePowerLatch,
	// AutoProfileSurplusLatch) ever get an entry.
	StartThresholdW map[string]float64
	// TargetSoC holds each latch profile's own stop-target override, keyed by
	// profile name. A missing/zero entry falls back to cfg.SoCCap. Only latch
	// profiles ever get an entry.
	TargetSoC map[string]int
}

// latchOverrides resolves the effective auto-charge profile (cs.AutoProfile,
// falling back to cfg.AutoProfile) and returns that profile's own
// start-threshold/target-SoC overrides. A nil map or missing entry reads as
// the zero value, so the existing effective* config-fallback in policy.go
// still applies unchanged.
func (cs ControlState) latchOverrides(cfg config.Control) (thresholdW float64, targetSoC int) {
	profile := cs.AutoProfile
	if profile == "" {
		profile = cfg.AutoProfile
	}
	return cs.StartThresholdW[profile], cs.TargetSoC[profile]
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
	// AutoProfile is the runtime auto-charge profile; empty falls back to
	// cfg.AutoProfile. See ControlState.AutoProfile.
	AutoProfile string
	// StartThresholdW overrides cfg.StartThresholdW for the active profile;
	// zero falls back to cfg.StartThresholdW. See ControlState.StartThresholdW.
	StartThresholdW float64
	// TargetSoC overrides cfg.SoCCap as the stop target for the active profile;
	// zero falls back to cfg.SoCCap. See ControlState.TargetSoC.
	TargetSoC int
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
