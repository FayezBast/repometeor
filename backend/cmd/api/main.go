// Command api runs repometeor-api, the public HTTP service.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/repometeor/repometeor/backend/internal/api"
	"github.com/repometeor/repometeor/backend/internal/clock"
	"github.com/repometeor/repometeor/backend/internal/config"
	"github.com/repometeor/repometeor/backend/internal/database"
	"github.com/repometeor/repometeor/backend/internal/repository"
	"github.com/repometeor/repometeor/backend/internal/service"
	"github.com/repometeor/repometeor/backend/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "repometeor-api: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// SIGINT and SIGTERM cancel the root context, which is what starts the
	// graceful shutdown path. A second signal restores the default behaviour so
	// an operator can always force an exit.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := telemetry.NewLogger(os.Stdout, cfg.App.LogLevel, "repometeor-api")
	log.Info("starting repometeor-api", slog.Any("config", cfg.Redacted()))

	metrics := telemetry.NewMetrics(api.Version)
	appClock := clock.New()

	db, err := database.Connect(ctx, cfg.Database, log)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()

	rateLimitKey, err := rateLimitKey()
	if err != nil {
		return err
	}

	svc := service.New(
		repository.NewRepositories(db),
		repository.NewSnapshots(db),
		repository.NewScores(db),
		repository.NewJobs(db),
		repository.NewRateLimits(db, rateLimitKey),
		cfg, appClock, log,
	)

	server := api.New(api.Options{
		Config:  cfg,
		Service: svc,
		DB:      db,
		Log:     log,
		Metrics: metrics,
		Clock:   appClock,
	})

	// Start the listener, then wait for either a signal or a listener failure.
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.Start() }()

	select {
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
		return nil

	case <-ctx.Done():
		// Stop intercepting signals so a second Ctrl-C terminates immediately.
		stop()
		log.Info("shutdown signal received")

		if err := server.Shutdown(context.Background()); err != nil {
			return fmt.Errorf("graceful shutdown failed: %w", err)
		}
		log.Info("repometeor-api stopped")
		return nil
	}
}

// rateLimitKey returns the HMAC key used to hash client addresses.
//
// It is generated per process rather than configured: the key never needs to
// outlive a deployment, and generating it removes one more secret an operator
// could accidentally commit. Rotating it simply resets the current windows.
// RATE_LIMIT_KEY allows a multi-instance deployment to share one key so that a
// client's allowance is consistent across instances.
func rateLimitKey() ([]byte, error) {
	if configured := os.Getenv("RATE_LIMIT_KEY"); configured != "" {
		return []byte(configured), nil
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, errors.New("could not generate a rate-limit key")
	}
	return key, nil
}
