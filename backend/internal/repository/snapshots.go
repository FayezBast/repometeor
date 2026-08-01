package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/repometeor/repometeor/backend/internal/database"
)

// Snapshots reads and writes the repository_snapshots time series.
type Snapshots struct {
	db *database.DB
}

// NewSnapshots constructs the store.
func NewSnapshots(db *database.DB) *Snapshots { return &Snapshots{db: db} }

const snapshotColumns = `
    id, repository_id, captured_at, bucket_start,
    stars, forks, open_issues, subscribers,
    latest_release_at, source`

func scanSnapshot(row pgx.Row) (Snapshot, error) {
	var s Snapshot
	err := row.Scan(
		&s.ID, &s.RepositoryID, &s.CapturedAt, &s.BucketStart,
		&s.Stars, &s.Forks, &s.OpenIssues, &s.Subscribers,
		&s.LatestReleaseAt, &s.Source,
	)
	if err != nil {
		return Snapshot{}, database.NormaliseNotFound(err)
	}
	// See normaliseRepositoryTimes: every emitted timestamp is UTC.
	s.CapturedAt = s.CapturedAt.UTC()
	s.BucketStart = s.BucketStart.UTC()
	s.LatestReleaseAt = utcPtr(s.LatestReleaseAt)
	return s, nil
}

// Upsert writes one snapshot for a repository and bucket.
//
// Collection is idempotent: re-running a poll inside the same bucket updates
// the existing row rather than creating a second observation, which is what
// keeps delta arithmetic honest when the worker restarts or a job is retried.
func (s *Snapshots) Upsert(ctx context.Context, snap Snapshot) (Snapshot, error) {
	if snap.Stars < 0 || snap.Forks < 0 || snap.OpenIssues < 0 {
		return Snapshot{}, fmt.Errorf("snapshot counts must not be negative (stars=%d forks=%d issues=%d)",
			snap.Stars, snap.Forks, snap.OpenIssues)
	}
	if snap.Source == "" {
		snap.Source = SourceGitHubAPI
	}

	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	row := s.db.Pool().QueryRow(ctx, `
        INSERT INTO repository_snapshots (
            repository_id, captured_at, bucket_start,
            stars, forks, open_issues, subscribers,
            latest_release_at, source
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
        ON CONFLICT (repository_id, bucket_start) DO UPDATE SET
            captured_at       = EXCLUDED.captured_at,
            stars             = EXCLUDED.stars,
            forks             = EXCLUDED.forks,
            open_issues       = EXCLUDED.open_issues,
            subscribers       = EXCLUDED.subscribers,
            latest_release_at = EXCLUDED.latest_release_at,
            source            = EXCLUDED.source
        RETURNING `+snapshotColumns,
		snap.RepositoryID, snap.CapturedAt.UTC(), snap.BucketStart.UTC(),
		snap.Stars, snap.Forks, snap.OpenIssues, snap.Subscribers,
		utcPtr(snap.LatestReleaseAt), string(snap.Source))
	return scanSnapshot(row)
}

// Latest returns the most recent snapshot for a repository.
func (s *Snapshots) Latest(ctx context.Context, repositoryID int64) (Snapshot, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	row := s.db.Pool().QueryRow(ctx, `
        SELECT `+snapshotColumns+`
        FROM repository_snapshots
        WHERE repository_id = $1
        ORDER BY captured_at DESC
        LIMIT 1`, repositoryID)
	return scanSnapshot(row)
}

// Since returns every snapshot for a repository captured at or after `from`,
// oldest first. Scoring reads a window's worth of history through this method.
func (s *Snapshots) Since(ctx context.Context, repositoryID int64, from time.Time) ([]Snapshot, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	rows, err := s.db.Pool().Query(ctx, `
        SELECT `+snapshotColumns+`
        FROM repository_snapshots
        WHERE repository_id = $1 AND captured_at >= $2
        ORDER BY captured_at ASC`, repositoryID, from.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectSnapshots(rows)
}

// SinceForMany loads history for a set of repositories in one query.
//
// The scoring run needs a window of history for every eligible repository; a
// per-repository query would be an N+1 pattern against the largest table in the
// database. Results are grouped by repository ID with each slice oldest first.
func (s *Snapshots) SinceForMany(ctx context.Context, repositoryIDs []int64, from time.Time) (map[int64][]Snapshot, error) {
	if len(repositoryIDs) == 0 {
		return map[int64][]Snapshot{}, nil
	}

	// Scoring reads can exceed the default per-query budget on a large cohort.
	ctx, cancel := context.WithTimeout(ctx, 3*s.db.QueryTimeout())
	defer cancel()

	rows, err := s.db.Pool().Query(ctx, `
        SELECT `+snapshotColumns+`
        FROM repository_snapshots
        WHERE repository_id = ANY($1) AND captured_at >= $2
        ORDER BY repository_id ASC, captured_at ASC`, repositoryIDs, from.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64][]Snapshot, len(repositoryIDs))
	for rows.Next() {
		snap, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out[snap.RepositoryID] = append(out[snap.RepositoryID], snap)
	}
	return out, rows.Err()
}

