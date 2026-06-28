package controller

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
)

// Commander publishes charge commands to evcc. Implemented by *evcc.Client.
type Commander interface {
	SetMode(mode string) error
	SetLimitSoC(pct int) error
}

// SnapshotProvider supplies the latest evcc state. Implemented by *evcc.Store.
type SnapshotProvider interface {
	Snapshot() evcc.Snapshot
}

// Recorder persists sessions, events, and samples. Implemented by *store.Store.
type Recorder interface {
	InsertEvent(ctx context.Context, e store.Event) error
	InsertSample(ctx context.Context, sm store.Sample) error
	StartSession(ctx context.Context, startedAt time.Time, startReason string, startVehicleSoC *int) (int64, error)
	EndSession(ctx context.Context, id int64, endedAt time.Time, stopReason string, endVehicleSoC *int, energyWh, avgChargeW, peakChargeW float64) error
	OpenSession(ctx context.Context) (*store.Session, error)
}

// Controller runs the periodic decision loop: read evcc state, decide, publish
// the desired mode on change, and persist sessions/events/samples.
type Controller struct {
	cfg   config.Control
	cmd   Commander
	snaps SnapshotProvider
	rec   Recorder
	clock Clock
	log   *slog.Logger

	// mu guards only the fields shared with the web layer (Status/SetOverride).
	mu            sync.Mutex
	state         State
	timers        Timers
	override      Override
	overrideUntil time.Time
	lastInputs    Inputs
	lastDecision  Decision
	lastSurplus   float64
	lastSnapshot  evcc.Snapshot
	lastTickAt    time.Time

	// The following are touched only by the loop goroutine (no lock needed).
	lastMode            string // last published desired mode ("" = none yet)
	lastPublishAt       time.Time
	limitSoCSet         bool // limitSoc backstop confirmed published at least once
	lastLimitPublishAt  time.Time
	prevBrokerConnected bool
	prevCharging        bool
	sessionID           int64
	sessionPeakW        float64
	sessionEnWh         float64
	sessionSumW         float64
	sessionTicks        int
}

// New builds a Controller.
func New(cfg config.Control, cmd Commander, snaps SnapshotProvider, rec Recorder, clock Clock, log *slog.Logger) *Controller {
	return &Controller{
		cfg:      cfg,
		cmd:      cmd,
		snaps:    snaps,
		rec:      rec,
		clock:    clock,
		log:      log,
		state:    StateIdle,
		override: OverrideAuto,
	}
}

// Run drives the loop until ctx is canceled. The evcc limitSoc backstop is
// (re)published from within the loop once the broker is connected (see
// publishLimitSoCBackstop), so it survives a broker that is down at startup.
func (c *Controller) Run(ctx context.Context) error {
	c.recoverDanglingSession(ctx)

	ticker := time.NewTicker(c.cfg.DecisionInterval)
	defer ticker.Stop()

	c.tick(ctx, c.clock.Now()) // act immediately on startup
	for {
		select {
		case <-ctx.Done():
			c.log.Info("controller: stopping")
			return nil
		case <-ticker.C:
			c.tick(ctx, c.clock.Now())
		}
	}
}

// tick performs one decision cycle. The mutex is held only for the brief reads
// and writes of web-shared state; all MQTT publishes and DB I/O happen outside
// the lock so a slow broker/disk never stalls the dashboard or override.
func (c *Controller) tick(ctx context.Context, now time.Time) {
	snap := c.snaps.Snapshot()

	c.mu.Lock()
	ov, ovUntil := c.override, c.overrideUntil
	prevState, prevTimers := c.state, c.timers
	c.mu.Unlock()

	in := InputsFromSnapshot(now, snap, c.cfg, ov, ovUntil)
	dec := Decide(now, in, prevState, prevTimers, c.cfg)
	surplus := Surplus(in)
	expireOverride := ov != OverrideAuto && !overrideActive(now, in)

	c.mu.Lock()
	c.state = dec.State
	c.timers = dec.Timers
	if expireOverride && c.override == ov && c.overrideUntil.Equal(ovUntil) {
		c.override = OverrideAuto
		c.overrideUntil = time.Time{}
	}
	c.lastInputs = in
	c.lastDecision = dec
	c.lastSurplus = surplus
	c.lastSnapshot = snap
	c.lastTickAt = now
	c.mu.Unlock()

	// --- loop-owned from here; no lock ---
	reconnected := snap.BrokerConnected && !c.prevBrokerConnected
	c.prevBrokerConnected = snap.BrokerConnected

	if dec.State != prevState {
		c.recordEvent(ctx, store.Event{
			TS: now, Type: "state_change",
			FromState: string(prevState), ToState: string(dec.State),
			Detail: dec.Reason,
		}, in, surplus)
		c.log.Info("controller: state change",
			"from", prevState, "to", dec.State, "reason", dec.Reason,
			"surplus", surplus, "vehicleSoc", in.VehicleSoC)
	}

	c.publishMode(ctx, now, dec, in, surplus, reconnected)
	c.publishLimitSoCBackstop(now, snap.BrokerConnected, reconnected)
	c.trackSession(ctx, now, in, dec.State)
	c.recordSample(ctx, now, in, surplus, dec)
}

