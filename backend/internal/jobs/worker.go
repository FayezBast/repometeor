// Package jobs implements the RepoMeteor worker: the durable queue runner and
// its handlers for discovery, collection, scoring and tier maintenance.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/FayezBast/repometeor/backend/internal/clock"
	"github.com/FayezBast/repometeor/backend/internal/config"
	"github.com/FayezBast/repometeor/backend/internal/repository"
	"github.com/FayezBast/repometeor/backend/internal/telemetry"
)

// Handler processes one job. Handlers must be idempotent: a job can be
// delivered more than once after a worker crash or a stale-lock recovery.
type Handler interface {
	// Kind is the job kind this handler serves.
	Kind() repository.JobKind
	// Handle processes the job. Returning nil marks it completed; returning an
	// error schedules a retry until max_attempts is reached.
	Handle(ctx context.Context, job repository.Job) error
}

// ErrDeferred signals that a job could not run yet but has not failed — for
// example because the GitHub rate budget is exhausted. The job returns to the
// queue at the supplied time without consuming an attempt.
type ErrDeferred struct {
	RunAfter time.Time
	Reason   string
}

func (e *ErrDeferred) Error() string {
	return fmt.Sprintf("deferred until %s: %s", e.RunAfter.Format(time.RFC3339), e.Reason)
}

// Deferred builds an ErrDeferred.
func Deferred(runAfter time.Time, reason string) error {
	return &ErrDeferred{RunAfter: runAfter, Reason: reason}
}

// Worker claims and executes jobs.
type Worker struct {
	id       string
	jobs     *repository.Jobs
	handlers map[repository.JobKind]Handler
	cfg      config.WorkerConfig
	clock    clock.Clock
	log      *slog.Logger
	metrics  *telemetry.Metrics
}

// NewWorker constructs a worker. The worker ID identifies lock ownership in the
// queue and appears in logs; it combines the hostname with the process ID so
// that two workers on one host remain distinguishable.
func NewWorker(
	jobs *repository.Jobs,
	cfg config.WorkerConfig,
	c clock.Clock,
	log *slog.Logger,
	metrics *telemetry.Metrics,
) *Worker {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown-host"
	}

	return &Worker{
		id:       fmt.Sprintf("%s-%d", hostname, os.Getpid()),
		jobs:     jobs,
		handlers: make(map[repository.JobKind]Handler),
		cfg:      cfg,
		clock:    c,
		log:      log,
		metrics:  metrics,
	}
}

// ID returns the worker's lock identity.
func (w *Worker) ID() string { return w.id }

// Register adds a handler. Registering two handlers for one kind is a
// programming error and panics at startup rather than silently dropping work.
func (w *Worker) Register(handler Handler) {
	if _, exists := w.handlers[handler.Kind()]; exists {
		panic(fmt.Sprintf("duplicate handler registered for job kind %q", handler.Kind()))
	}
	w.handlers[handler.Kind()] = handler
}

// Run processes jobs until ctx is cancelled.
//
// Shutdown is graceful: the loop stops claiming new work as soon as the context
// is done, and Run returns only once the jobs already in flight have finished
// or the shutdown deadline has passed. A job that is still running when the
// deadline expires is left locked and will be recovered by the stale-lock
// sweeper, so no work is lost.
func (w *Worker) Run(ctx context.Context) error {
	w.log.InfoContext(ctx, "worker started",
		slog.String("workerId", w.id),
		slog.Int("handlers", len(w.handlers)),
		slog.String("pollInterval", w.cfg.JobPollInterval.String()),
	)

	var inFlight sync.WaitGroup

	ticker := time.NewTicker(w.cfg.JobPollInterval)
	defer ticker.Stop()

	sweeper := time.NewTicker(w.cfg.StaleJobTimeout / 3)
	defer sweeper.Stop()

	for {
		select {
		case <-ctx.Done():
			w.log.Info("worker draining in-flight jobs", slog.String("workerId", w.id))

			drained := make(chan struct{})
			go func() {
				inFlight.Wait()
				close(drained)
			}()

			select {
			case <-drained:
				w.log.Info("worker stopped cleanly", slog.String("workerId", w.id))
			case <-time.After(w.cfg.ShutdownTimeout):
				w.log.Warn("worker shutdown deadline reached; in-flight jobs will be recovered from their locks",
					slog.String("workerId", w.id))
			}
			return nil

		case <-sweeper.C:
			w.recoverStaleJobs(ctx)

		case <-ticker.C:
			w.claimAndRun(ctx, &inFlight)
		}
	}
}

