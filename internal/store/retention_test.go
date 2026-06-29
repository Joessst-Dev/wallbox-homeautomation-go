package store_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

var _ = Describe("Retention pruning", func() {
	var (
		s   *store.Store
		ctx context.Context
	)

	BeforeEach(func() {
		s = newTempStore()
		ctx = context.Background()
	})

	Describe("PruneSamples", func() {
		It("deletes only samples strictly older than the cutoff and reports the count", func() {
			base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
			for i := 0; i < 5; i++ {
				Expect(s.InsertSample(ctx, store.Sample{TS: base.Add(time.Duration(i) * time.Hour)})).To(Succeed())
			}
			cutoff := base.Add(2 * time.Hour) // removes i=0,1 (i=2 is not strictly before)

			n, err := s.PruneSamples(ctx, cutoff)
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(int64(2)))

			got, err := s.Samples(ctx, base.Add(-time.Hour), base.Add(time.Hour*10))
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(HaveLen(3))
			Expect(got[0].TS).To(Equal(cutoff))
		})
	})

	Describe("PruneEvents", func() {
		It("deletes only events strictly older than the cutoff and reports the count", func() {
			base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
			for i := 0; i < 4; i++ {
				Expect(s.InsertEvent(ctx, store.Event{TS: base.Add(time.Duration(i) * time.Hour), Type: "state_change"})).To(Succeed())
			}
			cutoff := base.Add(3 * time.Hour) // removes i=0,1,2

			n, err := s.PruneEvents(ctx, cutoff)
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(int64(3)))

			got, err := s.RecentEvents(ctx, 100)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(HaveLen(1))
		})
	})
})

var _ = Describe("UpdateSessionMetrics", func() {
	var (
		s   *store.Store
		ctx context.Context
	)

	BeforeEach(func() {
		s = newTempStore()
		ctx = context.Background()
	})

	It("updates the running metrics of an open session without closing it", func() {
		startedAt := time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)
		id, err := s.StartSession(ctx, startedAt, "surplus", intPtr(40))
		Expect(err).NotTo(HaveOccurred())

		Expect(s.UpdateSessionMetrics(ctx, id, 1234, 3000, 3600)).To(Succeed())

		open, err := s.OpenSession(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(open).NotTo(BeNil())
		Expect(open.EndedAt).To(BeNil()) // still open
		Expect(open.EnergyWh).To(Equal(1234.0))
		Expect(open.AvgChargeW).To(Equal(3000.0))
		Expect(open.PeakChargeW).To(Equal(3600.0))
	})

	It("errors with ErrNoRows when no session matches", func() {
		err := s.UpdateSessionMetrics(ctx, 999, 1, 2, 3)
		Expect(err).To(HaveOccurred())
	})

	It("preserves a genuine 0% StartVehicleSoC (regression for socPtr/NULL collapse)", func() {
		startedAt := time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)
		id, err := s.StartSession(ctx, startedAt, "surplus", intPtr(0))
		Expect(err).NotTo(HaveOccurred())

		open, err := s.OpenSession(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(open.ID).To(Equal(id))
		Expect(open.StartVehicleSoC).NotTo(BeNil())
		Expect(*open.StartVehicleSoC).To(Equal(0))
	})
})

var _ = Describe("Samples row cap", func() {
	var (
		s   *store.Store
		ctx context.Context
	)

	BeforeEach(func() {
		s = newTempStore()
		ctx = context.Background()
	})

	It("enforces a positive hard maximum and returns within it, oldest first", func() {
		Expect(store.MaxHistoryRows()).To(BeNumerically(">", 0))

		base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		const n = 100
		for i := 0; i < n; i++ {
			Expect(s.InsertSample(ctx, store.Sample{TS: base.Add(time.Duration(i) * time.Minute)})).To(Succeed())
		}
		got, err := s.Samples(ctx, base, base.Add(time.Duration(n)*time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(len(got)).To(BeNumerically("<=", store.MaxHistoryRows()))
		Expect(got).To(HaveLen(n))
		Expect(got[0].TS).To(Equal(base)) // oldest first
	})
})
