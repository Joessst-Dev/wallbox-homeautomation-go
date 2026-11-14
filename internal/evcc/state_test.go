package evcc_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
)

var _ = Describe("Store.Apply", func() {
	var (
		store *evcc.Store
		t     evcc.Topics
		now   time.Time
	)

	BeforeEach(func() {
		t = evcc.NewTopics("evcc", "1")
		store = evcc.NewStore(t)
		now = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	})

	It("parses float power payloads with trimmed decimals", func() {
		Expect(store.Apply(t.GridPower, "1234.567", now)).To(BeTrue())
		Expect(store.Apply(t.PVPower, "4000", now)).To(BeTrue())
		snap := store.Snapshot()
		Expect(snap.Grid.Value).To(Equal(1234.567))
		Expect(snap.Grid.Seen).To(BeTrue())
		Expect(snap.Grid.At).To(Equal(now))
		Expect(snap.PV.Value).To(Equal(4000.0))
	})

	It("parses charging/connected as the literal strings true/false (not 1/0)", func() {
		Expect(store.Apply(t.Charging, "true", now)).To(BeTrue())
		Expect(store.Apply(t.Connected, "false", now)).To(BeTrue())
		snap := store.Snapshot()
		Expect(snap.Charging.Value).To(BeTrue())
		Expect(snap.Charging.Seen).To(BeTrue())
		Expect(snap.Connected.Value).To(BeFalse())
		Expect(snap.Connected.Seen).To(BeTrue())
	})

	It("rejects 1/0 boolean encodings", func() {
		Expect(store.Apply(t.Charging, "1", now)).To(BeFalse())
		Expect(store.Snapshot().Charging.Seen).To(BeFalse())
	})

	It("maps evcc/status online/offline to the Online metric", func() {
		Expect(store.Apply(t.Status, "online", now)).To(BeTrue())
		Expect(store.Snapshot().Online.Value).To(BeTrue())
		Expect(store.Apply(t.Status, "offline", now)).To(BeTrue())
		Expect(store.Snapshot().Online.Value).To(BeFalse())
	})

	It("uses camelCase limitSoc for the SoC limit read topic", func() {
		Expect(t.LimitSoC).To(Equal("evcc/loadpoints/1/limitSoc"))
		Expect(store.Apply(t.LimitSoC, "80", now)).To(BeTrue())
		Expect(store.Snapshot().LimitSoC.Value).To(Equal(80.0))
	})

	It("builds camelCase set topics (lowercase limitsoc is silently ignored by evcc)", func() {
		Expect(t.ModeSet).To(Equal("evcc/loadpoints/1/mode/set"))
		Expect(t.LimitSoCSet).To(Equal("evcc/loadpoints/1/limitSoc/set"))
	})

	It("ignores unknown topics and unparseable payloads without panicking", func() {
		Expect(store.Apply("evcc/loadpoints/1/unknown", "x", now)).To(BeFalse())
		Expect(store.Apply(t.GridPower, "not-a-number", now)).To(BeFalse())
		Expect(store.Snapshot().Grid.Seen).To(BeFalse())
	})

	It("records vehicleSoc and keeps Seen=false until first received", func() {
		Expect(store.Snapshot().VehicleSoC.Seen).To(BeFalse())
		Expect(store.Apply(t.VehicleSoC, "55", now)).To(BeTrue())
		Expect(store.Snapshot().VehicleSoC.Value).To(Equal(55.0))
	})

	Describe("FloatMetric.ChangedAt", func() {
		It("is set on first receipt", func() {
			Expect(store.Apply(t.VehicleSoC, "55", now)).To(BeTrue())
			Expect(store.Snapshot().VehicleSoC.ChangedAt).To(Equal(now))
		})

		It("does not move when evcc re-sends the same value, while At does", func() {
			Expect(store.Apply(t.VehicleSoC, "55", now)).To(BeTrue())
			later := now.Add(10 * time.Minute)
			Expect(store.Apply(t.VehicleSoC, " 55 ", later)).To(BeTrue())

			soc := store.Snapshot().VehicleSoC
			Expect(soc.At).To(Equal(later))
			Expect(soc.ChangedAt).To(Equal(now))
		})

		It("moves when the value changes", func() {
			Expect(store.Apply(t.VehicleSoC, "55", now)).To(BeTrue())
			later := now.Add(10 * time.Minute)
			Expect(store.Apply(t.VehicleSoC, "56", later)).To(BeTrue())
			Expect(store.Snapshot().VehicleSoC.ChangedAt).To(Equal(later))
		})

		It("is untouched by an unparseable payload", func() {
			Expect(store.Apply(t.VehicleSoC, "55", now)).To(BeTrue())
			Expect(store.Apply(t.VehicleSoC, "n/a", now.Add(time.Minute))).To(BeFalse())
			soc := store.Snapshot().VehicleSoC
			Expect(soc.Value).To(Equal(55.0))
			Expect(soc.ChangedAt).To(Equal(now))
		})
	})

	Describe("charge-power floor, stall, and max-current topics", func() {
		It("uses the camelCase evcc topic names", func() {
			Expect(t.EffectiveMinCurrent).To(Equal("evcc/loadpoints/1/effectiveMinCurrent"))
			Expect(t.PhasesConfigured).To(Equal("evcc/loadpoints/1/phasesConfigured"))
			Expect(t.PhasesActive).To(Equal("evcc/loadpoints/1/phasesActive"))
			Expect(t.ChargerStatusReason).To(Equal("evcc/loadpoints/1/chargerStatusReason"))
			Expect(t.MaxCurrent).To(Equal("evcc/loadpoints/1/maxCurrent"))
			Expect(t.MaxCurrentSet).To(Equal("evcc/loadpoints/1/maxCurrent/set"))
		})

		It("parses effectiveMinCurrent, phasesConfigured, phasesActive, and maxCurrent", func() {
			Expect(store.Apply(t.EffectiveMinCurrent, "8", now)).To(BeTrue())
			Expect(store.Apply(t.PhasesConfigured, "0", now)).To(BeTrue())
			Expect(store.Apply(t.PhasesActive, "3", now)).To(BeTrue())
			Expect(store.Apply(t.MaxCurrent, "16", now)).To(BeTrue())

			snap := store.Snapshot()
			Expect(snap.EffectiveMinCurrent).To(Equal(evcc.FloatMetric{Value: 8, At: now, ChangedAt: now, Seen: true}))
			// 0 (automatic phase switching) is a genuine, seen value — not "unset".
			Expect(snap.PhasesConfigured.Seen).To(BeTrue())
			Expect(snap.PhasesConfigured.Value).To(Equal(0.0))
			Expect(snap.PhasesActive.Value).To(Equal(3.0))
			Expect(snap.MaxCurrent.Value).To(Equal(16.0))
		})

		It("ignores unparseable numeric payloads", func() {
			Expect(store.Apply(t.EffectiveMinCurrent, "", now)).To(BeFalse())
			Expect(store.Apply(t.PhasesConfigured, "auto", now)).To(BeFalse())
			snap := store.Snapshot()
			Expect(snap.EffectiveMinCurrent.Seen).To(BeFalse())
			Expect(snap.PhasesConfigured.Seen).To(BeFalse())
		})

		It("stores chargerStatusReason as a display string", func() {
			Expect(store.Apply(t.ChargerStatusReason, "waitingforauthorization\n", now)).To(BeTrue())
			Expect(store.Snapshot().ChargerStatusReason).To(Equal(
				evcc.StringMetric{Value: "waitingforauthorization", At: now, Seen: true}))
		})
	})
})
