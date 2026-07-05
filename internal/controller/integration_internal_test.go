package controller

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

// newRealStore opens a real, migrated SQLite Store backed by a fresh temp-file
// database that is closed automatically when the spec ends. It mirrors the
// newTempStore helper in internal/store/store_suite_test.go, wiring a real
// Recorder into the controller so persistence is exercised end to end.
func newRealStore() *store.Store {
	GinkgoHelper()
	path := filepath.Join(GinkgoT().TempDir(), "wha.db")
	s, err := store.Open(path)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		Expect(s.Close()).To(Succeed())
	})
	return s
}

// latestMode returns the last mode the fake commander published ("" if none).
func latestMode(c *fakeCommander) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.modes) == 0 {
		return ""
	}
	return c.modes[len(c.modes)-1]
}

var _ = Describe("Controller integration (real store)", func() {
	var (
		cfg   config.Control
		cmd   *fakeCommander
		snaps *fakeSnaps
		st    *store.Store
		clk   *FakeClock
		ctrl  *Controller
		t0    time.Time
		ctx   context.Context
	)

	// newCtrl builds a controller over the current cfg + real store, so specs
	// that need dwell can re-tune cfg and rebuild before ticking.
	newCtrl := func() *Controller {
		return New(cfg, cmd, snaps, st, clk, discardLogger())
	}

	BeforeEach(func() {
		cfg = config.Default().Control
		cfg.StartDwell = 0 // most specs want the surplus edge to resolve immediately
		cfg.StopDwell = 0
		cmd = &fakeCommander{}
		snaps = &fakeSnaps{}
		st = newRealStore()
		t0 = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
		clk = NewFakeClock(t0)
		ctx = context.Background()
		ctrl = newCtrl()
	})

	// surplusSnap is a healthy, connected snapshot exporting `-grid` watts to the
	// grid (grid is negative when exporting), yielding a positive surplus.
	surplusSnap := func(now time.Time, gridW float64) evcc.Snapshot {
		s := healthySnap(now)
		s.Grid = evcc.FloatMetric{Value: gridW, At: now, Seen: true}
		return s
	}

	// chargingSnap additionally reports the loadpoint actively charging at chargeW.
	chargingSnap := func(now time.Time, chargeW float64) evcc.Snapshot {
		s := surplusSnap(now, -5000)
		s.ChargePower = evcc.FloatMetric{Value: chargeW, At: now, Seen: true}
		s.Charging = evcc.BoolMetric{Value: true, At: now, Seen: true}
		return s
	}

	openSession := func() *store.Session {
		GinkgoHelper()
		open, err := st.OpenSession(ctx)
		Expect(err).NotTo(HaveOccurred())
		return open
	}

	Describe("fail-safe ladder (first match wins)", func() {
		DescribeTable("forces off when data is unusable",
			func(mutate func(now time.Time) evcc.Snapshot, tickDelay time.Duration) {
				snaps.set(mutate(t0))
				now := t0.Add(tickDelay)
				clk.Set(now)
				ctrl.tick(ctx, now)

				Expect(ctrl.Status().State).To(Equal(StateFailSafe))
				Expect(ctrl.Status().DesiredMode).To(Equal(ModeOff))
				Expect(latestMode(cmd)).To(Equal(ModeOff))
			},
			Entry("broker disconnected", func(now time.Time) evcc.Snapshot {
				s := healthySnap(now)
				s.BrokerConnected = false
				return s
			}, time.Duration(0)),
			Entry("evcc offline (LWT)", func(now time.Time) evcc.Snapshot {
				s := healthySnap(now)
				s.Online = evcc.BoolMetric{Value: false, At: now, Seen: true}
				return s
			}, time.Duration(0)),
			// healthySnap stamps its metrics at now; the table ticks StaleTimeout+1s
			// later, so grid/pv read as stale.
			Entry("grid/pv metric past StaleTimeout", healthySnap,
				config.Default().Control.StaleTimeout+time.Second),
		)

		It("honors an active force-off override → off", func() {
			ctrl.SetOverride(OverrideForceOff, time.Time{}, false, 0)
			snaps.set(surplusSnap(t0, -5000)) // ample surplus, must still be off
			ctrl.tick(ctx, clk.Now())

			Expect(ctrl.Status().State).To(Equal(StateIdle))
			Expect(ctrl.Status().DesiredMode).To(Equal(ModeOff))
			Expect(latestMode(cmd)).To(Equal(ModeOff))
		})

		It("charges at the effective mode under a force-on override even with zero surplus", func() {
			ctrl.SetOverride(OverrideForceOn, time.Time{}, false, 0)
			snaps.set(healthySnap(t0)) // zero surplus
			ctrl.tick(ctx, clk.Now())

			Expect(ctrl.Status().State).To(Equal(StateCharging))
			Expect(ctrl.Status().DesiredMode).To(Equal(cfg.EnableMode))
			Expect(latestMode(cmd)).To(Equal(cfg.EnableMode))
		})

		It("reverts an expiring override to auto on the tick after it lapses", func() {
			snaps.set(healthySnap(t0))
			ctrl.SetOverride(OverrideForceOn, t0.Add(time.Minute), false, 0)
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().Override).To(Equal(OverrideForceOn))

			clk.Advance(2 * time.Minute)
			snaps.set(healthySnap(clk.Now()))
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().Override).To(Equal(OverrideAuto))
		})

		It("forces off when required data has not been seen yet (!Ready)", func() {
			s := healthySnap(t0)
			s.VehicleSoC = evcc.FloatMetric{} // never seen → not ready (but not stale)
			snaps.set(s)
			ctrl.tick(ctx, clk.Now())

			Expect(ctrl.Status().State).To(Equal(StateIdle))
			Expect(ctrl.Status().ReasonCode).To(Equal(ReasonAwaitingData))
			Expect(ctrl.Status().DesiredMode).To(Equal(ModeOff))
		})

		It("forces off when no vehicle is connected (!Connected)", func() {
			s := surplusSnap(t0, -5000)
			s.Connected = evcc.BoolMetric{Value: false, At: t0, Seen: true}
			snaps.set(s)
			ctrl.tick(ctx, clk.Now())

			Expect(ctrl.Status().State).To(Equal(StateIdle))
			Expect(ctrl.Status().ReasonCode).To(Equal(ReasonNoVehicle))
			Expect(ctrl.Status().DesiredMode).To(Equal(ModeOff))
		})

		It("latches off at the SoC cap until SoC falls below the resume boundary", func() {
			atCap := func(now time.Time, soc int) evcc.Snapshot {
				s := surplusSnap(now, -5000) // ample surplus; only the cap holds it off
				s.VehicleSoC = evcc.FloatMetric{Value: float64(soc), At: now, Seen: true}
				return s
			}

			// At the cap → off.
			snaps.set(atCap(t0, cfg.SoCCap))
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().State).To(Equal(StateSocReached))
			Expect(ctrl.Status().DesiredMode).To(Equal(ModeOff))

			// Between resumeBelow and the cap → still latched off.
			clk.Advance(cfg.DecisionInterval)
			snaps.set(atCap(clk.Now(), cfg.SoCResumeBelow))
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().State).To(Equal(StateSocReached))
			Expect(ctrl.Status().DesiredMode).To(Equal(ModeOff))

			// Below resumeBelow → latch releases and the surplus path takes over.
			clk.Advance(cfg.DecisionInterval)
			snaps.set(atCap(clk.Now(), cfg.SoCResumeBelow-1))
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().State).To(Equal(StateSurplusPending)) // StartDwell arms
			clk.Advance(cfg.DecisionInterval)
			snaps.set(atCap(clk.Now(), cfg.SoCResumeBelow-1))
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().State).To(Equal(StateCharging))
		})
	})

	Describe("surplus hysteresis and dwell", func() {
		It("rises idle → surplus-pending → charging (mode pv) after the start dwell", func() {
			cfg.StartDwell = 2 * time.Minute
			cfg.StopDwell = 0
			ctrl = newCtrl()

			// Below threshold → idle.
			snaps.set(healthySnap(t0))
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().State).To(Equal(StateIdle))

			// Surplus crosses the start threshold → pending (dwell armed).
			clk.Advance(cfg.DecisionInterval)
			snaps.set(surplusSnap(clk.Now(), -3000))
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().State).To(Equal(StateSurplusPending))
			Expect(ctrl.Status().DesiredMode).To(Equal(ModeOff))

			// Still within the dwell → still pending.
			clk.Advance(time.Minute)
			snaps.set(surplusSnap(clk.Now(), -3000))
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().State).To(Equal(StateSurplusPending))

			// Dwell elapsed → charging at the pv mode.
			clk.Advance(2 * time.Minute)
			snaps.set(surplusSnap(clk.Now(), -3000))
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().State).To(Equal(StateCharging))
			Expect(ctrl.Status().DesiredMode).To(Equal(config.EnableModePV))
			Expect(latestMode(cmd)).To(Equal(config.EnableModePV))
		})

		// startCharging drives ctrl into StateCharging via the surplus path with a
		// zero start dwell, leaving the given stop dwell in place.
		startCharging := func(stopDwell time.Duration) {
			GinkgoHelper()
			cfg.StartDwell = 0
			cfg.StopDwell = stopDwell
			ctrl = newCtrl()

			snaps.set(surplusSnap(t0, -3000))
			ctrl.tick(ctx, clk.Now()) // idle → surplus-pending
			clk.Advance(cfg.DecisionInterval)
			snaps.set(surplusSnap(clk.Now(), -3000))
			ctrl.tick(ctx, clk.Now()) // surplus-pending → charging
			Expect(ctrl.Status().State).To(Equal(StateCharging))
		}

		It("falls charging → stop-pending → idle (mode off) after the stop dwell", func() {
			startCharging(3 * time.Minute)

			// Surplus drops below stop threshold → stop-pending (still charging).
			clk.Advance(cfg.DecisionInterval)
			snaps.set(surplusSnap(clk.Now(), 1000)) // importing → negative surplus
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().State).To(Equal(StateStopPending))
			Expect(ctrl.Status().DesiredMode).To(Equal(config.EnableModePV))

			// Still within the stop dwell → still pending.
			clk.Advance(time.Minute)
			snaps.set(surplusSnap(clk.Now(), 1000))
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().State).To(Equal(StateStopPending))

			// Stop dwell elapsed → idle, mode off.
			clk.Advance(3 * time.Minute)
			snaps.set(surplusSnap(clk.Now(), 1000))
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().State).To(Equal(StateIdle))
			Expect(ctrl.Status().DesiredMode).To(Equal(ModeOff))
			Expect(latestMode(cmd)).To(Equal(ModeOff))
		})

		It("holds charging through a brief dip shorter than the stop dwell", func() {
			startCharging(3 * time.Minute)

			// Brief dip → stop-pending armed.
			clk.Advance(cfg.DecisionInterval)
			snaps.set(surplusSnap(clk.Now(), 1000))
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().State).To(Equal(StateStopPending))

			// Surplus recovers before the dwell elapses → charging resumes, never off.
			clk.Advance(time.Minute)
			snaps.set(surplusSnap(clk.Now(), -3000))
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().State).To(Equal(StateCharging))
			Expect(ctrl.Status().DesiredMode).To(Equal(config.EnableModePV))
		})
	})

	Describe("session lifecycle against the real DB", func() {
		It("opens an open charge_sessions row on the charging edge with start reason surplus", func() {
			snaps.set(healthySnap(t0))
			ctrl.tick(ctx, clk.Now())
			Expect(openSession()).To(BeNil())

			clk.Advance(cfg.DecisionInterval)
			snaps.set(chargingSnap(clk.Now(), 3000))
			ctrl.tick(ctx, clk.Now())

			open := openSession()
			Expect(open).NotTo(BeNil())
			Expect(open.StartReason).To(Equal("surplus"))
			Expect(open.EndedAt).To(BeNil())
		})

		It("opens with start reason override when the charge is forced on", func() {
			ctrl.SetOverride(OverrideForceOn, time.Time{}, false, 0)
			snaps.set(chargingSnap(t0, 3000))
			ctrl.tick(ctx, clk.Now())

			open := openSession()
			Expect(open).NotTo(BeNil())
			Expect(open.StartReason).To(Equal("override"))
		})

		It("accumulates energy and peak power per tick while charging", func() {
			snaps.set(chargingSnap(t0, 3000))
			ctrl.tick(ctx, clk.Now())
			first := openSession()
			Expect(first).NotTo(BeNil())
			Expect(first.EnergyWh).To(BeNumerically(">", 0))
			Expect(first.PeakChargeW).To(Equal(3000.0))

			clk.Advance(cfg.DecisionInterval)
			snaps.set(chargingSnap(clk.Now(), 5000))
			ctrl.tick(ctx, clk.Now())
			second := openSession()
			Expect(second.EnergyWh).To(BeNumerically(">", first.EnergyWh))
			Expect(second.PeakChargeW).To(Equal(5000.0))
		})

		DescribeTable("closes the session with the correct stop reason and final metrics",
			func(setup func(now time.Time), closing func(now time.Time) evcc.Snapshot, wantReason string) {
				// Open a session first.
				snaps.set(chargingSnap(t0, 3000))
				ctrl.tick(ctx, clk.Now())
				Expect(openSession()).NotTo(BeNil())

				// Apply the closing condition on the next tick.
				clk.Advance(cfg.DecisionInterval)
				if setup != nil {
					setup(clk.Now())
				}
				snaps.set(closing(clk.Now()))
				ctrl.tick(ctx, clk.Now())

				Expect(openSession()).To(BeNil())
				sessions, err := st.RecentSessions(ctx, 1)
				Expect(err).NotTo(HaveOccurred())
				Expect(sessions).To(HaveLen(1))
				Expect(sessions[0].StopReason).To(Equal(wantReason))
				Expect(sessions[0].EndedAt).NotTo(BeNil())
				Expect(sessions[0].EnergyWh).To(BeNumerically(">", 0))
			},
			Entry("surplus_low: charging stops with insufficient surplus", nil,
				healthySnap, "surplus_low"),
			Entry("soc_cap: vehicle reaches the cap", nil,
				func(now time.Time) evcc.Snapshot {
					s := healthySnap(now)
					s.VehicleSoC = evcc.FloatMetric{Value: 80, At: now, Seen: true}
					return s
				}, "soc_cap"),
			Entry("stale: broker disconnects", nil,
				func(now time.Time) evcc.Snapshot {
					s := healthySnap(now)
					s.BrokerConnected = false
					return s
				}, "stale"),
			Entry("override: operator forces off",
				func(_ time.Time) { ctrl.SetOverride(OverrideForceOff, time.Time{}, false, 0) },
				healthySnap, "override"),
		)

		It("closes a dangling session left open by a previous run on startup (restart)", func() {
			id, err := st.StartSession(ctx, t0.Add(-time.Hour), "surplus", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(st.UpdateSessionMetrics(ctx, id, 4200, 3000, 3500)).To(Succeed())

			snaps.set(healthySnap(t0)) // not charging → startup tick opens nothing new
			fresh := newCtrl()

			runCtx, cancel := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				defer close(done)
				_ = fresh.Run(runCtx)
			}()

			Eventually(openSession).Should(BeNil())
			cancel()
			Eventually(done).Should(BeClosed())

			sessions, err := st.RecentSessions(ctx, 1)
			Expect(err).NotTo(HaveOccurred())
			Expect(sessions).To(HaveLen(1))
			Expect(sessions[0].ID).To(Equal(id))
			Expect(sessions[0].StopReason).To(Equal("restart"))
			Expect(sessions[0].EnergyWh).To(Equal(4200.0))
		})
	})

	Describe("limitSoc backstop target", func() {
		It("targets SoCCap normally, lifts to SoCMax under a cap-bypass force-on, and restores on clear", func() {
			snaps.set(healthySnap(t0))
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.lastLimit()).To(Equal(cfg.SoCCap))

			ctrl.SetOverride(OverrideForceOn, time.Time{}, true, 0)
			clk.Advance(cfg.DecisionInterval)
			snaps.set(healthySnap(clk.Now()))
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.lastLimit()).To(Equal(cfg.SoCMax))

			ctrl.SetOverride(OverrideAuto, time.Time{}, false, 0)
			clk.Advance(cfg.DecisionInterval)
			snaps.set(healthySnap(clk.Now()))
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.lastLimit()).To(Equal(cfg.SoCCap))
		})
	})

	Describe("charge-power persistence across restart", func() {
		It("restores the persisted mode for a fresh controller over the same DB and publishes it", func() {
			Expect(ctrl.SetChargePower(config.EnableModeNow)).To(Succeed())

			v, ok, err := st.GetSetting(ctx, "charge_power")
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal(config.EnableModeNow))

			cmd2 := &fakeCommander{}
			fresh := New(cfg, cmd2, snaps, st, clk, discardLogger())
			Expect(fresh.Status().ChargePower).To(Equal(cfg.EnableMode)) // config default before load
			fresh.loadChargePower(ctx)
			Expect(fresh.Status().ChargePower).To(Equal(config.EnableModeNow))

			// Force on so it charges; the published mode must be the restored "now",
			// not the config default "pv".
			fresh.SetOverride(OverrideForceOn, time.Time{}, false, 0)
			snaps.set(healthySnap(t0))
			fresh.tick(ctx, clk.Now())
			Expect(fresh.Status().DesiredMode).To(Equal(config.EnableModeNow))
			Expect(latestMode(cmd2)).To(Equal(config.EnableModeNow))
		})
	})

	Describe("retention pruning", func() {
		It("prunes only samples and events older than the retention window", func() {
			cfg.RetentionWindow = 90 * 24 * time.Hour
			old := t0.Add(-cfg.RetentionWindow - time.Hour)
			recent := t0.Add(-time.Hour)

			Expect(st.InsertSample(ctx, store.Sample{TS: old, State: "idle"})).To(Succeed())
			Expect(st.InsertSample(ctx, store.Sample{TS: recent, State: "charging"})).To(Succeed())
			Expect(st.InsertEvent(ctx, store.Event{TS: old, Type: "state_change"})).To(Succeed())
			Expect(st.InsertEvent(ctx, store.Event{TS: recent, Type: "state_change"})).To(Succeed())

			before := t0.Add(-cfg.RetentionWindow)
			ns, err := st.PruneSamples(ctx, before)
			Expect(err).NotTo(HaveOccurred())
			Expect(ns).To(Equal(int64(1)))
			ne, err := st.PruneEvents(ctx, before)
			Expect(err).NotTo(HaveOccurred())
			Expect(ne).To(Equal(int64(1)))

			samples, err := st.Samples(ctx, t0.Add(-2*cfg.RetentionWindow), t0)
			Expect(err).NotTo(HaveOccurred())
			Expect(samples).To(HaveLen(1))
			Expect(samples[0].TS).To(BeTemporally("~", recent, time.Second))

			events, err := st.RecentEvents(ctx, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(events).To(HaveLen(1))
			Expect(events[0].TS).To(BeTemporally("~", recent, time.Second))
		})
	})
})
