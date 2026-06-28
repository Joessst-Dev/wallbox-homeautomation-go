package controller_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/controller"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

// trackingCommander records how many times each publish method is called.
type trackingCommander struct {
	setModeCalls     atomic.Int64
	setLimitSoCCalls atomic.Int64
	lastMode         atomic.Value
}

func (f *trackingCommander) SetMode(mode string) error {
	f.setModeCalls.Add(1)
	f.lastMode.Store(mode)
	return nil
}

func (f *trackingCommander) SetLimitSoC(_ int) error {
	f.setLimitSoCCalls.Add(1)
	return nil
}

// noopRecorder satisfies the Recorder interface with no-op stubs.
type noopRecorder struct {
	nextID int64
}

func (r *noopRecorder) InsertEvent(_ context.Context, _ store.Event) error   { return nil }
func (r *noopRecorder) InsertSample(_ context.Context, _ store.Sample) error { return nil }
func (r *noopRecorder) OpenSession(_ context.Context) (*store.Session, error) {
	return nil, nil
}
func (r *noopRecorder) StartSession(_ context.Context, _ time.Time, _ string, _ *int) (int64, error) {
	r.nextID++
	return r.nextID, nil
}
func (r *noopRecorder) EndSession(_ context.Context, _ int64, _ time.Time, _ string, _ *int, _, _, _ float64) error {
	return nil
}

// staticSnapProvider always returns the same snapshot.
type staticSnapProvider struct {
	snap evcc.Snapshot
}

func (p *staticSnapProvider) Snapshot() evcc.Snapshot { return p.snap }

// baseSnap returns a healthy, non-stale snapshot with no surplus (all power zero,
// Online=true). The EV is disconnected so no session is opened.
func baseSnap(now time.Time) evcc.Snapshot {
	fresh := evcc.FloatMetric{At: now, Seen: true}
	return evcc.Snapshot{
		BrokerConnected: true,
		Online:          evcc.BoolMetric{Value: true, At: now, Seen: true},
		Grid:            evcc.FloatMetric{Value: 500, At: now, Seen: true}, // importing, no surplus
		PV:              evcc.FloatMetric{Value: 0, At: now, Seen: true},
		Home:            fresh,
		BatteryPower:    fresh,
		BatterySoC:      evcc.FloatMetric{Value: 80, At: now, Seen: true},
		ChargePower:     fresh,
		VehicleSoC:      evcc.FloatMetric{Value: 50, At: now, Seen: true},
		Connected:       evcc.BoolMetric{Value: false, At: now, Seen: true},
		Charging:        evcc.BoolMetric{Value: false, At: now, Seen: true},
	}
}

