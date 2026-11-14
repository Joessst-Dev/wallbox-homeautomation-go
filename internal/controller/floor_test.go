package controller_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/controller"
)

// floorConfig is baseConfig with the field-reported loadpoint: a three-phase
// 8 A minimum (5520 W) plus the default 200 W margin → a 5720 W start floor,
// well above the 1400 W configured start threshold.
func floorConfig() config.Control {
	cfg := baseConfig()
	cfg.MinCurrentA = 8
	cfg.Phases = 3
	cfg.StartMarginW = 200
	return cfg
}

// withSurplus returns baseInputs exporting w watts (no battery, no charge).
func withSurplus(w float64) controller.Inputs {
	in := baseInputs()
	in.GridW = -w
	return in
}

var _ = Describe("MinChargePowerW", func() {
	DescribeTable("derives the minimum holdable charge power from evcc, else the config fallback",
		func(mutate func(in *controller.Inputs), expected float64) {
			in := baseInputs()
			mutate(&in)
			Expect(controller.MinChargePowerW(in, floorConfig())).To(BeNumerically("~", expected, 1e-9))
		},
		Entry("nothing published → cfg 8 A × 230 V × 3 phases",
			func(*controller.Inputs) {}, 5520.0),
		Entry("evcc effectiveMinCurrent overrides the current fallback",
			func(in *controller.Inputs) { in.EffectiveMinCurrentA = 6 }, 4140.0),
		Entry("a fractional effectiveMinCurrent is used as-is",
			func(in *controller.Inputs) { in.EffectiveMinCurrentA = 6.5 }, 4485.0),
		Entry("a non-positive effectiveMinCurrent falls back to cfg",
			func(in *controller.Inputs) { in.EffectiveMinCurrentA = -1 }, 5520.0),
		Entry("evcc phasesConfigured=1 overrides the phase fallback",
			func(in *controller.Inputs) {
				in.PhasesConfigured, in.PhasesConfiguredKnown = 1, true
			}, 1840.0),
		Entry("phasesConfigured=0 (auto 1p/3p switching) starts single-phase",
			func(in *controller.Inputs) {
				in.PhasesConfigured, in.PhasesConfiguredKnown = 0, true
			}, 1840.0),
		Entry("an unpublished phasesConfigured of 0 is NOT auto — falls back to cfg",
			func(in *controller.Inputs) { in.PhasesConfigured = 0 }, 5520.0),
		Entry("a nonsensical negative phasesConfigured falls back to cfg",
			func(in *controller.Inputs) {
				in.PhasesConfigured, in.PhasesConfiguredKnown = -1, true
			}, 5520.0),
		Entry("both from evcc: 16 A three-phase",
			func(in *controller.Inputs) {
				in.EffectiveMinCurrentA = 16
				in.PhasesConfigured, in.PhasesConfiguredKnown = 3, true
			}, 11040.0),
	)
})

