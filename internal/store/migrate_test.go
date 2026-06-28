package store_test

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

var _ = Describe("Migrations", func() {
	var dbPath string

	BeforeEach(func() {
		dbPath = filepath.Join(GinkgoT().TempDir(), "wha.db")
	})

	It("runs migrations on Open, producing a usable schema", func() {
		s, err := store.Open(dbPath)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(s.Close()).To(Succeed()) })

		// Schema is usable: a repo query against a migrated table succeeds.
		_, err = s.RecentSessions(context.Background(), 10)
		Expect(err).NotTo(HaveOccurred())
	})

	It("is idempotent: re-opening the same database succeeds", func() {
		s1, err := store.Open(dbPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(s1.Close()).To(Succeed())

		s2, err := store.Open(dbPath)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(s2.Close()).To(Succeed()) })

		_, err = s2.RecentEvents(context.Background(), 10)
		Expect(err).NotTo(HaveOccurred())
	})
})
