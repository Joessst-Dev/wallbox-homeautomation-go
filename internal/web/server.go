// Package web implements the HTTP layer of wha: a Fiber v2 application that
// serves an htmx-driven dashboard plus a small JSON API over the controller and
// the persistence store. Templates and static assets are embedded into the
// binary so the server has no runtime file dependencies.
package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/template/html/v2"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/controller"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/updater"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/web/i18n"
)

// Controller is the subset of *controller.Controller the web layer needs.
type Controller interface {
	Status() controller.StatusView
	SetOverride(mode controller.Override, until time.Time, capBypass bool, capBypassSoC int)
	SetChargePower(mode string) error
	SetAutoProfile(profile string) error
	SetStartThreshold(w float64) error
	SetTargetSoC(pct int) error
}

// Store is the subset of *store.Store the web layer needs.
type Store interface {
	RecentSessions(ctx context.Context, limit int) ([]store.Session, error)
	RecentEvents(ctx context.Context, limit int) ([]store.Event, error)
	Samples(ctx context.Context, from, to time.Time) ([]store.Sample, error)
}

// Updater is the subset of *updater.Updater the web layer needs: read the
// current update state, force a GHCR re-check, and request an upgrade.
type Updater interface {
	Info(ctx context.Context) updater.Info
	Check(ctx context.Context) (updater.Info, error)
	Trigger(ctx context.Context, version string) error
}

// Server owns the Fiber app and the dependencies its handlers close over.
type Server struct {
	cfg  config.Web
	app  *fiber.App
	ctrl Controller
	st   Store
	upd  Updater
	i18n *i18n.Bundle
	log  *slog.Logger
	now  func() time.Time
}

// New builds a Server with the embedded template engine, middleware, and routes
// wired up. It does not start listening; call Start for that.
func New(cfg config.Web, ctrl Controller, st Store, upd Updater, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}

	bundle := i18n.New()

	s := &Server{
		cfg:  cfg,
		ctrl: ctrl,
		st:   st,
		upd:  upd,
		i18n: bundle,
		log:  log,
		now:  time.Now,
	}

	s.app = fiber.New(fiber.Config{
		Views:                 newEngine(bundle),
		DisableStartupMessage: true,
		ErrorHandler:          s.errorHandler,
	})

	s.app.Use(recover.New())
	s.app.Use(s.langMiddleware)
	s.registerRoutes()

	return s
}

// newEngine constructs the HTML template engine over the embedded templates
// directory. Template names are their paths relative to "templates" without the
// .html suffix (e.g. "dashboard", "partials/status"). The translation funcs are
// registered before the engine's first Load (triggered lazily by Fiber on the
// first Render), since the html/template FuncMap can only be extended pre-parse.
func newEngine(bundle *i18n.Bundle) *html.Engine {
	sub, err := fs.Sub(embeddedFS, "templates")
	if err != nil {
		// embeddedFS is a compile-time constant; a failure here is a programmer
		// error, not a runtime condition.
		panic(fmt.Errorf("web: sub templates FS: %w", err))
	}
	engine := html.NewFileSystem(http.FS(sub), ".html")
	engine.AddFunc("t", bundle.T)
	engine.AddFunc("tOr", bundle.TOr)
	// deref lets templates pass a *int (e.g. SessionVM.StartVehicleSoC) into a
	// %d-formatted t()/tOr() call: fmt.Sprintf("%d", p) on a *int formats the
	// pointer's address, not the pointee, so the value must be dereferenced first.
	engine.AddFunc("deref", func(p *int) int {
		if p == nil {
			return 0
		}
		return *p
	})
	// safeHTML marks a translated catalog string as pre-escaped HTML so a
	// translation may embed static markup (e.g. <code>/<strong>). Catalogs are
	// embedded, build-time content, never user input, so this is safe.
	engine.AddFunc("safeHTML", func(s string) template.HTML { return template.HTML(s) })
	return engine
}

// langCookieName is the cookie that pins the operator's chosen dashboard
// language across page loads and htmx polls.
const langCookieName = "lang"

