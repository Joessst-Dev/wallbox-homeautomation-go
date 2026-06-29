package controller

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

// --- fakes ---------------------------------------------------------------

// fakeCommander records SetMode / SetLimitSoC calls and can be told to fail.
type fakeCommander struct {
	mu         sync.Mutex
	modes      []string
	limitCalls int
	limits     []int
	failLimit  bool
}

func (f *fakeCommander) SetMode(mode string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modes = append(f.modes, mode)
	return nil
}

func (f *fakeCommander) SetLimitSoC(pct int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.limitCalls++
	if f.failLimit {
		return errFake
	}
	f.limits = append(f.limits, pct)
	return nil
}

func (f *fakeCommander) lastLimit() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.limits) == 0 {
		return -1
	}
	return f.limits[len(f.limits)-1]
}

func (f *fakeCommander) limitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.limitCalls
}

func (f *fakeCommander) modeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.modes)
}

// fakeSnaps returns whatever snapshot it is told to.
type fakeSnaps struct {
	mu   sync.Mutex
	snap evcc.Snapshot
}

func (f *fakeSnaps) set(s evcc.Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snap = s
}

func (f *fakeSnaps) Snapshot() evcc.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}

// fakeRecorder implements the full Recorder interface in memory.
type fakeRecorder struct {
	mu sync.Mutex

	events  []store.Event
	samples []store.Sample

	nextID  int64
	open    *store.Session
	closed  []store.Session
	updates int

	openOnStart *store.Session // returned by the first OpenSession (recovery)

	pruneSamplesCalls int
	pruneEventsCalls  int
	lastPruneBefore   time.Time

	settings map[string]string
}

func (r *fakeRecorder) InsertEvent(_ context.Context, e store.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *fakeRecorder) InsertSample(_ context.Context, sm store.Sample) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.samples = append(r.samples, sm)
	return nil
}

func (r *fakeRecorder) StartSession(_ context.Context, startedAt time.Time, reason string, soc *int) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	r.open = &store.Session{ID: r.nextID, StartedAt: startedAt, StartReason: reason, StartVehicleSoC: soc}
	return r.nextID, nil
}

func (r *fakeRecorder) UpdateSessionMetrics(_ context.Context, id int64, energyWh, avg, peak float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates++
	if r.open != nil && r.open.ID == id {
		r.open.EnergyWh = energyWh
		r.open.AvgChargeW = avg
		r.open.PeakChargeW = peak
	}
	return nil
}

func (r *fakeRecorder) EndSession(_ context.Context, id int64, endedAt time.Time, reason string, soc *int, energyWh, avg, peak float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	et := endedAt
	r.closed = append(r.closed, store.Session{
		ID: id, EndedAt: &et, StopReason: reason, EndVehicleSoC: soc,
		EnergyWh: energyWh, AvgChargeW: avg, PeakChargeW: peak,
	})
	if r.open != nil && r.open.ID == id {
		r.open = nil
	}
	return nil
}

func (r *fakeRecorder) OpenSession(_ context.Context) (*store.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.openOnStart != nil {
		s := r.openOnStart
		r.openOnStart = nil
		return s, nil
	}
	return nil, nil
}

func (r *fakeRecorder) PruneSamples(_ context.Context, before time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneSamplesCalls++
	r.lastPruneBefore = before
	return 0, nil
}

func (r *fakeRecorder) PruneEvents(_ context.Context, before time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneEventsCalls++
	r.lastPruneBefore = before
	return 0, nil
}

func (r *fakeRecorder) GetSetting(_ context.Context, key string) (string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.settings[key]
	return v, ok, nil
}

func (r *fakeRecorder) SetSetting(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.settings == nil {
		r.settings = map[string]string{}
	}
	r.settings[key] = value
	return nil
}

func (r *fakeRecorder) updateCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.updates
}

var errFake = errFakeType("boom")

type errFakeType string

func (e errFakeType) Error() string { return string(e) }

