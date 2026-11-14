package controller

import (
	"math"
	"time"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
)

// InputsFromSnapshot maps a raw evcc Snapshot into the decision Inputs, applying
// the freshness and readiness policy. This is a pure function so the mapping is
// unit-testable without MQTT.
//
//   - Ready becomes true once the metrics required to make a decision (grid, pv,
//     vehicleSoc, connected) have each been received at least once.
//   - Stale (→ fail-safe) is driven by the fast power metrics, the broker
//     connection, and evcc's online (LWT) status. Vehicle SoC is deliberately
//     NOT part of staleness: evcc refreshes it at most ~hourly, so the last
//     known value is used for the SoC cap regardless of age (the evcc limitSoc
//     backstop bounds any overshoot).
func InputsFromSnapshot(now time.Time, s evcc.Snapshot, cfg config.Control, ctrl ControlState) Inputs {
	fresh := func(m evcc.FloatMetric) bool {
		return m.Seen && now.Sub(m.At) <= cfg.StaleTimeout
	}

	ready := s.Grid.Seen && s.PV.Seen && s.VehicleSoC.Seen && s.Connected.Seen

	stale := !s.BrokerConnected ||
		(s.Online.Seen && !s.Online.Value) ||
		!fresh(s.Grid) ||
		!fresh(s.PV)

	thresholdW, targetSoC := ctrl.latchOverrides(cfg)

	return Inputs{
		GridW:                s.Grid.Value,
		PVW:                  s.PV.Value,
		HomeW:                s.Home.Value,
		BatteryW:             s.BatteryPower.Value,
		ChargeW:              s.ChargePower.Value,
		BatterySoC:           int(math.Round(s.BatterySoC.Value)),
		VehicleSoC:           int(math.Round(s.VehicleSoC.Value)),
		VehicleSoCKnown:      s.VehicleSoC.Seen,
		Charging:             s.Charging.Value,
		Enabled:              s.Enabled.Value,
		Connected:            s.Connected.Value,
		Ready:                ready,
		Stale:                stale,
		Override:             ctrl.Override,
		OverrideUntil:        ctrl.OverrideUntil,
		OverrideCapBypass:    ctrl.CapBypass,
		OverrideCapBypassSoC: ctrl.CapBypassSoC,
		ChargePower:          ctrl.ChargePower,
		AutoProfile:          ctrl.AutoProfile,
		StartThresholdW:      thresholdW,
		TargetSoC:            targetSoC,
		// The min-charge-power inputs are slow, retained evcc settings, not live
		// measurements, so (like vehicle SoC) they are not freshness-gated.
		EffectiveMinCurrentA:  s.EffectiveMinCurrent.Value,
		PhasesConfigured:      int(math.Round(s.PhasesConfigured.Value)),
		PhasesConfiguredKnown: s.PhasesConfigured.Seen,
	}
}

// SoCEstimatedAfter is how long the vehicle SoC may sit unchanged while
// charging before the dashboard labels it an estimate. evcc only polls the
// Renault cloud about hourly; between polls the value it publishes does not
// track what is actually going into the battery.
const SoCEstimatedAfter = 5 * time.Minute

// VehicleSoCEstimated reports whether the vehicle SoC shown to the operator
// should be marked as an estimate: the car is charging and the SoC value has not
// changed for longer than SoCEstimatedAfter. It is display-only — the SoC is
// deliberately never stale-gated in Decide (the evcc limitSoc backstop bounds
// any overshoot). A metric without a ChangedAt (never received) is not flagged.
func VehicleSoCEstimated(now time.Time, charging bool, soc evcc.FloatMetric) bool {
	if !charging || !soc.Seen || soc.ChangedAt.IsZero() {
		return false
	}
	return now.Sub(soc.ChangedAt) > SoCEstimatedAfter
}
