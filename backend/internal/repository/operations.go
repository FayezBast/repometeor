package repository

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/repometeor/repometeor/backend/internal/database"
)

// Cache stores conditional-request validators for GitHub resources.
type Cache struct {
	db *database.DB
}

// NewCache constructs the store.
func NewCache(db *database.DB) *Cache { return &Cache{db: db} }

// Get returns the stored validators for a request key.
func (c *Cache) Get(ctx context.Context, key string) (CacheEntry, error) {
	ctx, cancel := c.db.Context(ctx)
	defer cancel()

	var e CacheEntry
	err := c.db.Pool().QueryRow(ctx, `
        SELECT request_key, COALESCE(etag, ''), COALESCE(last_modified, ''),
               COALESCE(last_status, 0), last_checked_at,
               COALESCE(rate_limit_resource, ''), COALESCE(response_hash, '')
        FROM github_request_cache
        WHERE request_key = $1`, key).Scan(
		&e.RequestKey, &e.ETag, &e.LastModified,
		&e.LastStatus, &e.LastCheckedAt, &e.RateLimitResource, &e.ResponseHash)
	if err != nil {
		return CacheEntry{}, database.NormaliseNotFound(err)
	}
	return e, nil
}

// Put stores validators. It never receives or persists an authorization header.
func (c *Cache) Put(ctx context.Context, e CacheEntry) error {
	ctx, cancel := c.db.Context(ctx)
	defer cancel()

	_, err := c.db.Pool().Exec(ctx, `
        INSERT INTO github_request_cache (
            request_key, etag, last_modified, last_status, last_checked_at, rate_limit_resource, response_hash
        ) VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), $4, $5, NULLIF($6, ''), NULLIF($7, ''))
        ON CONFLICT (request_key) DO UPDATE SET
            etag                = COALESCE(EXCLUDED.etag, github_request_cache.etag),
            last_modified       = COALESCE(EXCLUDED.last_modified, github_request_cache.last_modified),
            last_status         = EXCLUDED.last_status,
            last_checked_at     = EXCLUDED.last_checked_at,
            rate_limit_resource = EXCLUDED.rate_limit_resource,
            response_hash       = COALESCE(EXCLUDED.response_hash, github_request_cache.response_hash)`,
		e.RequestKey, e.ETag, e.LastModified, e.LastStatus, e.LastCheckedAt.UTC(), e.RateLimitResource, e.ResponseHash)
	return err
}

