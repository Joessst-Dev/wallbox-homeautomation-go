package store_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

var _ = Describe("Samples time-series", func() {
	var (
		s   *store.Store
		ctx context.Context
	)

	BeforeEach(func() {
		s = newTempStore()
		ctx = context.Background()
	})

	It("round-trips a sample including the Charging bool", func() {
		ts := time.Date(2026, 6, 28, 11, 0, 0, 0, time.UTC)
		sm := store.Sample{
			TS:         ts,
			GridW:      -1500,
			PVW:        5000,
			HomeW:      800,
			BatterySoC: 60,
			BatteryW:   200,
			ChargeW:    2700,
			VehicleSoC: 55,
			Charging:   true,
			Mode:       "pv",
			SurplusW:   2700,
			State:      "charging",
		}
		Expect(s.InsertSample(ctx, sm)).To(Succeed())

		got, err := s.Samples(ctx, ts.Add(-time.Hour), ts.Add(time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))

		g := got[0]
		Expect(g.ID).To(BeNumerically(">", 0))
		Expect(g.TS).To(Equal(ts))
		Expect(g.GridW).To(Equal(-1500.0))
		Expect(g.PVW).To(Equal(5000.0))
		Expect(g.HomeW).To(Equal(800.0))
		Expect(g.BatterySoC).To(Equal(60))
		Expect(g.BatteryW).To(Equal(200.0))
		Expect(g.ChargeW).To(Equal(2700.0))
		Expect(g.VehicleSoC).To(Equal(55))
		Expect(g.Charging).To(BeTrue())
		Expect(g.Mode).To(Equal("pv"))
		Expect(g.SurplusW).To(Equal(2700.0))
		Expect(g.State).To(Equal("charging"))
	})

	It("round-trips a false Charging value", func() {
		ts := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
		Expect(s.InsertSample(ctx, store.Sample{TS: ts, Charging: false})).To(Succeed())

		got, err := s.Samples(ctx, ts, ts)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))
		Expect(got[0].Charging).To(BeFalse())
	})

	It("filters by the inclusive [from, to] range and returns oldest first", func() {
		base := time.Date(2026, 6, 28, 0, 0, 0, 0, time.UTC)
		times := []time.Time{
			base,                    // t0
			base.Add(1 * time.Hour), // t1
			base.Add(2 * time.Hour), // t2
			base.Add(3 * time.Hour), // t3
			base.Add(4 * time.Hour), // t4
		}
		// Insert out of order to confirm ordering comes from the query.
		for _, i := range []int{2, 0, 4, 1, 3} {
			Expect(s.InsertSample(ctx, store.Sample{TS: times[i], State: "s"})).To(Succeed())
		}

		// Range covers t1..t3 inclusive.
		got, err := s.Samples(ctx, times[1], times[3])
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(3))
		Expect(got[0].TS).To(Equal(times[1]))
		Expect(got[1].TS).To(Equal(times[2]))
		Expect(got[2].TS).To(Equal(times[3]))
	})

	It("returns an empty slice when nothing is in range", func() {
		ts := time.Date(2026, 6, 28, 6, 0, 0, 0, time.UTC)
		Expect(s.InsertSample(ctx, store.Sample{TS: ts})).To(Succeed())

		got, err := s.Samples(ctx, ts.Add(time.Hour), ts.Add(2*time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())
	})
})

var _ = Describe("PruneSamples", func() {
	var (
		s   *store.Store
		ctx context.Context
	)

	BeforeEach(func() {
		s = newTempStore()
		ctx = context.Background()
	})

	It("deletes samples strictly older than `before` and returns the count", func() {
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		// Three samples: two old, one at the cutoff boundary.
		Expect(s.InsertSample(ctx, store.Sample{TS: base})).To(Succeed())
		Expect(s.InsertSample(ctx, store.Sample{TS: base.Add(time.Hour)})).To(Succeed())
		Expect(s.InsertSample(ctx, store.Sample{TS: base.Add(2 * time.Hour)})).To(Succeed())

		cutoff := base.Add(2 * time.Hour) // strictly before cutoff → first two deleted
		n, err := s.PruneSamples(ctx, cutoff)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeNumerically("==", 2))

		// Only the sample at exactly `cutoff` should remain.
		remaining, err := s.Samples(ctx, base, base.Add(3*time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(remaining).To(HaveLen(1))
		Expect(remaining[0].TS).To(Equal(base.Add(2 * time.Hour)))
	})

	It("returns 0 and no error when no rows are old enough", func() {
		ts := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
		Expect(s.InsertSample(ctx, store.Sample{TS: ts})).To(Succeed())

		n, err := s.PruneSamples(ctx, ts.Add(-time.Hour)) // cutoff before the row
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeNumerically("==", 0))
	})

	It("returns 0 and no error on an empty table", func() {
		n, err := s.PruneSamples(ctx, time.Now())
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeNumerically("==", 0))
	})

	It("deletes all rows when `before` is in the far future", func() {
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := 0; i < 5; i++ {
			Expect(s.InsertSample(ctx, store.Sample{TS: base.Add(time.Duration(i) * time.Hour)})).To(Succeed())
		}

		n, err := s.PruneSamples(ctx, base.Add(24*time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeNumerically("==", 5))

		remaining, err := s.Samples(ctx, base, base.Add(48*time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(remaining).To(BeEmpty())
	})
})
