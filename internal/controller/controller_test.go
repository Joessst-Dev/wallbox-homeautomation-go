package controller

import (
	"context"
	"errors"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

// ---- test doubles ----

type fakeCommander struct {
	modes     []string
	limitSoCs []int
	modeErr   error
	limitErr  error
}

func (f *fakeCommander) SetMode(mode string) error {
	if f.modeErr != nil {
		return f.modeErr
	}
	f.modes = append(f.modes, mode)
	return nil
}

func (f *fakeCommander) SetLimitSoC(pct int) error {
	if f.limitErr != nil {
		return f.limitErr
	}
	f.limitSoCs = append(f.limitSoCs, pct)
	return nil
}

type fakeSnaps struct {
	snap evcc.Snapshot
}

func (f *fakeSnaps) Snapshot() evcc.Snapshot { return f.snap }

type fakeEndCall struct {
	id          int64
	stopReason  string
	energyWh    float64
	avgChargeW  float64
	peakChargeW float64
}

type fakeRecorder struct {
	events      []store.Event
	samples     []store.Sample
	startCalls  int
	endCalls    []fakeEndCall
	openSession *store.Session
	nextID      int64
}

func (f *fakeRecorder) InsertEvent(_ context.Context, e store.Event) error {
	f.events = append(f.events, e)
	return nil
}

func (f *fakeRecorder) InsertSample(_ context.Context, sm store.Sample) error {
	f.samples = append(f.samples, sm)
	return nil
}

func (f *fakeRecorder) StartSession(_ context.Context, _ time.Time, _ string, _ *int) (int64, error) {
	f.nextID++
	f.startCalls++
	return f.nextID, nil
}

func (f *fakeRecorder) EndSession(_ context.Context, id int64, _ time.Time, stopReason string, _ *int, energyWh, avgChargeW, peakChargeW float64) error {
	f.endCalls = append(f.endCalls, fakeEndCall{
		id: id, stopReason: stopReason,
		energyWh: energyWh, avgChargeW: avgChargeW, peakChargeW: peakChargeW,
	})
	return nil
}

func (f *fakeRecorder) OpenSession(_ context.Context) (*store.Session, error) {
	return f.openSession, nil
}

// ---- helpers ----

func ctrlCfg() config.Control {
	return config.Control{
		EnableMode:       config.EnableModePV,
		StartThresholdW:  1400,
		StopThresholdW:   0,
		StartDwell:       60 * time.Second,
		StopDwell:        120 * time.Second,
		SoCCap:           80,
		SoCResumeBelow:   78,
		DecisionInterval: 15 * time.Second,
		StaleTimeout:     60 * time.Second,
		Republish:        5 * time.Minute,
	}
}

// controllerReadySnap returns a healthy, non-stale snapshot: broker connected, all
// required metrics fresh, vehicle connected, not actively charging.
func controllerReadySnap(now time.Time) evcc.Snapshot {
	return evcc.Snapshot{
		BrokerConnected: true,
		Online:          evcc.BoolMetric{Value: true, At: now, Seen: true},
		Grid:            evcc.FloatMetric{Value: -3000, At: now, Seen: true},
		PV:              evcc.FloatMetric{Value: 5000, At: now, Seen: true},
		Home:            evcc.FloatMetric{At: now, Seen: true},
		BatteryPower:    evcc.FloatMetric{At: now, Seen: true},
		BatterySoC:      evcc.FloatMetric{Value: 60, At: now, Seen: true},
		ChargePower:     evcc.FloatMetric{At: now, Seen: true},
		VehicleSoC:      evcc.FloatMetric{Value: 55, At: now, Seen: true},
		Connected:       evcc.BoolMetric{Value: true, At: now, Seen: true},
		Charging:        evcc.BoolMetric{Value: false, At: now, Seen: true},
	}
}

// controllerChargingSnap is like controllerReadySnap but with Charging=true and
// ChargePower set to chargeW.
func controllerChargingSnap(now time.Time, chargeW float64) evcc.Snapshot {
	s := controllerReadySnap(now)
	s.Charging = evcc.BoolMetric{Value: true, At: now, Seen: true}
	s.ChargePower = evcc.FloatMetric{Value: chargeW, At: now, Seen: true}
	return s
}

// ---- specs ----

var _ = Describe("Controller.tick side effects", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		clk    *FakeClock
		cmd    *fakeCommander
		snaps  *fakeSnaps
		rec    *fakeRecorder
		ctrl   *Controller
		t0     time.Time
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		t0 = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
		clk = NewFakeClock(t0)
		cmd = &fakeCommander{}
		snaps = &fakeSnaps{}
		rec = &fakeRecorder{}
		ctrl = New(ctrlCfg(), cmd, snaps, rec, clk, slog.Default())
	})

	AfterEach(func() { cancel() })

	// ---- limitSoc backstop ----

	Describe("limitSoc backstop", func() {
		It("publishes limitSoc on the first connected tick", func() {
			snaps.snap = controllerReadySnap(t0)
			ctrl.tick(ctx, t0)

			Expect(cmd.limitSoCs).To(ConsistOf(80))
		})

		It("does not publish while the broker is disconnected", func() {
			snap := controllerReadySnap(t0)
			snap.BrokerConnected = false
			snaps.snap = snap

			ctrl.tick(ctx, t0)

			Expect(cmd.limitSoCs).To(BeEmpty())
		})

		It("retries each tick until the first successful publish", func() {
			cmd.limitErr = errors.New("mqtt down")
			snaps.snap = controllerReadySnap(t0)
			ctrl.tick(ctx, t0)
			Expect(cmd.limitSoCs).To(BeEmpty())

			cmd.limitErr = nil
			ctrl.tick(ctx, t0) // retry: limitSoCSet is still false
			Expect(cmd.limitSoCs).To(HaveLen(1))
		})

		It("re-publishes on broker reconnect even within the Republish window", func() {
			snaps.snap = controllerReadySnap(t0)
			ctrl.tick(ctx, t0)
			Expect(cmd.limitSoCs).To(HaveLen(1))

			// Disconnect.
			disconn := controllerReadySnap(t0)
			disconn.BrokerConnected = false
			snaps.snap = disconn
			clk.Advance(10 * time.Second)
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.limitSoCs).To(HaveLen(1)) // no extra publish while disconnected

			// Reconnect — must re-publish immediately.
			clk.Advance(10 * time.Second)
			snaps.snap = controllerReadySnap(clk.Now())
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.limitSoCs).To(HaveLen(2))
		})

		It("re-publishes after the Republish cadence elapses", func() {
			snaps.snap = controllerReadySnap(t0)
			ctrl.tick(ctx, t0)
			Expect(cmd.limitSoCs).To(HaveLen(1))

			clk.Advance(5 * time.Minute)
			snaps.snap = controllerReadySnap(clk.Now())
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.limitSoCs).To(HaveLen(2))
		})
	})

	// ---- publishMode cadence ----

	Describe("publishMode", func() {
		It("publishes mode on the first tick", func() {
			snaps.snap = controllerReadySnap(t0)
			ctrl.tick(ctx, t0)

			Expect(cmd.modes).NotTo(BeEmpty())
		})

		It("suppresses redundant publishes within the Republish window", func() {
			snaps.snap = controllerReadySnap(t0)
			ctrl.tick(ctx, t0)
			after1 := len(cmd.modes)

			clk.Advance(10 * time.Second)
			ctrl.tick(ctx, clk.Now())

			Expect(cmd.modes).To(HaveLen(after1))
		})

		It("re-publishes after the Republish cadence even when mode is unchanged", func() {
			snaps.snap = controllerReadySnap(t0)
			ctrl.tick(ctx, t0)

			clk.Advance(5 * time.Minute)
			snaps.snap = controllerReadySnap(clk.Now())
			ctrl.tick(ctx, clk.Now())

			Expect(len(cmd.modes)).To(BeNumerically(">=", 2))
		})

		It("re-publishes immediately on broker reconnect", func() {
			snaps.snap = controllerReadySnap(t0)
			ctrl.tick(ctx, t0)
			after1 := len(cmd.modes)

			// Disconnect.
			disc := controllerReadySnap(t0)
			disc.BrokerConnected = false
			snaps.snap = disc
			clk.Advance(10 * time.Second)
			ctrl.tick(ctx, clk.Now())

			// Reconnect — must re-publish even though mode is unchanged.
			clk.Advance(10 * time.Second)
			snaps.snap = controllerReadySnap(clk.Now())
			ctrl.tick(ctx, clk.Now())

			Expect(len(cmd.modes)).To(BeNumerically(">", after1))
		})
	})

	// ---- session open/close edges ----

	Describe("session tracking", func() {
		It("opens a session on the Charging false→true edge", func() {
			// Non-charging tick establishes prevCharging=false.
			snaps.snap = controllerReadySnap(t0)
			ctrl.tick(ctx, t0)
			Expect(rec.startCalls).To(Equal(0))

			// Charging edge: StartSession must be called.
			clk.Advance(15 * time.Second)
			snaps.snap = controllerChargingSnap(clk.Now(), 3000)
			ctrl.tick(ctx, clk.Now())
			Expect(rec.startCalls).To(Equal(1))
		})

		It("closes the session on the Charging true→false edge", func() {
			// Start charging (prevCharging was false).
			snaps.snap = controllerChargingSnap(t0, 3000)
			ctrl.tick(ctx, t0)
			Expect(rec.startCalls).To(Equal(1))

			// Stop charging.
			clk.Advance(15 * time.Second)
			snaps.snap = controllerReadySnap(clk.Now())
			ctrl.tick(ctx, clk.Now())
			Expect(rec.endCalls).To(HaveLen(1))
		})

		It("accumulates energy over multiple ticks and reports correct totals at session close", func() {
			chargeW := 3000.0
			dt := ctrlCfg().DecisionInterval.Hours() // 15 s in hours

			// Three consecutive charging ticks.
			for i := 0; i < 3; i++ {
				snaps.snap = controllerChargingSnap(clk.Now(), chargeW)
				ctrl.tick(ctx, clk.Now())
				clk.Advance(15 * time.Second)
			}
			// Charging stops.
			snaps.snap = controllerReadySnap(clk.Now())
			ctrl.tick(ctx, clk.Now())

			Expect(rec.endCalls).To(HaveLen(1))
			end := rec.endCalls[0]
			Expect(end.energyWh).To(BeNumerically("~", chargeW*dt*3, 0.001))
			Expect(end.avgChargeW).To(BeNumerically("~", chargeW, 0.001))
			Expect(end.peakChargeW).To(BeNumerically("~", chargeW, 0.001))
		})
	})

	// ---- override auto-expiry ----

	Describe("override auto-expiry", func() {
		It("resets a timed override to Auto once its expiry passes", func() {
			ctrl.SetOverride(OverrideForceOff, t0.Add(time.Minute))

			// Before expiry: override still active.
			snaps.snap = controllerReadySnap(t0)
			ctrl.tick(ctx, t0)
			Expect(ctrl.Status().Override).To(Equal(OverrideForceOff))

			// After expiry: must revert to Auto.
			clk.Set(t0.Add(2 * time.Minute))
			snaps.snap = controllerReadySnap(clk.Now())
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().Override).To(Equal(OverrideAuto))
		})

		It("preserves a no-expiry override indefinitely", func() {
			ctrl.SetOverride(OverrideForceOn, time.Time{}) // zero = never expires

			clk.Advance(24 * time.Hour)
			snaps.snap = controllerReadySnap(clk.Now())
			ctrl.tick(ctx, clk.Now())

			Expect(ctrl.Status().Override).To(Equal(OverrideForceOn))
		})
	})

	// ---- dangling session recovery ----

	Describe("recoverDanglingSession", func() {
		It("closes a session left open from a previous run, using 'restart' as the stop reason", func() {
			rec.openSession = &store.Session{
				ID:          42,
				StartedAt:   t0.Add(-time.Hour),
				EnergyWh:    1200,
				AvgChargeW:  3000,
				PeakChargeW: 3600,
			}

			ctrl.recoverDanglingSession(ctx)

			Expect(rec.endCalls).To(HaveLen(1))
			Expect(rec.endCalls[0].id).To(Equal(int64(42)))
			Expect(rec.endCalls[0].stopReason).To(Equal("restart"))
		})

		It("does nothing when there is no open session", func() {
			rec.openSession = nil
			ctrl.recoverDanglingSession(ctx)
			Expect(rec.endCalls).To(BeEmpty())
		})
	})
})
