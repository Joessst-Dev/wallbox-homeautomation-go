package store_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

var _ = Describe("Settings KV", func() {
	var (
		s   *store.Store
		ctx context.Context
	)

	BeforeEach(func() {
		s = newTempStore()
		ctx = context.Background()
	})

	It("reports not-found for an unset key", func() {
		v, ok, err := s.GetSetting(ctx, "charge_power")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		Expect(v).To(BeEmpty())
	})

	It("round-trips a value", func() {
		Expect(s.SetSetting(ctx, "charge_power", "now")).To(Succeed())
		v, ok, err := s.GetSetting(ctx, "charge_power")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("now"))
	})

	It("upserts an existing key", func() {
		Expect(s.SetSetting(ctx, "charge_power", "now")).To(Succeed())
		Expect(s.SetSetting(ctx, "charge_power", "pv")).To(Succeed())
		v, ok, err := s.GetSetting(ctx, "charge_power")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("pv"))
	})

	It("distinguishes a stored empty string from not-found", func() {
		Expect(s.SetSetting(ctx, "empty", "")).To(Succeed())
		v, ok, err := s.GetSetting(ctx, "empty")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(v).To(BeEmpty())
	})
})
