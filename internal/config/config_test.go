package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
)

func TestConfig(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Config Suite")
}

var _ = Describe("Config.Validate socMax", func() {
	var cfg config.Config

	BeforeEach(func() {
		cfg = config.Default() // SoCCap 80, SoCMax 100
	})

	It("accepts the default", func() {
		Expect(cfg.Validate()).To(Succeed())
	})

	It("accepts a ceiling between the cap and 100", func() {
		cfg.Control.SoCMax = 90
		Expect(cfg.Validate()).To(Succeed())
	})

	It("rejects a ceiling at or below the cap", func() {
		cfg.Control.SoCMax = 80
		Expect(cfg.Validate()).To(MatchError(ContainSubstring("socMax")))
	})

	It("rejects a ceiling above 100", func() {
		cfg.Control.SoCMax = 101
		Expect(cfg.Validate()).To(MatchError(ContainSubstring("socMax")))
	})
})

var _ = Describe("Config defaults for the charge-power floor, stall detection, and max current", func() {
	It("defaults to an 8 A three-phase fallback with a 200 W start margin", func() {
		c := config.Default().Control
		Expect(c.MinCurrentA).To(Equal(8))
		Expect(c.Phases).To(Equal(3))
		Expect(c.StartMarginW).To(Equal(200.0))
	})

	It("defaults the max-current ceiling to a 16 A circuit", func() {
		Expect(config.Default().Control.MaxCurrentCeilingA).To(Equal(16))
	})

	It("defaults the stall timeout to 3m and the recovery off-pulse to 90s", func() {
		c := config.Default().Control
		Expect(c.StallTimeout).To(Equal(3 * time.Minute))
		Expect(c.StallRecoveryOff).To(Equal(90 * time.Second))
	})
})

var _ = Describe("Config.Validate charge-current, phases, and stall settings", func() {
	DescribeTable("validates the new control settings",
		func(mutate func(c *config.Control), wantErr string) {
			cfg := config.Default()
			mutate(&cfg.Control)
			if wantErr == "" {
				Expect(cfg.Validate()).To(Succeed())
				return
			}
			Expect(cfg.Validate()).To(MatchError(ContainSubstring(wantErr)))
		},
		Entry("minCurrentA at the 6 A lower bound", func(c *config.Control) { c.MinCurrentA = 6 }, ""),
		Entry("minCurrentA below 6 A", func(c *config.Control) { c.MinCurrentA = 5 }, "minCurrentA"),
		Entry("minCurrentA above 32 A", func(c *config.Control) {
			c.MinCurrentA = 33
			c.MaxCurrentCeilingA = 32
		}, "minCurrentA"),
		Entry("single phase", func(c *config.Control) { c.Phases = 1 }, ""),
		Entry("two phases", func(c *config.Control) { c.Phases = 2 }, "phases"),
		Entry("zero phases", func(c *config.Control) { c.Phases = 0 }, "phases"),
		Entry("zero start margin", func(c *config.Control) { c.StartMarginW = 0 }, ""),
		Entry("negative start margin", func(c *config.Control) { c.StartMarginW = -1 }, "startMarginW"),
		Entry("ceiling equal to minCurrentA", func(c *config.Control) { c.MaxCurrentCeilingA = c.MinCurrentA }, ""),
		Entry("ceiling below minCurrentA", func(c *config.Control) { c.MaxCurrentCeilingA = c.MinCurrentA - 1 }, "maxCurrentCeilingA"),
		Entry("ceiling at 32 A", func(c *config.Control) { c.MaxCurrentCeilingA = 32 }, ""),
		Entry("ceiling above 32 A", func(c *config.Control) { c.MaxCurrentCeilingA = 33 }, "maxCurrentCeilingA"),
		Entry("zero stall timeout", func(c *config.Control) { c.StallTimeout = 0 }, "stallTimeout"),
		Entry("zero stall recovery off-pulse", func(c *config.Control) { c.StallRecoveryOff = 0 }, "stallRecoveryOff"),
	)
})

var _ = Describe("Load env overrides for the new control keys", func() {
	It("maps WHA_CONTROL_* env vars onto the camelCase keys", func() {
		t := GinkgoT()
		// Point WHA_CONFIG at an empty file so a stray config.yaml can't interfere.
		path := filepath.Join(t.TempDir(), "config.yaml")
		Expect(os.WriteFile(path, []byte("{}\n"), 0o600)).To(Succeed())
		t.Setenv("WHA_CONFIG", path)
		t.Setenv("WHA_CONTROL_MINCURRENTA", "6")
		t.Setenv("WHA_CONTROL_PHASES", "1")
		t.Setenv("WHA_CONTROL_STARTMARGINW", "150")
		t.Setenv("WHA_CONTROL_MAXCURRENTCEILINGA", "10")
		t.Setenv("WHA_CONTROL_STALLTIMEOUT", "4m")
		t.Setenv("WHA_CONTROL_STALLRECOVERYOFF", "2m")

		cfg, err := config.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Control.MinCurrentA).To(Equal(6))
		Expect(cfg.Control.Phases).To(Equal(1))
		Expect(cfg.Control.StartMarginW).To(Equal(150.0))
		Expect(cfg.Control.MaxCurrentCeilingA).To(Equal(10))
		Expect(cfg.Control.StallTimeout).To(Equal(4 * time.Minute))
		Expect(cfg.Control.StallRecoveryOff).To(Equal(2 * time.Minute))
	})
})