// publishMode sends the desired mode when it changes, on reconnect, or on the
// periodic republish cadence (set-topics are not retained).
func (c *Controller) publishMode(ctx context.Context, now time.Time, dec Decision, in Inputs, surplus float64, reconnected bool) {
	if dec.DesiredMode == c.lastMode && !reconnected && now.Sub(c.lastPublishAt) < c.cfg.Republish {
		return
	}
	changed := dec.DesiredMode != c.lastMode
	if err := c.cmd.SetMode(dec.DesiredMode); err != nil {
		c.log.Warn("controller: SetMode failed", "mode", dec.DesiredMode, "err", err)
		return
	}
	if changed {
		c.recordEvent(ctx, store.Event{
			TS: now, Type: "command", Action: "set_mode:" + dec.DesiredMode,
			Detail: dec.Reason,
		}, in, surplus)
	}
	c.lastMode = dec.DesiredMode
	c.lastPublishAt = now
}

// publishLimitSoCBackstop keeps evcc's loadpoint limitSoc set to the SoC cap so
// evcc enforces the stop even if wha dies. It retries until the first success,
// re-asserts on every broker reconnect, and refreshes on the republish cadence.
func (c *Controller) publishLimitSoCBackstop(now time.Time, brokerConnected, reconnected bool) {
	if !brokerConnected {
		return
	}
	if c.limitSoCSet && !reconnected && now.Sub(c.lastLimitPublishAt) < c.cfg.Republish {
		return
	}
	if err := c.cmd.SetLimitSoC(c.cfg.SoCCap); err != nil {
		c.log.Warn("controller: SetLimitSoC backstop failed (will retry)", "err", err)
		return
	}
	c.limitSoCSet = true
	c.lastLimitPublishAt = now
	c.log.Debug("controller: limitSoc backstop published", "cap", c.cfg.SoCCap)
}

// trackSession opens/closes charge sessions on the evcc charging edge and
// accumulates energy while charging (skipping stale windows to avoid drift).
func (c *Controller) trackSession(ctx context.Context, now time.Time, in Inputs, state State) {
	switch {
	case in.Charging && !c.prevCharging:
		c.sessionPeakW, c.sessionEnWh, c.sessionSumW, c.sessionTicks = 0, 0, 0, 0
		id, err := c.rec.StartSession(ctx, now, startReasonFor(now, in), intPtrOrNil(in.VehicleSoC))
		if err != nil {
			c.log.Warn("controller: StartSession failed", "err", err)
		} else {
			c.sessionID = id
		}
	case !in.Charging && c.prevCharging:
		c.closeSession(ctx, now, in, stopReasonFor(now, state, in))
	}

	if in.Charging && !in.Stale {
		dt := c.cfg.DecisionInterval.Hours()
		c.sessionEnWh += in.ChargeW * dt
		c.sessionSumW += in.ChargeW
		c.sessionTicks++
		if in.ChargeW > c.sessionPeakW {
			c.sessionPeakW = in.ChargeW
		}
	}
	c.prevCharging = in.Charging
}

