package web

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/controller"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/web/i18n"
)

const (
	defaultSessionsLimit = 20
	defaultEventsLimit   = 50
	defaultHistoryWindow = 24 * time.Hour
	// maxHistoryWindow caps the requested history span so a single request cannot
	// ask the store for an unbounded range.
	maxHistoryWindow = 7 * 24 * time.Hour
)

// handleHealthz is a liveness probe: it always reports the process is up.
func (s *Server) handleHealthz(c *fiber.Ctx) error {
	return c.SendString("ok")
}

// handleReadyz is a readiness probe: ready only once the MQTT broker connection
// is established, since without it the controller has no live data.
func (s *Server) handleReadyz(c *fiber.Ctx) error {
	if s.ctrl.Status().Snapshot.BrokerConnected {
		return c.SendString("ok")
	}
	return c.Status(fiber.StatusServiceUnavailable).SendString("not ready")
}

// handleDashboard renders the full dashboard page within the layout.
func (s *Server) handleDashboard(c *fiber.Ctx) error {
	lang := langFromCtx(c)
	status := newStatusVM(s.now(), s.ctrl.Status())

	sessions, err := s.st.RecentSessions(c.Context(), defaultSessionsLimit)
	if err != nil {
		return fmt.Errorf("dashboard: recent sessions: %w", err)
	}

	vm := dashboardVM{
		Title:    s.i18n.T(lang, "dashboard.title"),
		Lang:     lang,
		Status:   statusView{StatusVM: status, Lang: lang},
		Sessions: sessionsView{Lang: lang, Sessions: newSessionVMs(sessions)},
	}
	return c.Render("dashboard", vm, "layout")
}

// handleSettings renders the full settings page within the layout: the
// auto-charge configuration and the software-update card.
func (s *Server) handleSettings(c *fiber.Ctx) error {
	lang := langFromCtx(c)
	vm := settingsVM{
		Title:  s.i18n.T(lang, "settings.title"),
		Lang:   lang,
		Status: statusView{StatusVM: newStatusVM(s.now(), s.ctrl.Status()), Lang: lang},
		Update: updateView{UpdateVM: newUpdateVM(s.now(), s.upd.Info(c.Context())), Lang: lang},
	}
	return c.Render("settings", vm, "layout")
}

// handleSettingsPartial renders just the auto-charge configuration fragment
// (slow poll / returned after profile, start-threshold, or target-SoC actions).
func (s *Server) handleSettingsPartial(c *fiber.Ctx) error {
	return s.renderSettingsPartial(c)
}

// renderSettingsPartial is shared by the poll endpoint and the auto-profile /
// start-threshold / target-SoC handlers so an htmx action returns the
// freshly-computed configuration.
func (s *Server) renderSettingsPartial(c *fiber.Ctx) error {
	vm := statusView{StatusVM: newStatusVM(s.now(), s.ctrl.Status()), Lang: langFromCtx(c)}
	return c.Render("partials/settings", vm)
}

// handleStatusPartial renders just the live-status fragment (htmx poll target).
func (s *Server) handleStatusPartial(c *fiber.Ctx) error {
	return s.renderStatusPartial(c)
}

// renderStatusPartial is shared by the poll endpoint and probes.
func (s *Server) renderStatusPartial(c *fiber.Ctx) error {
	vm := statusView{StatusVM: newStatusVM(s.now(), s.ctrl.Status()), Lang: langFromCtx(c)}
	return c.Render("partials/status", vm)
}

// handleControlsPartial renders just the operator-controls fragment (slow poll /
// returned after override or charge-power actions).
func (s *Server) handleControlsPartial(c *fiber.Ctx) error {
	return s.renderControlsPartial(c)
}

// renderControlsPartial is shared by the poll endpoint and the override/charge-power
// handlers so an htmx action returns the freshly-computed controls.
func (s *Server) renderControlsPartial(c *fiber.Ctx) error {
	vm := statusView{StatusVM: newStatusVM(s.now(), s.ctrl.Status()), Lang: langFromCtx(c)}
	return c.Render("partials/controls", vm)
}

