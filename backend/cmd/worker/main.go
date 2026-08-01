// Command worker runs repometeor-worker: the collection and scoring engine.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/repometeor/repometeor/backend/internal/clock"
	"github.com/repometeor/repometeor/backend/internal/config"
	"github.com/repometeor/repometeor/backend/internal/database"
	"github.com/repometeor/repometeor/backend/internal/github"
	"github.com/repometeor/repometeor/backend/internal/jobs"
	"github.com/repometeor/repometeor/backend/internal/repository"
	"github.com/repometeor/repometeor/backend/internal/telemetry"
)

// version is the worker build version.
const version = "0.1.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "repometeor-worker: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := telemetry.NewLogger(os.Stdout, cfg.App.LogLevel, "repometeor-worker")
	log.Info("starting repometeor-worker", slog.Any("config", cfg.Redacted()))

	metrics := telemetry.NewMetrics(version)
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

	provider := buildProvider(cfg, log, metrics)
	budget := github.NewBudget(cfg.GitHub.RateBudgetPercent, appClock)

	worker := jobs.NewWorker(queue, cfg.Worker, appClock, log, metrics)
	worker.Register(jobs.NewCollector(repos, snaps, queue, cache, provider, budget,
		cfg.Tracking, appClock, log, metrics))
	worker.Register(jobs.NewDiscoverer(repos, queue, discoveryRuns, provider, budget,
		cfg.Worker, appClock, log, metrics))
	worker.Register(jobs.NewScorer(repos, snaps, scores, cfg.Tracking, appClock, log, metrics))
	worker.Register(jobs.NewTierRefresher(repos, scores, cfg.Tracking, appClock, log))

	scheduler := jobs.NewScheduler(queue, cfg.Worker, cfg.App.DemoMode, appClock, log)

	// The collection dispatcher is separate from the queue runner: it turns
	// "this repository is due" into queued jobs, which the runner then executes.
	dispatcher := newDispatcher(repos, queue, cfg, appClock, log)

	var wg sync.WaitGroup
	runComponent := func(name string, fn func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(ctx); err != nil {
				log.Error("component stopped with an error",
					slog.String("component", name), slog.String("error", err.Error()))
			}
		}()
	}

	runComponent("worker", worker.Run)
	runComponent("scheduler", scheduler.Run)
	if !cfg.App.DemoMode {
		runComponent("dispatcher", dispatcher.Run)
	} else {
		log.Info("collection dispatcher disabled: demo mode uses seeded data and contacts no external service")
	}

	<-ctx.Done()
	stop()
	log.Info("shutdown signal received")

	// Each component observes the cancelled context and drains on its own; the
	// wait group is what makes the process outlive that draining.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Info("repometeor-worker stopped")
	case <-time.After(cfg.Worker.ShutdownTimeout + 5*time.Second):
		log.Warn("shutdown deadline exceeded; exiting with work still in flight")
	}
	return nil
}

// buildProvider selects the GitHub implementation for the configured mode.
//
// Demo mode uses the fixture provider, which never opens a socket. That means
// a demo deployment cannot contact GitHub even if it is misconfigured.
func buildProvider(cfg *config.Config, log *slog.Logger, metrics *telemetry.Metrics) github.Provider {
	if cfg.App.DemoMode {
		log.Info("demo mode: using the fixture GitHub provider; no network requests will be made")
		return github.NewFixtureProvider()
	}
	return github.NewClient(cfg.GitHub, log, metrics)
}

// dispatcher turns due repositories into queued collection jobs.
type dispatcher struct {
	repositories *repository.Repositories
	jobs         *repository.Jobs
	cfg          *config.Config
	clock        clock.Clock
	log          *slog.Logger
}

func newDispatcher(
	repos *repository.Repositories,
	queue *repository.Jobs,
	cfg *config.Config,
	c clock.Clock,
	log *slog.Logger,
) *dispatcher {
	return &dispatcher{repositories: repos, jobs: queue, cfg: cfg, clock: c, log: log}
}

// Run enqueues collection jobs for repositories whose next check has arrived.
func (d *dispatcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(d.cfg.Worker.JobPollInterval * 5)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.log.Info("collection dispatcher stopped")
			return nil
		case <-ticker.C:
			d.dispatch(ctx)
		}
	}
}

func (d *dispatcher) dispatch(ctx context.Context) {
	now := d.clock.Now()

	// The batch is bounded so that a large backlog is worked through steadily
	// rather than queued all at once.
	due, err := d.repositories.DueForCollection(ctx, now, d.cfg.Worker.JobBatchSize*4)
	if err != nil {
		d.log.ErrorContext(ctx, "could not list due repositories", slog.String("error", err.Error()))
		return
	}

	queued := 0
	for _, repo := range due {
		payload := jobs.CollectPayload{Owner: repo.OwnerLogin, Name: repo.Name}
		encoded, err := marshalPayload(payload)
		if err != nil {
			continue
		}

		_, err = d.jobs.Enqueue(ctx, repository.EnqueueInput{
			Kind:         repository.JobCollectRepository,
			RepositoryID: &repo.ID,
			Priority:     priorityForTier(repo.TrackingTier),
			RunAfter:     now,
			MaxAttempts:  3,
			Payload:      encoded,
			// One active collection job per repository at a time.
			DedupeKey: "collect:" + repo.RepoName().Lower().String(),
		})
		if err == nil {
			queued++
		}
	}

	if queued > 0 {
		d.log.DebugContext(ctx, "queued collection jobs", slog.Int("count", queued))
	}
}

func priorityForTier(tier repository.TrackingTier) int {
	switch tier {
	case repository.TierHot:
		return 30
	case repository.TierWarm:
		return 20
	default:
		return 10
	}
}

func marshalPayload(v any) ([]byte, error) { return json.Marshal(v) }
