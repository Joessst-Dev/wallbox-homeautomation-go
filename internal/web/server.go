// Package web implements the HTTP layer of wha: a Fiber v2 application that
// serves an htmx-driven dashboard plus a small JSON API over the controller and
// the persistence store. Templates and static assets are embedded into the
// binary so the server has no runtime file dependencies.
package web

import (
	"context"
	"errors"
	"fmt"
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
)

// Controller is the subset of *controller.Controller the web layer needs.
type Controller interface {
	Status() controller.StatusView
	SetOverride(mode controller.Override, until time.Time, capBypass bool)
	SetChargePower(mode string) error
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
	log  *slog.Logger
	now  func() time.Time
}

// New builds a Server with the embedded template engine, middleware, and routes
// wired up. It does not start listening; call Start for that.
func New(cfg config.Web, ctrl Controller, st Store, upd Updater, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}

	s := &Server{
		cfg:  cfg,
		ctrl: ctrl,
		st:   st,
		upd:  upd,
		log:  log,
		now:  time.Now,
	}

	s.app = fiber.New(fiber.Config{
		Views:                 newEngine(),
		DisableStartupMessage: true,
		ErrorHandler:          s.errorHandler,
	})

	s.app.Use(recover.New())
	s.registerRoutes()

	return s
}

// newEngine constructs the HTML template engine over the embedded templates
// directory. Template names are their paths relative to "templates" without the
// .html suffix (e.g. "dashboard", "partials/status").
func newEngine() *html.Engine {
	sub, err := fs.Sub(embeddedFS, "templates")
	if err != nil {
		// embeddedFS is a compile-time constant; a failure here is a programmer
		// error, not a runtime condition.
		panic(fmt.Errorf("web: sub templates FS: %w", err))
	}
	engine := html.NewFileSystem(http.FS(sub), ".html")
	return engine
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
	// instead of 404ing.
	app.Get("/favicon.ico", func(c *fiber.Ctx) error {
		return c.Redirect("/static/favicon.svg", fiber.StatusFound)
	})

	// Health probes.
	app.Get("/healthz", s.handleHealthz)
	app.Get("/readyz", s.handleReadyz)

	// HTML pages and htmx partials.
	app.Get("/", s.handleDashboard)
	app.Get("/partials/status", s.handleStatusPartial)
	app.Get("/partials/sessions", s.handleSessionsPartial)
	app.Get("/partials/update", s.handleUpdatePartial)

	// JSON + form API.
	api := app.Group("/api")
	api.Get("/status", s.handleAPIStatus)
	api.Post("/override", s.handleAPIOverride)
	api.Post("/charge-power", s.handleChargePower)
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
