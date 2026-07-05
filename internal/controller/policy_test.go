package controller_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/controller"
)

// baseConfig returns a valid Control tuning for use as a test baseline.
func baseConfig() config.Control {
	return config.Control{
		EnableMode:       config.EnableModePV,
		AutoProfile:      config.AutoProfileSurplus,
		StartThresholdW:  1400,
		StopThresholdW:   0,
		StartDwell:       60 * time.Second,
		StopDwell:        120 * time.Second,
		SoCCap:           80,
		SoCResumeBelow:   78,
		SoCMax:           100,
		DecisionInterval: 15 * time.Second,
		StaleTimeout:     60 * time.Second,
		Republish:        5 * time.Minute,
	}
}

// baseInputs returns a minimal valid (ready, fresh, vehicle connected) Inputs
// snapshot.
func baseInputs() controller.Inputs {
	return controller.Inputs{Ready: true, Connected: true}
}

var _ = Describe("Surplus", func() {
	DescribeTable("computes PV power available to the car",
		func(in controller.Inputs, expected float64) {
			Expect(controller.Surplus(in)).To(Equal(expected))
		},
		Entry("export plus existing charge",
			controller.Inputs{ChargeW: 2000, GridW: -1000}, 3000.0),
		Entry("import reduces surplus",
			controller.Inputs{ChargeW: 0, GridW: 500}, -500.0),
		Entry("battery discharge is excluded from surplus",
			controller.Inputs{ChargeW: 0, GridW: -1000, BatteryW: 800}, 200.0),
		Entry("battery discharge can drive surplus negative",
			controller.Inputs{ChargeW: 0, GridW: 0, BatteryW: 1500}, -1500.0),
		Entry("battery charging (negative BatteryW) is ignored",
			controller.Inputs{ChargeW: 0, GridW: -2000, BatteryW: -800}, 2000.0),
		Entry("idle, no flows",
			controller.Inputs{}, 0.0),
	)
})

