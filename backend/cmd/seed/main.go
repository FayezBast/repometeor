// Command seed writes the deterministic demo dataset into PostgreSQL.
//
//	seed                     seed against the current time
//	seed --at 2026-08-01T12:00:00Z
//	                         seed against a fixed reference time, which makes
//	                         the resulting rows byte-for-byte reproducible
//
// Everything written by this command is fictional and carries the source
// 'demo_seed'. It is safe to run repeatedly: repositories are keyed on their
// fictional GitHub ID and snapshots on (repository, bucket), so a second run
// updates rather than duplicates.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/repometeor/repometeor/backend/internal/clock"
	"github.com/repometeor/repometeor/backend/internal/config"
	"github.com/repometeor/repometeor/backend/internal/database"
	"github.com/repometeor/repometeor/backend/internal/demo"
	"github.com/repometeor/repometeor/backend/internal/repository"
	"github.com/repometeor/repometeor/backend/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "seed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		at    = flag.String("at", "", "reference time in RFC 3339 (default: now)")
		force = flag.Bool("force", false, "seed even when DEMO_MODE is false")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Seeding a live instance would mix fictional repositories into real
	// rankings, so it requires an explicit override.
	if !cfg.App.DemoMode && !*force {
		return fmt.Errorf("refusing to seed demo data while DEMO_MODE=false; pass --force to override")
	}

	log := telemetry.NewLogger(os.Stdout, cfg.App.LogLevel, "repometeor-seed")

	referenceTime := time.Time{}
	if *at != "" {
		parsed, err := time.Parse(time.RFC3339, *at)
		if err != nil {
			return fmt.Errorf("--at must be an RFC 3339 timestamp: %w", err)
		}
		referenceTime = parsed.UTC()
	}

	db, err := database.Connect(ctx, cfg.Database, log)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()

	seeder := demo.NewSeeder(
		repository.NewRepositories(db),
		repository.NewSnapshots(db),
		clock.New(),
		log,
	)

	result, err := seeder.Seed(ctx, referenceTime, cfg.Tracking.SnapshotBucketInterval)
	if err != nil {
		return err
	}

	summary := demo.Summarise(demo.Build(result.ReferenceTime))
	fmt.Printf("\nSeeded demo data (reference time %s)\n\n%s\n",
		result.ReferenceTime.Format(time.RFC3339), summary)
	fmt.Printf("Wrote %d repositories and %d snapshots in %s.\n",
		result.Repositories, result.Snapshots, result.Duration.Round(time.Millisecond))
	fmt.Println("\nThis data is fictional. The web UI shows a permanent \"Demo data\" indicator.")

	return nil
}
