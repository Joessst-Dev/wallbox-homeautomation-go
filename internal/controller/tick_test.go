package controller

// Internal-package tests so they can access unexported fields (testHook).

import (
	"context"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

// --- fake dependencies -------------------------------------------------------

type stubCommander struct{}

func (s *stubCommander) SetMode(_ string) error  { return nil }
func (s *stubCommander) SetLimitSoC(_ int) error { return nil }

type stubSnaps struct{ snap evcc.Snapshot }

func (s *stubSnaps) Snapshot() evcc.Snapshot { return s.snap }

type stubRecorder struct{}

func (s *stubRecorder) InsertEvent(_ context.Context, _ store.Event) error   { return nil }
func (s *stubRecorder) InsertSample(_ context.Context, _ store.Sample) error { return nil }
func (s *stubRecorder) StartSession(_ context.Context, _ time.Time, _ string, _ *int) (int64, error) {
	return 1, nil
}
func (s *stubRecorder) EndSession(_ context.Context, _ int64, _ time.Time, _ string, _ *int, _, _, _ float64) error {
	return nil
}
func (s *stubRecorder) OpenSession(_ context.Context) (*store.Session, error) { return nil, nil }

// freshSnap returns a snapshot that is ready (all required fields seen) and
// not stale (broker connected, metrics fresh as of `now`).
func freshSnap(now time.Time) evcc.Snapshot {
	return evcc.Snapshot{
		Grid:            evcc.FloatMetric{Seen: true, At: now},
		PV:              evcc.FloatMetric{Seen: true, At: now},
		VehicleSoC:      evcc.FloatMetric{Seen: true, At: now, Value: 50},
		Connected:       evcc.BoolMetric{Seen: true, At: now, Value: true},
		BrokerConnected: true,
	}
}

func tickCfg() config.Control {
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

// --- specs -------------------------------------------------------------------

var _ = Describe("tick override expiry", func() {
	var (
		now   time.Time
		cfg   config.Control
		snaps *stubSnaps
		ctrl  *Controller
	)

	BeforeEach(func() {
		now = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
		cfg = tickCfg()
		snaps = &stubSnaps{snap: freshSnap(now)}
		ctrl = New(cfg, &stubCommander{}, snaps, &stubRecorder{}, NewFakeClock(now), slog.Default())
	})

	Context("sequential expiry", func() {
		It("clears an expired non-auto override to OverrideAuto on the next tick", func() {
			past := now.Add(-time.Second)
			ctrl.SetOverride(OverrideForceOff, past)

			ctrl.tick(context.Background(), now)

			Expect(ctrl.Status().Override).To(Equal(OverrideAuto))
		})

		It("does not clear a non-expired override", func() {
			future := now.Add(time.Minute)
			ctrl.SetOverride(OverrideForceOn, future)

			ctrl.tick(context.Background(), now)

			Expect(ctrl.Status().Override).To(Equal(OverrideForceOn))
		})

		It("does not clear an override with no expiry (zero time)", func() {
			ctrl.SetOverride(OverrideForceOff, time.Time{})

			ctrl.tick(context.Background(), now)

			Expect(ctrl.Status().Override).To(Equal(OverrideForceOff))
		})
	})

	Context("TOCTOU race fix", func() {
		// testHook is called after tick's first mu.Unlock() and before the second
		// mu.Lock(), letting us inject a concurrent SetOverride deterministically.

		It("preserves a SetOverride that races into the expiry window", func() {
			// Arrange: set an override that has already expired.
			past := now.Add(-time.Second)
			ctrl.SetOverride(OverrideForceOff, past)

			// Act: inject SetOverride(ForceOn) in the race window between locks.
			ctrl.testHook = func() {
				ctrl.SetOverride(OverrideForceOn, time.Time{})
			}
			ctrl.tick(context.Background(), now)

			// Assert: ForceOn must survive — tick must not overwrite it with Auto.
			Expect(ctrl.Status().Override).To(Equal(OverrideForceOn))
		})

		It("still clears the override when SetOverride was NOT called in the window", func() {
			past := now.Add(-time.Second)
			ctrl.SetOverride(OverrideForceOff, past)
			// No testHook → c.override == ov at second lock → expiry runs normally.
			ctrl.tick(context.Background(), now)

			Expect(ctrl.Status().Override).To(Equal(OverrideAuto))
		})
	})
})