var _ = Describe("Decide", func() {
	var (
		cfg   config.Control
		clock *controller.FakeClock
		t0    time.Time
	)

	BeforeEach(func() {
		cfg = baseConfig()
		t0 = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
		clock = controller.NewFakeClock(t0)
	})

	Context("fail-safe and readiness (highest priority)", func() {
		It("forces off when data is stale, regardless of surplus", func() {
			in := baseInputs()
			in.Stale = true
			in.ChargeW = 10000 // huge surplus that would otherwise charge
			in.GridW = -10000

			d := controller.Decide(clock.Now(), in, controller.StateCharging, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateFailSafe))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.Timers).To(Equal(controller.Timers{}))
			Expect(d.ReasonCode).To(Equal(controller.ReasonStaleOff))
		})

		It("stays off until ready", func() {
			in := controller.Inputs{Ready: false, GridW: -5000}

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.Timers).To(Equal(controller.Timers{}))
			Expect(d.ReasonCode).To(Equal(controller.ReasonAwaitingData))
		})

		It("prefers fail-safe over not-ready when both hold", func() {
			in := controller.Inputs{Ready: false, Stale: true}

			d := controller.Decide(clock.Now(), in, controller.StateCharging, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateFailSafe))
			Expect(d.ReasonCode).To(Equal(controller.ReasonStaleOff))
		})
	})

	Context("manual override", func() {
		It("ForceOn enables charging despite low surplus", func() {
			in := baseInputs()
			in.Override = controller.OverrideForceOn
			in.GridW = 5000 // heavy import, no surplus

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateCharging))
			Expect(d.DesiredMode).To(Equal(config.EnableModePV))
			Expect(d.Timers).To(Equal(controller.Timers{}))
			Expect(d.ReasonCode).To(Equal(controller.ReasonOverrideOn))
		})

		It("ForceOff disables charging despite high surplus", func() {
			in := baseInputs()
			in.Override = controller.OverrideForceOff
			in.ChargeW = 10000
			in.GridW = -10000

			d := controller.Decide(clock.Now(), in, controller.StateCharging, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.ReasonCode).To(Equal(controller.ReasonOverrideOff))
		})

		It("falls back to automatic logic when the override has expired", func() {
			in := baseInputs()
			in.Override = controller.OverrideForceOn
			in.OverrideUntil = t0.Add(-1 * time.Second) // already expired
			in.GridW = 5000                             // no surplus

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.ReasonCode).To(Equal(controller.ReasonSurplusBelowStart))
		})

		It("honors a not-yet-expired override expiry", func() {
			in := baseInputs()
			in.Override = controller.OverrideForceOn
			in.OverrideUntil = t0.Add(1 * time.Minute)
			in.GridW = 5000

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateCharging))
			Expect(d.ReasonCode).To(Equal(controller.ReasonOverrideOn))
		})

		It("uses automatic logic under OverrideAuto", func() {
			in := baseInputs()
			in.Override = controller.OverrideAuto
			in.GridW = 5000

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.ReasonCode).To(Equal(controller.ReasonSurplusBelowStart))
		})

		It("fail-safe takes precedence over an active override", func() {
			in := baseInputs()
			in.Stale = true
			in.Override = controller.OverrideForceOn

			d := controller.Decide(clock.Now(), in, controller.StateCharging, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateFailSafe))
			Expect(d.ReasonCode).To(Equal(controller.ReasonStaleOff))
		})

		It("ForceOn kick-starts charging even before any data is ready (breaks first-run deadlock)", func() {
			in := controller.Inputs{Ready: false, Override: controller.OverrideForceOn}

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateCharging))
			Expect(d.DesiredMode).To(Equal(config.EnableModePV))
			Expect(d.ReasonCode).To(Equal(controller.ReasonOverrideOn))
		})

		It("ForceOff turns off even before any data is ready (off is always safe)", func() {
			in := controller.Inputs{Ready: false, Override: controller.OverrideForceOff}

			d := controller.Decide(clock.Now(), in, controller.StateCharging, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.ReasonCode).To(Equal(controller.ReasonOverrideOff))
		})

		It("stays off when not ready under Auto (override does not apply)", func() {
			in := controller.Inputs{Ready: false, Override: controller.OverrideAuto}

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.ReasonCode).To(Equal(controller.ReasonAwaitingData))
		})
	})

	Context("vehicle connection gate", func() {
		It("never starts charging when no vehicle is connected, despite high sustained surplus", func() {
			in := baseInputs()
			in.Connected = false
			in.ChargeW = 10000
			in.GridW = -10000 // strong surplus

			// Even with the start dwell already elapsed, it must not charge.
			timers := controller.Timers{CrossedUpAt: t0}
			clock.Advance(5 * time.Minute)

			d := controller.Decide(clock.Now(), in, controller.StateSurplusPending, timers, cfg)

			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.Timers).To(Equal(controller.Timers{}))
			Expect(d.ReasonCode).To(Equal(controller.ReasonNoVehicle))
		})

		It("an active ForceOn override still wins over the connection gate", func() {
			in := baseInputs()
			in.Connected = false
			in.Override = controller.OverrideForceOn

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateCharging))
			Expect(d.DesiredMode).To(Equal(config.EnableModePV))
			Expect(d.ReasonCode).To(Equal(controller.ReasonOverrideOn))
		})

		It("resumes normal dwell behavior when a vehicle is plugged in with surplus present", func() {
			// Disconnected with surplus: stays idle, no timer armed.
			disconnected := baseInputs()
			disconnected.Connected = false
			disconnected.ChargeW = 1500 // surplus 1500 >= start

			d0 := controller.Decide(clock.Now(), disconnected, controller.StateIdle, controller.Timers{}, cfg)
			Expect(d0.State).To(Equal(controller.StateIdle))
			Expect(d0.Timers).To(Equal(controller.Timers{}))
			Expect(d0.ReasonCode).To(Equal(controller.ReasonNoVehicle))

			// Vehicle now plugged in: surplus path arms the start dwell.
			connected := baseInputs()
			connected.ChargeW = 1500

			d1 := controller.Decide(clock.Now(), connected, d0.State, d0.Timers, cfg)
			Expect(d1.State).To(Equal(controller.StateSurplusPending))
			Expect(d1.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d1.Timers.CrossedUpAt).To(Equal(t0))
			Expect(d1.ReasonCode).To(Equal(controller.ReasonSurplusDwellStart))

			// After the start dwell elapses, it charges.
			clock.Advance(60 * time.Second)
			d2 := controller.Decide(clock.Now(), connected, d1.State, d1.Timers, cfg)
			Expect(d2.State).To(Equal(controller.StateCharging))
			Expect(d2.DesiredMode).To(Equal(config.EnableModePV))
			Expect(d2.ReasonCode).To(Equal(controller.ReasonSurplusCharging))
		})
	})

	Context("SoC cap latch", func() {
		It("stops charging once SoC reaches the cap", func() {
			in := baseInputs()
			in.VehicleSoC = 80
			in.ChargeW = 10000
			in.GridW = -10000 // plenty of surplus

			d := controller.Decide(clock.Now(), in, controller.StateCharging, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateSocReached))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.ReasonCode).To(Equal(controller.ReasonSoCCap))
		})

		It("latches off at 79 (below cap but >= resumeBelow)", func() {
			in := baseInputs()
			in.VehicleSoC = 79
			in.ChargeW = 10000
			in.GridW = -10000

			d := controller.Decide(clock.Now(), in, controller.StateSocReached, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateSocReached))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.ReasonCode).To(Equal(controller.ReasonSoCLatch))
		})

		It("becomes eligible to charge again once SoC drops below resumeBelow", func() {
			in := baseInputs()
			in.VehicleSoC = 77
			in.ChargeW = 10000
			in.GridW = -10000 // strong surplus

			d := controller.Decide(clock.Now(), in, controller.StateSocReached, controller.Timers{}, cfg)

			// Leaves the latch; re-enters the normal surplus path (start dwell).
			Expect(d.State).To(Equal(controller.StateSurplusPending))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.ReasonCode).To(Equal(controller.ReasonSurplusDwellStart))
		})

		It("does not latch from a non-SocReached state when below resumeBelow", func() {
			in := baseInputs()
			in.VehicleSoC = 79 // >= resumeBelow but we were idle, not latched
			in.GridW = 5000    // no surplus

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.ReasonCode).To(Equal(controller.ReasonSurplusBelowStart))
		})

		It("ignores a leftover TargetSoC under the default surplus profile and caps at cfg.SoCCap", func() {
			// Regression: at the Inputs level, a stale TargetSoC can still arrive
			// scalar (e.g. from a caller bypassing the per-profile ControlState
			// storage). A 90% target here must not raise the safety cap once the
			// profile is surplus.
			in := baseInputs()
			in.AutoProfile = config.AutoProfileSurplus // explicit surplus (as after a profile switch)
			in.TargetSoC = 90                          // stale leftover from a latch session
			in.VehicleSoC = 85                         // above cfg.SoCCap (80), below the stale target
			in.ChargeW = 10000
			in.GridW = -10000 // plenty of surplus

			d := controller.Decide(clock.Now(), in, controller.StateCharging, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateSocReached))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.ReasonCode).To(Equal(controller.ReasonSoCCap))
		})

		It("ignores a leftover TargetSoC when AutoProfile is unset (falls back to surplus)", func() {
			in := baseInputs() // AutoProfile "" → falls back to cfg.AutoProfile (surplus)
			in.TargetSoC = 100
			in.VehicleSoC = 85
			in.ChargeW = 10000
			in.GridW = -10000

			d := controller.Decide(clock.Now(), in, controller.StateCharging, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateSocReached))
			Expect(d.ReasonCode).To(Equal(controller.ReasonSoCCap))
		})
	})

	Context("start dwell from idle", func() {
		It("enters surplus_pending and arms the timer when surplus first crosses start", func() {
			in := baseInputs()
			in.ChargeW = 1500 // surplus 1500 >= 1400
			in.GridW = 0

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateSurplusPending))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.Timers.CrossedUpAt).To(Equal(t0))
			Expect(d.Timers.CrossedDownAt).To(BeZero())
			Expect(d.ReasonCode).To(Equal(controller.ReasonSurplusDwellStart))
		})

		It("stays pending while the start dwell has not elapsed", func() {
			in := baseInputs()
			in.ChargeW = 1500

			timers := controller.Timers{CrossedUpAt: t0}
			clock.Advance(30 * time.Second) // < 60s dwell

			d := controller.Decide(clock.Now(), in, controller.StateSurplusPending, timers, cfg)

			Expect(d.State).To(Equal(controller.StateSurplusPending))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.Timers.CrossedUpAt).To(Equal(t0)) // unchanged
			Expect(d.ReasonCode).To(Equal(controller.ReasonSurplusDwellPending))
		})

		It("starts charging once the start dwell elapses", func() {
			in := baseInputs()
			in.ChargeW = 1500

			timers := controller.Timers{CrossedUpAt: t0}
			clock.Advance(60 * time.Second) // == dwell

			d := controller.Decide(clock.Now(), in, controller.StateSurplusPending, timers, cfg)

			Expect(d.State).To(Equal(controller.StateCharging))
			Expect(d.DesiredMode).To(Equal(config.EnableModePV))
			Expect(d.Timers).To(Equal(controller.Timers{}))
			Expect(d.ReasonCode).To(Equal(controller.ReasonSurplusCharging))
		})

		It("resets the dwell when surplus drops back below start before charging", func() {
			in := baseInputs()
			in.GridW = 200 // surplus -200 < 1400

			timers := controller.Timers{CrossedUpAt: t0}
			clock.Advance(30 * time.Second)

			d := controller.Decide(clock.Now(), in, controller.StateSurplusPending, timers, cfg)

			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.Timers).To(Equal(controller.Timers{}))
			Expect(d.ReasonCode).To(Equal(controller.ReasonSurplusBelowStart))
		})
	})

	Context("stop dwell and anti-flap from charging", func() {
		It("keeps charging on a brief dip that recovers within the stop dwell", func() {
			// Dip below stop threshold arms the stop timer but keeps charging.
			dip := baseInputs()
			dip.GridW = 500 // surplus -500 < 0 (stop threshold)

			d1 := controller.Decide(clock.Now(), dip, controller.StateCharging, controller.Timers{}, cfg)
			Expect(d1.State).To(Equal(controller.StateStopPending))
			Expect(d1.DesiredMode).To(Equal(config.EnableModePV)) // still charging
			Expect(d1.Timers.CrossedDownAt).To(Equal(t0))
			Expect(d1.ReasonCode).To(Equal(controller.ReasonStopDwellStart))

			// Surplus recovers within the dwell window: cancel the pending stop.
			clock.Advance(30 * time.Second)
			recover := baseInputs()
			recover.ChargeW = 1000 // surplus 1000 >= 0 (>= stop threshold)

			d2 := controller.Decide(clock.Now(), recover, d1.State, d1.Timers, cfg)
			Expect(d2.State).To(Equal(controller.StateCharging))
			Expect(d2.DesiredMode).To(Equal(config.EnableModePV))
			Expect(d2.Timers).To(Equal(controller.Timers{})) // anti-flap: timer cleared
			Expect(d2.ReasonCode).To(Equal(controller.ReasonCharging))
		})

		It("stays in stop_pending while the stop dwell has not elapsed", func() {
			in := baseInputs()
			in.GridW = 500 // below stop threshold

			timers := controller.Timers{CrossedDownAt: t0}
			clock.Advance(60 * time.Second) // < 120s dwell

			d := controller.Decide(clock.Now(), in, controller.StateStopPending, timers, cfg)

			Expect(d.State).To(Equal(controller.StateStopPending))
			Expect(d.DesiredMode).To(Equal(config.EnableModePV)) // keep charging
			Expect(d.Timers.CrossedDownAt).To(Equal(t0))
			Expect(d.ReasonCode).To(Equal(controller.ReasonStopDwellPending))
		})

		It("stops once a sustained drop exceeds the stop dwell", func() {
			in := baseInputs()
			in.GridW = 500

			timers := controller.Timers{CrossedDownAt: t0}
			clock.Advance(120 * time.Second) // == stop dwell

			d := controller.Decide(clock.Now(), in, controller.StateStopPending, timers, cfg)

			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.Timers).To(Equal(controller.Timers{}))
			Expect(d.ReasonCode).To(Equal(controller.ReasonStopLow))
		})
	})

	Context("hysteresis band (between stop and start thresholds)", func() {
		It("keeps an already-charging session charging", func() {
			in := baseInputs()
			in.ChargeW = 700 // surplus 700: >= stop (0), < start (1400)

			d := controller.Decide(clock.Now(), in, controller.StateCharging, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateCharging))
			Expect(d.DesiredMode).To(Equal(config.EnableModePV))
			Expect(d.ReasonCode).To(Equal(controller.ReasonCharging))
		})

		It("does not start a new session from idle", func() {
			in := baseInputs()
			in.ChargeW = 700 // surplus 700: below start threshold

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.Timers).To(Equal(controller.Timers{}))
			Expect(d.ReasonCode).To(Equal(controller.ReasonSurplusBelowStart))
		})
	})

	Context("enable mode propagation", func() {
		It("publishes the configured enable mode when starting (now mode)", func() {
			cfg.EnableMode = config.EnableModeNow
			in := baseInputs()
			in.ChargeW = 1500

			timers := controller.Timers{CrossedUpAt: t0}
			clock.Advance(60 * time.Second)

			d := controller.Decide(clock.Now(), in, controller.StateSurplusPending, timers, cfg)

			Expect(d.State).To(Equal(controller.StateCharging))
			Expect(d.DesiredMode).To(Equal(config.EnableModeNow))
			Expect(d.ReasonCode).To(Equal(controller.ReasonSurplusCharging))
		})
	})

	Context("runtime charge-power mode", func() {
		It("force-on uses the runtime ChargePower over the config default", func() {
			in := baseInputs()
			in.Override = controller.OverrideForceOn
			in.ChargePower = config.EnableModeNow // config default is pv
			in.GridW = 5000                       // no surplus

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateCharging))
			Expect(d.DesiredMode).To(Equal(config.EnableModeNow))
			Expect(d.ReasonCode).To(Equal(controller.ReasonOverrideOn))
		})

		It("automatic start uses the runtime ChargePower", func() {
			in := baseInputs()
			in.ChargePower = config.EnableModeNow
			in.ChargeW = 1500 // surplus over the start threshold

			timers := controller.Timers{CrossedUpAt: t0}
			clock.Advance(60 * time.Second)

			d := controller.Decide(clock.Now(), in, controller.StateSurplusPending, timers, cfg)

			Expect(d.State).To(Equal(controller.StateCharging))
			Expect(d.DesiredMode).To(Equal(config.EnableModeNow))
			Expect(d.ReasonCode).To(Equal(controller.ReasonSurplusCharging))
		})

		It("falls back to the config default when ChargePower is unset", func() {
			in := baseInputs()
			in.Override = controller.OverrideForceOn // ChargePower empty

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.DesiredMode).To(Equal(config.EnableModePV))
			Expect(d.ReasonCode).To(Equal(controller.ReasonOverrideOn))
		})
	})
})

