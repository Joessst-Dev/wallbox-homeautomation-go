package store_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

var _ = Describe("Events audit log", func() {
	var (
		s   *store.Store
		ctx context.Context
	)

	BeforeEach(func() {
		s = newTempStore()
		ctx = context.Background()
	})

	It("round-trips a fully populated event", func() {
		ts := time.Date(2026, 6, 28, 9, 30, 0, 0, time.UTC)
		e := store.Event{
			TS:         ts,
			Type:       "state_change",
			FromState:  "idle",
			ToState:    "charging",
			Action:     "start",
			SurplusW:   3200,
			GridW:      -3200,
			PVW:        6000,
			BatterySoC: 75,
			BatteryW:   500,
			VehicleSoC: 40,
			ChargeW:    3000,
			Detail:     "surplus threshold reached",
		}
		Expect(s.InsertEvent(ctx, e)).To(Succeed())

		got, err := s.RecentEvents(ctx, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))

		g := got[0]
		Expect(g.ID).To(BeNumerically(">", 0))
		Expect(g.TS).To(Equal(ts))
		Expect(g.Type).To(Equal("state_change"))
		Expect(g.FromState).To(Equal("idle"))
		Expect(g.ToState).To(Equal("charging"))
		Expect(g.Action).To(Equal("start"))
		Expect(g.SurplusW).To(Equal(3200.0))
		Expect(g.GridW).To(Equal(-3200.0))
		Expect(g.PVW).To(Equal(6000.0))
		Expect(g.BatterySoC).To(Equal(75))
		Expect(g.BatteryW).To(Equal(500.0))
		Expect(g.VehicleSoC).To(Equal(40))
		Expect(g.ChargeW).To(Equal(3000.0))
		Expect(g.Detail).To(Equal("surplus threshold reached"))
	})

	It("returns events newest first and honors the limit", func() {
		base := time.Date(2026, 6, 28, 0, 0, 0, 0, time.UTC)
		for i := 0; i < 4; i++ {
			e := store.Event{
				TS:     base.Add(time.Duration(i) * time.Minute),
				Type:   "command",
				Action: "tick",
			}
			Expect(s.InsertEvent(ctx, e)).To(Succeed())
		}

		got, err := s.RecentEvents(ctx, 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(2))
		Expect(got[0].TS).To(Equal(base.Add(3 * time.Minute)))
		Expect(got[1].TS).To(Equal(base.Add(2 * time.Minute)))
	})

	It("returns an empty slice when there are no events", func() {
		got, err := s.RecentEvents(ctx, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())
	})
})
