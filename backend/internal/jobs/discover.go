package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/FayezBast/repometeor/backend/internal/clock"
	"github.com/FayezBast/repometeor/backend/internal/config"
	"github.com/FayezBast/repometeor/backend/internal/github"
	"github.com/FayezBast/repometeor/backend/internal/repository"
	"github.com/FayezBast/repometeor/backend/internal/telemetry"
)

// Discoverer finds new repositories to track.
//
// Discovery uses GitHub's repository search API with partitioned queries. It
// never parses HTML and never reads GitHub Trending: every candidate is the
// result of a documented search qualifier, which is what makes RepoMeteor's
// coverage explainable and its terms-of-service position clear.
type Discoverer struct {
	repositories *repository.Repositories
	jobs         *repository.Jobs
	runs         *repository.Discovery
	provider     github.Provider
	budget       *github.Budget
	worker       config.WorkerConfig
	clock        clock.Clock
	log          *slog.Logger
	metrics      *telemetry.Metrics
}

// NewDiscoverer constructs the discovery handler.
func NewDiscoverer(
	repos *repository.Repositories,
	jobs *repository.Jobs,
	runs *repository.Discovery,
	provider github.Provider,
	budget *github.Budget,
	worker config.WorkerConfig,
	c clock.Clock,
	log *slog.Logger,
	metrics *telemetry.Metrics,
) *Discoverer {
	return &Discoverer{
		repositories: repos, jobs: jobs, runs: runs,
		provider: provider, budget: budget, worker: worker,
		clock: c, log: log, metrics: metrics,
	}
}

// Kind implements Handler.
func (d *Discoverer) Kind() repository.JobKind { return repository.JobDiscoverRepositories }

// Handle implements Handler.
//
// Each planned search runs independently: one failing partition does not abort
// the run, because partial discovery is far better than none. Every partition
// is recorded in discovery_runs so coverage gaps are auditable after the fact.
func (d *Discoverer) Handle(ctx context.Context, _ repository.Job) error {
	now := d.clock.Now()
	plans := github.PlanDiscovery(now, d.worker.DiscoveryCreatedLookback, d.worker.DiscoveryPushedLookback)

	var (
		totalCandidates int
		totalAccepted   int
		failures        int
	)

	for _, plan := range plans {
		if err := ctx.Err(); err != nil {
			return err
		}

		decision := d.budget.Allow("search", github.PurposeBackground)
		if !decision.Allowed {
			d.log.InfoContext(ctx, "stopping discovery early; search budget reached",
				slog.String("reason", decision.Reason),
				slog.Int("plansCompleted", totalCandidates))
			// The remainder is not a failure: the next scheduled run picks up
			// where this one stopped once the budget refills.
			break
		}

		candidates, accepted, err := d.runPlan(ctx, plan, now)
		totalCandidates += candidates
		totalAccepted += accepted

		if err != nil {
			failures++
			d.log.WarnContext(ctx, "discovery partition failed",
				slog.String("strategy", plan.Strategy),
				slog.String("language", plan.Input.Language),
				slog.String("error", err.Error()),
			)
			// A rate-limit error means every subsequent partition would fail
			// too, so stop rather than burning through the plan list.
			if kind := github.KindOf(err); kind == github.ErrRateLimited || kind == github.ErrSecondaryRateLimit {
				break
			}
		}
	}

	d.metrics.AddDiscovery("candidate", totalCandidates)
	d.metrics.AddDiscovery("accepted", totalAccepted)

	d.log.InfoContext(ctx, "discovery run finished",
		slog.Int("candidates", totalCandidates),
		slog.Int("accepted", totalAccepted),
		slog.Int("failedPartitions", failures),
	)

	if failures == len(plans) && len(plans) > 0 {
		return fmt.Errorf("every discovery partition failed (%d of %d)", failures, len(plans))
	}
	return nil
}

// runPlan executes one partitioned search and queues collection for new finds.
func (d *Discoverer) runPlan(ctx context.Context, plan github.DiscoveryPlan, now time.Time) (candidates, accepted int, err error) {
	query := github.BuildSearchQuery(plan.Input)

	runID, err := d.runs.Start(ctx, plan.Strategy, query, now)
	if err != nil {
		return 0, 0, fmt.Errorf("record discovery run: %w", err)
	}

	record := repository.DiscoveryRun{ID: runID, Strategy: plan.Strategy, Query: query}
	defer func() {
		record.CandidatesSeen = candidates
		record.CandidatesAccepted = accepted
		record.Succeeded = err == nil
		if err != nil {
			record.ErrorSummary = err.Error()
		}
		if finishErr := d.runs.Finish(ctx, record); finishErr != nil {
			d.log.WarnContext(ctx, "could not close discovery run record",
				slog.String("error", finishErr.Error()))
		}
	}()

	result, searchErr := d.provider.SearchRepositories(ctx, plan.Input)
	if searchErr != nil {
		return 0, 0, searchErr
	}

	d.budget.Observe(result.RateLimit)
	record.PagesVisited = 1
	record.RateLimitResource = result.RateLimit.Resource
	if result.RateLimit.Known {
		remaining := result.RateLimit.Remaining
		record.RateLimitRemaining = &remaining
	}

	if result.IncompleteResults {
		// GitHub timed out server-side. The page is still usable; note it so
		// coverage gaps are explainable later.
		d.log.InfoContext(ctx, "search returned incomplete results",
			slog.String("strategy", plan.Strategy),
			slog.String("language", plan.Input.Language))
	}

	candidates = len(result.Repositories)
	for _, candidate := range result.Repositories {
		queued, queueErr := d.considerCandidate(ctx, candidate, now)
		if queueErr != nil {
			d.log.WarnContext(ctx, "could not queue discovered repository",
				slog.String("repository", candidate.FullName),
				slog.String("error", queueErr.Error()))
			continue
		}
		if queued {
			accepted++
		}
	}

	return candidates, accepted, nil
}

// considerCandidate queues a collection job for a repository worth tracking.
//
// Deduplication happens at two levels: GitHub's numeric ID identifies
// repositories already known, and the job dedupe key prevents two partitions
// that both surface the same repository from queueing it twice.
func (d *Discoverer) considerCandidate(ctx context.Context, candidate github.Repository, now time.Time) (bool, error) {
	if candidate.IsFork || candidate.IsArchived || candidate.IsDisabled || candidate.IsPrivate {
		return false, nil
	}

	if _, err := d.repositories.GetByGitHubID(ctx, candidate.ID); err == nil {
		// Already tracked; its own polling schedule governs collection.
		return false, nil
	}

	_, err := d.jobs.Enqueue(ctx, repository.EnqueueInput{
		Kind:        repository.JobCollectRepository,
		Priority:    5,
		RunAfter:    now,
		MaxAttempts: 3,
		Payload:     repository.CollectPayload{Owner: candidate.Owner, Name: candidate.Name}.Encode(),
		DedupeKey:   "collect:" + candidate.FullName,
	})
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, repository.ErrDuplicateJob):
		return false, nil
	default:
		return false, err
	}
}
