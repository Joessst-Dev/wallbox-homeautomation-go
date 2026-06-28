package web

import (
	"fmt"
	"math"
	"time"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/controller"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

// StatusVM is the flat status view model shared by the JSON API and the HTML
// templates. It is computed once from a controller.StatusView so that templates
// contain no logic: every value is already rounded, formatted, or reduced to a
// boolean. Numeric *_W fields are whole watts; *_KW fields are pre-formatted
// strings with one decimal for readable display.
type StatusVM struct {
	State       string `json:"state"`
	Reason      string `json:"reason"`
	DesiredMode string `json:"desiredMode"`
	Override    string `json:"override"`

	// OverrideUntil is the RFC3339 auto-revert time, empty when no expiry.
	OverrideUntil string `json:"overrideUntil,omitempty"`
	// OverrideActive is true when the override is anything other than auto.
	OverrideActive bool `json:"overrideActive"`

	SurplusW  int    `json:"surplusW"`
	SurplusKW string `json:"surplusKW"`

	GridW    int `json:"gridW"`
	PVW      int `json:"pvW"`
	HomeW    int `json:"homeW"`
	BatteryW int `json:"batteryW"`
	ChargeW  int `json:"chargeW"`

	GridKW    string `json:"gridKW"`
	PVKW      string `json:"pvKW"`
	HomeKW    string `json:"homeKW"`
	BatteryKW string `json:"batteryKW"`
	ChargeKW  string `json:"chargeKW"`

	BatterySoC int `json:"batterySoC"`
	VehicleSoC int `json:"vehicleSoC"`

	Charging  bool `json:"charging"`
	Connected bool `json:"connected"`
	Ready     bool `json:"ready"`
	Stale     bool `json:"stale"`

	Online          bool `json:"online"`
	BrokerConnected bool `json:"brokerConnected"`

	UpdatedAt  string `json:"updatedAt,omitempty"`
	UpdatedAgo string `json:"updatedAgo,omitempty"`
	HasUpdated bool   `json:"hasUpdated"`
}

// newStatusVM flattens a controller.StatusView into the template/JSON view
// model. now is injected so the "ago" formatting is testable and deterministic.
func newStatusVM(now time.Time, v controller.StatusView) StatusVM {
	in := v.Inputs
	snap := v.Snapshot

	vm := StatusVM{
		State:       string(v.State),
		Reason:      v.Reason,
		DesiredMode: v.DesiredMode,
		Override:    string(v.Override),

		OverrideActive: v.Override != controller.OverrideAuto,

		SurplusW:  watts(v.Surplus),
		SurplusKW: kw(v.Surplus),

		GridW:    watts(in.GridW),
		PVW:      watts(in.PVW),
		HomeW:    watts(in.HomeW),
		BatteryW: watts(in.BatteryW),
		ChargeW:  watts(in.ChargeW),

		GridKW:    kw(in.GridW),
		PVKW:      kw(in.PVW),
		HomeKW:    kw(in.HomeW),
		BatteryKW: kw(in.BatteryW),
		ChargeKW:  kw(in.ChargeW),

		BatterySoC: in.BatterySoC,
		VehicleSoC: in.VehicleSoC,

		Charging:  in.Charging,
		Connected: in.Connected,
		Ready:     in.Ready,
		Stale:     in.Stale,

		Online:          snap.Online.Value,
		BrokerConnected: snap.BrokerConnected,
	}

	if !v.OverrideUntil.IsZero() {
		vm.OverrideUntil = v.OverrideUntil.UTC().Format(time.RFC3339)
	}
	if !v.UpdatedAt.IsZero() {
		vm.HasUpdated = true
		vm.UpdatedAt = v.UpdatedAt.UTC().Format(time.RFC3339)
		vm.UpdatedAgo = humanizeAgo(now.Sub(v.UpdatedAt))
	}

	return vm
}

// SessionVM is the flattened, display-ready form of a charge session.
type SessionVM struct {
	ID              int64  `json:"id"`
	StartedAt       string `json:"startedAt"`
	EndedAt         string `json:"endedAt,omitempty"`
	Open            bool   `json:"open"`
	StartReason     string `json:"startReason"`
	StopReason      string `json:"stopReason,omitempty"`
	StartVehicleSoC *int   `json:"startVehicleSoC,omitempty"`
	EndVehicleSoC   *int   `json:"endVehicleSoC,omitempty"`
	EnergyWh        int    `json:"energyWh"`
	EnergyKWh       string `json:"energyKWh"`
	AvgChargeW      int    `json:"avgChargeW"`
	PeakChargeW     int    `json:"peakChargeW"`
}

func newSessionVM(s store.Session) SessionVM {
	vm := SessionVM{
		ID:              s.ID,
		StartedAt:       s.StartedAt.UTC().Format(time.RFC3339),
		Open:            s.EndedAt == nil,
		StartReason:     s.StartReason,
		StopReason:      s.StopReason,
		StartVehicleSoC: s.StartVehicleSoC,
		EndVehicleSoC:   s.EndVehicleSoC,
		EnergyWh:        int(math.Round(s.EnergyWh)),
		EnergyKWh:       fmt.Sprintf("%.2f", s.EnergyWh/1000),
		AvgChargeW:      watts(s.AvgChargeW),
		PeakChargeW:     watts(s.PeakChargeW),
	}
	if s.EndedAt != nil {
		vm.EndedAt = s.EndedAt.UTC().Format(time.RFC3339)
	}
	return vm
}

func newSessionVMs(in []store.Session) []SessionVM {
	out := make([]SessionVM, 0, len(in))
	for _, s := range in {
		out = append(out, newSessionVM(s))
	}
	return out
}

// dashboardVM is the top-level binding passed to the dashboard/layout templates.
type dashboardVM struct {
	Title    string
	Status   StatusVM
	Sessions []SessionVM
}

// watts rounds a power value to whole watts.
func watts(v float64) int {
	return int(math.Round(v))
}

// kw formats a power value as kilowatts with one decimal place.
func kw(v float64) string {
	return fmt.Sprintf("%.1f", v/1000)
}

// humanizeAgo renders a duration as a short relative label.
func humanizeAgo(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}
