package store_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

var _ = Describe("Charge sessions", func() {
	var (
		s   *store.Store
		ctx context.Context
	)

	BeforeEach(func() {
		s = newTempStore()
		ctx = context.Background()
	})

	Describe("StartSession / OpenSession / EndSession lifecycle", func() {
		It("tracks an open session and then closes it", func() {
			startedAt := time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)

			id, err := s.StartSession(ctx, startedAt, "surplus", intPtr(42))
			Expect(err).NotTo(HaveOccurred())
			Expect(id).To(BeNumerically(">", 0))

			open, err := s.OpenSession(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(open).NotTo(BeNil())
			Expect(open.ID).To(Equal(id))
			Expect(open.StartedAt).To(Equal(startedAt))
			Expect(open.StartReason).To(Equal("surplus"))
			Expect(open.EndedAt).To(BeNil())
			Expect(open.StartVehicleSoC).To(Equal(intPtr(42)))
			Expect(open.EndVehicleSoC).To(BeNil())

			endedAt := startedAt.Add(2 * time.Hour)
			err = s.EndSession(ctx, id, endedAt, "full", intPtr(80), 7000, 3500, 7400)
			Expect(err).NotTo(HaveOccurred())

			open, err = s.OpenSession(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(open).To(BeNil())
		})

		It("returns nil when no session has ever been started", func() {
			open, err := s.OpenSession(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(open).To(BeNil())
		})

		It("errors when ending a non-existent session", func() {
			err := s.EndSession(ctx, 999, time.Now().UTC(), "stop", nil, 0, 0, 0)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("FlushSession", func() {
		It("updates energy metrics while keeping the session open", func() {
			startedAt := time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)
			id, err := s.StartSession(ctx, startedAt, "surplus", nil)
			Expect(err).NotTo(HaveOccurred())

			open, err := s.OpenSession(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(open.EnergyWh).To(Equal(0.0))
			Expect(open.AvgChargeW).To(Equal(0.0))
			Expect(open.PeakChargeW).To(Equal(0.0))

			Expect(s.FlushSession(ctx, id, 1234.5, 617.25, 750.0)).To(Succeed())

			open, err = s.OpenSession(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(open).NotTo(BeNil())
			Expect(open.EnergyWh).To(Equal(1234.5))
			Expect(open.AvgChargeW).To(Equal(617.25))
			Expect(open.PeakChargeW).To(Equal(750.0))
			Expect(open.EndedAt).To(BeNil())
		})

		It("overwrites a previous flush with the latest accumulators", func() {
			startedAt := time.Date(2026, 6, 28, 11, 0, 0, 0, time.UTC)
			id, err := s.StartSession(ctx, startedAt, "surplus", nil)
			Expect(err).NotTo(HaveOccurred())

			Expect(s.FlushSession(ctx, id, 100.0, 400.0, 500.0)).To(Succeed())
			Expect(s.FlushSession(ctx, id, 200.0, 420.0, 550.0)).To(Succeed())

			open, err := s.OpenSession(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(open.EnergyWh).To(Equal(200.0))
			Expect(open.AvgChargeW).To(Equal(420.0))
			Expect(open.PeakChargeW).To(Equal(550.0))
		})

		It("errors on a non-existent session", func() {
			Expect(s.FlushSession(ctx, 999, 0, 0, 0)).To(HaveOccurred())
		})

		It("errors on an already-closed session", func() {
			startedAt := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
			id, err := s.StartSession(ctx, startedAt, "surplus", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(s.EndSession(ctx, id, startedAt.Add(time.Hour), "full", nil, 500, 250, 300)).To(Succeed())

			Expect(s.FlushSession(ctx, id, 500, 250, 300)).To(HaveOccurred())
		})

		It("flushed values are used by crash recovery (OpenSession reads them back)", func() {
			startedAt := time.Date(2026, 6, 28, 13, 0, 0, 0, time.UTC)
			id, err := s.StartSession(ctx, startedAt, "surplus", intPtr(30))
			Expect(err).NotTo(HaveOccurred())

			Expect(s.FlushSession(ctx, id, 4500.0, 3000.0, 3600.0)).To(Succeed())

			// Simulate what recoverDanglingSession does: read the open session and
			// close it using the persisted values.
			open, err := s.OpenSession(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(open).NotTo(BeNil())

			endedAt := startedAt.Add(90 * time.Minute)
			Expect(s.EndSession(ctx, open.ID, endedAt, "restart", nil,
				open.EnergyWh, open.AvgChargeW, open.PeakChargeW)).To(Succeed())

			sessions, err := s.RecentSessions(ctx, 1)
			Expect(err).NotTo(HaveOccurred())
			Expect(sessions[0].EnergyWh).To(Equal(4500.0))
			Expect(sessions[0].AvgChargeW).To(Equal(3000.0))
			Expect(sessions[0].PeakChargeW).To(Equal(3600.0))
			Expect(sessions[0].StopReason).To(Equal("restart"))
		})
	})

	Describe("RecentSessions", func() {
		It("round-trips values, including nullable fields, newest first", func() {
			t1 := time.Date(2026, 6, 28, 8, 0, 0, 0, time.UTC)
			t2 := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

			// First session: fully closed with all SoC values.
			id1, err := s.StartSession(ctx, t1, "surplus", intPtr(20))
			Expect(err).NotTo(HaveOccurred())
			Expect(s.EndSession(ctx, id1, t1.Add(time.Hour), "full", intPtr(90), 5000, 2500, 6000)).To(Succeed())

			// Second session: still open, with nil start SoC.
			id2, err := s.StartSession(ctx, t2, "manual", nil)
			Expect(err).NotTo(HaveOccurred())

			sessions, err := s.RecentSessions(ctx, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(sessions).To(HaveLen(2))

			// Newest first.
			Expect(sessions[0].ID).To(Equal(id2))
			Expect(sessions[0].StartedAt).To(Equal(t2))
			Expect(sessions[0].StartReason).To(Equal("manual"))
			Expect(sessions[0].StartVehicleSoC).To(BeNil())
			Expect(sessions[0].EndedAt).To(BeNil())
			Expect(sessions[0].StopReason).To(Equal(""))
			Expect(sessions[0].EndVehicleSoC).To(BeNil())

			Expect(sessions[1].ID).To(Equal(id1))
			Expect(sessions[1].StartedAt).To(Equal(t1))
			Expect(sessions[1].EndedAt).NotTo(BeNil())
			Expect(*sessions[1].EndedAt).To(Equal(t1.Add(time.Hour)))
			Expect(sessions[1].StopReason).To(Equal("full"))
			Expect(sessions[1].StartVehicleSoC).To(Equal(intPtr(20)))
			Expect(sessions[1].EndVehicleSoC).To(Equal(intPtr(90)))
			Expect(sessions[1].EnergyWh).To(Equal(5000.0))
			Expect(sessions[1].AvgChargeW).To(Equal(2500.0))
			Expect(sessions[1].PeakChargeW).To(Equal(6000.0))
		})

		It("respects the limit", func() {
			base := time.Date(2026, 6, 28, 0, 0, 0, 0, time.UTC)
			for i := 0; i < 5; i++ {
				_, err := s.StartSession(ctx, base.Add(time.Duration(i)*time.Hour), "surplus", nil)
				Expect(err).NotTo(HaveOccurred())
			}

			sessions, err := s.RecentSessions(ctx, 2)
			Expect(err).NotTo(HaveOccurred())
			Expect(sessions).To(HaveLen(2))
			// Newest (latest started_at) first.
			Expect(sessions[0].StartedAt).To(Equal(base.Add(4 * time.Hour)))
			Expect(sessions[1].StartedAt).To(Equal(base.Add(3 * time.Hour)))
		})
	})
})
