package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"strconv"
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
	SetMaxCurrent(amps int) error
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
	UpdateSessionMetrics(ctx context.Context, id int64, energyWh, avgChargeW, peakChargeW float64) error
	EndSession(ctx context.Context, id int64, endedAt time.Time, stopReason string, endVehicleSoC *int, energyWh, avgChargeW, peakChargeW float64) error
	OpenSession(ctx context.Context) (*store.Session, error)
	PruneSamples(ctx context.Context, before time.Time) (int64, error)
	PruneEvents(ctx context.Context, before time.Time) (int64, error)
	GetSetting(ctx context.Context, key string) (string, bool, error)
	SetSetting(ctx context.Context, key, value string) error
}

// Settings keys under which the persistent operator choices are stored.
// startThresholdKeyPrefix/targetSoCKeyPrefix are joined with a profile name
// (via settingKey) since those two are per-latch-profile, not global.
const (
	chargePowerSetting      = "charge_power"
	autoProfileSetting      = "auto_profile"
	startThresholdKeyPrefix = "start_threshold_w:"
	targetSoCKeyPrefix      = "target_soc:"
	maxCurrentSetting       = "max_current_a"
)

// settingKey joins a per-profile settings key prefix with the profile name.
func settingKey(prefix, profile string) string {
	return prefix + profile
}

// latchProfiles lists the auto-charge profiles that get their own persisted
// start-threshold/target-SoC overrides (config.AutoProfileSurplus uses the
// static cfg.StartThresholdW/cfg.SoCCap instead).
var latchProfiles = []string{config.AutoProfilePowerLatch, config.AutoProfileSurplusLatch}

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
	mu           sync.Mutex
	state        State
	timers       Timers
	ctrl         ControlState
	lastInputs   Inputs
	lastDecision Decision
	lastSurplus  float64
	lastSnapshot evcc.Snapshot
	lastTickAt   time.Time
	// stall is written only by the loop (inside the locked section of tick) and
	// read by Status; the loop reads its own previous value under the lock too.
	stall StallState

	// The following are touched only by the loop goroutine (no lock needed).
	lastMode           string // last published desired mode ("" = none yet)
	lastPublishAt      time.Time
	limitSoCSet        bool // limitSoc backstop confirmed published at least once
	lastLimitSoC       int  // last published limitSoc target (for change detection)
	lastLimitPublishAt time.Time
	// lastMaxCurrent is the max-current limit wha last published (0 = none yet).
	lastMaxCurrent          int
	lastMaxCurrentPublishAt time.Time
	// lastStartThresholdW is the last effective start threshold logged, so the
	// min-charge-power floor is logged on change rather than every tick.
	lastStartThresholdW float64
	prevBrokerConnected bool
	prevOnline          bool
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
		cfg:   cfg,
		cmd:   cmd,
		snaps: snaps,
		rec:   rec,
		clock: clock,
		log:   log,
		state: StateIdle,
		ctrl: ControlState{
			Override:    OverrideAuto,
			ChargePower: cfg.EnableMode,
			AutoProfile: cfg.AutoProfile,
		},
	}
}

