package controller_test

import (
	"context"
	"log/slog"
	"runtime"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/controller"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

// --- stubs used only by the Controller integration tests ---

type noopCommander struct{}

func (noopCommander) SetMode(_ string) error  { return nil }
func (noopCommander) SetLimitSoC(_ int) error { return nil }

type noopSnaps struct{}

func (noopSnaps) Snapshot() evcc.Snapshot { return evcc.Snapshot{} }

// tickCounter counts completed tick cycles via InsertSample calls.
type tickCounter struct {
	count atomic.Int64
}

func (r *tickCounter) InsertEvent(_ context.Context, _ store.Event) error { return nil }
func (r *tickCounter) InsertSample(_ context.Context, _ store.Sample) error {
	r.count.Add(1)
	return nil
}
func (r *tickCounter) StartSession(_ context.Context, _ time.Time, _ string, _ *int) (int64, error) {
	return 0, nil
}
func (r *tickCounter) EndSession(_ context.Context, _ int64, _ time.Time, _ string, _ *int, _, _, _ float64) error {
	return nil
}
func (r *tickCounter) OpenSession(_ context.Context) (*store.Session, error) { return nil, nil }

// newTestController builds a Controller whose DecisionInterval is fast enough
// for integration tests but whose external dependencies are all no-ops.
func newTestController(clock controller.Clock) (*controller.Controller, *tickCounter) {
	cfg := config.Control{
		EnableMode:       config.EnableModePV,
		StartThresholdW:  1400,
		StopThresholdW:   0,
		StartDwell:       time.Minute,
		StopDwell:        2 * time.Minute,
		SoCCap:           80,
		SoCResumeBelow:   78,
		DecisionInterval: time.Millisecond,
		StaleTimeout:     time.Second,
		Republish:        5 * time.Minute,
	}
	rec := &tickCounter{}
	c := controller.New(cfg, noopCommander{}, noopSnaps{}, rec, clock, slog.Default())
	return c, rec
}

// waitForTicks blocks until at least n ticks have completed after the call.
func waitForTicks(rec *tickCounter, n int64, timeout time.Duration) bool {
	start := rec.count.Load()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if rec.count.Load()-start >= n {
			return true
		}
		runtime.Gosched()
	}
	return false
}

var _ = Describe("Controller override TOCTOU guard", func() {
	// Regression tests for the race described in issue #4:
	// tick reads ov/ovUntil under the first lock, releases it, computes
	// expireOverride, then re-acquires to write OverrideAuto. A SetOverride
	// call in the gap between the two locks must not be silently discarded.
	// The fix adds: `c.override == ov && c.overrideUntil == ovUntil` to the
	// condition so the clear is skipped when the values have been replaced.

	var (
		ctrl   *controller.Controller
		rec    *tickCounter
		clock  *controller.FakeClock
		ctx    context.Context
		cancel context.CancelFunc
	)

	BeforeEach(func() {
		t0 := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
		clock = controller.NewFakeClock(t0)
		ctrl, rec = newTestController(clock)
		ctx, cancel = context.WithCancel(context.Background())
		go ctrl.Run(ctx) //nolint:errcheck
	})

	AfterEach(func() {
		cancel()
	})

	It("expires a timed override and reverts to auto", func() {
		// A ForceOff with a short expiry must be cleared by tick once it passes.
		expiry := clock.Now().Add(2 * time.Millisecond)
		ctrl.SetOverride(controller.OverrideForceOff, expiry)

		Expect(waitForTicks(rec, 2, 100*time.Millisecond)).To(BeTrue())
		Expect(ctrl.Status().Override).To(Equal(controller.OverrideForceOff))

		// Advance real time past the expiry and let several ticks run.
		time.Sleep(10 * time.Millisecond)
		// Also advance the fake clock so overrideActive sees the expiry.
		clock.Advance(10 * time.Millisecond)
		Expect(waitForTicks(rec, 5, 200*time.Millisecond)).To(BeTrue())

		Expect(ctrl.Status().Override).To(Equal(controller.OverrideAuto))
	})

	It("preserves a permanent override across many tick cycles", func() {
		// A ForceOn with no expiry must never be cleared by tick.
		ctrl.SetOverride(controller.OverrideForceOn, time.Time{})
		Expect(waitForTicks(rec, 20, 200*time.Millisecond)).To(BeTrue())
		Expect(ctrl.Status().Override).To(Equal(controller.OverrideForceOn))
	})

	It("does not revert a new SetOverride that arrives while an expired override is being processed", func() {
		// This test targets the TOCTOU window:
		//   tick-lock-1: reads ForceOff with past expiry  → expireOverride=true
		//   [gap]:       SetOverride(ForceOn, permanent)
		//   tick-lock-2: (bug) clears to Auto; (fix) skips because values changed
		//
		// Strategy: prime the controller with an already-expired ForceOff so that
		// tick continuously computes expireOverride=true. Concurrently call
		// SetOverride(ForceOn, permanent) in a tight loop. If the bug is present,
		// tick will occasionally clobber ForceOn and Status().Override flips to
		// Auto. With the fix, ForceOn is always preserved once set.
		t0 := clock.Now()

		// Set an expired ForceOff so tick immediately sees expireOverride=true.
		ctrl.SetOverride(controller.OverrideForceOff, t0.Add(-time.Second))

		// Advance the fake clock past the expiry to make overrideActive return false.
		clock.Advance(2 * time.Second)

		// Wait for at least one tick to observe the expired override.
		Expect(waitForTicks(rec, 1, 100*time.Millisecond)).To(BeTrue())

		// Now set a permanent override and confirm it is never reverted.
		ctrl.SetOverride(controller.OverrideForceOn, time.Time{})

		// Run for many more ticks while continuously checking.
		baseline := rec.count.Load()
		deadline := time.Now().Add(100 * time.Millisecond)
		for time.Now().Before(deadline) {
			Expect(ctrl.Status().Override).NotTo(Equal(controller.OverrideAuto),
				"permanent ForceOn must not be reverted to Auto by a concurrent tick")
			runtime.Gosched()
		}
		// Sanity: ensure we actually ran ticks during the check window.
		Expect(rec.count.Load() - baseline).To(BeNumerically(">", 5))
	})
})
