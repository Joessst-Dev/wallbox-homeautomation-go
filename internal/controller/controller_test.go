package controller_test

import (
	"context"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/controller"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

// fakeCmd satisfies the Commander interface without doing anything.
type fakeCmd struct{}

func (f *fakeCmd) SetMode(_ string) error  { return nil }
func (f *fakeCmd) SetLimitSoC(_ int) error { return nil }

// fakeSnaps wraps a single Snapshot for the SnapshotProvider interface.
type fakeSnaps struct{ snap evcc.Snapshot }

func (f *fakeSnaps) Snapshot() evcc.Snapshot { return f.snap }

// fakeRec is an in-memory Recorder that captures FlushSession calls.
type fakeRec struct {
	nextID      int64
	openSession *store.Session
	flushCalls  []flushArg
}

type flushArg struct {
	id          int64
	energyWh    float64
	avgChargeW  float64
	peakChargeW float64
}

func (r *fakeRec) InsertEvent(_ context.Context, _ store.Event) error   { return nil }
func (r *fakeRec) InsertSample(_ context.Context, _ store.Sample) error { return nil }

func (r *fakeRec) StartSession(_ context.Context, startedAt time.Time, reason string, soc *int) (int64, error) {
	r.nextID++
	r.openSession = &store.Session{
		ID: r.nextID, StartedAt: startedAt, StartReason: reason, StartVehicleSoC: soc,
	}
	return r.nextID, nil
}

func (r *fakeRec) EndSession(_ context.Context, id int64, endedAt time.Time, reason string, soc *int, energyWh, avgChargeW, peakChargeW float64) error {
	if r.openSession != nil && r.openSession.ID == id {
		r.openSession.EndedAt = &endedAt
		r.openSession.StopReason = reason
		r.openSession.EnergyWh = energyWh
		r.openSession.AvgChargeW = avgChargeW
		r.openSession.PeakChargeW = peakChargeW
	}
	return nil
}

func (r *fakeRec) FlushSession(_ context.Context, id int64, energyWh, avgChargeW, peakChargeW float64) error {
	r.flushCalls = append(r.flushCalls, flushArg{id, energyWh, avgChargeW, peakChargeW})
	if r.openSession != nil && r.openSession.ID == id {
		r.openSession.EnergyWh = energyWh
		r.openSession.AvgChargeW = avgChargeW
		r.openSession.PeakChargeW = peakChargeW
	}
	return nil
}

func (r *fakeRec) OpenSession(_ context.Context) (*store.Session, error) {
	if r.openSession != nil && r.openSession.EndedAt == nil {
		return r.openSession, nil
	}
	return nil, nil
}

// newTestController returns a controller wired up with controllable fakes.
func newTestController(cfg config.Control, snaps *fakeSnaps, rec *fakeRec, clk *controller.FakeClock) *controller.Controller {
	return controller.New(cfg, &fakeCmd{}, snaps, rec, clk, slog.Default())
}

var _ = Describe("Controller session flush", func() {
	var (
		cfg   config.Control
		snaps *fakeSnaps
		rec   *fakeRec
		clk   *controller.FakeClock
		ctrl  *controller.Controller
		ctx   context.Context
		t0    time.Time
	)

	BeforeEach(func() {
		t0 = time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)
		clk = controller.NewFakeClock(t0)
		cfg = baseConfig()
		rec = &fakeRec{}

		// Build a snapshot that looks like the broker is connected and charging
		// is active with a 3 kW load.
		snaps = &fakeSnaps{snap: evcc.Snapshot{
			BrokerConnected: true,
			Online:          evcc.BoolMetric{Value: true, Seen: true},
			Grid:            evcc.FloatMetric{Value: -1000, Seen: true, At: t0},
			PV:              evcc.FloatMetric{Value: 4000, Seen: true, At: t0},
			ChargePower:     evcc.FloatMetric{Value: 3000, Seen: true, At: t0},
			Charging:        evcc.BoolMetric{Value: true, Seen: true},
			Connected:       evcc.BoolMetric{Value: true, Seen: true},
			VehicleSoC:      evcc.FloatMetric{Value: 50, Seen: true},
		}}

		ctx = context.Background()
		ctrl = newTestController(cfg, snaps, rec, clk)
	})

	Describe("FlushSession during charging", func() {
		It("calls FlushSession after each tick that accumulates energy", func() {
			// First tick: no previous charging state → starts a session but the
			// current tick is the first accumulation tick.
			ctrl.Tick(ctx, clk.Now())
			Expect(rec.flushCalls).To(HaveLen(1))
			Expect(rec.flushCalls[0].energyWh).To(BeNumerically(">", 0))

			// Second tick: another accumulation → another flush with a larger value.
			clk.Advance(cfg.DecisionInterval)
			snaps.snap.Grid.At = clk.Now()
			snaps.snap.PV.At = clk.Now()
			snaps.snap.ChargePower.At = clk.Now()
			ctrl.Tick(ctx, clk.Now())
			Expect(rec.flushCalls).To(HaveLen(2))
			Expect(rec.flushCalls[1].energyWh).To(BeNumerically(">", rec.flushCalls[0].energyWh))
		})

		It("does not call FlushSession when stale", func() {
			// Make the snapshot stale by back-dating the fast power metrics.
			stalePast := t0.Add(-2 * cfg.StaleTimeout)
			snaps.snap.Grid.At = stalePast
			snaps.snap.PV.At = stalePast
			snaps.snap.ChargePower.At = stalePast

			ctrl.Tick(ctx, clk.Now())
			Expect(rec.flushCalls).To(BeEmpty())
		})

		It("does not call FlushSession when not charging", func() {
			snaps.snap.Charging.Value = false
			ctrl.Tick(ctx, clk.Now())
			Expect(rec.flushCalls).To(BeEmpty())
		})

		It("persists flushed values so OpenSession reflects running totals", func() {
			ctrl.Tick(ctx, clk.Now())
			Expect(rec.flushCalls).NotTo(BeEmpty())

			open, err := rec.OpenSession(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(open).NotTo(BeNil())
			Expect(open.EnergyWh).To(BeNumerically(">", 0))
			Expect(open.PeakChargeW).To(BeNumerically(">", 0))
		})
	})
})
