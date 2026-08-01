package repository

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/repometeor/repometeor/backend/internal/database"
)

// Jobs is the durable work queue.
//
// Claiming uses SELECT ... FOR UPDATE SKIP LOCKED so that several worker
// instances can share one queue without coordinating: each transaction takes
// rows nobody else has locked, and a crashed worker's rows become claimable
// again through stale-lock recovery rather than being lost.
type Jobs struct {
	db *database.DB
}

// NewJobs constructs the queue.
func NewJobs(db *database.DB) *Jobs { return &Jobs{db: db} }

const jobColumns = `
    id, kind, repository_id, payload,
    status, priority, run_after,
    attempts, max_attempts,
    locked_at, COALESCE(locked_by, ''), COALESCE(last_error, ''), COALESCE(dedupe_key, ''),
    created_at, updated_at, completed_at`

func scanJob(row pgx.Row) (Job, error) {
	var j Job
	err := row.Scan(
		&j.ID, &j.Kind, &j.RepositoryID, &j.Payload,
		&j.Status, &j.Priority, &j.RunAfter,
		&j.Attempts, &j.MaxAttempts,
		&j.LockedAt, &j.LockedBy, &j.LastError, &j.DedupeKey,
		&j.CreatedAt, &j.UpdatedAt, &j.CompletedAt,
	)
	if err != nil {
		return Job{}, database.NormaliseNotFound(err)
	}
	return j, nil
}

// ErrDuplicateJob is returned when a dedupe key already has an active job.
var ErrDuplicateJob = errors.New("a queued or running job already exists for this dedupe key")

// Enqueue adds a job.
//
// When DedupeKey is set and an active job already holds it, Enqueue returns the
// existing job and ErrDuplicateJob rather than creating a duplicate. Callers
// that treat deduplication as success can ignore the error.
func (s *Jobs) Enqueue(ctx context.Context, in EnqueueInput) (Job, error) {
	if in.Kind == "" {
		return Job{}, errors.New("job kind is required")
	}
	if in.MaxAttempts <= 0 {
		in.MaxAttempts = 5
	}
	if in.RunAfter.IsZero() {
		in.RunAfter = time.Now()
	}
	if in.Payload == nil {
		in.Payload = []byte(`{}`)
	}

	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	row := s.db.Pool().QueryRow(ctx, `
        INSERT INTO crawl_jobs (kind, repository_id, payload, priority, run_after, max_attempts, dedupe_key)
        VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))
        ON CONFLICT (dedupe_key) WHERE dedupe_key IS NOT NULL AND status IN ('queued', 'running')
        DO NOTHING
        RETURNING `+jobColumns,
		string(in.Kind), in.RepositoryID, in.Payload, in.Priority, in.RunAfter.UTC(), in.MaxAttempts, in.DedupeKey)

	job, err := scanJob(row)
	if errors.Is(err, database.ErrNotFound) && in.DedupeKey != "" {
		existing, findErr := s.findActiveByDedupeKey(ctx, in.DedupeKey)
		if findErr != nil {
			return Job{}, fmt.Errorf("dedupe conflict but existing job not found: %w", findErr)
		}
		return existing, ErrDuplicateJob
	}
	return job, err
}

func (s *Jobs) findActiveByDedupeKey(ctx context.Context, key string) (Job, error) {
	row := s.db.Pool().QueryRow(ctx, `
        SELECT `+jobColumns+`
        FROM crawl_jobs
        WHERE dedupe_key = $1 AND status IN ('queued', 'running')
        LIMIT 1`, key)
	return scanJob(row)
}