// langMiddleware resolves the request language once per request (cookie wins,
// else Accept-Language, else i18n.DefaultLang) and stores it for handlers to
// read via langFromCtx.
func (s *Server) langMiddleware(c *fiber.Ctx) error {
	c.Locals("lang", s.i18n.Resolve(c.Cookies(langCookieName), c.Get(fiber.HeaderAcceptLanguage)))
	return c.Next()
}

// langFromCtx reads the language resolved by langMiddleware.
func langFromCtx(c *fiber.Ctx) string {
	lang, _ := c.Locals("lang").(string)
	return lang
}

// registerRoutes wires every HTTP route to its handler.
func (s *Server) registerRoutes() {
	app := s.app

	// Static assets served from the embedded static directory.
	staticFS, err := fs.Sub(embeddedFS, "static")
	if err != nil {
		panic(fmt.Errorf("web: sub static FS: %w", err))
	}
	app.Use("/static", filesystem.New(filesystem.Config{
		Root: http.FS(staticFS),
	}))

	// Browsers (and some tools) request /favicon.ico directly regardless of the
	// <link rel="icon"> tag; redirect them to the embedded SVG so it resolves
	// instead of 404ing. The target is stable for the binary's lifetime, so a
	// permanent redirect lets clients cache the mapping and skip the round-trip.
	app.Get("/favicon.ico", func(c *fiber.Ctx) error {
		return c.Redirect("/static/favicon.svg", fiber.StatusMovedPermanently)
	})

	// PWA service worker: served at the root (not /static) so its scope covers
	// the whole app, per the Service-Worker-Allowed header it sets.
	app.Get("/sw.js", s.handleServiceWorker)

	// Secure-access / PWA-install setup page and the certificate downloads it
	// links to.
	app.Get("/setup", s.handleSetup)
	app.Get("/setup/root.crt", s.handleSetupRootCert)
	app.Get("/setup/wha-ca.mobileconfig", s.handleSetupMobileconfig)

	// Health probes.
	app.Get("/healthz", s.handleHealthz)
	app.Get("/readyz", s.handleReadyz)

	// HTML pages and htmx partials.
	app.Get("/", s.handleDashboard)
	app.Get("/settings", s.handleSettings)
	app.Get("/partials/status", s.handleStatusPartial)
	app.Get("/partials/controls", s.handleControlsPartial)
	app.Get("/partials/settings", s.handleSettingsPartial)
	app.Get("/partials/sessions", s.handleSessionsPartial)
	app.Get("/partials/update", s.handleUpdatePartial)
	app.Get("/lang/:code", s.handleSetLang)

	// JSON + form API.
	api := app.Group("/api")
	api.Get("/status", s.handleAPIStatus)
	api.Post("/override", s.handleAPIOverride)
	api.Post("/charge-power", s.handleChargePower)
	api.Post("/auto-profile", s.handleAutoProfile)
	api.Post("/start-threshold", s.handleStartThreshold)
	api.Post("/target-soc", s.handleTargetSoC)
	api.Get("/sessions", s.handleAPISessions)
	api.Get("/events", s.handleAPIEvents)
	api.Get("/history", s.handleAPIHistory)
	api.Post("/update/check", s.handleUpdateCheck)
	api.Post("/update/apply", s.handleUpdateApply)
}

// Start binds and serves on BindAddr:Port. It blocks until the server is shut
// down, returning nil on a clean Shutdown.
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.BindAddr, s.cfg.Port)
	if err := s.app.Listen(addr); err != nil {
		return fmt.Errorf("web: listen on %s: %w", addr, err)
	}
	return nil
}

// Shutdown gracefully stops the server, waiting for in-flight requests up to the
// context deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.app.ShutdownWithContext(ctx); err != nil {
		return fmt.Errorf("web: shutdown: %w", err)
	}
	return nil
}

// App exposes the underlying Fiber app so tests can drive it via App().Test.
func (s *Server) App() *fiber.App {
	return s.app
}

// errorHandler renders unhandled handler errors as structured JSON.
func (s *Server) errorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	msg := err.Error()
	var fe *fiber.Error
	if errors.As(err, &fe) {
		code = fe.Code
		msg = fe.Message
	}
	s.log.Warn("web: request error", "path", c.Path(), "status", code, "err", err)
	return c.Status(code).JSON(fiber.Map{"ok": false, "error": msg})
}
