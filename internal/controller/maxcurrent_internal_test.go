package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
)

var _ = Describe("Controller max charge current", func() {
	var (
		cfg   config.Control
		cmd   *fakeCommander
		snaps *fakeSnaps
		rec   *fakeRecorder
		clk   *FakeClock
		ctrl  *Controller
		t0    time.Time
		ctx   context.Context
	)

	tick := func(d time.Duration) {
		clk.Advance(d)
		snaps.set(healthySnap(clk.Now()))
		ctrl.tick(ctx, clk.Now())
	}

	BeforeEach(func() {
		cfg = config.Default().Control // MinCurrentA 8, MaxCurrentCeilingA 16
		cmd = &fakeCommander{}
		snaps = &fakeSnaps{}
		rec = &fakeRecorder{}
		t0 = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
		clk = NewFakeClock(t0)
		ctx = context.Background()
		ctrl = New(cfg, cmd, snaps, rec, clk, discardLogger())
	})

	Describe("SetMaxCurrent validation and persistence", func() {
		It("accepts the inclusive [minCurrentA, ceiling] range, persists, and reports it", func() {
			Expect(ctrl.SetMaxCurrent(cfg.MinCurrentA)).To(Succeed())
			Expect(ctrl.Status().MaxCurrentA).To(Equal(cfg.MinCurrentA))

			Expect(ctrl.SetMaxCurrent(cfg.MaxCurrentCeilingA)).To(Succeed())
			Expect(ctrl.Status().MaxCurrentA).To(Equal(cfg.MaxCurrentCeilingA))

			v, ok, err := rec.GetSetting(ctx, "max_current_a")
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal("16"))
		})

		It("reports the accepted bounds in Status", func() {
			st := ctrl.Status()
			Expect(st.MinCurrentA).To(Equal(cfg.MinCurrentA))
			Expect(st.MaxCurrentCeilingA).To(Equal(cfg.MaxCurrentCeilingA))
			Expect(st.MaxCurrentA).To(BeZero(), "no limit by default")
		})

		DescribeTable("rejects out-of-range values with ErrInvalidMaxCurrent and changes nothing",
			func(amps int) {
				Expect(ctrl.SetMaxCurrent(amps)).To(MatchError(ErrInvalidMaxCurrent))
				Expect(ctrl.Status().MaxCurrentA).To(BeZero())
				_, ok, _ := rec.GetSetting(ctx, "max_current_a")
				Expect(ok).To(BeFalse())
			},
			Entry("below minCurrentA", 7),
			Entry("above the ceiling", 17),
			Entry("far above the ceiling", 63),
			Entry("negative", -8),
		)

		It("does not update Status when the persist fails", func() {
			rec.setSettingErr = errFake
			err := ctrl.SetMaxCurrent(10)
			Expect(err).To(HaveOccurred())
			Expect(err).NotTo(MatchError(ErrInvalidMaxCurrent))
			Expect(ctrl.Status().MaxCurrentA).To(BeZero())
		})

		It("treats 0 as clear: an owned limit is lifted to the ceiling and persisted there", func() {
			Expect(ctrl.SetMaxCurrent(10)).To(Succeed())
			Expect(ctrl.SetMaxCurrent(0)).To(Succeed())
			Expect(ctrl.Status().MaxCurrentA).To(Equal(cfg.MaxCurrentCeilingA))
			v, _, _ := rec.GetSetting(ctx, "max_current_a")
			Expect(v).To(Equal("16"))
		})

		It("makes clear a no-op when wha never owned a limit", func() {
			Expect(ctrl.ClearMaxCurrent()).To(Succeed())
			Expect(ctrl.Status().MaxCurrentA).To(BeZero())
			_, ok, _ := rec.GetSetting(ctx, "max_current_a")
			Expect(ok).To(BeFalse())
			Expect(rec.eventsOfType("command")).To(BeEmpty())
		})

		It("does not change state when persisting a clear fails", func() {
			Expect(ctrl.SetMaxCurrent(10)).To(Succeed())
			rec.setSettingErr = errFake
			Expect(ctrl.ClearMaxCurrent()).NotTo(Succeed())
			Expect(ctrl.Status().MaxCurrentA).To(Equal(10))
		})

		It("records an audit event for set and clear", func() {
			Expect(ctrl.SetMaxCurrent(10)).To(Succeed())
			Expect(ctrl.ClearMaxCurrent()).To(Succeed())
			cmds := rec.eventsOfType("command")
			Expect(cmds).To(HaveLen(2))
			Expect(cmds[0].Action).To(Equal("max_current:10"))
			Expect(cmds[1].Action).To(Equal("max_current:clear"))
		})
	})

	Describe("loadMaxCurrent", func() {
		DescribeTable("restores the stored limit, clamping an out-of-range one and persisting the clamp",
			func(stored string, want int, wantStored string) {
				rec.settings = map[string]string{"max_current_a": stored}
				ctrl.loadMaxCurrent(ctx)
				Expect(ctrl.Status().MaxCurrentA).To(Equal(want))
				v, _, _ := rec.GetSetting(ctx, "max_current_a")
				Expect(v).To(Equal(wantStored))
			},
			Entry("a valid limit", "10", 10, "10"),
			Entry("the ceiling (a cleared limit)", "16", 16, "16"),
			Entry("legacy 0 → unset", "0", 0, "0"),
			Entry("negative → unset", "-3", 0, "-3"),
			Entry("garbage → unset", "ten", 0, "ten"),
			// evcc still holds 20 A: dropping it would leave that in force while wha
			// claims "no limit". Clamp to the (since lowered) ceiling instead.
			Entry("above a ceiling lowered since it was stored → clamped down", "20", 16, "16"),
			Entry("below minCurrentA → clamped up", "6", 8, "8"),
		)

		It("publishes a clamped stored limit on the first tick", func() {
			rec.settings = map[string]string{"max_current_a": "20"}
			ctrl.loadMaxCurrent(ctx)
			tick(0)
			Expect(cmd.publishedMaxCurrents()).To(Equal([]int{cfg.MaxCurrentCeilingA}))
		})

		It("leaves the limit unset when nothing is stored", func() {
			ctrl.loadMaxCurrent(ctx)
			Expect(ctrl.Status().MaxCurrentA).To(BeZero())
		})
	})

	Describe("publishing", func() {
		It("never publishes while no limit is set", func() {
			tick(0)
			tick(cfg.Republish + time.Second)

			// Even a broker reconnect does not publish.
			down := healthySnap(clk.Now())
			down.BrokerConnected = false
			snaps.set(down)
			ctrl.tick(ctx, clk.Now())
			tick(cfg.DecisionInterval)

			Expect(cmd.publishedMaxCurrents()).To(BeEmpty())
		})

		It("publishes on first tick, on change, on the cadence, and on reconnect — but not every tick", func() {
			Expect(ctrl.SetMaxCurrent(8)).To(Succeed())
			tick(0)
			Expect(cmd.publishedMaxCurrents()).To(Equal([]int{8}))

			tick(cfg.DecisionInterval) // unchanged plain tick
			Expect(cmd.publishedMaxCurrents()).To(Equal([]int{8}))

			Expect(ctrl.SetMaxCurrent(10)).To(Succeed())
			tick(cfg.DecisionInterval) // change
			Expect(cmd.publishedMaxCurrents()).To(Equal([]int{8, 10}))

			tick(cfg.Republish) // cadence
			Expect(cmd.publishedMaxCurrents()).To(Equal([]int{8, 10, 10}))

			down := healthySnap(clk.Now())
			down.BrokerConnected = false
			snaps.set(down)
			clk.Advance(cfg.DecisionInterval)
			ctrl.tick(ctx, clk.Now()) // disconnected → nothing
			Expect(cmd.publishedMaxCurrents()).To(HaveLen(3))

			tick(cfg.DecisionInterval) // reconnect edge → forced
			Expect(cmd.publishedMaxCurrents()).To(Equal([]int{8, 10, 10, 10}))
		})

		It("re-publishes on the evcc online edge", func() {
			Expect(ctrl.SetMaxCurrent(8)).To(Succeed())
			tick(0)

			off := healthySnap(clk.Now())
			off.Online = evcc.BoolMetric{Value: false, At: clk.Now(), Seen: true}
			snaps.set(off)
			clk.Advance(cfg.DecisionInterval)
			ctrl.tick(ctx, clk.Now())

			tick(cfg.DecisionInterval) // online edge
			Expect(cmd.publishedMaxCurrents()).To(Equal([]int{8, 8}))
		})

		It("retries a failed publish on the next tick", func() {
			Expect(ctrl.SetMaxCurrent(8)).To(Succeed())
			cmd.failMaxCurrent = true
			tick(0)
			Expect(cmd.publishedMaxCurrents()).To(BeEmpty())

			cmd.failMaxCurrent = false
			tick(cfg.DecisionInterval)
			Expect(cmd.publishedMaxCurrents()).To(Equal([]int{8}))
		})

		It("after a clear keeps re-asserting the ceiling like any limit (cadence)", func() {
			Expect(ctrl.SetMaxCurrent(8)).To(Succeed())
			tick(0)
			Expect(ctrl.ClearMaxCurrent()).To(Succeed())

			tick(cfg.DecisionInterval)
			Expect(cmd.publishedMaxCurrents()).To(Equal([]int{8, cfg.MaxCurrentCeilingA}))

			tick(cfg.Republish)
			Expect(cmd.publishedMaxCurrents()).To(Equal([]int{8, 16, 16}))
			Expect(rec.eventsOfType("command")).To(ContainElement(HaveField("Action", "max_current:clear")))
		})

		It("re-sends the ceiling after a clear when evcc comes back online (a lost publish is not final)", func() {
			Expect(ctrl.SetMaxCurrent(8)).To(Succeed())
			tick(0)
			Expect(ctrl.ClearMaxCurrent()).To(Succeed())

			// evcc is offline while the clear is published: that publish is lost.
			off := healthySnap(clk.Now())
			off.Online = evcc.BoolMetric{Value: false, At: clk.Now(), Seen: true}
			snaps.set(off)
			clk.Advance(cfg.DecisionInterval)
			ctrl.tick(ctx, clk.Now())
			Expect(cmd.publishedMaxCurrents()).To(Equal([]int{8, 16}))

			tick(cfg.DecisionInterval) // online edge → forced re-publish
			Expect(cmd.publishedMaxCurrents()).To(Equal([]int{8, 16, 16}))
		})

		It("re-publishes the ceiling after a restart following a clear", func() {
			Expect(ctrl.SetMaxCurrent(8)).To(Succeed())
			tick(0)
			Expect(ctrl.ClearMaxCurrent()).To(Succeed())

			cmd2 := &fakeCommander{}
			fresh := New(cfg, cmd2, snaps, rec, clk, discardLogger())
			fresh.loadMaxCurrent(ctx)
			clk.Advance(cfg.DecisionInterval)
			snaps.set(healthySnap(clk.Now()))
			fresh.tick(ctx, clk.Now())
			Expect(cmd2.publishedMaxCurrents()).To(Equal([]int{cfg.MaxCurrentCeilingA}))
		})

		It("retries a failed publish of the cleared ceiling", func() {
			Expect(ctrl.SetMaxCurrent(8)).To(Succeed())
			tick(0)
			Expect(ctrl.ClearMaxCurrent()).To(Succeed())

			cmd.failMaxCurrent = true
			tick(cfg.DecisionInterval)
			cmd.failMaxCurrent = false
			tick(cfg.DecisionInterval)
			Expect(cmd.publishedMaxCurrents()).To(Equal([]int{8, cfg.MaxCurrentCeilingA}))
		})

		It("publishes nothing when cleared without ever having owned a limit", func() {
			Expect(ctrl.ClearMaxCurrent()).To(Succeed())
			tick(0)
			Expect(cmd.publishedMaxCurrents()).To(BeEmpty())
		})

		It("re-clamps at publish time so nothing above the ceiling ever leaves wha", func() {
			// Bypass SetMaxCurrent's validation to prove the publish-time clamp.
			ctrl.mu.Lock()
			ctrl.ctrl.MaxCurrentA = 63
			ctrl.mu.Unlock()
			tick(0)
			Expect(cmd.publishedMaxCurrents()).To(Equal([]int{cfg.MaxCurrentCeilingA}))
		})

		It("clamps a too-low in-memory value up to minCurrentA (evcc would silently drop it)", func() {
			ctrl.mu.Lock()
			ctrl.ctrl.MaxCurrentA = 2
			ctrl.mu.Unlock()
			tick(0)
			Expect(cmd.publishedMaxCurrents()).To(Equal([]int{cfg.MinCurrentA}))
		})
	})

	Describe("clampMaxCurrent", func() {
		DescribeTable("bounds to [MinCurrentA, MaxCurrentCeilingA] with the ceiling winning",
			func(minA, ceilA, in, want int) {
				c := config.Control{MinCurrentA: minA, MaxCurrentCeilingA: ceilA}
				Expect(clampMaxCurrent(in, c)).To(Equal(want))
			},
			Entry("inside", 6, 16, 10, 10),
			Entry("above", 6, 16, 32, 16),
			Entry("below", 6, 16, 1, 6),
			Entry("misconfigured ceiling below min: ceiling still wins", 10, 8, 9, 8),
		)
	})
})
