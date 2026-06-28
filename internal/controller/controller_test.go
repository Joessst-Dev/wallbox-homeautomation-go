package controller_test

import (
	"context"
	"log/slog"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/controller"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

// fakeCommander satisfies the Commander interface, no-op.
type fakeCommander struct{}

func (f *fakeCommander) SetMode(_ string) error  { return nil }
func (f *fakeCommander) SetLimitSoC(_ int) error { return nil }

// fakeSnapProvider returns a snapshot that can be swapped atomically.
type fakeSnapProvider struct {
	mu   sync.Mutex
	snap evcc.Snapshot
}

func (f *fakeSnapProvider) Snapshot() evcc.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}

// metricsCall records arguments from a single UpdateSessionMetrics invocation.
type metricsCall struct {
	energyWh    float64
	avgChargeW  float64
	peakChargeW float64
}

// fakeRecorder satisfies the Recorder interface for controller tests.
// All access to shared state is guarded by mu.
type fakeRecorder struct {
	mu           sync.Mutex
	nextID       int64
	openSess     *store.Session
	metricsCalls []metricsCall
}

func (f *fakeRecorder) InsertEvent(_ context.Context, _ store.Event) error   { return nil }
func (f *fakeRecorder) InsertSample(_ context.Context, _ store.Sample) error { return nil }

func (f *fakeRecorder) OpenSession(_ context.Context) (*store.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.openSess, nil
}

func (f *fakeRecorder) StartSession(_ context.Context, startedAt time.Time, _ string, _ *int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.openSess = &store.Session{ID: f.nextID, StartedAt: startedAt}
	return f.nextID, nil
}

func (f *fakeRecorder) EndSession(_ context.Context, id int64, _ time.Time, _ string, _ *int, _, _, _ float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.openSess != nil && f.openSess.ID == id {
		f.openSess = nil
	}
	return nil
}

func (f *fakeRecorder) UpdateSessionMetrics(_ context.Context, _ int64, energyWh, avgChargeW, peakChargeW float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metricsCalls = append(f.metricsCalls, metricsCall{
		energyWh: energyWh, avgChargeW: avgChargeW, peakChargeW: peakChargeW,
	})
	return nil
}

func (f *fakeRecorder) snapshotMetricsCalls() []metricsCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]metricsCall, len(f.metricsCalls))
	copy(out, f.metricsCalls)
	return out
}

// chargingSnap returns a healthy, non-stale snapshot where the EV is actively charging.
func chargingSnap(now time.Time, chargeW float64) evcc.Snapshot {
	return evcc.Snapshot{
		BrokerConnected: true,
		Online:          evcc.BoolMetric{Value: true, At: now, Seen: true},
		Grid:            evcc.FloatMetric{Value: -500, At: now, Seen: true},
		PV:              evcc.FloatMetric{Value: 5000, At: now, Seen: true},
		Home:            evcc.FloatMetric{Value: 500, At: now, Seen: true},
		ChargePower:     evcc.FloatMetric{Value: chargeW, At: now, Seen: true},
		BatterySoC:      evcc.FloatMetric{Value: 80, At: now, Seen: true},
		VehicleSoC:      evcc.FloatMetric{Value: 50, At: now, Seen: true},
		Connected:       evcc.BoolMetric{Value: true, At: now, Seen: true},
		Charging:        evcc.BoolMetric{Value: true, At: now, Seen: true},
	}
}

// runAndCollect starts c in a goroutine, waits until cond is satisfied over the
// recorded metrics calls, cancels, waits for shutdown, then returns all calls.
func runAndCollect(c *controller.Controller, rec *fakeRecorder, cond func([]metricsCall) bool) []metricsCall {
	GinkgoHelper()
	cctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.Run(cctx)
	}()
	Eventually(func() bool { return cond(rec.snapshotMetricsCalls()) }, "2s", "1ms").Should(BeTrue())
	cancel()
	Eventually(done, "2s").Should(BeClosed())
	return rec.snapshotMetricsCalls()
}

var _ = Describe("Controller session flush", func() {
	var (
		clk *controller.FakeClock
		rec *fakeRecorder
		snp *fakeSnapProvider
		cfg config.Control
		t0  time.Time
	)

	BeforeEach(func() {
		t0 = time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)
		clk = controller.NewFakeClock(t0)
		rec = &fakeRecorder{}
		snp = &fakeSnapProvider{}
		cfg = config.Default().Control
		cfg.DecisionInterval = time.Millisecond // fast ticks for tests
	})

	It("calls UpdateSessionMetrics on every non-stale charging tick", func() {
		snp.snap = chargingSnap(t0, 7200)
		c := controller.New(cfg, &fakeCommander{}, snp, rec, clk, slog.Default())

		calls := runAndCollect(c, rec, func(mc []metricsCall) bool { return len(mc) >= 1 })
		Expect(calls).NotTo(BeEmpty())
	})

	It("flushes correct energy, average, and peak on the first charging tick", func() {
		chargeW := 7200.0
		snp.snap = chargingSnap(t0, chargeW)
		c := controller.New(cfg, &fakeCommander{}, snp, rec, clk, slog.Default())

		calls := runAndCollect(c, rec, func(mc []metricsCall) bool { return len(mc) >= 1 })

		dt := cfg.DecisionInterval.Hours()
		first := calls[0]
		Expect(first.energyWh).To(BeNumerically("~", chargeW*dt, 1e-9))
		Expect(first.avgChargeW).To(BeNumerically("~", chargeW, 1e-9))
		Expect(first.peakChargeW).To(BeNumerically("~", chargeW, 1e-9))
	})

	It("energy grows monotonically across multiple ticks", func() {
		chargeW := 3600.0
		snp.snap = chargingSnap(t0, chargeW)
		c := controller.New(cfg, &fakeCommander{}, snp, rec, clk, slog.Default())

		calls := runAndCollect(c, rec, func(mc []metricsCall) bool { return len(mc) >= 3 })

		for i := 1; i < len(calls); i++ {
			Expect(calls[i].energyWh).To(BeNumerically(">", calls[i-1].energyWh),
				"flush %d energy should exceed flush %d", i, i-1)
		}
	})
})