var _ = Describe("Decide (latch profiles)", func() {
	var (
		cfg   config.Control
		clock *controller.FakeClock
		t0    time.Time
	)

	BeforeEach(func() {
		cfg = baseConfig()
		t0 = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
		clock = controller.NewFakeClock(t0)
	})

	Context("power_latch profile (triggers on PV production)", func() {
		It("stays idle below the PV start threshold, even with a huge net surplus", func() {
			in := baseInputs()
			in.AutoProfile = config.AutoProfilePowerLatch
			in.PVW = 1000     // below the 1400W threshold
			in.GridW = -10000 // huge export/surplus — irrelevant to power_latch

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.ReasonCode).To(Equal(controller.ReasonLatchBelowStart))
		})

		It("arms the start dwell once PV production crosses the threshold, despite no net surplus", func() {
			in := baseInputs()
			in.AutoProfile = config.AutoProfilePowerLatch
			in.PVW = 1500   // >= 1400W threshold
			in.GridW = 5000 // heavy import: no net surplus — irrelevant to power_latch

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateSurplusPending))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.Timers.CrossedUpAt).To(Equal(t0))
			Expect(d.ReasonCode).To(Equal(controller.ReasonLatchStart))
		})

		It("starts charging at full power once the start dwell elapses", func() {
			in := baseInputs()
			in.AutoProfile = config.AutoProfilePowerLatch
			in.PVW = 1500

			timers := controller.Timers{CrossedUpAt: t0}
			clock.Advance(60 * time.Second) // == start dwell

			d := controller.Decide(clock.Now(), in, controller.StateSurplusPending, timers, cfg)

			Expect(d.State).To(Equal(controller.StateCharging))
			Expect(d.DesiredMode).To(Equal(config.EnableModeNow))
			Expect(d.ReasonCode).To(Equal(controller.ReasonLatchCharging))
		})
	})

	Context("surplus_latch profile (triggers on net surplus)", func() {
		It("stays idle below the surplus start threshold, despite huge PV production", func() {
			in := baseInputs()
			in.AutoProfile = config.AutoProfileSurplusLatch
			in.PVW = 10000 // huge PV — irrelevant to surplus_latch
			in.GridW = 5000
			in.ChargeW = 0 // surplus -5000, below threshold

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.ReasonCode).To(Equal(controller.ReasonLatchBelowStart))
		})

		It("arms the start dwell once net surplus crosses the threshold", func() {
			in := baseInputs()
			in.AutoProfile = config.AutoProfileSurplusLatch
			in.PVW = 0
			in.ChargeW = 1500 // surplus 1500 >= threshold
			in.GridW = 0

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateSurplusPending))
			Expect(d.ReasonCode).To(Equal(controller.ReasonLatchStart))
		})
	})

	Context("latch holds through a trigger dip", func() {
		It("keeps charging at full power even when the trigger collapses to zero", func() {
			in := baseInputs()
			in.AutoProfile = config.AutoProfilePowerLatch
			in.PVW = 0 // trigger fully collapsed
			in.GridW = 5000

			d := controller.Decide(clock.Now(), in, controller.StateCharging, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateCharging))
			Expect(d.DesiredMode).To(Equal(config.EnableModeNow))
			Expect(d.ReasonCode).To(Equal(controller.ReasonLatchCharging))
		})

		It("re-latches a stray stop_pending left over from a mid-flight profile switch", func() {
			in := baseInputs()
			in.AutoProfile = config.AutoProfileSurplusLatch
			in.GridW = 5000 // would trigger a stop under the surplus profile

			d := controller.Decide(clock.Now(), in, controller.StateStopPending, controller.Timers{CrossedDownAt: t0}, cfg)

			Expect(d.State).To(Equal(controller.StateCharging))
			Expect(d.Timers).To(Equal(controller.Timers{}))
			Expect(d.ReasonCode).To(Equal(controller.ReasonLatchCharging))
		})
	})

	Context("runtime start-threshold override", func() {
		It("uses the operator-chosen threshold instead of the config default", func() {
			in := baseInputs()
			in.AutoProfile = config.AutoProfilePowerLatch
			in.StartThresholdW = 3000 // overrides the 1400W config default
			in.PVW = 2000             // below the override, above the config default

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.ReasonCode).To(Equal(controller.ReasonLatchBelowStart))
		})
	})

	Context("stop at the runtime target SoC", func() {
		It("stops once VehicleSoC reaches the runtime target", func() {
			in := baseInputs()
			in.AutoProfile = config.AutoProfilePowerLatch
			in.TargetSoC = 90
			in.VehicleSoC = 90
			in.PVW = 10000 // would otherwise keep charging

			d := controller.Decide(clock.Now(), in, controller.StateCharging, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateSocReached))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.ReasonCode).To(Equal(controller.ReasonSoCCap))
		})

		It("keeps latching below the runtime target", func() {
			in := baseInputs()
			in.AutoProfile = config.AutoProfilePowerLatch
			in.TargetSoC = 90
			in.VehicleSoC = 89
			in.PVW = 10000

			d := controller.Decide(clock.Now(), in, controller.StateCharging, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateCharging))
			Expect(d.ReasonCode).To(Equal(controller.ReasonLatchCharging))
		})

		It("shifts the resume boundary with the target so the latch band width is unchanged", func() {
			// cfg: SoCCap 80, SoCResumeBelow 78 → band width 2; target 90 → resume at 88.
			atBoundary := baseInputs()
			atBoundary.AutoProfile = config.AutoProfilePowerLatch
			atBoundary.TargetSoC = 90
			atBoundary.VehicleSoC = 88
			atBoundary.PVW = 10000

			d := controller.Decide(clock.Now(), atBoundary, controller.StateSocReached, controller.Timers{}, cfg)
			Expect(d.State).To(Equal(controller.StateSocReached))
			Expect(d.ReasonCode).To(Equal(controller.ReasonSoCLatch))

			belowBoundary := baseInputs()
			belowBoundary.AutoProfile = config.AutoProfilePowerLatch
			belowBoundary.TargetSoC = 90
			belowBoundary.VehicleSoC = 87 // below the 88 resume boundary
			belowBoundary.PVW = 10000

			d2 := controller.Decide(clock.Now(), belowBoundary, controller.StateSocReached, controller.Timers{}, cfg)
			Expect(d2.State).To(Equal(controller.StateSurplusPending))
			Expect(d2.ReasonCode).To(Equal(controller.ReasonLatchStart))
		})
	})

	Context("fail-safes still take priority over an active latch", func() {
		It("forces off when data is stale, regardless of the trigger", func() {
			in := baseInputs()
			in.AutoProfile = config.AutoProfilePowerLatch
			in.Stale = true
			in.PVW = 10000

			d := controller.Decide(clock.Now(), in, controller.StateCharging, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateFailSafe))
			Expect(d.ReasonCode).To(Equal(controller.ReasonStaleOff))
		})

		It("a ForceOff override wins over an active latch", func() {
			in := baseInputs()
			in.AutoProfile = config.AutoProfileSurplusLatch
			in.Override = controller.OverrideForceOff
			in.ChargeW = 10000
			in.GridW = -10000

			d := controller.Decide(clock.Now(), in, controller.StateCharging, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.DesiredMode).To(Equal(controller.ModeOff))
			Expect(d.ReasonCode).To(Equal(controller.ReasonOverrideOff))
		})

		It("stays off when not ready", func() {
			in := controller.Inputs{Ready: false, AutoProfile: config.AutoProfilePowerLatch, PVW: 10000}

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.ReasonCode).To(Equal(controller.ReasonAwaitingData))
		})

		It("never charges when no vehicle is connected", func() {
			in := baseInputs()
			in.Connected = false
			in.AutoProfile = config.AutoProfileSurplusLatch
			in.ChargeW = 10000
			in.GridW = -10000

			d := controller.Decide(clock.Now(), in, controller.StateIdle, controller.Timers{}, cfg)

			Expect(d.State).To(Equal(controller.StateIdle))
			Expect(d.ReasonCode).To(Equal(controller.ReasonNoVehicle))
		})
	})
})