var _ = Describe("Controller evcc online (LWT) edge", func() {
	var (
		ctx context.Context
		t0  time.Time
		cfg config.Control
	)

	BeforeEach(func() {
		ctx = context.Background()
		t0 = time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)
		cfg = config.Default().Control
		cfg.Republish = 5 * time.Minute // long cadence — republish within cadence must be triggered by Online edge
	})

	It("re-publishes the desired mode immediately when evcc comes back online", func() {
		cmd := &trackingCommander{}
		snp := &staticSnapProvider{snap: baseSnap(t0)}
		clk := controller.NewFakeClock(t0)
		c := controller.New(cfg, cmd, snp, &noopRecorder{}, clk, slog.Default())

		// Tick 1 (t0): evcc online, no surplus → mode="off" published.
		c.Tick(ctx, t0)
		afterFirstTick := cmd.setModeCalls.Load()
		Expect(afterFirstTick).To(BeNumerically(">=", 1))

		// Tick 2: evcc goes offline via LWT — broker stays up.
		t1 := t0.Add(2 * time.Second)
		snap2 := baseSnap(t1)
		snap2.Online = evcc.BoolMetric{Value: false, At: t1, Seen: true}
		// Grid/PV remain fresh so stale is driven by Online=false only.
		snap2.Grid.At = t1
		snap2.PV.At = t1
		snp.snap = snap2
		c.Tick(ctx, t1)
		// Stale (Online=false) → failsafe → mode unchanged ("off"). SetMode
		// may or may not fire depending on state transition; we don't care.

		// Tick 3: evcc restarts and comes back online. Still within Republish cadence.
		t2 := t1.Add(3 * time.Second) // total: 5s < 5min Republish
		snap3 := baseSnap(t2)
		snap3.Online = evcc.BoolMetric{Value: true, At: t2, Seen: true}
		snap3.Grid.At = t2
		snap3.PV.At = t2
		snp.snap = snap3
		beforeTick3 := cmd.setModeCalls.Load()
		c.Tick(ctx, t2)

		// The Online false→true edge must have triggered an immediate SetMode
		// even though the Republish cadence has not elapsed.
		Expect(cmd.setModeCalls.Load()).To(BeNumerically(">", beforeTick3),
			"SetMode must be called on evcc online edge (within Republish cadence)")
	})

	It("re-publishes the limitSoc backstop immediately when evcc comes back online", func() {
		cmd := &trackingCommander{}
		snp := &staticSnapProvider{snap: baseSnap(t0)}
		clk := controller.NewFakeClock(t0)
		c := controller.New(cfg, cmd, snp, &noopRecorder{}, clk, slog.Default())

		// Tick 1: initial publish — limitSoc backstop is set.
		c.Tick(ctx, t0)
		Expect(cmd.setLimitSoCCalls.Load()).To(BeNumerically(">=", 1))

		// Tick 2: evcc goes offline. limitSoCSet=true and within cadence → no republish.
		t1 := t0.Add(2 * time.Second)
		snap2 := baseSnap(t1)
		snap2.Online = evcc.BoolMetric{Value: false, At: t1, Seen: true}
		snap2.Grid.At = t1
		snap2.PV.At = t1
		snp.snap = snap2
		c.Tick(ctx, t1)
		afterOffline := cmd.setLimitSoCCalls.Load()

		// Tick 3: evcc comes back online within Republish cadence.
		t2 := t1.Add(3 * time.Second)
		snap3 := baseSnap(t2)
		snap3.Online = evcc.BoolMetric{Value: true, At: t2, Seen: true}
		snap3.Grid.At = t2
		snap3.PV.At = t2
		snp.snap = snap3
		c.Tick(ctx, t2)

		// limitSoc must be re-asserted on the Online edge without waiting for Republish.
		Expect(cmd.setLimitSoCCalls.Load()).To(BeNumerically(">", afterOffline),
			"SetLimitSoC must be called on evcc online edge (within Republish cadence)")
	})

	It("does not treat a steady online→online tick as a restart", func() {
		cmd := &trackingCommander{}
		snp := &staticSnapProvider{snap: baseSnap(t0)}
		clk := controller.NewFakeClock(t0)
		c := controller.New(cfg, cmd, snp, &noopRecorder{}, clk, slog.Default())

		// Tick 1: initial publish.
		c.Tick(ctx, t0)
		afterFirst := cmd.setModeCalls.Load()
		limitAfterFirst := cmd.setLimitSoCCalls.Load()

		// Tick 2 (still online, within Republish, mode unchanged) → no extra publishes.
		t1 := t0.Add(2 * time.Second)
		snap2 := baseSnap(t1)
		snap2.Grid.At = t1
		snap2.PV.At = t1
		snp.snap = snap2
		c.Tick(ctx, t1)

		Expect(cmd.setModeCalls.Load()).To(Equal(afterFirst),
			"SetMode must not fire on steady online→online ticks within cadence")
		Expect(cmd.setLimitSoCCalls.Load()).To(Equal(limitAfterFirst),
			"SetLimitSoC must not fire on steady online→online ticks within cadence")
	})
})
