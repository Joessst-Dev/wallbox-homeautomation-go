package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
)

var _ = Describe("Controller Status: start-threshold floor and SoC freshness", func() {
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

	// exporting is a healthy snapshot exporting w watts, optionally with evcc's
	// own minimum-current / phase reports.
	exporting := func(now time.Time, w float64) evcc.Snapshot {
		s := healthySnap(now)
		s.Grid = evcc.FloatMetric{Value: -w, At: now, Seen: true}
		return s
	}

	BeforeEach(func() {
		cfg = config.Default().Control // 1400 W configured, 8 A × 3 phases + 200 W fallback floor
		cfg.StartDwell = 0
		cmd = &fakeCommander{}
		snaps = &fakeSnaps{}
		rec = &fakeRecorder{}
		t0 = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
		clk = NewFakeClock(t0)
		ctx = context.Background()
		ctrl = New(cfg, cmd, snaps, rec, clk, discardLogger())
	})

	It("reports the config-fallback floor as the effective start threshold before evcc reports anything", func() {
		st := ctrl.Status()
		Expect(st.ConfiguredStartThresholdW).To(Equal(1400.0))
		Expect(st.MinChargePowerW).To(Equal(5520.0))
		Expect(st.StartThresholdW).To(Equal(5720.0))
	})

	It("does not start the field case (3 kW surplus, three-phase 8 A charger), then follows evcc's own report", func() {
		snaps.set(exporting(t0, 3000))
		ctrl.tick(ctx, clk.Now())
		Expect(ctrl.Status().State).To(Equal(StateIdle))
		Expect(ctrl.Status().ReasonCode).To(Equal(ReasonSurplusBelowStart))

		// evcc reports single-phase 6 A: the floor drops to 1580 W and 3 kW starts.
		clk.Advance(cfg.DecisionInterval)
		s := exporting(clk.Now(), 3000)
		s.EffectiveMinCurrent = evcc.FloatMetric{Value: 6, At: clk.Now(), Seen: true}
		s.PhasesConfigured = evcc.FloatMetric{Value: 1, At: clk.Now(), Seen: true}
		snaps.set(s)
		ctrl.tick(ctx, clk.Now())

		st := ctrl.Status()
		Expect(st.State).To(Equal(StateSurplusPending))
		Expect(st.MinChargePowerW).To(Equal(1380.0))
		Expect(st.StartThresholdW).To(Equal(1580.0))
	})

	It("reports a latch profile's runtime override, floored, right after it is set (before the next tick)", func() {
		Expect(ctrl.SetAutoProfile(config.AutoProfileSurplusLatch)).To(Succeed())
		Expect(ctrl.SetStartThreshold(3000)).To(Succeed())
		st := ctrl.Status()
		Expect(st.ConfiguredStartThresholdW).To(Equal(3000.0))
		Expect(st.StartThresholdW).To(Equal(5720.0))

		Expect(ctrl.SetAutoProfile(config.AutoProfilePowerLatch)).To(Succeed())
		Expect(ctrl.Status().StartThresholdW).To(Equal(1400.0), "power_latch is not floored")
	})

	It("surfaces the vehicle SoC change time and flags an unchanged SoC while charging as estimated", func() {
		charging := func(now time.Time, socChangedAt time.Time) evcc.Snapshot {
			s := healthySnap(now)
			s.Charging = evcc.BoolMetric{Value: true, At: now, Seen: true}
			s.ChargePower = evcc.FloatMetric{Value: 4000, At: now, Seen: true}
			s.VehicleSoC = evcc.FloatMetric{Value: 55, At: now, ChangedAt: socChangedAt, Seen: true}
			return s
		}

		snaps.set(charging(t0, t0))
		ctrl.tick(ctx, clk.Now())
		Expect(ctrl.Status().VehicleSoCChangedAt).To(Equal(t0))
		Expect(ctrl.Status().VehicleSoCEstimated).To(BeFalse())

		clk.Advance(SoCEstimatedAfter + time.Second)
		snaps.set(charging(clk.Now(), t0)) // evcc re-sent the same value
		ctrl.tick(ctx, clk.Now())
		st := ctrl.Status()
		Expect(st.VehicleSoCEstimated).To(BeTrue())
		Expect(st.Inputs.Stale).To(BeFalse(), "an unchanged SoC never trips the fail-safe")
	})
})
