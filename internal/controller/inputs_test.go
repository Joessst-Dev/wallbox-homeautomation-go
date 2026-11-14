package controller_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/controller"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
)

var _ = Describe("InputsFromSnapshot", func() {
	var (
		now time.Time
		cfg config.Control
	)

	BeforeEach(func() {
		now = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
		cfg = config.Default().Control // StaleTimeout 60s
	})

	// fullSnap returns a healthy snapshot with all required metrics fresh.
	fullSnap := func() evcc.Snapshot {
		fresh := evcc.FloatMetric{Value: 0, At: now, Seen: true}
		return evcc.Snapshot{
			BrokerConnected: true,
			Online:          evcc.BoolMetric{Value: true, At: now, Seen: true},
			Grid:            evcc.FloatMetric{Value: -3000, At: now, Seen: true},
			PV:              evcc.FloatMetric{Value: 4000, At: now, Seen: true},
			Home:            fresh,
			BatteryPower:    fresh,
			BatterySoC:      evcc.FloatMetric{Value: 90, At: now, Seen: true},
			ChargePower:     fresh,
			VehicleSoC:      evcc.FloatMetric{Value: 55, At: now.Add(-3 * time.Hour), Seen: true},
			Connected:       evcc.BoolMetric{Value: true, At: now, Seen: true},
		}
	}

	It("is ready and not stale when required metrics are present and power is fresh", func() {
		in := controller.InputsFromSnapshot(now, fullSnap(), cfg, controller.ControlState{Override: controller.OverrideAuto})
		Expect(in.Ready).To(BeTrue())
		Expect(in.Stale).To(BeFalse())
		Expect(in.Connected).To(BeTrue())
		Expect(in.VehicleSoC).To(Equal(55))
	})

	It("treats a 3-hour-old vehicleSoc as valid (NOT stale)", func() {
		s := fullSnap()
		s.VehicleSoC.At = now.Add(-3 * time.Hour) // far beyond StaleTimeout, within SoC policy
		in := controller.InputsFromSnapshot(now, s, cfg, controller.ControlState{Override: controller.OverrideAuto})
		Expect(in.Stale).To(BeFalse(), "old vehicleSoc must not trip fail-safe")
		Expect(in.Ready).To(BeTrue())
	})

	It("is stale when grid power is older than StaleTimeout", func() {
		s := fullSnap()
		s.Grid.At = now.Add(-2 * time.Minute)
		in := controller.InputsFromSnapshot(now, s, cfg, controller.ControlState{Override: controller.OverrideAuto})
		Expect(in.Stale).To(BeTrue())
	})

	It("is stale when the broker is disconnected", func() {
		s := fullSnap()
		s.BrokerConnected = false
		in := controller.InputsFromSnapshot(now, s, cfg, controller.ControlState{Override: controller.OverrideAuto})
		Expect(in.Stale).To(BeTrue())
	})

	It("is stale when evcc reports offline via LWT", func() {
		s := fullSnap()
		s.Online = evcc.BoolMetric{Value: false, At: now, Seen: true}
		in := controller.InputsFromSnapshot(now, s, cfg, controller.ControlState{Override: controller.OverrideAuto})
		Expect(in.Stale).To(BeTrue())
	})

	It("is not ready until vehicleSoc and connected have been seen at least once", func() {
		s := fullSnap()
		s.VehicleSoC.Seen = false
		Expect(controller.InputsFromSnapshot(now, s, cfg, controller.ControlState{Override: controller.OverrideAuto}).Ready).To(BeFalse())

		s = fullSnap()
		s.Connected.Seen = false
		Expect(controller.InputsFromSnapshot(now, s, cfg, controller.ControlState{Override: controller.OverrideAuto}).Ready).To(BeFalse())
	})

	It("preserves a genuine 0% SoC and marks it known", func() {
		s := fullSnap()
		s.VehicleSoC = evcc.FloatMetric{Value: 0, At: now, Seen: true}
		in := controller.InputsFromSnapshot(now, s, cfg, controller.ControlState{Override: controller.OverrideAuto})
		Expect(in.VehicleSoC).To(Equal(0))
		Expect(in.VehicleSoCKnown).To(BeTrue())
		Expect(in.Ready).To(BeTrue())
	})

	It("marks VehicleSoCKnown false when SoC was never seen", func() {
		s := fullSnap()
		s.VehicleSoC.Seen = false
		in := controller.InputsFromSnapshot(now, s, cfg, controller.ControlState{Override: controller.OverrideAuto})
		Expect(in.VehicleSoCKnown).To(BeFalse())
	})

	It("computes surplus from the mapped inputs", func() {
		// export 3000W, car drawing 0 → surplus 3000
		in := controller.InputsFromSnapshot(now, fullSnap(), cfg, controller.ControlState{Override: controller.OverrideAuto})
		Expect(controller.Surplus(in)).To(Equal(3000.0))
	})
})