// --- helpers -------------------------------------------------------------

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// healthySnap is a fresh, connected, online snapshot that yields a not-stale,
// ready, connected Inputs.
func healthySnap(now time.Time) evcc.Snapshot {
	fresh := evcc.FloatMetric{Value: 0, At: now, Seen: true}
	return evcc.Snapshot{
		BrokerConnected: true,
		Online:          evcc.BoolMetric{Value: true, At: now, Seen: true},
		Grid:            evcc.FloatMetric{Value: 0, At: now, Seen: true},
		PV:              evcc.FloatMetric{Value: 0, At: now, Seen: true},
		Home:            fresh,
		BatteryPower:    fresh,
		BatterySoC:      evcc.FloatMetric{Value: 50, At: now, Seen: true},
		ChargePower:     fresh,
		VehicleSoC:      evcc.FloatMetric{Value: 50, At: now, Seen: true},
		Connected:       evcc.BoolMetric{Value: true, At: now, Seen: true},
	}
}

// --- specs ---------------------------------------------------------------

var _ = Describe("socPtr", func() {
	DescribeTable("maps (value, known) to a pointer or nil",
		func(v int, known bool, expectNil bool, expectVal int) {
			got := socPtr(v, known)
			if expectNil {
				Expect(got).To(BeNil())
				return
			}
			Expect(got).NotTo(BeNil())
			Expect(*got).To(Equal(expectVal))
		},
		Entry("unknown zero → nil", 0, false, true, 0),
		Entry("unknown nonzero → nil", 42, false, true, 0),
		Entry("known zero → *0", 0, true, false, 0),
		Entry("known eighty → *80", 80, true, false, 80),
	)
})

