// Package app is the composition root: it wires the store, evcc MQTT client,
// control loop, and web server together and runs them under one errgroup with
// graceful shutdown.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/config"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/controller"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/evcc"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/store"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/updater"
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/web"
)

// BuildInfo carries the ldflags-injected build identity from main into the
// components that surface it (the web dashboard and the updater's version check).
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// Run starts all components and blocks until the process is signaled or a
// component fails.
func Run(ctx context.Context, cfg config.Config, build BuildInfo) error {
	log := newLogger(cfg.Log.Level)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(cfg.DB.Path)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = st.Close() }()
	log.Info("store opened", "path", cfg.DB.Path)

	client := evcc.NewClient(cfg.MQTT, cfg.EVCC.LoadpointID, log)
	ctrl := controller.New(cfg.Control, client, client.Store(), st, controller.RealClock{}, log)
	upd := updater.New(cfg.Update, build.Version)
	srv := web.New(cfg.Web, ctrl, st, upd, log)

	g, ctx := errgroup.WithContext(ctx)

	// Prime the GHCR check once at startup so the dashboard can show whether an
	// update is available without the operator clicking "Check" first. Best
	// effort: failures (offline broker, no network) are logged and retried on
	// demand from the UI.
	if cfg.Update.Enabled {
		go func() {
			if _, err := upd.Check(ctx); err != nil {
				log.Info("updater: initial check failed", "err", err)
			}
		}()
	}

	// MQTT: connect (with built-in retry) without blocking shutdown.
	g.Go(func() error {
		go func() {
			if err := client.Connect(); err != nil {
				log.Warn("mqtt: initial connect failed (will keep retrying)", "err", err)
			}
		}()
		<-ctx.Done()
		client.Disconnect()
		return nil
	})

	// Control loop.
	g.Go(func() error {
		return ctrl.Run(ctx)
	})

	// Web server with graceful shutdown.
	g.Go(func() error {
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutdownCtx); err != nil {
				log.Warn("web: shutdown error", "err", err)
			}
		}()
		log.Info("web server listening", "addr", fmt.Sprintf("%s:%d", cfg.Web.BindAddr, cfg.Web.Port))
		return srv.Start()
	})

	if err := g.Wait(); err != nil {
		return err
	}
	log.Info("shutdown complete")
	return nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}