// Run drives the loop until ctx is canceled. The evcc limitSoc backstop is
// (re)published from within the loop once the broker is connected (see
// publishLimitSoCBackstop), so it survives a broker that is down at startup.
func (c *Controller) Run(ctx context.Context) error {
	c.recoverDanglingSession(ctx)
	c.loadChargePower(ctx)
	c.loadAutoProfile(ctx)
	c.loadStartThreshold(ctx)
	c.loadTargetSoC(ctx)
	c.loadMaxCurrent(ctx)

	if c.cfg.RetentionWindow > 0 {
		go c.janitor(ctx)
	}

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
	ctrl := c.ctrl
	// ctrl is a shallow struct copy, so its per-profile maps still alias c.ctrl's
	// live storage. Clone them under the lock before reading them unlocked below
	// (via latchOverrides in InputsFromSnapshot), so a concurrent Set* write can't
	// trigger a fatal "concurrent map read and map write". maps.Clone is nil-safe.
	ctrl.StartThresholdW = maps.Clone(c.ctrl.StartThresholdW)
	ctrl.TargetSoC = maps.Clone(c.ctrl.TargetSoC)
	prevState, prevTimers := c.state, c.timers
	prevStall := c.stall
	c.mu.Unlock()

	in := InputsFromSnapshot(now, snap, c.cfg, ctrl)
	dec := Decide(now, in, prevState, prevTimers, c.cfg)
	surplus := Surplus(in)
	limitTarget := LimitSoCTarget(now, in, c.cfg)
	expireOverride := ctrl.Override != OverrideAuto && !overrideActive(now, in)
	// Stall detection runs after Decide and never alters it: it can only swap
	// the *published* mode for off during its one recovery pulse.
	stall := StepStall(now, in, dec.DesiredMode, limitTarget, prevStall, c.cfg)

	c.mu.Lock()
	c.state = dec.State
	c.timers = dec.Timers
	c.stall = stall
	// Only clear the override if it is still the one we read above: a SetOverride
	// (e.g. a manual ForceOff) racing in during the unlocked window must win, not
	// be silently discarded by this stale-read expiry. The cap-bypass clears with
	// the override; the persistent charge-power setting is left untouched.
	if expireOverride && c.ctrl.Override == ctrl.Override &&
		c.ctrl.OverrideUntil.Equal(ctrl.OverrideUntil) && c.ctrl.CapBypass == ctrl.CapBypass {
		c.ctrl.Override = OverrideAuto
		c.ctrl.OverrideUntil = time.Time{}
		c.ctrl.CapBypass = false
		c.ctrl.CapBypassSoC = 0
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
	// onlineEdge fires when evcc's LWT transitions to online (e.g. evcc restarted
	// while our broker connection stayed up): re-assert mode + limitSoc backstop
	// without waiting for the Republish cadence.
	onlineEdge := snap.Online.Value && !c.prevOnline
	c.prevOnline = snap.Online.Value
	force := reconnected || onlineEdge

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

	c.logStartThreshold(in)
	c.recordStallTransitions(ctx, now, prevStall, stall, in, surplus)

	// pub is the decision actually sent to evcc: Decide's, except during the
	// stall-recovery pulse, which may only ever force off.
	pub := dec
	if stall.RecoveryActive(now) {
		pub.DesiredMode = ModeOff
		pub.Reason = "stall recovery: off pulse"
	}

	c.publishMode(ctx, now, pub, in, surplus, force)
	c.publishLimitSoC(ctx, now, limitTarget, in, surplus, snap.BrokerConnected, force)
	c.publishMaxCurrent(ctx, now, ctrl.MaxCurrentA, in, surplus, snap.BrokerConnected, force)
	c.trackSession(ctx, now, in, dec.State)
	c.recordSample(ctx, now, in, surplus, pub)
}

// logStartThreshold logs the effective start threshold whenever it changes
// while the min-charge-power floor lifts it above the configured value, so the
// operator can see why a low startThresholdW is not honored without a log line
// per tick.
func (c *Controller) logStartThreshold(in Inputs) {
	eff := effectiveStartThreshold(in, c.cfg)
	if eff == c.lastStartThresholdW {
		return
	}
	c.lastStartThresholdW = eff
	if configured := configuredStartThreshold(in, c.cfg); eff > configured {
		c.log.Info("controller: start threshold floored at minimum charge power",
			"configuredW", configured, "effectiveW", eff,
			"minChargePowerW", MinChargePowerW(in, c.cfg), "startMarginW", c.cfg.StartMarginW)
	}
}

// recordStallTransitions writes audit events and logs for the stall latch and
// the recovery pulse edges between prev and next.
func (c *Controller) recordStallTransitions(ctx context.Context, now time.Time, prev, next StallState, in Inputs, surplus float64) {
	if !prev.Stalled && next.Stalled {
		c.recordEvent(ctx, store.Event{
			TS: now, Type: "stalled", Action: "detected",
			Detail: "charging wanted and enabled, but no power for " + c.cfg.StallTimeout.String(),
		}, in, surplus)
		c.log.Warn("controller: loadpoint stalled", "since", next.StalledSince,
			"chargeW", in.ChargeW, "vehicleSoc", in.VehicleSoC, "recoveryPending", next.RecoveryActive(now))
	}
	if prev.Stalled && !next.Stalled {
		c.recordEvent(ctx, store.Event{TS: now, Type: "stalled", Action: "cleared"}, in, surplus)
		c.log.Info("controller: loadpoint stall cleared")
	}
	if prev.RecoveryUntil.IsZero() && !next.RecoveryUntil.IsZero() {
		c.recordEvent(ctx, store.Event{
			TS: now, Type: "stall_recovery", Action: "off",
			Detail: "one-time off pulse for " + c.cfg.StallRecoveryOff.String(),
		}, in, surplus)
		c.log.Warn("controller: stall recovery: forcing off", "until", next.RecoveryUntil)
	}
	if !prev.RecoveryUntil.IsZero() && next.RecoveryUntil.IsZero() {
		c.recordEvent(ctx, store.Event{TS: now, Type: "stall_recovery", Action: "resume"}, in, surplus)
		c.log.Info("controller: stall recovery: resuming normal control")
	}
}

// publishMode sends the desired mode when it changes, when forced (broker
// reconnect or evcc online edge), or on the periodic republish cadence
// (set-topics are not retained).
func (c *Controller) publishMode(ctx context.Context, now time.Time, dec Decision, in Inputs, surplus float64, force bool) {
	if dec.DesiredMode == c.lastMode && !force && now.Sub(c.lastPublishAt) < c.cfg.Republish {
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

// publishLimitSoC keeps evcc's loadpoint limitSoc set to target so evcc enforces
// the stop even if wha dies. target is normally the SoC cap, but is lifted to
// SoCMax while an explicit cap-bypass override is active. It publishes on first
// run, whenever the target changes (so reverting a bypass restores the cap at
// once), when forced (broker reconnect or evcc online edge), and on the
// republish cadence.
func (c *Controller) publishLimitSoC(ctx context.Context, now time.Time, target int, in Inputs, surplus float64, brokerConnected, force bool) {
	if !brokerConnected {
		return
	}
	changed := target != c.lastLimitSoC
	if c.limitSoCSet && !changed && !force && now.Sub(c.lastLimitPublishAt) < c.cfg.Republish {
		return
	}
	if err := c.cmd.SetLimitSoC(target); err != nil {
		c.log.Warn("controller: SetLimitSoC backstop failed (will retry)", "err", err)
		return
	}
	if changed && c.limitSoCSet {
		c.recordEvent(ctx, store.Event{
			TS: now, Type: "command", Action: "set_limit_soc:" + strconv.Itoa(target),
		}, in, surplus)
	}
	c.limitSoCSet = true
	c.lastLimitSoC = target
	c.lastLimitPublishAt = now
	c.log.Debug("controller: limitSoc backstop published", "limit", target)
}

// clampMaxCurrent bounds a max-current limit to [cfg.MinCurrentA,
// cfg.MaxCurrentCeilingA]. The ceiling is applied last so it always wins: wha
// never publishes above the circuit rating, whatever got into memory.
func clampMaxCurrent(amps int, cfg config.Control) int {
	amps = max(amps, cfg.MinCurrentA)
	return min(amps, cfg.MaxCurrentCeilingA)
}

// publishMaxCurrent keeps evcc's loadpoint maxCurrent at the operator's limit.
// Like the limitSoc backstop it publishes on first run, on change, when forced
// (broker reconnect / evcc online edge), and on the republish cadence, and the
// value is re-clamped at publish time. want == 0 means wha has never owned a
// limit, so nothing is published. (A cleared limit is held at the ceiling, not
// 0 — see ClearMaxCurrent — so it keeps being re-asserted like any other.)
func (c *Controller) publishMaxCurrent(ctx context.Context, now time.Time, want int, in Inputs, surplus float64, brokerConnected, force bool) {
	if !brokerConnected || want <= 0 {
		return
	}

	amps := clampMaxCurrent(want, c.cfg)
	changed := amps != c.lastMaxCurrent
	if !changed && !force && now.Sub(c.lastMaxCurrentPublishAt) < c.cfg.Republish {
		return
	}
	if err := c.cmd.SetMaxCurrent(amps); err != nil {
		c.log.Warn("controller: SetMaxCurrent failed (will retry)", "amps", amps, "err", err)
		return
	}
	if changed {
		c.recordEvent(ctx, store.Event{
			TS: now, Type: "command", Action: "set_max_current:" + strconv.Itoa(amps),
		}, in, surplus)
	}
	c.lastMaxCurrent = amps
	c.lastMaxCurrentPublishAt = now
	c.log.Debug("controller: maxCurrent published", "amps", amps)
}

// trackSession opens/closes charge sessions on the evcc charging edge and
// accumulates energy while charging (skipping stale windows to avoid drift).
func (c *Controller) trackSession(ctx context.Context, now time.Time, in Inputs, state State) {
	switch {
	case in.Charging && !c.prevCharging:
		c.sessionPeakW, c.sessionEnWh, c.sessionSumW, c.sessionTicks = 0, 0, 0, 0
		id, err := c.rec.StartSession(ctx, now, startReasonFor(now, in), socPtr(in.VehicleSoC, in.VehicleSoCKnown))
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
		// Persist the running totals every tick so a crash leaves the open row with
		// meaningful energy/avg/peak for recoverDanglingSession to close (single
		// writer + WAL keeps this cheap at our volume).
		if c.sessionID != 0 {
			avg := c.sessionSumW / float64(c.sessionTicks) // ticks >= 1 here
			if err := c.rec.UpdateSessionMetrics(ctx, c.sessionID, c.sessionEnWh, avg, c.sessionPeakW); err != nil {
				c.log.Warn("controller: UpdateSessionMetrics failed", "err", err)
			}
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
	if err := c.rec.EndSession(ctx, c.sessionID, now, reason, socPtr(in.VehicleSoC, in.VehicleSoCKnown),
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

// janitor periodically prunes samples and events older than RetentionWindow so
// the SQLite database does not grow without bound. It runs until ctx is done.
func (c *Controller) janitor(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.RetentionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.prune(ctx)
		}
	}
}

// prune deletes samples and events older than the retention window, logging the
// number of rows removed.
func (c *Controller) prune(ctx context.Context) {
	before := c.clock.Now().Add(-c.cfg.RetentionWindow)
	samples, err := c.rec.PruneSamples(ctx, before)
	if err != nil {
		c.log.Warn("controller: PruneSamples failed", "err", err)
	}
	events, err := c.rec.PruneEvents(ctx, before)
	if err != nil {
		c.log.Warn("controller: PruneEvents failed", "err", err)
	}
	if samples > 0 || events > 0 {
		c.log.Info("controller: pruned old rows",
			"before", before, "samples", samples, "events", events)
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
// capBypass is meaningful only for OverrideForceOn: it lifts the SoC cap to the
// chosen capBypassSoC target (or SoCMax when 0) for the duration of the override;
// it is forced off for any other mode.
func (c *Controller) SetOverride(mode Override, until time.Time, capBypass bool, capBypassSoC int) {
	if mode != OverrideForceOn {
		capBypass = false
	}
	// Keep CapBypassSoC consistent with CapBypass: a target only has meaning while
	// the bypass is on, so never retain a stale value the operator didn't intend.
	if !capBypass {
		capBypassSoC = 0
	}
	c.mu.Lock()
	c.ctrl.Override = mode
	c.ctrl.OverrideUntil = until
	c.ctrl.CapBypass = capBypass
	c.ctrl.CapBypassSoC = capBypassSoC
	c.mu.Unlock()

	action := "override:" + string(mode)
	if capBypass {
		action += ":past_cap"
	}
	if err := c.rec.InsertEvent(context.Background(), store.Event{
		TS: c.clock.Now(), Type: "override", Action: action,
	}); err != nil {
		c.log.Warn("controller: InsertEvent (override) failed", "err", err)
	}
	c.log.Info("controller: override set", "mode", mode, "until", until, "capBypass", capBypass, "capBypassSoC", capBypassSoC)
}

// ErrInvalidChargePower is returned by SetChargePower when the requested mode is
// not a recognized evcc charge mode. Callers (e.g. the web layer) use it to tell
// a client error apart from a persistence failure.
var ErrInvalidChargePower = errors.New("invalid charge power mode")

// SetChargePower sets the persistent charge-power mode (config.EnableModePV or
// config.EnableModeNow) used whenever charging is enabled, in both automatic and
// force-on charging. The choice is persisted so it survives restarts. The DB
// write happens before the in-memory update so a failed persist never leaves
// Status() reporting a mode that a restart would not restore.
func (c *Controller) SetChargePower(mode string) error {
	if mode != config.EnableModePV && mode != config.EnableModeNow {
		return fmt.Errorf("%w %q (want %q or %q)", ErrInvalidChargePower, mode, config.EnableModePV, config.EnableModeNow)
	}
	if err := c.rec.SetSetting(context.Background(), chargePowerSetting, mode); err != nil {
		return fmt.Errorf("persist charge power: %w", err)
	}
	c.mu.Lock()
	c.ctrl.ChargePower = mode
	c.mu.Unlock()

	if err := c.rec.InsertEvent(context.Background(), store.Event{
		TS: c.clock.Now(), Type: "command", Action: "charge_power:" + mode,
	}); err != nil {
		c.log.Warn("controller: InsertEvent (charge_power) failed", "err", err)
	}
	c.log.Info("controller: charge power set", "mode", mode)
	return nil
}

// loadChargePower restores the persisted charge-power mode at startup, keeping
// the config default when none is stored or the stored value is invalid.
func (c *Controller) loadChargePower(ctx context.Context) {
	mode, ok, err := c.rec.GetSetting(ctx, chargePowerSetting)
	if err != nil {
		c.log.Warn("controller: load charge power failed (using default)", "err", err)
		return
	}
	if !ok || (mode != config.EnableModePV && mode != config.EnableModeNow) {
		return
	}
	c.mu.Lock()
	c.ctrl.ChargePower = mode
	c.mu.Unlock()
	c.log.Info("controller: charge power restored", "mode", mode)
}

// ErrInvalidAutoProfile is returned by SetAutoProfile when the requested
// profile is not one of the recognized auto-charge profiles.
var ErrInvalidAutoProfile = errors.New("invalid auto-charge profile")

// SetAutoProfile sets the persistent auto-charge profile (config.AutoProfileSurplus,
// AutoProfilePowerLatch, or AutoProfileSurplusLatch) used for automatic control.
// The DB write happens before the in-memory update so a failed persist never
// leaves Status() reporting a profile that a restart would not restore.
func (c *Controller) SetAutoProfile(profile string) error {
	switch profile {
	case config.AutoProfileSurplus, config.AutoProfilePowerLatch, config.AutoProfileSurplusLatch:
	default:
		return fmt.Errorf("%w %q (want %q, %q, or %q)", ErrInvalidAutoProfile, profile,
			config.AutoProfileSurplus, config.AutoProfilePowerLatch, config.AutoProfileSurplusLatch)
	}
	if err := c.rec.SetSetting(context.Background(), autoProfileSetting, profile); err != nil {
		return fmt.Errorf("persist auto profile: %w", err)
	}
	c.mu.Lock()
	c.ctrl.AutoProfile = profile
	c.mu.Unlock()

	if err := c.rec.InsertEvent(context.Background(), store.Event{
		TS: c.clock.Now(), Type: "command", Action: "auto_profile:" + profile,
	}); err != nil {
		c.log.Warn("controller: InsertEvent (auto_profile) failed", "err", err)
	}
	c.log.Info("controller: auto profile set", "profile", profile)
	return nil
}

// loadAutoProfile restores the persisted auto-charge profile at startup,
// keeping the config default when none is stored or the stored value is
// invalid.
func (c *Controller) loadAutoProfile(ctx context.Context) {
	profile, ok, err := c.rec.GetSetting(ctx, autoProfileSetting)
	if err != nil {
		c.log.Warn("controller: load auto profile failed (using default)", "err", err)
		return
	}
	switch profile {
	case config.AutoProfileSurplus, config.AutoProfilePowerLatch, config.AutoProfileSurplusLatch:
	default:
		return
	}
	if !ok {
		return
	}
	c.mu.Lock()
	c.ctrl.AutoProfile = profile
	c.mu.Unlock()
	c.log.Info("controller: auto profile restored", "profile", profile)
}

// ErrInvalidStartThreshold is returned by SetStartThreshold when the requested
// value fails validation.
var ErrInvalidStartThreshold = errors.New("invalid start threshold")

// SetStartThreshold sets the persistent start-threshold override (in watts,
// 100 W steps) used by the active auto-charge profile's start trigger. It must
// be positive, a multiple of 100, and greater than cfg.StopThresholdW — the
// same invariant config.Validate enforces for the static default, re-checked
// here because a runtime value bypasses it. It only applies to a latch
// profile (config.AutoProfilePowerLatch, AutoProfileSurplusLatch); the default
// surplus profile uses the static cfg.StartThresholdW hysteresis instead. The
// value is stored under the active profile's own key, so switching profiles
// never leaks one profile's threshold into another.
func (c *Controller) SetStartThreshold(w float64) error {
	if w <= 0 || math.Mod(w, 100) != 0 {
		return fmt.Errorf("%w %.0f (want a positive multiple of 100)", ErrInvalidStartThreshold, w)
	}
	if w <= c.cfg.StopThresholdW {
		return fmt.Errorf("%w %.0f (must be greater than stopThresholdW %.0f)", ErrInvalidStartThreshold, w, c.cfg.StopThresholdW)
	}
	c.mu.Lock()
	profile := c.ctrl.AutoProfile
	c.mu.Unlock()
	if profile == "" {
		profile = c.cfg.AutoProfile
	}
	if profile != config.AutoProfilePowerLatch && profile != config.AutoProfileSurplusLatch {
		return fmt.Errorf("%w: only applies to a latch profile (active profile is %q)", ErrInvalidStartThreshold, profile)
	}
	if err := c.rec.SetSetting(context.Background(), settingKey(startThresholdKeyPrefix, profile), strconv.FormatFloat(w, 'f', -1, 64)); err != nil {
		return fmt.Errorf("persist start threshold: %w", err)
	}
	c.mu.Lock()
	if c.ctrl.StartThresholdW == nil {
		c.ctrl.StartThresholdW = make(map[string]float64)
	}
	c.ctrl.StartThresholdW[profile] = w
	c.mu.Unlock()

	if err := c.rec.InsertEvent(context.Background(), store.Event{
		TS: c.clock.Now(), Type: "command", Action: "start_threshold:" + profile + ":" + strconv.FormatFloat(w, 'f', -1, 64),
	}); err != nil {
		c.log.Warn("controller: InsertEvent (start_threshold) failed", "err", err)
	}
	c.log.Info("controller: start threshold set", "profile", profile, "startThresholdW", w)
	return nil
}

// loadStartThreshold restores each latch profile's persisted start-threshold
// override at startup, keeping the config default (via
// effectiveStartThreshold) for a profile whose value is missing or invalid.
func (c *Controller) loadStartThreshold(ctx context.Context) {
	for _, profile := range latchProfiles {
		raw, ok, err := c.rec.GetSetting(ctx, settingKey(startThresholdKeyPrefix, profile))
		if err != nil {
			c.log.Warn("controller: load start threshold failed (using default)", "profile", profile, "err", err)
			continue
		}
		if !ok {
			continue
		}
		w, err := strconv.ParseFloat(raw, 64)
		if err != nil || w <= 0 || math.Mod(w, 100) != 0 || w <= c.cfg.StopThresholdW {
			c.log.Warn("controller: stored start threshold invalid (using default)", "profile", profile, "value", raw)
			continue
		}
		c.mu.Lock()
		if c.ctrl.StartThresholdW == nil {
			c.ctrl.StartThresholdW = make(map[string]float64)
		}
		c.ctrl.StartThresholdW[profile] = w
		c.mu.Unlock()
		c.log.Info("controller: start threshold restored", "profile", profile, "startThresholdW", w)
	}
}

// ErrInvalidTargetSoC is returned by SetTargetSoC when the requested value
// fails validation.
var ErrInvalidTargetSoC = errors.New("invalid target SoC")

// SetTargetSoC sets the persistent target-SoC override (10 % steps, 50–100)
// used as the stop target by the active auto-charge profile. It only applies
// to a latch profile (config.AutoProfilePowerLatch, AutoProfileSurplusLatch);
// the default surplus profile always uses cfg.SoCCap as its cap. The value is
// stored under the active profile's own key, so switching profiles never
// leaks one profile's target into another.
func (c *Controller) SetTargetSoC(pct int) error {
	if pct < 50 || pct > 100 || pct%10 != 0 {
		return fmt.Errorf("%w %d (want a multiple of 10 within 50..100)", ErrInvalidTargetSoC, pct)
	}
	c.mu.Lock()
	profile := c.ctrl.AutoProfile
	c.mu.Unlock()
	if profile == "" {
		profile = c.cfg.AutoProfile
	}
	if profile != config.AutoProfilePowerLatch && profile != config.AutoProfileSurplusLatch {
		return fmt.Errorf("%w: only applies to a latch profile (active profile is %q)", ErrInvalidTargetSoC, profile)
	}
	if err := c.rec.SetSetting(context.Background(), settingKey(targetSoCKeyPrefix, profile), strconv.Itoa(pct)); err != nil {
		return fmt.Errorf("persist target SoC: %w", err)
	}
	c.mu.Lock()
	if c.ctrl.TargetSoC == nil {
		c.ctrl.TargetSoC = make(map[string]int)
	}
	c.ctrl.TargetSoC[profile] = pct
	c.mu.Unlock()

	if err := c.rec.InsertEvent(context.Background(), store.Event{
		TS: c.clock.Now(), Type: "command", Action: "target_soc:" + profile + ":" + strconv.Itoa(pct),
	}); err != nil {
		c.log.Warn("controller: InsertEvent (target_soc) failed", "err", err)
	}
	c.log.Info("controller: target SoC set", "profile", profile, "targetSoC", pct)
	return nil
}

// loadTargetSoC restores each latch profile's persisted target-SoC override
// at startup, keeping the config default (via effectiveTargetSoC) for a
// profile whose value is missing or invalid.
func (c *Controller) loadTargetSoC(ctx context.Context) {
	for _, profile := range latchProfiles {
		raw, ok, err := c.rec.GetSetting(ctx, settingKey(targetSoCKeyPrefix, profile))
		if err != nil {
			c.log.Warn("controller: load target SoC failed (using default)", "profile", profile, "err", err)
			continue
		}
		if !ok {
			continue
		}
		pct, err := strconv.Atoi(raw)
		if err != nil || pct < 50 || pct > 100 || pct%10 != 0 {
			c.log.Warn("controller: stored target SoC invalid (using default)", "profile", profile, "value", raw)
			continue
		}
		c.mu.Lock()
		if c.ctrl.TargetSoC == nil {
			c.ctrl.TargetSoC = make(map[string]int)
		}
		c.ctrl.TargetSoC[profile] = pct
		c.mu.Unlock()
		c.log.Info("controller: target SoC restored", "profile", profile, "targetSoC", pct)
	}
}

// ErrInvalidMaxCurrent is returned by SetMaxCurrent when the requested limit
// is outside [cfg.MinCurrentA, cfg.MaxCurrentCeilingA] (and not 0 = clear).
var ErrInvalidMaxCurrent = errors.New("invalid max charge current")

// SetMaxCurrent sets the persistent max charge-current limit (amps) wha
// publishes to evcc's loadpoint maxCurrent, e.g. to keep a "now" charge below
// what the circuit can carry. It must lie within [cfg.MinCurrentA,
// cfg.MaxCurrentCeilingA]; 0 clears the limit (see ClearMaxCurrent). A limit
// equal to the ceiling means "no wha restriction" for display purposes. The DB
// write happens before the in-memory update so a failed persist never leaves
// Status() reporting a limit that a restart would not restore. The loop
// publishes the change on its next tick (never under c.mu).
func (c *Controller) SetMaxCurrent(amps int) error {
	if amps == 0 {
		return c.ClearMaxCurrent()
	}
	if amps < c.cfg.MinCurrentA || amps > c.cfg.MaxCurrentCeilingA {
		return fmt.Errorf("%w %d A (want %d..%d A, or 0 to clear)",
			ErrInvalidMaxCurrent, amps, c.cfg.MinCurrentA, c.cfg.MaxCurrentCeilingA)
	}
	if err := c.rec.SetSetting(context.Background(), maxCurrentSetting, strconv.Itoa(amps)); err != nil {
		return fmt.Errorf("persist max current: %w", err)
	}
	c.mu.Lock()
	c.ctrl.MaxCurrentA = amps
	c.mu.Unlock()

	if err := c.rec.InsertEvent(context.Background(), store.Event{
		TS: c.clock.Now(), Type: "command", Action: "max_current:" + strconv.Itoa(amps),
	}); err != nil {
		c.log.Warn("controller: InsertEvent (max_current) failed", "err", err)
	}
	c.log.Info("controller: max current set", "amps", amps)
	return nil
}

// ClearMaxCurrent lifts the operator's max-current limit. evcc persists the
// last maxCurrent it received, so simply going quiet would leave the old limit
// in force forever; instead the limit becomes cfg.MaxCurrentCeilingA and is
// persisted, so the normal publish triggers (first tick after a restart, change,
// reconnect, evcc online edge, republish cadence) keep re-asserting the ceiling
// even if a single publish is lost. If wha has never owned a limit, clearing is
// a no-op: there is nothing at evcc to undo.
func (c *Controller) ClearMaxCurrent() error {
	c.mu.Lock()
	owned := c.ctrl.MaxCurrentA != 0
	c.mu.Unlock()
	if !owned {
		return nil
	}
	ceiling := c.cfg.MaxCurrentCeilingA
	if err := c.rec.SetSetting(context.Background(), maxCurrentSetting, strconv.Itoa(ceiling)); err != nil {
		return fmt.Errorf("persist max current: %w", err)
	}
	c.mu.Lock()
	c.ctrl.MaxCurrentA = ceiling
	c.mu.Unlock()

	if err := c.rec.InsertEvent(context.Background(), store.Event{
		TS: c.clock.Now(), Type: "command", Action: "max_current:clear",
	}); err != nil {
		c.log.Warn("controller: InsertEvent (max_current) failed", "err", err)
	}
	c.log.Info("controller: max current cleared", "resetToA", c.cfg.MaxCurrentCeilingA)
	return nil
}

// loadMaxCurrent restores the persisted max-current limit at startup. A
// missing, unparseable, or non-positive value leaves the limit unset. An
// out-of-range value (e.g. stored before control.maxCurrentCeilingA was lowered)
// is NOT dropped: evcc still holds it, so it is clamped into [minCurrentA,
// ceiling], persisted, and then published by the first tick like any limit.
func (c *Controller) loadMaxCurrent(ctx context.Context) {
	raw, ok, err := c.rec.GetSetting(ctx, maxCurrentSetting)
	if err != nil {
		c.log.Warn("controller: load max current failed (no limit)", "err", err)
		return
	}
	if !ok {
		return
	}
	amps, err := strconv.Atoi(raw)
	if err != nil || amps <= 0 {
		return
	}
	if clamped := clampMaxCurrent(amps, c.cfg); clamped != amps {
		c.log.Warn("controller: stored max current out of range, clamping", "stored", amps, "clamped", clamped,
			"minCurrentA", c.cfg.MinCurrentA, "maxCurrentCeilingA", c.cfg.MaxCurrentCeilingA)
		if err := c.rec.SetSetting(ctx, maxCurrentSetting, strconv.Itoa(clamped)); err != nil {
			c.log.Warn("controller: persist clamped max current failed", "err", err)
		}
		amps = clamped
	}
	c.mu.Lock()
	c.ctrl.MaxCurrentA = amps
	c.mu.Unlock()
	c.log.Info("controller: max current restored", "amps", amps)
}

// StatusView is a read-only snapshot of the controller for the web layer.
type StatusView struct {
	State         State
	DesiredMode   string
	Reason        string
	ReasonCode    ReasonCode
	Override      Override
	OverrideUntil time.Time
	CapBypass     bool
	CapBypassSoC  int
	ChargePower   string
	AutoProfile   string
	// StartThresholdW is the effective start threshold (after the
	// min-charge-power floor); ConfiguredStartThresholdW is the operator's value
	// before it, and MinChargePowerW the floor's base (without StartMarginW).
	StartThresholdW           float64
	ConfiguredStartThresholdW float64
	MinChargePowerW           float64
	TargetSoC                 int
	SoCCap                    int
	SoCMax                    int
	// MaxCurrentA is the operator's max charge-current limit (0 = wha never set
	// one; MaxCurrentCeilingA = cleared, i.e. no wha restriction);
	// MinCurrentA/MaxCurrentCeilingA are the bounds SetMaxCurrent accepts.
	MaxCurrentA        int
	MinCurrentA        int
	MaxCurrentCeilingA int
	// Stalled is true while the loadpoint is flagged stalled (see StallState);
	// StalledSince is when that stall began, StallRecoveryActive whether the
	// one-time recovery off pulse is in force.
	Stalled             bool
	StalledSince        time.Time
	StallRecoveryActive bool
	// VehicleSoCChangedAt is when evcc's vehicle SoC value last changed;
	// VehicleSoCEstimated is true while charging with that value older than
	// SoCEstimatedAfter (display only, never a stale gate).
	VehicleSoCChangedAt time.Time
	VehicleSoCEstimated bool
	Surplus             float64
	Inputs              Inputs
	Snapshot            evcc.Snapshot
	UpdatedAt           time.Time
}

// Status returns the latest decision state.
func (c *Controller) Status() StatusView {
	c.mu.Lock()
	defer c.mu.Unlock()
	// effIn is the last tick's Inputs (for the evcc-derived min-charge-power
	// floor) with the *current* runtime overrides layered on, so the
	// effective-value helpers (shared with Decide) resolve the same precedence
	// here even right after a Set* call, before the next tick.
	thresholdW, targetSoC := c.ctrl.latchOverrides(c.cfg)
	effIn := c.lastInputs
	effIn.AutoProfile = c.ctrl.AutoProfile
	effIn.StartThresholdW = thresholdW
	effIn.TargetSoC = targetSoC
	return StatusView{
		State:                     c.state,
		DesiredMode:               c.lastDecision.DesiredMode,
		Reason:                    c.lastDecision.Reason,
		ReasonCode:                c.lastDecision.ReasonCode,
		Override:                  c.ctrl.Override,
		OverrideUntil:             c.ctrl.OverrideUntil,
		CapBypass:                 c.ctrl.CapBypass,
		CapBypassSoC:              c.ctrl.CapBypassSoC,
		ChargePower:               c.ctrl.ChargePower,
		AutoProfile:               effectiveProfile(effIn, c.cfg),
		StartThresholdW:           effectiveStartThreshold(effIn, c.cfg),
		ConfiguredStartThresholdW: configuredStartThreshold(effIn, c.cfg),
		MinChargePowerW:           MinChargePowerW(effIn, c.cfg),
		TargetSoC:                 effectiveTargetSoC(effIn, c.cfg),
		SoCCap:                    c.cfg.SoCCap,
		SoCMax:                    c.cfg.SoCMax,
		MaxCurrentA:               c.ctrl.MaxCurrentA,
		MinCurrentA:               c.cfg.MinCurrentA,
		MaxCurrentCeilingA:        c.cfg.MaxCurrentCeilingA,
		Stalled:                   c.stall.Stalled,
		StalledSince:              c.stall.StalledSince,
		StallRecoveryActive:       c.stall.RecoveryActive(c.lastTickAt),
		VehicleSoCChangedAt:       c.lastSnapshot.VehicleSoC.ChangedAt,
		VehicleSoCEstimated:       VehicleSoCEstimated(c.lastTickAt, c.lastInputs.Charging, c.lastSnapshot.VehicleSoC),
		Surplus:                   c.lastSurplus,
		Inputs:                    c.lastInputs,
		Snapshot:                  c.lastSnapshot,
		UpdatedAt:                 c.lastTickAt,
	}
}

// socPtr returns a pointer to v only when the value is actually known, so a
// genuine 0% vehicle SoC is persisted as 0 rather than collapsed to NULL.
func socPtr(v int, known bool) *int {
	if !known {
		return nil
	}
	return &v
}