var _ = Describe("LimitSoCTarget", func() {
	var (
		cfg config.Control
		t0  time.Time
	)

	BeforeEach(func() {
		cfg = baseConfig() // SoCCap 80, SoCMax 100
		t0 = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	})

	It("returns the cap for automatic control", func() {
		in := controller.Inputs{Override: controller.OverrideAuto}
		Expect(controller.LimitSoCTarget(t0, in, cfg)).To(Equal(cfg.SoCCap))
	})

	It("returns the cap for a plain force-on (no bypass)", func() {
		in := controller.Inputs{Override: controller.OverrideForceOn}
		Expect(controller.LimitSoCTarget(t0, in, cfg)).To(Equal(cfg.SoCCap))
	})

	It("lifts to SoCMax for a cap-bypass force-on", func() {
		in := controller.Inputs{Override: controller.OverrideForceOn, OverrideCapBypass: true}
		Expect(controller.LimitSoCTarget(t0, in, cfg)).To(Equal(cfg.SoCMax))
	})

	It("lifts to SoCMax for a still-active, time-bounded cap-bypass", func() {
		in := controller.Inputs{
			Override:          controller.OverrideForceOn,
			OverrideCapBypass: true,
			OverrideUntil:     t0.Add(time.Hour), // not yet expired
		}
		Expect(controller.LimitSoCTarget(t0, in, cfg)).To(Equal(cfg.SoCMax))
	})

	It("ignores cap-bypass once the override has expired", func() {
		in := controller.Inputs{
			Override:          controller.OverrideForceOn,
			OverrideCapBypass: true,
			OverrideUntil:     t0.Add(-time.Second), // expired
		}
		Expect(controller.LimitSoCTarget(t0, in, cfg)).To(Equal(cfg.SoCCap))
	})

	It("ignores cap-bypass when set on a non-force-on override", func() {
		in := controller.Inputs{Override: controller.OverrideForceOff, OverrideCapBypass: true}
		Expect(controller.LimitSoCTarget(t0, in, cfg)).To(Equal(cfg.SoCCap))
	})

	It("uses the operator-chosen target SoC when OverrideCapBypassSoC is set", func() {
		in := controller.Inputs{
			Override:             controller.OverrideForceOn,
			OverrideCapBypass:    true,
			OverrideCapBypassSoC: 90,
		}
		Expect(controller.LimitSoCTarget(t0, in, cfg)).To(Equal(90))
	})

	It("clamps OverrideCapBypassSoC below SoCCap+1 up to SoCCap+1", func() {
		in := controller.Inputs{
			Override:             controller.OverrideForceOn,
			OverrideCapBypass:    true,
			OverrideCapBypassSoC: 70, // below SoCCap (80)
		}
		Expect(controller.LimitSoCTarget(t0, in, cfg)).To(Equal(cfg.SoCCap + 1))
	})

	It("clamps OverrideCapBypassSoC above SoCMax down to SoCMax", func() {
		in := controller.Inputs{
			Override:             controller.OverrideForceOn,
			OverrideCapBypass:    true,
			OverrideCapBypassSoC: 110,
		}
		Expect(controller.LimitSoCTarget(t0, in, cfg)).To(Equal(cfg.SoCMax))
	})

	It("uses the effective target SoC for an active latch profile, unclamped", func() {
		in := controller.Inputs{
			Override:    controller.OverrideAuto,
			AutoProfile: config.AutoProfilePowerLatch,
			TargetSoC:   65, // below SoCCap (80): a latch target is not clamped to SoCCap+1
		}
		Expect(controller.LimitSoCTarget(t0, in, cfg)).To(Equal(65))
	})

	It("falls back to cfg.SoCCap for a latch profile with no target override", func() {
		in := controller.Inputs{Override: controller.OverrideAuto, AutoProfile: config.AutoProfileSurplusLatch}
		Expect(controller.LimitSoCTarget(t0, in, cfg)).To(Equal(cfg.SoCCap))
	})

	It("a cap-bypass override still wins over an active latch profile", func() {
		in := controller.Inputs{
			Override:          controller.OverrideForceOn,
			OverrideCapBypass: true,
			AutoProfile:       config.AutoProfilePowerLatch,
			TargetSoC:         65,
		}
		Expect(controller.LimitSoCTarget(t0, in, cfg)).To(Equal(cfg.SoCMax))
	})
})