// handleSetLang validates the requested language, pins it in a long-lived
// cookie, and redirects back so the whole page reloads localized. It only ever
// redirects to a same-origin path (never to an absolute or cross-origin URL, so
// it cannot be abused as an open redirect); anything else falls back to "/".
func (s *Server) handleSetLang(c *fiber.Ctx) error {
	code := c.Params("code")
	if !isSupportedLang(code) {
		return fiber.NewError(fiber.StatusBadRequest, "unsupported language")
	}

	c.Cookie(&fiber.Cookie{
		Name:     langCookieName,
		Value:    code,
		MaxAge:   int((365 * 24 * time.Hour).Seconds()),
		SameSite: fiber.CookieSameSiteLaxMode,
	})

	return c.Redirect(sameOriginPath(c.Get(fiber.HeaderReferer), string(c.Request().Host())))
}

// sameOriginPath returns the path+query to redirect to after a language switch.
// It accepts referer only when it is same-origin (its host matches the request
// host), and then returns just the local path+query so the redirect can never
// point at another origin. Anything else — empty, unparseable, or cross-origin
// — yields "/".
func sameOriginPath(referer, host string) string {
	if referer == "" {
		return "/"
	}
	u, err := url.Parse(referer)
	if err != nil || u.Host != host {
		return "/"
	}
	if p := u.RequestURI(); p != "" {
		return p
	}
	return "/"
}

// isSupportedLang reports whether code names one of i18n.Supported.
func isSupportedLang(code string) bool {
	for _, sup := range i18n.Supported {
		if code == sup {
			return true
		}
	}
	return false
}

// handleSessionsPartial renders the recent-sessions table fragment.
func (s *Server) handleSessionsPartial(c *fiber.Ctx) error {
	sessions, err := s.st.RecentSessions(c.Context(), defaultSessionsLimit)
	if err != nil {
		return fmt.Errorf("sessions partial: %w", err)
	}
	vm := sessionsView{Lang: langFromCtx(c), Sessions: newSessionVMs(sessions)}
	return c.Render("partials/sessions", vm)
}

// handleAPIStatus returns the flat status view model as JSON.
func (s *Server) handleAPIStatus(c *fiber.Ctx) error {
	return c.JSON(newStatusVM(s.now(), s.ctrl.Status()))
}

// overrideRequest is the accepted body for POST /api/override (JSON or form).
type overrideRequest struct {
	Mode         string  `json:"mode" form:"mode"`
	Hours        float64 `json:"hours" form:"hours"`
	CapBypass    bool    `json:"capBypass" form:"capBypass"`
	CapBypassSoC int     `json:"capBypassSoC" form:"capBypassSoC"`
}

// handleAPIOverride sets the manual override. It accepts both form-encoded and
// JSON bodies. For htmx requests it returns the refreshed controls partial so the
// UI updates in place; otherwise it returns {"ok":true}.
func (s *Server) handleAPIOverride(c *fiber.Ctx) error {
	var req overrideRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid override body")
	}

	mode, err := parseOverride(req.Mode)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	var until time.Time
	if req.Hours > 0 {
		until = s.now().Add(time.Duration(req.Hours * float64(time.Hour)))
	}

	// CapBypass (charge past the SoC cap) only applies to a force-on override; the
	// controller ignores it for other modes, but constrain it here too for clarity.
	capBypass := req.CapBypass && mode == controller.OverrideForceOn
	s.ctrl.SetOverride(mode, until, capBypass, req.CapBypassSoC)

	if c.Get("HX-Request") != "" {
		return s.renderControlsPartial(c)
	}
	return c.JSON(fiber.Map{"ok": true})
}

// chargePowerRequest is the accepted body for POST /api/charge-power.
type chargePowerRequest struct {
	Power string `json:"power" form:"power"`
}

// handleChargePower sets the persistent charge-power mode (pv|now) used whenever
// charging is enabled. For htmx requests it returns the refreshed status partial;
// otherwise {"ok":true}.
func (s *Server) handleChargePower(c *fiber.Ctx) error {
	var req chargePowerRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid charge-power body")
	}
	// Fiber's BodyParser (non-Immutable) returns strings that alias the pooled
	// fasthttp request buffer; SetChargePower retains this string in long-lived
	// state, so it must be copied out before the buffer is recycled for a later
	// request.
	if err := s.ctrl.SetChargePower(strings.Clone(req.Power)); err != nil {
		if errors.Is(err, controller.ErrInvalidChargePower) {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		// A persistence failure is a server error, not a client mistake.
		s.log.Warn("web: set charge power failed", "err", err)
		return fiber.NewError(fiber.StatusInternalServerError, "failed to set charge power")
	}
	if c.Get("HX-Request") != "" {
		return s.renderControlsPartial(c)
	}
	return c.JSON(fiber.Map{"ok": true})
}