var _ = Describe("Controller.tick side effects", func() {
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

	BeforeEach(func() {
		cfg = config.Default().Control
		cfg.StartDwell = 0 // start charging the moment surplus is sufficient
		cfg.StopDwell = 0
		cmd = &fakeCommander{}
		snaps = &fakeSnaps{}
		rec = &fakeRecorder{}
		t0 = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
		clk = NewFakeClock(t0)
		ctx = context.Background()
		ctrl = New(cfg, cmd, snaps, rec, clk, discardLogger())
	})

	Describe("limitSoc backstop", func() {
		It("publishes once on first broker connect and retries until success", func() {
			cmd.failLimit = true
			snaps.set(healthySnap(t0))

			ctrl.tick(ctx, clk.Now())
			Expect(cmd.limitCount()).To(Equal(1)) // attempted

			clk.Advance(cfg.DecisionInterval)
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.limitCount()).To(Equal(2)) // retried (still failing)

			cmd.failLimit = false
			clk.Advance(cfg.DecisionInterval)
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.limitCount()).To(Equal(3)) // succeeds

			// Now that it is set, it should not re-publish on the next plain tick.
			clk.Advance(cfg.DecisionInterval)
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.limitCount()).To(Equal(3))
		})

		It("re-publishes on a broker reconnect", func() {
			snaps.set(healthySnap(t0))
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.limitCount()).To(Equal(1))

			// Broker drops.
			down := healthySnap(t0)
			down.BrokerConnected = false
			snaps.set(down)
			clk.Advance(cfg.DecisionInterval)
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.limitCount()).To(Equal(1)) // not published while disconnected

			// Broker back: reconnect edge forces a re-publish.
			snaps.set(healthySnap(clk.Now()))
			clk.Advance(cfg.DecisionInterval)
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.limitCount()).To(Equal(2))
		})

		It("re-publishes on the evcc online (LWT) edge without a broker drop", func() {
			snaps.set(healthySnap(t0))
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.limitCount()).To(Equal(1))

			// evcc goes offline (broker stays up).
			off := healthySnap(clk.Now())
			off.Online = evcc.BoolMetric{Value: false, At: clk.Now(), Seen: true}
			snaps.set(off)
			clk.Advance(cfg.DecisionInterval)
			ctrl.tick(ctx, clk.Now())
			// broker still up (brokerConnected=true), so the backstop publish is
			// gated by limitSoCSet && !force && cadence-not-elapsed — not staleness:
			// Online going false is not an online edge, so force stays false. Still 1.
			Expect(cmd.limitCount()).To(Equal(1))

			// evcc back online → online edge forces a re-publish.
			snaps.set(healthySnap(clk.Now()))
			clk.Advance(cfg.DecisionInterval)
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.limitCount()).To(Equal(2))
		})

		It("re-publishes after the Republish cadence", func() {
			snaps.set(healthySnap(t0))
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.limitCount()).To(Equal(1))

			clk.Advance(cfg.Republish + time.Second)
			snaps.set(healthySnap(clk.Now()))
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.limitCount()).To(Equal(2))
		})

		It("lifts to SoCMax for a cap-bypass force-on and restores the cap when it clears", func() {
			snaps.set(healthySnap(t0))
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.lastLimit()).To(Equal(cfg.SoCCap)) // normal backstop

			// Operator forces on and opts to charge past the cap.
			ctrl.SetOverride(OverrideForceOn, time.Time{}, true)
			clk.Advance(cfg.DecisionInterval)
			snaps.set(healthySnap(clk.Now()))
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.lastLimit()).To(Equal(cfg.SoCMax)) // lifted, republished on change

			// Back to auto: the cap is restored immediately (target change), not only
			// after the republish cadence.
			ctrl.SetOverride(OverrideAuto, time.Time{}, false)
			clk.Advance(cfg.DecisionInterval)
			snaps.set(healthySnap(clk.Now()))
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.lastLimit()).To(Equal(cfg.SoCCap))
		})
	})

	Describe("charge power", func() {
		It("defaults to the configured enable mode", func() {
			Expect(ctrl.Status().ChargePower).To(Equal(cfg.EnableMode))
		})

		It("SetChargePower validates, updates Status, and persists", func() {
			Expect(ctrl.SetChargePower("bogus")).NotTo(Succeed())

			Expect(ctrl.SetChargePower(config.EnableModeNow)).To(Succeed())
			Expect(ctrl.Status().ChargePower).To(Equal(config.EnableModeNow))

			v, ok, err := rec.GetSetting(ctx, "charge_power")
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal(config.EnableModeNow))
		})

		It("restores the persisted mode at startup via loadChargePower", func() {
			rec.settings = map[string]string{"charge_power": config.EnableModeNow}
			fresh := New(cfg, cmd, snaps, rec, clk, discardLogger())
			Expect(fresh.Status().ChargePower).To(Equal(cfg.EnableMode)) // not loaded yet

			fresh.loadChargePower(ctx)
			Expect(fresh.Status().ChargePower).To(Equal(config.EnableModeNow))
		})
	})

	Describe("mode publishing", func() {
		It("publishes on change but not on an unchanged plain tick", func() {
			snaps.set(healthySnap(t0))
			ctrl.tick(ctx, clk.Now()) // first publish (off)
			Expect(cmd.modeCount()).To(Equal(1))

			clk.Advance(cfg.DecisionInterval)
			snaps.set(healthySnap(clk.Now()))
			ctrl.tick(ctx, clk.Now()) // unchanged → no publish
			Expect(cmd.modeCount()).To(Equal(1))
		})

		It("re-publishes the mode on the online edge", func() {
			snaps.set(healthySnap(t0))
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.modeCount()).To(Equal(1))

			off := healthySnap(clk.Now())
			off.Online = evcc.BoolMetric{Value: false, At: clk.Now(), Seen: true}
			snaps.set(off)
			clk.Advance(cfg.DecisionInterval)
			ctrl.tick(ctx, clk.Now()) // still off mode, but stale; no edge yet

			snaps.set(healthySnap(clk.Now()))
			clk.Advance(cfg.DecisionInterval)
			before := cmd.modeCount()
			ctrl.tick(ctx, clk.Now()) // online edge → forced re-publish
			Expect(cmd.modeCount()).To(Equal(before + 1))
		})
	})

	Describe("session tracking", func() {
		chargingSnap := func(now time.Time, chargeW float64) evcc.Snapshot {
			s := healthySnap(now)
			s.Grid = evcc.FloatMetric{Value: -5000, At: now, Seen: true}
			s.ChargePower = evcc.FloatMetric{Value: chargeW, At: now, Seen: true}
			s.Charging = evcc.BoolMetric{Value: true, At: now, Seen: true}
			return s
		}

		It("opens on the charging edge, accumulates energy, flushes metrics, and closes", func() {
			// Not charging yet.
			snaps.set(healthySnap(t0))
			ctrl.tick(ctx, clk.Now())
			Expect(rec.open).To(BeNil())

			// Charging false→true edge: session opens.
			clk.Advance(cfg.DecisionInterval)
			snaps.set(chargingSnap(clk.Now(), 3000))
			ctrl.tick(ctx, clk.Now())
			Expect(rec.open).NotTo(BeNil())
			Expect(rec.open.EnergyWh).To(BeNumerically(">", 0))
			Expect(rec.updateCount()).To(Equal(1)) // flushed this tick

			// Another charging tick accumulates more and flushes again.
			clk.Advance(cfg.DecisionInterval)
			snaps.set(chargingSnap(clk.Now(), 3000))
			ctrl.tick(ctx, clk.Now())
			Expect(rec.updateCount()).To(Equal(2))
			Expect(rec.open.PeakChargeW).To(Equal(3000.0))

			// Charging true→false edge: session closes.
			clk.Advance(cfg.DecisionInterval)
			snaps.set(healthySnap(clk.Now()))
			ctrl.tick(ctx, clk.Now())
			Expect(rec.open).To(BeNil())
			Expect(rec.closed).To(HaveLen(1))
			Expect(rec.closed[0].EnergyWh).To(BeNumerically(">", 0))
		})
	})

	Describe("override auto-expiry (TOCTOU)", func() {
		It("resets to Auto when an expiring override lapses", func() {
			snaps.set(healthySnap(t0))
			ctrl.SetOverride(OverrideForceOff, t0.Add(time.Minute), false)
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().Override).To(Equal(OverrideForceOff))

			clk.Advance(2 * time.Minute)
			snaps.set(healthySnap(clk.Now()))
			ctrl.tick(ctx, clk.Now())
			Expect(ctrl.Status().Override).To(Equal(OverrideAuto))
		})

		It("never silently discards a concurrent SetOverride that races the expiry write-back", func() {
			// Across many interleavings of tick (which would expire a stale override)
			// and a manual SetOverride that lands in the read→write-back gap, the
			// manual override must always win. The only way the final value could be
			// Auto is the TOCTOU bug; the guard makes the outcome deterministic.
			snaps.set(healthySnap(t0))
			for i := range 300 {
				// Seed an already-expired ForceOn that a buggy tick would clear.
				ctrl.SetOverride(OverrideForceOn, t0.Add(-time.Minute), false)

				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					ctrl.tick(ctx, clk.Now())
				}()
				go func() {
					defer wg.Done()
					ctrl.SetOverride(OverrideForceOff, time.Time{}, false) // the winner
				}()
				wg.Wait()

				Expect(ctrl.Status().Override).To(Equal(OverrideForceOff),
					"manual override lost to a stale-read expiry on iteration %d", i)
			}
		})
	})

	Describe("dangling-session recovery", func() {
		It("closes a session left open by a previous run with its stored metrics", func() {
			rec.openOnStart = &store.Session{ID: 99, StartedAt: t0.Add(-time.Hour), EnergyWh: 4200, AvgChargeW: 3000, PeakChargeW: 3500}
			ctrl.recoverDanglingSession(ctx)
			Expect(rec.closed).To(HaveLen(1))
			Expect(rec.closed[0].ID).To(Equal(int64(99)))
			Expect(rec.closed[0].StopReason).To(Equal("restart"))
			Expect(rec.closed[0].EnergyWh).To(Equal(4200.0))
		})
	})

	Describe("janitor wiring", func() {
		It("prunes on its interval and stops on context cancel", func() {
			cfg.RetentionWindow = 24 * time.Hour
			cfg.RetentionInterval = 10 * time.Millisecond
			ctrl = New(cfg, cmd, snaps, rec, clk, discardLogger())

			cctx, cancel := context.WithCancel(ctx)
			go ctrl.janitor(cctx)

			Eventually(func() int {
				rec.mu.Lock()
				defer rec.mu.Unlock()
				return rec.pruneSamplesCalls
			}).Should(BeNumerically(">", 0))
			Eventually(func() int {
				rec.mu.Lock()
				defer rec.mu.Unlock()
				return rec.pruneEventsCalls
			}).Should(BeNumerically(">", 0))

			rec.mu.Lock()
			gotBefore := rec.lastPruneBefore
			rec.mu.Unlock()
			Expect(gotBefore).To(Equal(clk.Now().Add(-cfg.RetentionWindow)))

			cancel()
		})
	})
})