var _ = Describe("InputsFromSnapshot (min-charge-power and stall inputs)", func() {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	cfg := config.Default().Control
	auto := controller.ControlState{Override: controller.OverrideAuto}

	It("leaves the evcc-derived floor inputs unset until evcc publishes them", func() {
		in := controller.InputsFromSnapshot(now, evcc.Snapshot{}, cfg, auto)
		Expect(in.EffectiveMinCurrentA).To(BeZero())
		Expect(in.PhasesConfiguredKnown).To(BeFalse())
		Expect(in.Enabled).To(BeFalse())
	})

	It("plumbs effectiveMinCurrent, phasesConfigured, and enabled through", func() {
		s := evcc.Snapshot{
			EffectiveMinCurrent: evcc.FloatMetric{Value: 6, At: now, Seen: true},
			PhasesConfigured:    evcc.FloatMetric{Value: 1, At: now, Seen: true},
			Enabled:             evcc.BoolMetric{Value: true, At: now, Seen: true},
		}
		in := controller.InputsFromSnapshot(now, s, cfg, auto)
		Expect(in.EffectiveMinCurrentA).To(Equal(6.0))
		Expect(in.PhasesConfigured).To(Equal(1))
		Expect(in.PhasesConfiguredKnown).To(BeTrue())
		Expect(in.Enabled).To(BeTrue())
	})

	It("keeps a published phasesConfigured of 0 (automatic switching) distinguishable from unset", func() {
		s := evcc.Snapshot{PhasesConfigured: evcc.FloatMetric{Value: 0, At: now, Seen: true}}
		in := controller.InputsFromSnapshot(now, s, cfg, auto)
		Expect(in.PhasesConfigured).To(Equal(0))
		Expect(in.PhasesConfiguredKnown).To(BeTrue())
	})

	It("does not freshness-gate the floor inputs (they are retained evcc settings)", func() {
		s := evcc.Snapshot{EffectiveMinCurrent: evcc.FloatMetric{Value: 6, At: now.Add(-24 * time.Hour), Seen: true}}
		in := controller.InputsFromSnapshot(now, s, cfg, auto)
		Expect(in.EffectiveMinCurrentA).To(Equal(6.0))
	})
})

var _ = Describe("VehicleSoCEstimated", func() {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	soc := func(changedAgo time.Duration) evcc.FloatMetric {
		return evcc.FloatMetric{Value: 55, At: now, ChangedAt: now.Add(-changedAgo), Seen: true}
	}

	It("uses a 5 minute threshold", func() {
		Expect(controller.SoCEstimatedAfter).To(Equal(5 * time.Minute))
	})

	DescribeTable("flags an unchanged SoC while charging as an estimate",
		func(charging bool, m evcc.FloatMetric, expected bool) {
			Expect(controller.VehicleSoCEstimated(now, charging, m)).To(Equal(expected))
		},
		Entry("charging, changed 20m ago → estimated", true, soc(20*time.Minute), true),
		Entry("charging, changed just over 5m ago → estimated", true, soc(controller.SoCEstimatedAfter+time.Second), true),
		Entry("charging, changed exactly 5m ago → not yet", true, soc(controller.SoCEstimatedAfter), false),
		Entry("charging, changed 1m ago → fresh", true, soc(time.Minute), false),
		Entry("not charging, changed hours ago → not an estimate (nothing is moving)", false, soc(3*time.Hour), false),
		Entry("charging, never received → not flagged", true, evcc.FloatMetric{}, false),
		Entry("charging, seen but no ChangedAt → not flagged", true, evcc.FloatMetric{Value: 55, At: now, Seen: true}, false),
	)

	It("never feeds staleness: an hours-old SoC keeps the inputs fresh", func() {
		cfg := config.Default().Control
		s := evcc.Snapshot{
			BrokerConnected: true,
			Grid:            evcc.FloatMetric{At: now, Seen: true},
			PV:              evcc.FloatMetric{At: now, Seen: true},
			VehicleSoC:      soc(3 * time.Hour),
			Connected:       evcc.BoolMetric{Value: true, At: now, Seen: true},
			Charging:        evcc.BoolMetric{Value: true, At: now, Seen: true},
		}
		in := controller.InputsFromSnapshot(now, s, cfg, controller.ControlState{Override: controller.OverrideAuto})
		Expect(in.Stale).To(BeFalse())
		Expect(controller.VehicleSoCEstimated(now, in.Charging, s.VehicleSoC)).To(BeTrue())
	})
})