// autoProfileRequest is the accepted body for POST /api/auto-profile.
type autoProfileRequest struct {
	Profile string `json:"profile" form:"profile"`
}

// handleAutoProfile sets the persistent auto-charge profile (surplus|power_latch|
// surplus_latch). For htmx requests it returns the refreshed settings partial;
// otherwise {"ok":true}.
func (s *Server) handleAutoProfile(c *fiber.Ctx) error {
	var req autoProfileRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid auto-profile body")
	}
	// Clone before retaining: Fiber's BodyParser (non-Immutable) returns strings
	// aliasing the pooled fasthttp request buffer, and SetAutoProfile stores this
	// into long-lived state (same hazard as handleChargePower). StartThresholdW/
	// TargetSoC are value types, so only the profile string needs copying.
	if err := s.ctrl.SetAutoProfile(strings.Clone(req.Profile)); err != nil {
		if errors.Is(err, controller.ErrInvalidAutoProfile) {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		s.log.Warn("web: set auto profile failed", "err", err)
		return fiber.NewError(fiber.StatusInternalServerError, "failed to set auto profile")
	}
	if c.Get("HX-Request") != "" {
		return s.renderSettingsPartial(c)
	}
	return c.JSON(fiber.Map{"ok": true})
}

// startThresholdRequest is the accepted body for POST /api/start-threshold.
type startThresholdRequest struct {
	StartThresholdW float64 `json:"startThresholdW" form:"startThresholdW"`
}

// handleStartThreshold sets the persistent start-threshold override (watts,
// 100 W steps) used by the active auto-charge profile's start trigger. For
// htmx requests it returns the refreshed settings partial; otherwise
// {"ok":true}.
func (s *Server) handleStartThreshold(c *fiber.Ctx) error {
	var req startThresholdRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid start-threshold body")
	}
	if err := s.ctrl.SetStartThreshold(req.StartThresholdW); err != nil {
		if errors.Is(err, controller.ErrInvalidStartThreshold) {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		s.log.Warn("web: set start threshold failed", "err", err)
		return fiber.NewError(fiber.StatusInternalServerError, "failed to set start threshold")
	}
	if c.Get("HX-Request") != "" {
		return s.renderSettingsPartial(c)
	}
	return c.JSON(fiber.Map{"ok": true})
}

// targetSoCRequest is the accepted body for POST /api/target-soc.
type targetSoCRequest struct {
	TargetSoC int `json:"targetSoC" form:"targetSoC"`
}

// handleTargetSoC sets the persistent target-SoC override (10 % steps,
// 50-100) used as the stop target by the active auto-charge profile. For htmx
// requests it returns the refreshed settings partial; otherwise {"ok":true}.
func (s *Server) handleTargetSoC(c *fiber.Ctx) error {
	var req targetSoCRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid target-soc body")
	}
	if err := s.ctrl.SetTargetSoC(req.TargetSoC); err != nil {
		if errors.Is(err, controller.ErrInvalidTargetSoC) {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		s.log.Warn("web: set target soc failed", "err", err)
		return fiber.NewError(fiber.StatusInternalServerError, "failed to set target soc")
	}
	if c.Get("HX-Request") != "" {
		return s.renderSettingsPartial(c)
	}
	return c.JSON(fiber.Map{"ok": true})
}

// handleUpdatePartial renders just the software-update fragment (htmx poll/
// refresh target).
func (s *Server) handleUpdatePartial(c *fiber.Ctx) error {
	return s.renderUpdatePartial(c)
}

// renderUpdatePartial is shared by the poll endpoint and the check/apply
// handlers so an htmx action returns the freshly-computed update state.
func (s *Server) renderUpdatePartial(c *fiber.Ctx) error {
	vm := updateView{UpdateVM: newUpdateVM(s.now(), s.upd.Info(c.Context())), Lang: langFromCtx(c)}
	return c.Render("partials/update", vm)
}