var _ = Describe("Decide start threshold floored at the minimum charge power", func() {
	var (
		cfg config.Control
		t0  time.Time
	)

	BeforeEach(func() {
		cfg = floorConfig()
		t0 = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	})

	Context("surplus profile", func() {
		DescribeTable("only arms the start dwell once surplus reaches max(startThresholdW, minPower+margin)",
			func(mutate func(in *controller.Inputs), surplus float64, wantState controller.State, wantCode controller.ReasonCode) {
				in := withSurplus(surplus)
				mutate(&in)
				d := controller.Decide(t0, in, controller.StateIdle, controller.Timers{}, cfg)
				Expect(d.State).To(Equal(wantState))
				Expect(d.ReasonCode).To(Equal(wantCode))
				Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			},
			// The field bug: 1.4 kW of surplus used to start a 5.5 kW-minimum charger.
			Entry("1.5 kW is above startThresholdW but below the 5720 W floor → idle",
				func(*controller.Inputs) {}, 1500.0, controller.StateIdle, controller.ReasonSurplusBelowStart),
			Entry("5.5 kW is still below the floor (margin not covered) → idle",
				func(*controller.Inputs) {}, 5600.0, controller.StateIdle, controller.ReasonSurplusBelowStart),
			Entry("exactly at the floor → dwell armed",
				func(*controller.Inputs) {}, 5720.0, controller.StateSurplusPending, controller.ReasonSurplusDwellStart),
			Entry("evcc reports single-phase 6 A (1580 W floor): 1.5 kW is still below → idle",
				func(in *controller.Inputs) {
					in.EffectiveMinCurrentA = 6
					in.PhasesConfigured, in.PhasesConfiguredKnown = 1, true
				}, 1500.0, controller.StateIdle, controller.ReasonSurplusBelowStart),
			Entry("evcc reports single-phase 6 A: 1.6 kW clears the 1580 W floor → dwell armed",
				func(in *controller.Inputs) {
					in.EffectiveMinCurrentA = 6
					in.PhasesConfigured, in.PhasesConfiguredKnown = 1, true
				}, 1600.0, controller.StateSurplusPending, controller.ReasonSurplusDwellStart),
			Entry("auto phase switching floors at single-phase power (2040 W): 2.1 kW → dwell armed",
				func(in *controller.Inputs) {
					in.PhasesConfigured, in.PhasesConfiguredKnown = 0, true
				}, 2100.0, controller.StateSurplusPending, controller.ReasonSurplusDwellStart),
		)

		It("keeps a configured threshold above the floor", func() {
			cfg.StartThresholdW = 7000
			d := controller.Decide(t0, withSurplus(6000), controller.StateIdle, controller.Timers{}, cfg)
			Expect(d.State).To(Equal(controller.StateIdle))

			d = controller.Decide(t0, withSurplus(7000), controller.StateIdle, controller.Timers{}, cfg)
			Expect(d.State).To(Equal(controller.StateSurplusPending))
		})

		It("still respects the start dwell above the floor", func() {
			armed := controller.Timers{CrossedUpAt: t0}
			d := controller.Decide(t0.Add(cfg.StartDwell-time.Second), withSurplus(6000), controller.StateSurplusPending, armed, cfg)
			Expect(d.State).To(Equal(controller.StateSurplusPending))

			d = controller.Decide(t0.Add(cfg.StartDwell), withSurplus(6000), controller.StateSurplusPending, armed, cfg)
			Expect(d.State).To(Equal(controller.StateCharging))
			Expect(d.DesiredMode).To(Equal(config.EnableModePV))
		})

		It("drops a pending start when surplus falls back below the floor", func() {
			armed := controller.Timers{CrossedUpAt: t0}
			d := controller.Decide(t0.Add(time.Second), withSurplus(3000), controller.StateSurplusPending, armed, cfg)
			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.Timers).To(Equal(controller.Timers{}))
		})

		It("only gates the start: an ongoing charge is judged by the stop threshold, not the floor", func() {
			// Charging at 5.5 kW with 1 kW of import → surplus 4.5 kW: below the start
			// floor, but above the 0 W stop threshold, so charging continues.
			in := baseInputs()
			in.ChargeW = 5500
			in.GridW = 1000
			d := controller.Decide(t0, in, controller.StateCharging, controller.Timers{}, cfg)
			Expect(d.State).To(Equal(controller.StateCharging))
			Expect(d.ReasonCode).To(Equal(controller.ReasonCharging))
		})
	})

	Context("surplus_latch profile (triggered by net surplus)", func() {
		It("floors a runtime start-threshold override at the minimum charge power", func() {
			in := withSurplus(5000)
			in.AutoProfile = config.AutoProfileSurplusLatch
			in.StartThresholdW = 3000 // operator-set, below the 5720 W floor

			d := controller.Decide(t0, in, controller.StateIdle, controller.Timers{}, cfg)
			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.ReasonCode).To(Equal(controller.ReasonLatchBelowStart))

			in = withSurplus(5720)
			in.AutoProfile = config.AutoProfileSurplusLatch
			in.StartThresholdW = 3000
			d = controller.Decide(t0, in, controller.StateIdle, controller.Timers{}, cfg)
			Expect(d.State).To(Equal(controller.StateSurplusPending))
			Expect(d.ReasonCode).To(Equal(controller.ReasonLatchStart))
		})
	})

	Context("power_latch profile (triggered by PV production, charges at full power)", func() {
		It("is not floored", func() {
			in := baseInputs()
			in.AutoProfile = config.AutoProfilePowerLatch
			in.PVW = 1500 // ≥ 1400 W threshold, far below the 5720 W floor

			d := controller.Decide(t0, in, controller.StateIdle, controller.Timers{}, cfg)
			Expect(d.State).To(Equal(controller.StateSurplusPending))
			Expect(d.ReasonCode).To(Equal(controller.ReasonLatchStart))
		})
	})

	Context("fail-safe priority is unchanged by the floor", func() {
		It("Stale still wins first", func() {
			in := withSurplus(20000)
			in.Stale = true
			d := controller.Decide(t0, in, controller.StateIdle, controller.Timers{}, cfg)
			Expect(d.State).To(Equal(controller.StateFailSafe))
			Expect(d.ReasonCode).To(Equal(controller.ReasonStaleOff))
		})

		It("a ForceOn override still charges with no surplus at all", func() {
			in := baseInputs()
			in.Override = controller.OverrideForceOn
			d := controller.Decide(t0, in, controller.StateIdle, controller.Timers{}, cfg)
			Expect(d.State).To(Equal(controller.StateCharging))
			Expect(d.ReasonCode).To(Equal(controller.ReasonOverrideOn))
		})

		It("!Connected still forces off above the floor", func() {
			in := withSurplus(20000)
			in.Connected = false
			d := controller.Decide(t0, in, controller.StateIdle, controller.Timers{}, cfg)
			Expect(d.ReasonCode).To(Equal(controller.ReasonNoVehicle))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
		})

		It("the SoC cap still forces off above the floor", func() {
			in := withSurplus(20000)
			in.VehicleSoC = cfg.SoCCap
			d := controller.Decide(t0, in, controller.StateIdle, controller.Timers{}, cfg)
			Expect(d.ReasonCode).To(Equal(controller.ReasonSoCCap))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
		})
	})
})
