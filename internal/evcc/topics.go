package evcc

import "fmt"

// Topics holds the resolved MQTT topic strings for one evcc loadpoint, derived
// from the configured prefix (default "evcc") and loadpoint id.
//
// All read topics are published retained by evcc as UTF-8 string payloads.
// Command topics use the SAME casing as their read counterparts — in
// particular the SoC-limit setter is "limitSoc/set" (camelCase); a lowercase
// "limitsoc/set" is silently ignored by evcc.
type Topics struct {
	// site (read)
	GridPower    string
	PVPower      string
	HomePower    string
	BatterySoC   string
	BatteryPower string
	Status       string // online/offline via MQTT LWT

	// loadpoint (read)
	ChargePower string
	Charging    string
	Connected   string
	Enabled     string
	VehicleSoC  string
	Mode        string
	LimitSoC    string

	// loadpoint (write)
	ModeSet     string
	LimitSoCSet string
}

// NewTopics builds the topic set for the given prefix and loadpoint id.
func NewTopics(prefix, loadpointID string) Topics {
	lp := fmt.Sprintf("%s/loadpoints/%s", prefix, loadpointID)
	return Topics{
		GridPower:    prefix + "/site/grid/power",
		PVPower:      prefix + "/site/pvPower",
		HomePower:    prefix + "/site/homePower",
		BatterySoC:   prefix + "/site/battery/soc",
		BatteryPower: prefix + "/site/battery/power",
		Status:       prefix + "/status",

		ChargePower: lp + "/chargePower",
		Charging:    lp + "/charging",
		Connected:   lp + "/connected",
		Enabled:     lp + "/enabled",
		VehicleSoC:  lp + "/vehicleSoc",
		Mode:        lp + "/mode",
		LimitSoC:    lp + "/limitSoc",

		ModeSet:     lp + "/mode/set",
		LimitSoCSet: lp + "/limitSoc/set",
	}
}

// readSubscriptions returns the topics to subscribe to (QoS level chosen by the
// client), mapped so the dispatcher can route incoming messages.
func (t Topics) readSubscriptions() []string {
	return []string{
		t.GridPower, t.PVPower, t.HomePower, t.BatterySoC, t.BatteryPower, t.Status,
		t.ChargePower, t.Charging, t.Connected, t.Enabled, t.VehicleSoC, t.Mode, t.LimitSoC,
	}
}
