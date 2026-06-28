package evcc_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEvcc(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "EVCC Suite")
}
