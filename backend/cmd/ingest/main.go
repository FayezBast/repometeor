// Command ingest runs one collection or scoring pass and exits.
//
// It exists for operators and for the smoke test: the worker normally runs
// these steps on a schedule, but being able to trigger one deterministically is
// what makes a fresh `docker compose up` produce rankings immediately instead
// of after the first scheduled tick.
//
//	ingest score              recompute rankings for every window
//	ingest tiers              recalculate polling tiers
//	ingest collect owner/name collect one repository now (live mode only)
//	ingest discover           run one discovery pass (live mode only)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/repometeor/repometeor/backend/internal/clock"
	"github.com/repometeor/repometeor/backend/internal/config"
	"github.com/repometeor/repometeor/backend/internal/database"
	"github.com/repometeor/repometeor/backend/internal/github"
	"github.com/repometeor/repometeor/backend/internal/jobs"
	"github.com/repometeor/repometeor/backend/internal/reponame"
	"github.com/repometeor/repometeor/backend/internal/repository"
	"github.com/repometeor/repometeor/backend/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ingest: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: ingest <score|tiers|collect owner/name|discover>")
	}
	command := os.Args[1]

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := telemetry.NewLogger(os.Stdout, cfg.App.LogLevel, "repometeor-ingest")
	metrics := telemetry.NewMetrics("ingest")
	appClock := clock.New()

	db, err := database.Connect(ctx, cfg.Database, log)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()

	repos := repository.NewRepositories(db)
	snaps := repository.NewSnapshots(db)
	scores := repository.NewScores(db)
	queue := repository.NewJobs(db)
	cache := repository.NewCache(db)
	discoveryRuns := repository.NewDiscovery(db)

	budget := github.NewBudget(cfg.GitHub.RateBudgetPercent, appClock)

	switch command {
	case "score":
		scorer := jobs.NewScorer(repos, snaps, scores, cfg.Tracking, appClock, log, metrics)
		if err := scorer.Handle(ctx, repository.Job{Kind: repository.JobScoreRepositories}); err != nil {
			return fmt.Errorf("scoring run: %w", err)
		}
		fmt.Println("scoring run complete")
		return nil

	case "tiers":
		refresher := jobs.NewTierRefresher(repos, scores, cfg.Tracking, appClock, log)
		if err := refresher.Handle(ctx, repository.Job{Kind: repository.JobRefreshTiers}); err != nil {
			return fmt.Errorf("tier refresh: %w", err)
		}
		fmt.Println("tier refresh complete")
		return nil

	case "collect":
		if cfg.App.DemoMode {
			return errors.New("collection is unavailable in demo mode; set DEMO_MODE=false and supply GITHUB_TOKEN")
		}
		if len(os.Args) < 3 {
			return errors.New("usage: ingest collect owner/name")
		}
		name, err := reponame.Parse(os.Args[2])
		if err != nil {
			return fmt.Errorf("invalid repository: %w", err)
		}

		provider := github.NewClient(cfg.GitHub, log, metrics)
		collector := jobs.NewCollector(repos, snaps, queue, cache, provider, budget,
			cfg.Tracking, appClock, log, metrics)

		payload, err := json.Marshal(jobs.CollectPayload{
			Owner: name.Owner, Name: name.Repository, UserRequested: true,
		})
		if err != nil {
			return err
		}

		if err := collector.Handle(ctx, repository.Job{
			Kind: repository.JobCollectRepository, Payload: payload,
		}); err != nil {
			return fmt.Errorf("collect %s: %w", name, err)
		}
		fmt.Printf("collected %s\n", name)
		return nil

	case "discover":
		if cfg.App.DemoMode {
			return errors.New("discovery is unavailable in demo mode; set DEMO_MODE=false and supply GITHUB_TOKEN")
		}
		provider := github.NewClient(cfg.GitHub, log, metrics)
		discoverer := jobs.NewDiscoverer(repos, queue, discoveryRuns, provider, budget,
			cfg.Worker, appClock, log, metrics)

		if err := discoverer.Handle(ctx, repository.Job{Kind: repository.JobDiscoverRepositories}); err != nil {
			return fmt.Errorf("discovery run: %w", err)
		}
		fmt.Println("discovery run complete")
		return nil

	default:
		return fmt.Errorf("unknown command %q; expected score, tiers, collect or discover", command)
	}
}