// handleUpdateCheck forces a GHCR re-check. For htmx requests it returns the
// refreshed update partial; otherwise the update info as JSON.
func (s *Server) handleUpdateCheck(c *fiber.Ctx) error {
	info, err := s.upd.Check(c.Context())
	if err != nil {
		// A failed check is not fatal: surface the last-known state to the UI
		// rather than a 500, but log the cause.
		s.log.Warn("web: update check failed", "err", err)
	}
	if c.Get("HX-Request") != "" {
		vm := updateView{UpdateVM: newUpdateVM(s.now(), info), Lang: langFromCtx(c)}
		return c.Render("partials/update", vm)
	}
	return c.JSON(newUpdateVM(s.now(), info))
}

// updateRequest is the accepted body for POST /api/update/apply (JSON or form).
type updateRequest struct {
	Version string `json:"version" form:"version"`
}

// handleUpdateApply requests an upgrade to the given version. The version must
// match the latest version GHCR currently offers (so the UI can only ever apply
// a real, checked release); the updater additionally enforces a strict semver
// format before writing the sidecar request.
func (s *Server) handleUpdateApply(c *fiber.Ctx) error {
	var req updateRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid update body")
	}

	info := s.upd.Info(c.Context())
	if !info.Enabled {
		return fiber.NewError(fiber.StatusBadRequest, "updates are disabled")
	}
	if req.Version == "" || req.Version != info.Latest {
		return fiber.NewError(fiber.StatusBadRequest, "version must equal the latest available release")
	}

	if err := s.upd.Trigger(c.Context(), req.Version); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	if c.Get("HX-Request") != "" {
		return s.renderUpdatePartial(c)
	}
	return c.JSON(fiber.Map{"ok": true})
}

// parseOverride maps an external mode string onto a controller.Override.
func parseOverride(mode string) (controller.Override, error) {
	switch mode {
	case string(controller.OverrideAuto):
		return controller.OverrideAuto, nil
	case string(controller.OverrideForceOn):
		return controller.OverrideForceOn, nil
	case string(controller.OverrideForceOff):
		return controller.OverrideForceOff, nil
	default:
		return "", fmt.Errorf("invalid override mode %q (want auto|on|off)", mode)
	}
}

// handleAPISessions returns recent sessions as JSON.
func (s *Server) handleAPISessions(c *fiber.Ctx) error {
	limit := queryLimit(c, "limit", defaultSessionsLimit)
	sessions, err := s.st.RecentSessions(c.Context(), limit)
	if err != nil {
		return fmt.Errorf("api sessions: %w", err)
	}
	return c.JSON(newSessionVMs(sessions))
}

// handleAPIEvents returns recent audit-log events as JSON.
func (s *Server) handleAPIEvents(c *fiber.Ctx) error {
	limit := queryLimit(c, "limit", defaultEventsLimit)
	events, err := s.st.RecentEvents(c.Context(), limit)
	if err != nil {
		return fmt.Errorf("api events: %w", err)
	}
	return c.JSON(events)
}

// handleAPIHistory returns time-series samples in [from, to] as JSON. from/to
// are RFC3339; defaults to the last 24h.
func (s *Server) handleAPIHistory(c *fiber.Ctx) error {
	now := s.now()
	to := now
	from := now.Add(-defaultHistoryWindow)

	if v := c.Query("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid from (want RFC3339)")
		}
		from = t
	}
	if v := c.Query("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid to (want RFC3339)")
		}
		to = t
	}
	if to.Before(from) {
		return fiber.NewError(fiber.StatusBadRequest, "to must not be before from")
	}
	// Clamp an over-long window to the most recent maxHistoryWindow so a single
	// request can never span an unbounded range.
	if to.Sub(from) > maxHistoryWindow {
		from = to.Add(-maxHistoryWindow)
	}

	samples, err := s.st.Samples(c.Context(), from, to)
	if err != nil {
		return fmt.Errorf("api history: %w", err)
	}
	return c.JSON(samples)
}

// queryLimit reads a positive integer query parameter, falling back to def when
// absent, unparseable, or non-positive.
func queryLimit(c *fiber.Ctx, key string, def int) int {
	v := c.QueryInt(key, def)
	if v <= 0 {
		return def
	}
	return v
}