func (w *Worker) claimAndRun(ctx context.Context, inFlight *sync.WaitGroup) {
	claimed, err := w.jobs.Claim(ctx, w.id, w.cfg.JobBatchSize, w.clock.Now())
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			w.log.ErrorContext(ctx, "failed to claim jobs", slog.String("error", err.Error()))
		}
		return
	}
	if len(claimed) == 0 {
		return
	}

	w.publishQueueDepth(ctx)

	for _, job := range claimed {
		inFlight.Add(1)
		go func(job repository.Job) {
			defer inFlight.Done()
			w.execute(ctx, job)
		}(job)
	}
}

// execute runs one job and records its outcome.
func (w *Worker) execute(ctx context.Context, job repository.Job) {
	// Use a context detached from cancellation so a shutdown does not abort a
	// job mid-write; the shutdown deadline in Run bounds how long this can take.
	jobCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.cfg.StaleJobTimeout)
	defer cancel()

	jobCtx = telemetry.WithJobID(jobCtx, fmt.Sprintf("%d", job.ID))
	start := time.Now()

	handler, ok := w.handlers[job.Kind]
	if !ok {
		err := fmt.Errorf("no handler registered for job kind %q", job.Kind)
		w.log.ErrorContext(jobCtx, "unhandled job kind", slog.String("kind", string(job.Kind)))
		w.finishFailed(jobCtx, job, err, start)
		return
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			err := fmt.Errorf("handler panicked: %v", recovered)
			w.log.ErrorContext(jobCtx, "job handler panicked",
				slog.String("kind", string(job.Kind)),
				slog.Int64("jobId", job.ID),
			)
			w.finishFailed(jobCtx, job, err, start)
		}
	}()

	err := handler.Handle(jobCtx, job)
	duration := time.Since(start)

	var deferErr *ErrDeferred
	switch {
	case err == nil:
		if completeErr := w.jobs.Complete(jobCtx, job.ID); completeErr != nil {
			w.log.ErrorContext(jobCtx, "failed to mark job completed",
				slog.Int64("jobId", job.ID), slog.String("error", completeErr.Error()))
		}
		w.metrics.ObserveJob(string(job.Kind), "completed", duration)
		w.log.DebugContext(jobCtx, "job completed",
			slog.String("kind", string(job.Kind)),
			slog.Int64("durationMs", duration.Milliseconds()),
		)

	case errors.As(err, &deferErr):
		if rescheduleErr := w.jobs.Reschedule(jobCtx, job.ID, deferErr.RunAfter, deferErr.Reason); rescheduleErr != nil {
			w.log.ErrorContext(jobCtx, "failed to reschedule job",
				slog.Int64("jobId", job.ID), slog.String("error", rescheduleErr.Error()))
		}
		w.metrics.ObserveJob(string(job.Kind), "deferred", duration)
		w.log.InfoContext(jobCtx, "job deferred",
			slog.String("kind", string(job.Kind)),
			slog.String("reason", deferErr.Reason),
			slog.String("runAfter", deferErr.RunAfter.Format(time.RFC3339)),
		)

	default:
		w.finishFailed(jobCtx, job, err, start)
	}
}

func (w *Worker) finishFailed(ctx context.Context, job repository.Job, cause error, start time.Time) {
	status, err := w.jobs.Fail(ctx, job.ID, cause, w.clock.Now())
	if err != nil {
		w.log.ErrorContext(ctx, "failed to record job failure",
			slog.Int64("jobId", job.ID), slog.String("error", err.Error()))
	}

	w.metrics.ObserveJob(string(job.Kind), string(status), time.Since(start))

	level := slog.LevelWarn
	if status == repository.JobDead {
		level = slog.LevelError
	}
	w.log.Log(ctx, level, "job failed",
		slog.String("kind", string(job.Kind)),
		slog.Int64("jobId", job.ID),
		slog.Int("attempt", job.Attempts),
		slog.Int("maxAttempts", job.MaxAttempts),
		slog.String("status", string(status)),
		slog.String("error", cause.Error()),
	)
}

