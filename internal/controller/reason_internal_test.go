package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// allReasonCodes is the canonical list of every ReasonCode Decide can emit.
// Adding a new code (and a branch that returns it) means adding it here, which
// makes the completeness specs below fail until reasonText also gains an entry —
// the guard that keeps ReasonCode the single source of truth for the prose.
var allReasonCodes = []ReasonCode{
	ReasonStaleOff,
	ReasonOverrideOff,
	ReasonOverrideOn,
	ReasonAwaitingData,
	ReasonNoVehicle,
	ReasonSoCCap,
	ReasonSoCLatch,
	ReasonSurplusDwellStart,
	ReasonSurplusCharging,
	ReasonSurplusDwellPending,
	ReasonSurplusBelowStart,
	ReasonStopDwellStart,
	ReasonStopLow,
	ReasonStopDwellPending,
	ReasonCharging,
}

var _ = Describe("reasonText", func() {
	It("has non-empty prose for every declared reason code", func() {
		for _, code := range allReasonCodes {
			Expect(reasonText[code]).NotTo(BeEmpty(), "missing reasonText entry for %q", code)
		}
	})

	It("has no entries beyond the declared reason codes", func() {
		Expect(reasonText).To(HaveLen(len(allReasonCodes)))
	})
})
