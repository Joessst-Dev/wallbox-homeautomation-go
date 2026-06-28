package store_test

import (
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

func TestStore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Store Suite")
}

// newTempStore opens a Store backed by a fresh temp-file database that is
// closed automatically when the spec ends.
func newTempStore() *store.Store {
	GinkgoHelper()
	path := filepath.Join(GinkgoT().TempDir(), "wha.db")
	s, err := store.Open(path)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		Expect(s.Close()).To(Succeed())
	})
	return s
}

// intPtr is a small helper for building *int values in specs.
func intPtr(v int) *int { return &v }