// Prune removes entries not revalidated since the cutoff.
func (c *Cache) Prune(ctx context.Context, before time.Time) (int, error) {
	ctx, cancel := c.db.Context(ctx)
	defer cancel()

	tag, err := c.db.Pool().Exec(ctx, `DELETE FROM github_request_cache WHERE last_checked_at < $1`, before.UTC())
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// Discovery records discovery runs.
type Discovery struct {
	db *database.DB
}

// NewDiscovery constructs the store.
func NewDiscovery(db *database.DB) *Discovery { return &Discovery{db: db} }

// Start opens a discovery run record and returns its ID.
func (d *Discovery) Start(ctx context.Context, strategy, query string, startedAt time.Time) (int64, error) {
	ctx, cancel := d.db.Context(ctx)
	defer cancel()

	var id int64
	err := d.db.Pool().QueryRow(ctx, `
        INSERT INTO discovery_runs (started_at, strategy, query)
        VALUES ($1, $2, $3)
        RETURNING id`, startedAt.UTC(), strategy, truncateForStorage(query, 1000)).Scan(&id)
	return id, err
}

// Finish closes a discovery run record.
func (d *Discovery) Finish(ctx context.Context, run DiscoveryRun) error {
	ctx, cancel := d.db.Context(ctx)
	defer cancel()

	_, err := d.db.Pool().Exec(ctx, `
        UPDATE discovery_runs SET
            finished_at          = $2,
            pages_visited        = $3,
            candidates_seen      = $4,
            candidates_accepted  = $5,
            rate_limit_remaining = $6,
            rate_limit_resource  = NULLIF($7, ''),
            succeeded            = $8,
            error_summary        = NULLIF($9, '')
        WHERE id = $1`,
		run.ID, time.Now().UTC(), run.PagesVisited, run.CandidatesSeen, run.CandidatesAccepted,
		run.RateLimitRemaining, run.RateLimitResource, run.Succeeded, truncateForStorage(run.ErrorSummary, 1000))
	return err
}

// Recent returns the most recent discovery runs, newest first.
func (d *Discovery) Recent(ctx context.Context, limit int) ([]DiscoveryRun, error) {
	ctx, cancel := d.db.Context(ctx)
	defer cancel()

	rows, err := d.db.Pool().Query(ctx, `
        SELECT id, started_at, finished_at, strategy, query,
               pages_visited, candidates_seen, candidates_accepted,
               rate_limit_remaining, COALESCE(rate_limit_resource, ''),
               succeeded, COALESCE(error_summary, '')
        FROM discovery_runs
        ORDER BY started_at DESC
        LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DiscoveryRun
	for rows.Next() {
		var r DiscoveryRun
		if err := rows.Scan(
			&r.ID, &r.StartedAt, &r.FinishedAt, &r.Strategy, &r.Query,
			&r.PagesVisited, &r.CandidatesSeen, &r.CandidatesAccepted,
			&r.RateLimitRemaining, &r.RateLimitResource,
			&r.Succeeded, &r.ErrorSummary,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RateLimits implements a durable fixed-window counter for public write
// endpoints.
//
// Clients are identified by an HMAC of their address under a per-process key,
// never by the address itself. That keeps the limiter effective while ensuring
// the database holds no IP addresses — see docs/PRIVACY.md.
type RateLimits struct {
	db  *database.DB
	key []byte
}

// NewRateLimits constructs the limiter. key should be random per deployment;
// rotating it simply resets the current windows.
func NewRateLimits(db *database.DB, key []byte) *RateLimits {
	return &RateLimits{db: db, key: key}
}

// BucketKey returns the salted, hashed identifier stored for a client.
func (r *RateLimits) BucketKey(scope, client string) string {
	mac := hmac.New(sha256.New, r.key)
	mac.Write([]byte(scope))
	mac.Write([]byte{0})
	mac.Write([]byte(client))
	return hex.EncodeToString(mac.Sum(nil))
}

// Allow increments the counter for the window containing `now` and reports
// whether the request is within the limit, along with the remaining allowance.
//
// The INSERT ... ON CONFLICT DO UPDATE ... RETURNING is a single atomic
// statement, so concurrent requests from one client cannot both read the same
// pre-increment value.
func (r *RateLimits) Allow(ctx context.Context, scope, client string, limit int, window time.Duration, now time.Time) (allowed bool, remaining int, resetAt time.Time, err error) {
	if limit <= 0 {
		return false, 0, now.Add(window), nil
	}

	windowStart := now.UTC().Truncate(window)
	resetAt = windowStart.Add(window)

	ctx, cancel := r.db.Context(ctx)
	defer cancel()

	var hits int
	err = r.db.Pool().QueryRow(ctx, `
        INSERT INTO rate_limit_counters (bucket_key, window_start, hits)
        VALUES ($1, $2, 1)
        ON CONFLICT (bucket_key, window_start) DO UPDATE SET hits = rate_limit_counters.hits + 1
        RETURNING hits`, r.BucketKey(scope, client), windowStart).Scan(&hits)
	if err != nil {
		return false, 0, resetAt, err
	}

	remaining = limit - hits
	if remaining < 0 {
		remaining = 0
	}
	return hits <= limit, remaining, resetAt, nil
}

// Prune deletes counters for elapsed windows.
func (r *RateLimits) Prune(ctx context.Context, before time.Time) (int, error) {
	ctx, cancel := r.db.Context(ctx)
	defer cancel()

	tag, err := r.db.Pool().Exec(ctx, `DELETE FROM rate_limit_counters WHERE window_start < $1`, before.UTC())
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