// recoverStaleJobs returns jobs whose worker died back to the queue.
func (w *Worker) recoverStaleJobs(ctx context.Context) {
	cutoff := w.clock.Now().Add(-w.cfg.StaleJobTimeout)

	recovered, err := w.jobs.RecoverStale(ctx, cutoff)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			w.log.ErrorContext(ctx, "stale job recovery failed", slog.String("error", err.Error()))
		}
		return
	}
	if recovered > 0 {
		w.log.WarnContext(ctx, "recovered stale jobs",
			slog.Int("count", recovered),
			slog.String("olderThan", w.cfg.StaleJobTimeout.String()),
		)
	}
	w.publishQueueDepth(ctx)
}

func (w *Worker) publishQueueDepth(ctx context.Context) {
	depth, err := w.jobs.Depth(ctx)
	if err != nil {
		return
	}
	for status, count := range depth {
		w.metrics.SetJobQueueDepth(string(status), count)
	}
}

// Scheduler enqueues the recurring jobs that keep RepoMeteor running.
//
// Recurrence is expressed through dedupe keys rather than an in-memory timer:
// the key is derived from the schedule slot, so two workers racing to enqueue
// the same run produce exactly one job, and a worker restart does not lose or
// duplicate a slot.
type Scheduler struct {
	jobs     *repository.Jobs
	cfg      config.WorkerConfig
	demoMode bool
	clock    clock.Clock
	log      *slog.Logger
}

// NewScheduler constructs the scheduler.
func NewScheduler(jobs *repository.Jobs, cfg config.WorkerConfig, demoMode bool, c clock.Clock, log *slog.Logger) *Scheduler {
	return &Scheduler{jobs: jobs, cfg: cfg, demoMode: demoMode, clock: c, log: log}
}

// Run enqueues recurring work until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) error {
	// Schedule immediately on startup so a fresh database produces rankings
	// without waiting a full interval.
	s.tick(ctx)

	ticker := time.NewTicker(schedulerInterval(s.cfg))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.log.Info("scheduler stopped")
			return nil
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func schedulerInterval(cfg config.WorkerConfig) time.Duration {
	// Tick often enough to catch the shortest schedule, but never faster than
	// once a minute.
	interval := cfg.ScoringInterval / 4
	if interval < time.Minute {
		interval = time.Minute
	}
	return interval
}

func (s *Scheduler) tick(ctx context.Context) {
	now := s.clock.Now()

	// Scoring always runs: it derives rankings from stored snapshots and needs
	// no network access, so it is just as necessary in demo mode.
	s.enqueue(ctx, repository.EnqueueInput{
		Kind:        repository.JobScoreRepositories,
		Priority:    50,
		RunAfter:    now,
		MaxAttempts: 3,
		DedupeKey:   slotKey("score", now, s.cfg.ScoringInterval),
	})

	s.enqueue(ctx, repository.EnqueueInput{
		Kind:        repository.JobRefreshTiers,
		Priority:    10,
		RunAfter:    now,
		MaxAttempts: 3,
		DedupeKey:   slotKey("tiers", now, s.cfg.ScoringInterval),
	})

	// Discovery contacts GitHub, so it is meaningless in demo mode.
	if s.cfg.DiscoveryEnabled && !s.demoMode {
		s.enqueue(ctx, repository.EnqueueInput{
			Kind:        repository.JobDiscoverRepositories,
			Priority:    20,
			RunAfter:    now,
			MaxAttempts: 3,
			DedupeKey:   slotKey("discover", now, s.cfg.DiscoveryInterval),
		})
	}
}

func (s *Scheduler) enqueue(ctx context.Context, in repository.EnqueueInput) {
	_, err := s.jobs.Enqueue(ctx, in)
	switch {
	case err == nil:
		s.log.DebugContext(ctx, "scheduled job",
			slog.String("kind", string(in.Kind)), slog.String("dedupeKey", in.DedupeKey))
	case errors.Is(err, repository.ErrDuplicateJob):
		// Expected: this slot is already queued or running.
	case errors.Is(err, context.Canceled):
		// Shutting down.
	default:
		s.log.ErrorContext(ctx, "failed to schedule job",
			slog.String("kind", string(in.Kind)), slog.String("error", err.Error()))
	}
}

// slotKey derives a dedupe key from the schedule slot containing `now`, so that
// exactly one job exists per interval regardless of how many workers tick.
func slotKey(prefix string, now time.Time, interval time.Duration) string {
	if interval <= 0 {
		interval = time.Hour
	}
	return fmt.Sprintf("%s:%d", prefix, now.UTC().Truncate(interval).Unix())
}
