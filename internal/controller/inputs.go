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
		Connected:            s.Connected.Value,
		Ready:                ready,
		Stale:                stale,
		Override:             ctrl.Override,
		OverrideUntil:        ctrl.OverrideUntil,
		OverrideCapBypass:    ctrl.CapBypass,
		OverrideCapBypassSoC: ctrl.CapBypassSoC,
		ChargePower:          ctrl.ChargePower,
	}
}
