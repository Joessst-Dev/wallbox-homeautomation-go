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
	"github.com/Joessst-Dev/wallbox-homeautomation-go/internal/web"
)

// Run starts all components and blocks until the process is signaled or a
// component fails.
func Run(ctx context.Context, cfg config.Config) error {
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
	srv := web.New(cfg.Web, ctrl, st, log)

	g, ctx := errgroup.WithContext(ctx)

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
