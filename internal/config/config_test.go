package config_test

import (
	"testing"

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