func (c *Controller) closeSession(ctx context.Context, now time.Time, in Inputs, reason string) {
	if c.sessionID == 0 {
		return
	}
	avg := 0.0
	if c.sessionTicks > 0 {
		avg = c.sessionSumW / float64(c.sessionTicks)
	}
	if err := c.rec.EndSession(ctx, c.sessionID, now, reason, intPtrOrNil(in.VehicleSoC),
		c.sessionEnWh, avg, c.sessionPeakW); err != nil {
		c.log.Warn("controller: EndSession failed", "err", err)
	}
	c.sessionID = 0
}

// recoverDanglingSession closes any session left open by a previous run.
func (c *Controller) recoverDanglingSession(ctx context.Context) {
	open, err := c.rec.OpenSession(ctx)
	if err != nil {
		c.log.Warn("controller: OpenSession check failed", "err", err)
		return
	}
	if open != nil {
		if err := c.rec.EndSession(ctx, open.ID, c.clock.Now(), "restart", nil,
			open.EnergyWh, open.AvgChargeW, open.PeakChargeW); err != nil {
			c.log.Warn("controller: failed to close dangling session", "err", err)
		} else {
			c.log.Info("controller: closed dangling session", "id", open.ID)
		}
	}
}

func startReasonFor(now time.Time, in Inputs) string {
	if overrideActive(now, in) {
		return "override"
	}
	return "surplus"
}

func stopReasonFor(now time.Time, state State, in Inputs) string {
	switch state {
	case StateSocReached:
		return "soc_cap"
	case StateFailSafe:
		return "stale"
	default:
		if overrideActive(now, in) {
			return "override"
		}
		return "surplus_low"
	}
}

func (c *Controller) recordEvent(ctx context.Context, e store.Event, in Inputs, surplus float64) {
	e.SurplusW = surplus
	e.GridW = in.GridW
	e.PVW = in.PVW
	e.BatterySoC = in.BatterySoC
	e.BatteryW = in.BatteryW
	e.VehicleSoC = in.VehicleSoC
	e.ChargeW = in.ChargeW
	if err := c.rec.InsertEvent(ctx, e); err != nil {
		c.log.Warn("controller: InsertEvent failed", "err", err)
	}
}

func (c *Controller) recordSample(ctx context.Context, now time.Time, in Inputs, surplus float64, dec Decision) {
	sm := store.Sample{
		TS: now, GridW: in.GridW, PVW: in.PVW, HomeW: in.HomeW,
		BatterySoC: in.BatterySoC, BatteryW: in.BatteryW,
		ChargeW: in.ChargeW, VehicleSoC: in.VehicleSoC,
		Charging: in.Charging, Mode: dec.DesiredMode,
		SurplusW: surplus, State: string(dec.State),
	}
	if err := c.rec.InsertSample(ctx, sm); err != nil {
		c.log.Warn("controller: InsertSample failed", "err", err)
	}
}

// SetOverride sets the manual override. until is the auto-revert time; pass the
// zero time for no expiry. Setting OverrideAuto resumes automatic control.
func (c *Controller) SetOverride(mode Override, until time.Time) {
	c.mu.Lock()
	c.override = mode
	c.overrideUntil = until
	c.mu.Unlock()

	if err := c.rec.InsertEvent(context.Background(), store.Event{
		TS: c.clock.Now(), Type: "override", Action: "override:" + string(mode),
	}); err != nil {
		c.log.Warn("controller: InsertEvent (override) failed", "err", err)
	}
	c.log.Info("controller: override set", "mode", mode, "until", until)
}

// StatusView is a read-only snapshot of the controller for the web layer.
type StatusView struct {
	State         State
	DesiredMode   string
	Reason        string
	Override      Override
	OverrideUntil time.Time
	Surplus       float64
	Inputs        Inputs
	Snapshot      evcc.Snapshot
	UpdatedAt     time.Time
}

// Status returns the latest decision state.
func (c *Controller) Status() StatusView {
	c.mu.Lock()
	defer c.mu.Unlock()
	return StatusView{
		State:         c.state,
		DesiredMode:   c.lastDecision.DesiredMode,
		Reason:        c.lastDecision.Reason,
		Override:      c.override,
		OverrideUntil: c.overrideUntil,
		Surplus:       c.lastSurplus,
		Inputs:        c.lastInputs,
		Snapshot:      c.lastSnapshot,
		UpdatedAt:     c.lastTickAt,
	}
}

func intPtrOrNil(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}
