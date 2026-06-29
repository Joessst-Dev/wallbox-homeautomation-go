package web

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/controller"
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
	status := newStatusVM(s.now(), s.ctrl.Status())

	sessions, err := s.st.RecentSessions(c.Context(), defaultSessionsLimit)
	if err != nil {
		return fmt.Errorf("dashboard: recent sessions: %w", err)
	}

	vm := dashboardVM{
		Title:    "wha — PV-surplus charging",
		Status:   status,
		Sessions: newSessionVMs(sessions),
		Update:   newUpdateVM(s.now(), s.upd.Info(c.Context())),
	}
	return c.Render("dashboard", vm, "layout")
}

// handleStatusPartial renders just the status fragment (htmx poll target).
func (s *Server) handleStatusPartial(c *fiber.Ctx) error {
	return s.renderStatusPartial(c)
}

// renderStatusPartial is shared by the poll endpoint and the override handler so
// an htmx override action returns the freshly-computed status.
func (s *Server) renderStatusPartial(c *fiber.Ctx) error {
	vm := newStatusVM(s.now(), s.ctrl.Status())
	return c.Render("partials/status", vm)
}

// handleSessionsPartial renders the recent-sessions table fragment.
func (s *Server) handleSessionsPartial(c *fiber.Ctx) error {
	sessions, err := s.st.RecentSessions(c.Context(), defaultSessionsLimit)
	if err != nil {
		return fmt.Errorf("sessions partial: %w", err)
	}
	return c.Render("partials/sessions", newSessionVMs(sessions))
}

// handleAPIStatus returns the flat status view model as JSON.
func (s *Server) handleAPIStatus(c *fiber.Ctx) error {
	return c.JSON(newStatusVM(s.now(), s.ctrl.Status()))
}

// overrideRequest is the accepted body for POST /api/override (JSON or form).
type overrideRequest struct {
	Mode  string  `json:"mode" form:"mode"`
	Hours float64 `json:"hours" form:"hours"`
}

// handleAPIOverride sets the manual override. It accepts both form-encoded and
// JSON bodies. For htmx requests it returns the refreshed status partial so the
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

	s.ctrl.SetOverride(mode, until)

	if c.Get("HX-Request") != "" {
		return s.renderStatusPartial(c)
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
	vm := newUpdateVM(s.now(), s.upd.Info(c.Context()))
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
		return c.Render("partials/update", newUpdateVM(s.now(), info))
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