// LatestForMany returns the newest snapshot per repository in one query.
func (s *Snapshots) LatestForMany(ctx context.Context, repositoryIDs []int64) (map[int64]Snapshot, error) {
	if len(repositoryIDs) == 0 {
		return map[int64]Snapshot{}, nil
	}

	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	// DISTINCT ON is the cheapest way to take the newest row per group in
	// PostgreSQL and uses the (repository_id, captured_at DESC) index directly.
	rows, err := s.db.Pool().Query(ctx, `
        SELECT DISTINCT ON (repository_id) `+snapshotColumns+`
        FROM repository_snapshots
        WHERE repository_id = ANY($1)
        ORDER BY repository_id, captured_at DESC`, repositoryIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64]Snapshot, len(repositoryIDs))
	for rows.Next() {
		snap, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out[snap.RepositoryID] = snap
	}
	return out, rows.Err()
}

// SparklinesForMany returns up to `points` recent star counts per repository,
// oldest first, for the small inline charts on repository cards.
func (s *Snapshots) SparklinesForMany(ctx context.Context, repositoryIDs []int64, points int) (map[int64][]int, error) {
	if len(repositoryIDs) == 0 || points <= 0 {
		return map[int64][]int{}, nil
	}

	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	rows, err := s.db.Pool().Query(ctx, `
        SELECT repository_id, stars
        FROM (
            SELECT repository_id, stars, captured_at,
                   ROW_NUMBER() OVER (PARTITION BY repository_id ORDER BY captured_at DESC) AS rn
            FROM repository_snapshots
            WHERE repository_id = ANY($1)
        ) ranked
        WHERE rn <= $2
        ORDER BY repository_id ASC, captured_at ASC`, repositoryIDs, points)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64][]int, len(repositoryIDs))
	for rows.Next() {
		var id int64
		var stars int
		if err := rows.Scan(&id, &stars); err != nil {
			return nil, err
		}
		out[id] = append(out[id], stars)
	}
	return out, rows.Err()
}

// Resolution controls how history is downsampled for the API.
type Resolution string

const (
	ResolutionRaw    Resolution = "raw"
	ResolutionHourly Resolution = "1h"
	ResolutionDaily  Resolution = "1d"
)

// History returns downsampled history for a repository.
//
// Downsampling happens in PostgreSQL so that a 90-day request does not stream
// thousands of rows into the API process. The resolution is an enum, never
// interpolated request text: it maps to a fixed date_trunc unit.
func (s *Snapshots) History(ctx context.Context, repositoryID int64, from time.Time, resolution Resolution) ([]HistoryPoint, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*s.db.QueryTimeout())
	defer cancel()

	var (
		rows pgx.Rows
		err  error
	)

	switch resolution {
	case ResolutionRaw:
		rows, err = s.db.Pool().Query(ctx, `
            SELECT captured_at, stars, forks
            FROM repository_snapshots
            WHERE repository_id = $1 AND captured_at >= $2
            ORDER BY captured_at ASC
            LIMIT 2000`, repositoryID, from.UTC())
	case ResolutionDaily:
		rows, err = s.db.Pool().Query(ctx, `
            SELECT DISTINCT ON (date_trunc('day', captured_at))
                   date_trunc('day', captured_at) AS at, stars, forks
            FROM repository_snapshots
            WHERE repository_id = $1 AND captured_at >= $2
            ORDER BY date_trunc('day', captured_at) ASC, captured_at DESC
            LIMIT 400`, repositoryID, from.UTC())
	default: // hourly
		rows, err = s.db.Pool().Query(ctx, `
            SELECT DISTINCT ON (date_trunc('hour', captured_at))
                   date_trunc('hour', captured_at) AS at, stars, forks
            FROM repository_snapshots
            WHERE repository_id = $1 AND captured_at >= $2
            ORDER BY date_trunc('hour', captured_at) ASC, captured_at DESC
            LIMIT 2200`, repositoryID, from.UTC())
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []HistoryPoint
	for rows.Next() {
		var p HistoryPoint
		if err := rows.Scan(&p.At, &p.Stars, &p.Forks); err != nil {
			return nil, err
		}
		p.At = p.At.UTC()
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Deltas are relative to the previous returned point, so they stay
	// consistent with whatever resolution the caller asked for.
	for i := 1; i < len(points); i++ {
		points[i].StarDelta = points[i].Stars - points[i-1].Stars
		points[i].ForkDelta = points[i].Forks - points[i-1].Forks
	}
	return points, nil
}

// Count returns the total number of stored snapshots.
func (s *Snapshots) Count(ctx context.Context) (int64, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	var n int64
	err := s.db.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM repository_snapshots`).Scan(&n)
	return n, err
}

// EarliestCapture returns the oldest snapshot time for a repository, which the
// UI uses to state honestly when RepoMeteor's own history begins.
func (s *Snapshots) EarliestCapture(ctx context.Context, repositoryID int64) (time.Time, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	var t *time.Time
	err := s.db.Pool().QueryRow(ctx,
		`SELECT MIN(captured_at) FROM repository_snapshots WHERE repository_id = $1`, repositoryID).Scan(&t)
	if err != nil {
		return time.Time{}, err
	}
	if t == nil {
		return time.Time{}, database.ErrNotFound
	}
	return t.UTC(), nil
}

func collectSnapshots(rows pgx.Rows) ([]Snapshot, error) {
	var out []Snapshot
	for rows.Next() {
		s, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
