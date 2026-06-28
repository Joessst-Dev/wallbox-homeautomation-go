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
		in := controller.InputsFromSnapshot(now, fullSnap(), cfg, controller.OverrideAuto, time.Time{})
		Expect(in.Ready).To(BeTrue())
		Expect(in.Stale).To(BeFalse())
		Expect(in.Connected).To(BeTrue())
		Expect(in.VehicleSoC).To(Equal(55))
		Expect(in.VehicleSoCKnown).To(BeTrue())
	})

	It("sets VehicleSoCKnown=false when VehicleSoC has not been seen", func() {
		s := fullSnap()
		s.VehicleSoC.Seen = false
		s.VehicleSoC.Value = 0
		in := controller.InputsFromSnapshot(now, s, cfg, controller.OverrideAuto, time.Time{})
		Expect(in.VehicleSoCKnown).To(BeFalse())
		Expect(in.Ready).To(BeFalse(), "unseen VehicleSoC should prevent Ready")
	})

	It("sets VehicleSoCKnown=true and VehicleSoC=0 when a genuine 0% reading is received", func() {
		s := fullSnap()
		s.VehicleSoC = evcc.FloatMetric{Value: 0, At: now, Seen: true}
		in := controller.InputsFromSnapshot(now, s, cfg, controller.OverrideAuto, time.Time{})
		Expect(in.VehicleSoCKnown).To(BeTrue())
		Expect(in.VehicleSoC).To(Equal(0))
	})

	It("treats a 3-hour-old vehicleSoc as valid (NOT stale)", func() {
		s := fullSnap()
		s.VehicleSoC.At = now.Add(-3 * time.Hour) // far beyond StaleTimeout, within SoC policy
		in := controller.InputsFromSnapshot(now, s, cfg, controller.OverrideAuto, time.Time{})
		Expect(in.Stale).To(BeFalse(), "old vehicleSoc must not trip fail-safe")
		Expect(in.Ready).To(BeTrue())
	})

	It("is stale when grid power is older than StaleTimeout", func() {
		s := fullSnap()
		s.Grid.At = now.Add(-2 * time.Minute)
		in := controller.InputsFromSnapshot(now, s, cfg, controller.OverrideAuto, time.Time{})
		Expect(in.Stale).To(BeTrue())
	})

	It("is stale when the broker is disconnected", func() {
		s := fullSnap()
		s.BrokerConnected = false
		in := controller.InputsFromSnapshot(now, s, cfg, controller.OverrideAuto, time.Time{})
		Expect(in.Stale).To(BeTrue())
	})

	It("is stale when evcc reports offline via LWT", func() {
		s := fullSnap()
		s.Online = evcc.BoolMetric{Value: false, At: now, Seen: true}
		in := controller.InputsFromSnapshot(now, s, cfg, controller.OverrideAuto, time.Time{})
		Expect(in.Stale).To(BeTrue())
	})

	It("is not ready until vehicleSoc and connected have been seen at least once", func() {
		s := fullSnap()
		s.VehicleSoC.Seen = false
		Expect(controller.InputsFromSnapshot(now, s, cfg, controller.OverrideAuto, time.Time{}).Ready).To(BeFalse())

		s = fullSnap()
		s.Connected.Seen = false
		Expect(controller.InputsFromSnapshot(now, s, cfg, controller.OverrideAuto, time.Time{}).Ready).To(BeFalse())
	})

	It("computes surplus from the mapped inputs", func() {
		// export 3000W, car drawing 0 → surplus 3000
		in := controller.InputsFromSnapshot(now, fullSnap(), cfg, controller.OverrideAuto, time.Time{})
		Expect(controller.Surplus(in)).To(Equal(3000.0))
	})
})
