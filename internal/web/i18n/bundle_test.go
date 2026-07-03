package i18n_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/web/i18n"
)

var _ = Describe("Bundle", func() {
	var b *i18n.Bundle

	BeforeEach(func() {
		b = i18n.New()
	})

	Describe("T", func() {
		It("returns the message in the requested language", func() {
			Expect(b.T("de", "nav.footer")).To(Equal("wha · läuft auf dem Pi"))
			Expect(b.T("en", "nav.footer")).To(Equal("wha · running on the Pi"))
		})

		It("falls back to German when the language is unsupported", func() {
			Expect(b.T("fr", "nav.footer")).To(Equal(b.T("de", "nav.footer")))
		})

		It("falls back to the raw key when it is missing from every catalog", func() {
			Expect(b.T("en", "nav.does_not_exist")).To(Equal("nav.does_not_exist"))
		})

		It("formats the message with the given args", func() {
			Expect(b.T("en", "time.ago", "5m")).To(Equal("5m ago"))
			Expect(b.T("de", "time.ago", "5m")).To(Equal("vor 5m"))
		})
	})

	Describe("TOr", func() {
		It("returns the translated label when the key exists", func() {
			Expect(b.TOr("en", "session.reason.soc_cap", "soc_cap")).To(Equal("SoC cap reached"))
		})

		It("returns the fallback when the key is missing from every catalog", func() {
			Expect(b.TOr("en", "session.reason.unmapped_code", "unmapped_code")).To(Equal("unmapped_code"))
		})
	})

	Describe("Match", func() {
		It("matches a simple language tag", func() {
			Expect(b.Match("en")).To(Equal("en"))
		})

		It("matches the primary subtag of a region-qualified tag", func() {
			Expect(b.Match("en-US,en;q=0.8")).To(Equal("en"))
		})

		It("defaults to German for an empty header", func() {
			Expect(b.Match("")).To(Equal("de"))
		})

		It("defaults to German when no tag in the header is supported", func() {
			Expect(b.Match("fr-FR,es;q=0.8")).To(Equal("de"))
		})
	})

	Describe("Resolve", func() {
		It("prefers a supported cookie over the Accept-Language header", func() {
			Expect(b.Resolve("en", "de")).To(Equal("en"))
		})

		It("falls back to matching Accept-Language when the cookie is unsupported", func() {
			Expect(b.Resolve("fr", "en")).To(Equal("en"))
		})

		It("defaults to German when neither is set", func() {
			Expect(b.Resolve("", "")).To(Equal("de"))
		})
	})

	Describe("catalog parity", func() {
		It("has the exact same key set in every locale", func() {
			catalogs := i18n.Catalogs(b)
			en, de := catalogs["en"], catalogs["de"]

			for key := range en {
				Expect(de).To(HaveKey(key), "de.json is missing key %q", key)
			}
			for key := range de {
				Expect(en).To(HaveKey(key), "en.json is missing key %q", key)
			}
		})
	})
})