// Claim atomically takes up to `limit` due jobs for the named worker.
//
// The UPDATE ... FROM (SELECT ... FOR UPDATE SKIP LOCKED) shape performs the
// selection and the state transition in a single statement, so no other worker
// can observe a job between being chosen and being marked running.
func (s *Jobs) Claim(ctx context.Context, workerID string, limit int, now time.Time) ([]Job, error) {
	if limit <= 0 {
		return nil, nil
	}

	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	rows, err := s.db.Pool().Query(ctx, `
        UPDATE crawl_jobs SET
            status     = 'running',
            attempts   = attempts + 1,
            locked_at  = $3,
            locked_by  = $1,
            updated_at = NOW()
        WHERE id IN (
            SELECT id FROM crawl_jobs
            WHERE status = 'queued' AND run_after <= $3
            ORDER BY priority DESC, run_after ASC, id ASC
            LIMIT $2
            FOR UPDATE SKIP LOCKED
        )
        RETURNING `+jobColumns,
		workerID, limit, now.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// The subquery's ORDER BY selects the right set of jobs, but PostgreSQL
	// gives no ordering guarantee for UPDATE ... RETURNING rows. Sorting here
	// restores the intended processing order within the batch, so a
	// high-priority job is not left until the end of it.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		if !out[i].RunAfter.Equal(out[j].RunAfter) {
			return out[i].RunAfter.Before(out[j].RunAfter)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Complete marks a job finished successfully.
func (s *Jobs) Complete(ctx context.Context, jobID int64) error {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	_, err := s.db.Pool().Exec(ctx, `
        UPDATE crawl_jobs SET
            status       = 'completed',
            completed_at = NOW(),
            locked_at    = NULL,
            locked_by    = NULL,
            last_error   = NULL,
            updated_at   = NOW()
        WHERE id = $1`, jobID)
	return err
}

// Fail records a failed attempt.
//
// If attempts remain the job returns to 'queued' with an exponential, jittered
// delay; otherwise it moves to 'dead' so an operator can inspect it. Jitter
// matters because a GitHub outage fails many jobs at once and un-jittered
// backoff would retry them all in the same instant.
func (s *Jobs) Fail(ctx context.Context, jobID int64, cause error, now time.Time) (JobStatus, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	message := "unknown error"
	if cause != nil {
		message = truncateForStorage(cause.Error(), 1000)
	}

	var (
		attempts    int
		maxAttempts int
	)
	err := s.db.Pool().QueryRow(ctx,
		`SELECT attempts, max_attempts FROM crawl_jobs WHERE id = $1`, jobID).Scan(&attempts, &maxAttempts)
	if err != nil {
		return "", database.NormaliseNotFound(err)
	}

	if attempts >= maxAttempts {
		_, err := s.db.Pool().Exec(ctx, `
            UPDATE crawl_jobs SET
                status       = 'dead',
                last_error   = $2,
                locked_at    = NULL,
                locked_by    = NULL,
                completed_at = NOW(),
                updated_at   = NOW()
            WHERE id = $1`, jobID, message)
		return JobDead, err
	}

	retryAt := now.Add(RetryDelay(attempts))
	_, err = s.db.Pool().Exec(ctx, `
        UPDATE crawl_jobs SET
            status     = 'queued',
            last_error = $2,
            run_after  = $3,
            locked_at  = NULL,
            locked_by  = NULL,
            updated_at = NOW()
        WHERE id = $1`, jobID, message, retryAt.UTC())
	return JobQueued, err
}

// Reschedule returns a job to the queue at a specific time without counting the
// attempt as a failure. The collector uses it when the GitHub rate budget is
// exhausted: the work is still valid, it simply cannot run yet.
func (s *Jobs) Reschedule(ctx context.Context, jobID int64, runAfter time.Time, reason string) error {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	_, err := s.db.Pool().Exec(ctx, `
        UPDATE crawl_jobs SET
            status     = 'queued',
            attempts   = GREATEST(attempts - 1, 0),
            run_after  = $2,
            last_error = NULLIF($3, ''),
            locked_at  = NULL,
            locked_by  = NULL,
            updated_at = NOW()
        WHERE id = $1`, jobID, runAfter.UTC(), truncateForStorage(reason, 500))
	return err
}

// RecoverStale returns jobs whose worker died to the queue.
//
// A job is stale when it has been 'running' for longer than the configured
// timeout. Its attempt count is preserved, so a job that repeatedly kills its
// worker still reaches the dead-letter state instead of looping forever.
func (s *Jobs) RecoverStale(ctx context.Context, olderThan time.Time) (int, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	tag, err := s.db.Pool().Exec(ctx, `
        UPDATE crawl_jobs SET
            status     = CASE WHEN attempts >= max_attempts THEN 'dead' ELSE 'queued' END,
            last_error = 'worker lock expired; job was reclaimed',
            locked_at  = NULL,
            locked_by  = NULL,
            run_after  = NOW(),
            updated_at = NOW()
        WHERE status = 'running' AND locked_at < $1`, olderThan.UTC())
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// Depth reports how many jobs are in each status, for metrics and the runbook.
func (s *Jobs) Depth(ctx context.Context) (map[JobStatus]int, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	rows, err := s.db.Pool().Query(ctx, `SELECT status, COUNT(*)::int FROM crawl_jobs GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[JobStatus]int{
		JobQueued: 0, JobRunning: 0, JobCompleted: 0, JobFailed: 0, JobDead: 0,
	}
	for rows.Next() {
		var status JobStatus
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		out[status] = count
	}
	return out, rows.Err()
}

// Get loads a single job.
func (s *Jobs) Get(ctx context.Context, jobID int64) (Job, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	row := s.db.Pool().QueryRow(ctx, `SELECT `+jobColumns+` FROM crawl_jobs WHERE id = $1`, jobID)
	return scanJob(row)
}

// PruneCompleted deletes finished jobs older than the cutoff so the table does
// not grow without bound. Dead jobs are kept for inspection.
func (s *Jobs) PruneCompleted(ctx context.Context, before time.Time) (int, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	tag, err := s.db.Pool().Exec(ctx,
		`DELETE FROM crawl_jobs WHERE status = 'completed' AND completed_at < $1`, before.UTC())
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// Retry backoff bounds.
const (
	retryBaseDelay = 30 * time.Second
	retryMaxDelay  = 30 * time.Minute
)

// RetryDelay returns the backoff before attempt number `attempts + 1`.
//
// The schedule is 30s, 60s, 120s, 240s … capped at 30 minutes, with up to ±25%
// jitter applied to spread out simultaneous failures.
func RetryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	// Cap the exponent before the shift so the multiplication cannot overflow.
	exponent := min(attempts-1, 16)
	delay := time.Duration(float64(retryBaseDelay) * math.Pow(2, float64(exponent)))
	if delay > retryMaxDelay || delay <= 0 {
		delay = retryMaxDelay
	}

	jitter := (rand.Float64()*0.5 - 0.25) * float64(delay) //nolint:gosec // jitter, not cryptography
	withJitter := time.Duration(float64(delay) + jitter)

	// Clamp after jitter so retryMaxDelay is a real upper bound.
	if withJitter < time.Second {
		return time.Second
	}
	if withJitter > retryMaxDelay {
		return retryMaxDelay
	}
	return withJitter
}
