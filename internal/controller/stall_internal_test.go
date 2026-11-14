package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

// eventsOfType returns the recorded events whose Type matches, in order.
func (r *fakeRecorder) eventsOfType(typ string) []store.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []store.Event
	for _, e := range r.events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// countOf counts the occurrences of want in xs.
func countOf(xs []string, want string) int {
	n := 0
	for _, x := range xs {
		if x == want {
			n++
		}
	}
	return n
}

// lastSample returns the most recently recorded sample.
func (r *fakeRecorder) lastSample() store.Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.samples[len(r.samples)-1]
}

var _ = Describe("Controller stall detection and recovery (loop)", func() {
	var (
		cfg   config.Control
		cmd   *fakeCommander
		snaps *fakeSnaps
		rec   *fakeRecorder
		clk   *FakeClock
		ctrl  *Controller
		t0    time.Time
		ctx   context.Context
	)

	// stuckSnap is the field symptom at now: plugged in, evcc enabled the
	// charger, not full — yet not charging and no power.
	stuckSnap := func(now time.Time) evcc.Snapshot {
		s := healthySnap(now)
		s.Enabled = evcc.BoolMetric{Value: true, At: now, Seen: true}
		return s
	}

	// tickAt moves the fake clock by d, refreshes the snapshot via mk, and ticks.
	tickAt := func(d time.Duration, mk func(time.Time) evcc.Snapshot) {
		clk.Advance(d)
		snaps.set(mk(clk.Now()))
		ctrl.tick(ctx, clk.Now())
	}

	BeforeEach(func() {
		cfg = config.Default().Control // StallTimeout 3m, StallRecoveryOff 90s
		cmd = &fakeCommander{}
		snaps = &fakeSnaps{}
		rec = &fakeRecorder{}
		t0 = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
		clk = NewFakeClock(t0)
		ctx = context.Background()
		ctrl = New(cfg, cmd, snaps, rec, clk, discardLogger())
		// A force-on keeps Decide's desired mode at "pv" independent of surplus,
		// isolating the stall logic.
		ctrl.SetOverride(OverrideForceOn, time.Time{}, false, 0)
	})

	It("flags the stall, pulses off once, resumes, and stays flagged when the pulse does not help", func() {
		tickAt(0, stuckSnap)
		Expect(cmd.publishedModes()).To(Equal([]string{config.EnableModePV}))
		Expect(ctrl.Status().Stalled).To(BeFalse())

		// Just short of StallTimeout: not yet.
		tickAt(cfg.StallTimeout-time.Second, stuckSnap)
		Expect(ctrl.Status().Stalled).To(BeFalse())
		Expect(cmd.publishedModes()).To(Equal([]string{config.EnableModePV}))

		// StallTimeout reached: flagged, and the one recovery pulse publishes off.
		tickAt(time.Second, stuckSnap)
		st := ctrl.Status()
		Expect(st.Stalled).To(BeTrue())
		Expect(st.StalledSince).To(Equal(t0))
		Expect(st.StallRecoveryActive).To(BeTrue())
		Expect(st.DesiredMode).To(Equal(config.EnableModePV), "Decide's own decision is untouched")
		Expect(cmd.publishedModes()).To(Equal([]string{config.EnableModePV, ModeOff}))
		Expect(rec.lastSample().Mode).To(Equal(ModeOff), "samples record what was actually published")

		stalled := rec.eventsOfType("stalled")
		Expect(stalled).To(HaveLen(1))
		Expect(stalled[0].Action).To(Equal("detected"))
		recovery := rec.eventsOfType("stall_recovery")
		Expect(recovery).To(HaveLen(1))
		Expect(recovery[0].Action).To(Equal("off"))

		// Mid-pulse: still off, nothing re-published.
		tickAt(cfg.StallRecoveryOff-time.Second, stuckSnap)
		Expect(cmd.publishedModes()).To(HaveLen(2))

		// Pulse over: normal desired mode is resumed.
		tickAt(time.Second, stuckSnap)
		Expect(cmd.publishedModes()).To(Equal([]string{config.EnableModePV, ModeOff, config.EnableModePV}))
		Expect(ctrl.Status().StallRecoveryActive).To(BeFalse())
		recovery = rec.eventsOfType("stall_recovery")
		Expect(recovery).To(HaveLen(2))
		Expect(recovery[1].Action).To(Equal("resume"))

		// Still stuck for a long time: stays flagged, never pulses off again.
		for range 10 {
			tickAt(cfg.StallTimeout, stuckSnap)
		}
		Expect(ctrl.Status().Stalled).To(BeTrue())
		Expect(countOf(cmd.publishedModes(), ModeOff)).To(Equal(1),
			"exactly one recovery pulse per connected session")
		Expect(len(cmd.publishedModes())).To(BeNumerically(">", 3), "the republish cadence kept re-asserting pv")
		Expect(latestMode(cmd)).To(Equal(config.EnableModePV))
	})

	It("clears the flag and records it once power flows", func() {
		tickAt(0, stuckSnap)
		tickAt(cfg.StallTimeout, stuckSnap)
		Expect(ctrl.Status().Stalled).To(BeTrue())

		tickAt(cfg.StallRecoveryOff, func(now time.Time) evcc.Snapshot {
			s := stuckSnap(now)
			s.Charging = evcc.BoolMetric{Value: true, At: now, Seen: true}
			s.ChargePower = evcc.FloatMetric{Value: 4000, At: now, Seen: true}
			return s
		})
		Expect(ctrl.Status().Stalled).To(BeFalse())
		stalled := rec.eventsOfType("stalled")
		Expect(stalled).To(HaveLen(2))
		Expect(stalled[1].Action).To(Equal("cleared"))
	})

	It("grants a fresh recovery after the vehicle is unplugged and plugged in again", func() {
		tickAt(0, stuckSnap)
		tickAt(cfg.StallTimeout, stuckSnap)
		tickAt(cfg.StallRecoveryOff, stuckSnap) // pulse spent

		// Unplug for two ticks → Decide still says pv under the force-on override,
		// but the stall state (and its budget) resets.
		unplugged := func(now time.Time) evcc.Snapshot {
			s := stuckSnap(now)
			s.Connected = evcc.BoolMetric{Value: false, At: now, Seen: true}
			return s
		}
		tickAt(time.Minute, unplugged)
		Expect(ctrl.Status().Stalled).To(BeFalse())
		tickAt(cfg.DecisionInterval, unplugged)

		before := len(cmd.publishedModes())
		tickAt(time.Minute, stuckSnap)
		tickAt(cfg.StallTimeout, stuckSnap)
		Expect(ctrl.Status().StallRecoveryActive).To(BeTrue())
		Expect(cmd.publishedModes()[before:]).To(ContainElement(ModeOff))
	})

	It("lets the fail-safe win: a broker drop mid-pulse publishes off via Decide and ends the pulse", func() {
		tickAt(0, stuckSnap)
		tickAt(cfg.StallTimeout, stuckSnap)
		Expect(ctrl.Status().StallRecoveryActive).To(BeTrue())

		tickAt(10*time.Second, func(now time.Time) evcc.Snapshot {
			s := stuckSnap(now)
			s.BrokerConnected = false
			return s
		})
		st := ctrl.Status()
		Expect(st.State).To(Equal(StateFailSafe))
		Expect(st.DesiredMode).To(Equal(ModeOff))
		Expect(st.StallRecoveryActive).To(BeFalse())
		Expect(latestMode(cmd)).To(Equal(ModeOff))

		// Still disconnected past where the pulse would have ended: the pulse's
		// "resume" must never raise charging while the fail-safe holds.
		pvBefore := countOf(cmd.publishedModes(), config.EnableModePV)
		tickAt(cfg.StallRecoveryOff, func(now time.Time) evcc.Snapshot {
			s := stuckSnap(now)
			s.BrokerConnected = false
			return s
		})
		Expect(latestMode(cmd)).To(Equal(ModeOff))
		Expect(countOf(cmd.publishedModes(), config.EnableModePV)).To(Equal(pvBefore))
	})

	It("lets the SoC cap win mid-pulse: off stays published and the pulse never resumes charging", func() {
		// Automatic (surplus) control, so the SoC cap applies (a ForceOn override
		// would outrank it in Decide's priority order).
		ctrl.SetOverride(OverrideAuto, time.Time{}, false, 0)
		autoCfg := cfg
		autoCfg.StartDwell = 0
		ctrl = New(autoCfg, cmd, snaps, rec, clk, discardLogger())
		stuckSurplus := func(soc float64) func(time.Time) evcc.Snapshot {
			return func(now time.Time) evcc.Snapshot {
				s := stuckSnap(now)
				s.Grid = evcc.FloatMetric{Value: -8000, At: now, Seen: true} // above the 5720 W floor
				s.VehicleSoC = evcc.FloatMetric{Value: soc, At: now, ChangedAt: now, Seen: true}
				return s
			}
		}

		tickAt(0, stuckSurplus(50))                    // arms the start dwell
		tickAt(cfg.DecisionInterval, stuckSurplus(50)) // charging (pv)
		tickAt(cfg.StallTimeout, stuckSurplus(50))     // stalled → pulse
		Expect(ctrl.Status().StallRecoveryActive).To(BeTrue())
		Expect(latestMode(cmd)).To(Equal(ModeOff))
		pvBefore := countOf(cmd.publishedModes(), config.EnableModePV)

		tickAt(10*time.Second, stuckSurplus(float64(cfg.SoCCap))) // SoC cap mid-pulse
		st := ctrl.Status()
		Expect(st.State).To(Equal(StateSocReached))
		Expect(st.DesiredMode).To(Equal(ModeOff))
		Expect(st.StallRecoveryActive).To(BeFalse())
		Expect(st.Stalled).To(BeFalse())
		Expect(latestMode(cmd)).To(Equal(ModeOff))

		tickAt(cfg.StallRecoveryOff, stuckSurplus(float64(cfg.SoCCap))) // past the pulse end
		Expect(latestMode(cmd)).To(Equal(ModeOff))
		Expect(countOf(cmd.publishedModes(), config.EnableModePV)).To(Equal(pvBefore))
		Expect(rec.eventsOfType("stall_recovery")).To(ContainElement(HaveField("Action", "resume")))
	})

	It("does not grant a second pulse after a single unplugged blip", func() {
		tickAt(0, stuckSnap)
		tickAt(cfg.StallTimeout, stuckSnap)
		tickAt(cfg.StallRecoveryOff, stuckSnap) // pulse spent

		tickAt(cfg.DecisionInterval, func(now time.Time) evcc.Snapshot {
			s := stuckSnap(now)
			s.Connected = evcc.BoolMetric{Value: false, At: now, Seen: true}
			return s
		})
		tickAt(cfg.DecisionInterval, stuckSnap)
		tickAt(cfg.StallTimeout, stuckSnap)

		Expect(ctrl.Status().Stalled).To(BeTrue())
		Expect(ctrl.Status().StallRecoveryActive).To(BeFalse())
		Expect(countOf(cmd.publishedModes(), ModeOff)).To(Equal(1))
	})

	It("never flags a loadpoint evcc has not enabled", func() {
		for range 20 {
			tickAt(cfg.StallTimeout, healthySnap) // Enabled unset → false
		}
		Expect(ctrl.Status().Stalled).To(BeFalse())
		Expect(rec.eventsOfType("stalled")).To(BeEmpty())
	})
})
