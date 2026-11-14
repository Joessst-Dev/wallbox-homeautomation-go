package controller_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/controller"
)

var _ = Describe("StepStall", func() {
	const target = 80

	var (
		cfg   config.Control
		clock *controller.FakeClock
		t0    time.Time
	)

	// stalledInputs is the field symptom: plugged in, evcc enabled the charger,
	// not full — yet evcc reports not charging and no power.
	stalledInputs := func() controller.Inputs {
		in := baseInputs()
		in.Enabled = true
		in.Charging = false
		in.ChargeW = 0
		in.VehicleSoC = 50
		return in
	}

	// step advances the detector by one call at the fake clock's current time.
	step := func(in controller.Inputs, desired string, prev controller.StallState) controller.StallState {
		return controller.StepStall(clock.Now(), in, desired, target, prev, cfg)
	}

	// stallFor runs the detector with the stalled inputs from t0 until it latches.
	stallFor := func() controller.StallState {
		GinkgoHelper()
		s := step(stalledInputs(), config.EnableModePV, controller.StallState{})
		clock.Advance(cfg.StallTimeout)
		s = step(stalledInputs(), config.EnableModePV, s)
		Expect(s.Stalled).To(BeTrue())
		return s
	}

	BeforeEach(func() {
		cfg = baseConfig()
		cfg.StallTimeout = 3 * time.Minute
		cfg.StallRecoveryOff = 90 * time.Second
		t0 = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
		clock = controller.NewFakeClock(t0)
	})

	Describe("detection", func() {
		It("starts the timer on the first stalled tick without flagging", func() {
			s := step(stalledInputs(), config.EnableModePV, controller.StallState{})
			Expect(s.Since).To(Equal(t0))
			Expect(s.Stalled).To(BeFalse())
			Expect(s.RecoveryActive(t0)).To(BeFalse())
		})

		It("does not flag before StallTimeout has elapsed", func() {
			s := step(stalledInputs(), config.EnableModePV, controller.StallState{})
			clock.Advance(cfg.StallTimeout - time.Second)
			s = step(stalledInputs(), config.EnableModePV, s)
			Expect(s.Stalled).To(BeFalse())
			Expect(s.Since).To(Equal(t0))
		})

		It("flags at StallTimeout and schedules the one recovery off pulse", func() {
			s := stallFor()
			now := clock.Now()
			Expect(s.StalledSince).To(Equal(t0))
			Expect(s.RecoveryUsed).To(BeTrue())
			Expect(s.RecoveryUntil).To(Equal(now.Add(cfg.StallRecoveryOff)))
			Expect(s.RecoveryActive(now)).To(BeTrue())
		})

		It("restarts the timer when the condition breaks before StallTimeout", func() {
			s := step(stalledInputs(), config.EnableModePV, controller.StallState{})
			clock.Advance(2 * time.Minute)
			flowing := stalledInputs()
			flowing.ChargeW = 1500
			s = step(flowing, config.EnableModePV, s)
			Expect(s.Since.IsZero()).To(BeTrue())

			clock.Advance(2 * time.Minute)
			s = step(stalledInputs(), config.EnableModePV, s)
			Expect(s.Since).To(Equal(clock.Now()))
			Expect(s.Stalled).To(BeFalse())
		})

		DescribeTable("never arms when the loadpoint is not expected to deliver power",
			func(mutate func(in *controller.Inputs), desired string) {
				in := stalledInputs()
				mutate(&in)
				s := step(in, desired, controller.StallState{})
				clock.Advance(time.Hour)
				s = step(in, desired, s)
				Expect(s.Stalled).To(BeFalse())
				Expect(s.Since.IsZero()).To(BeTrue())
				Expect(s.RecoveryUsed).To(BeFalse())
			},
			Entry("desired mode is off", func(*controller.Inputs) {}, controller.ModeOff),
			Entry("no vehicle connected", func(in *controller.Inputs) { in.Connected = false }, config.EnableModePV),
			Entry("evcc has not enabled the charger", func(in *controller.Inputs) { in.Enabled = false }, config.EnableModePV),
			Entry("evcc reports charging", func(in *controller.Inputs) { in.Charging = true }, config.EnableModePV),
			Entry("power at the 100 W threshold is flowing", func(in *controller.Inputs) {
				in.ChargeW = controller.StallChargeThresholdW
			}, config.EnableModePV),
			Entry("vehicle already at the target SoC", func(in *controller.Inputs) { in.VehicleSoC = target }, config.EnableModePV),
			Entry("data is stale", func(in *controller.Inputs) { in.Stale = true }, config.EnableModeNow),
		)

		It("counts a trickle just below 100 W as no power", func() {
			in := stalledInputs()
			in.ChargeW = controller.StallChargeThresholdW - 1
			s := step(in, config.EnableModeNow, controller.StallState{})
			clock.Advance(cfg.StallTimeout)
			s = step(in, config.EnableModeNow, s)
			Expect(s.Stalled).To(BeTrue())
		})
	})

	Describe("recovery", func() {
		It("holds the whole state through the off pulse, even as evcc disables the charger", func() {
			s := stallFor()
			clock.Advance(cfg.StallRecoveryOff - time.Second)
			disabled := stalledInputs()
			disabled.Enabled = false // evcc reacts to the off mode
			held := step(disabled, config.EnableModePV, s)
			Expect(held).To(Equal(s))
			Expect(held.RecoveryActive(clock.Now())).To(BeTrue())
		})

		It("ends the pulse on time, keeps the stall flagged, and never schedules a second recovery", func() {
			s := stallFor()
			clock.Advance(cfg.StallRecoveryOff)
			disabled := stalledInputs()
			disabled.Enabled = false // evcc still re-enabling right after the pulse
			s = step(disabled, config.EnableModePV, s)
			Expect(s.RecoveryActive(clock.Now())).To(BeFalse())
			Expect(s.RecoveryUntil.IsZero()).To(BeTrue())
			Expect(s.Stalled).To(BeTrue(), "a transient 'not enabled' must not clear the latch")
			Expect(s.StalledSince).To(Equal(t0))

			// The car is still stuck: well past another StallTimeout, no new pulse.
			for range 20 {
				clock.Advance(cfg.StallTimeout)
				s = step(stalledInputs(), config.EnableModePV, s)
				Expect(s.Stalled).To(BeTrue())
				Expect(s.RecoveryActive(clock.Now())).To(BeFalse())
			}
		})

		It("clears the flag once power flows after the pulse", func() {
			s := stallFor()
			clock.Advance(cfg.StallRecoveryOff + time.Minute)
			recovered := stalledInputs()
			recovered.Charging = true
			recovered.ChargeW = 4000
			s = step(recovered, config.EnableModePV, s)
			Expect(s.Stalled).To(BeFalse())
			Expect(s.StalledSince.IsZero()).To(BeTrue())
			Expect(s.RecoveryUsed).To(BeTrue(), "still the same connected session")
		})

		It("clears the flag when the vehicle reaches the target", func() {
			s := stallFor()
			clock.Advance(cfg.StallRecoveryOff)
			full := stalledInputs()
			full.VehicleSoC = target
			s = step(full, config.EnableModePV, s)
			Expect(s.Stalled).To(BeFalse())
		})

		It("cancels the pulse and the flag when Decide wants off, but keeps the spent budget", func() {
			s := stallFor()
			clock.Advance(10 * time.Second) // mid-pulse
			s = step(stalledInputs(), controller.ModeOff, s)
			Expect(s).To(Equal(controller.StallState{RecoveryUsed: true}))

			// Wanting to charge again in the same session: flagged again, no new pulse.
			s = step(stalledInputs(), config.EnableModePV, s)
			clock.Advance(cfg.StallTimeout)
			s = step(stalledInputs(), config.EnableModePV, s)
			Expect(s.Stalled).To(BeTrue())
			Expect(s.RecoveryActive(clock.Now())).To(BeFalse())
		})

		It("lets a stale snapshot cancel a pulse in progress (fail-safe wins)", func() {
			s := stallFor()
			clock.Advance(10 * time.Second)
			stale := stalledInputs()
			stale.Stale = true
			s = step(stale, config.EnableModePV, s)
			Expect(s.RecoveryActive(clock.Now())).To(BeFalse())
			Expect(s.RecoveryUsed).To(BeTrue())
		})

		It("cancels the pulse on a single unplugged tick but keeps the spent budget (a blip is not a new session)", func() {
			s := stallFor()
			clock.Advance(10 * time.Second)
			unplugged := stalledInputs()
			unplugged.Connected = false
			s = step(unplugged, config.EnableModePV, s)
			Expect(s).To(Equal(controller.StallState{RecoveryUsed: true, Unplugged: true}))

			// Back on the very next tick, still stuck: flagged again, but no second pulse.
			clock.Advance(15 * time.Second)
			s = step(stalledInputs(), config.EnableModePV, s)
			Expect(s.Unplugged).To(BeFalse())
			clock.Advance(cfg.StallTimeout)
			s = step(stalledInputs(), config.EnableModePV, s)
			Expect(s.Stalled).To(BeTrue())
			Expect(s.RecoveryUsed).To(BeTrue())
			Expect(s.RecoveryActive(clock.Now())).To(BeFalse())
		})

		It("resets everything, including the recovery budget, after two consecutive unplugged ticks", func() {
			s := stallFor()
			clock.Advance(10 * time.Second)
			unplugged := stalledInputs()
			unplugged.Connected = false
			s = step(unplugged, config.EnableModePV, s)
			clock.Advance(15 * time.Second)
			s = step(unplugged, config.EnableModePV, s)
			Expect(s).To(Equal(controller.StallState{Unplugged: true}))

			// Plugged in again and stuck again: a fresh recovery is allowed.
			clock.Advance(time.Hour)
			s = step(stalledInputs(), config.EnableModePV, s)
			clock.Advance(cfg.StallTimeout)
			s = step(stalledInputs(), config.EnableModePV, s)
			Expect(s.Stalled).To(BeTrue())
			Expect(s.RecoveryActive(clock.Now())).To(BeTrue())
		})
	})

	Describe("RecoveryActive", func() {
		It("is false for the zero value and at/after RecoveryUntil", func() {
			Expect(controller.StallState{}.RecoveryActive(t0)).To(BeFalse())
			s := controller.StallState{RecoveryUntil: t0.Add(time.Minute)}
			Expect(s.RecoveryActive(t0)).To(BeTrue())
			Expect(s.RecoveryActive(t0.Add(time.Minute))).To(BeFalse())
		})
	})
})
