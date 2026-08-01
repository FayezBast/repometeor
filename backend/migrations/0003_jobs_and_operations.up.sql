-- 0003_jobs_and_operations: the worker's durable queue and operational records.

CREATE TABLE crawl_jobs (
    id            BIGSERIAL PRIMARY KEY,
    kind          TEXT        NOT NULL,
    repository_id BIGINT      REFERENCES repositories(id) ON DELETE CASCADE,
    payload       JSONB       NOT NULL DEFAULT '{}',

    status        TEXT        NOT NULL DEFAULT 'queued',
    priority      INTEGER     NOT NULL DEFAULT 0,
    run_after     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    attempts      INTEGER     NOT NULL DEFAULT 0,
    max_attempts  INTEGER     NOT NULL DEFAULT 5,

    locked_at     TIMESTAMPTZ,
    locked_by     TEXT,
    last_error    TEXT,
    dedupe_key    TEXT,

    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at  TIMESTAMPTZ,

    CONSTRAINT crawl_jobs_kind_valid CHECK (
        kind IN ('discover_repositories', 'collect_repository', 'score_repositories', 'refresh_tiers')
    ),
    CONSTRAINT crawl_jobs_status_valid CHECK (
        status IN ('queued', 'running', 'completed', 'failed', 'dead')
    ),
    CONSTRAINT crawl_jobs_attempts_nonnegative CHECK (attempts >= 0),
    CONSTRAINT crawl_jobs_max_attempts_positive CHECK (max_attempts > 0),
    -- A running job must always record who holds it, so stale-lock recovery can
    -- reason about ownership.
    CONSTRAINT crawl_jobs_running_is_locked CHECK (
        status <> 'running' OR (locked_at IS NOT NULL AND locked_by IS NOT NULL)
    )
);

-- Claiming order: highest priority first, then oldest eligible job.
CREATE INDEX crawl_jobs_claim_idx
    ON crawl_jobs (priority DESC, run_after ASC, id ASC)
    WHERE status = 'queued';

-- Stale-lock recovery scans only running jobs.
CREATE INDEX crawl_jobs_running_locked_at_idx
    ON crawl_jobs (locked_at)
    WHERE status = 'running';

CREATE INDEX crawl_jobs_repository_id_idx ON crawl_jobs (repository_id) WHERE repository_id IS NOT NULL;
CREATE INDEX crawl_jobs_status_idx        ON crawl_jobs (status);

-- Deduplication applies only to jobs that have not finished. Once a job is
-- completed, failed or dead the same dedupe_key may be enqueued again, which is
-- exactly what a recurring schedule needs.
CREATE UNIQUE INDEX crawl_jobs_active_dedupe_key
    ON crawl_jobs (dedupe_key)
    WHERE dedupe_key IS NOT NULL AND status IN ('queued', 'running');

COMMENT ON TABLE  crawl_jobs IS 'Durable work queue. Claimed with FOR UPDATE SKIP LOCKED; retried with exponential backoff; exhausted jobs move to status dead.';
COMMENT ON COLUMN crawl_jobs.dedupe_key IS 'Optional key that prevents a duplicate queued/running job. Enforced by a partial unique index.';
COMMENT ON COLUMN crawl_jobs.locked_by IS 'Worker instance identifier holding the claim.';

-- Audit trail for repository discovery.
CREATE TABLE discovery_runs (
    id                  BIGSERIAL PRIMARY KEY,
    started_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at         TIMESTAMPTZ,
    strategy            TEXT        NOT NULL,
    query               TEXT        NOT NULL,
    pages_visited       INTEGER     NOT NULL DEFAULT 0,
    candidates_seen     INTEGER     NOT NULL DEFAULT 0,
    candidates_accepted INTEGER     NOT NULL DEFAULT 0,
    rate_limit_remaining INTEGER,
    rate_limit_resource TEXT,
    succeeded           BOOLEAN     NOT NULL DEFAULT FALSE,
    error_summary       TEXT,

    CONSTRAINT discovery_runs_counts_nonnegative CHECK (
        pages_visited >= 0 AND candidates_seen >= 0 AND candidates_accepted >= 0
    )
);

CREATE INDEX discovery_runs_started_at_idx ON discovery_runs (started_at DESC);

COMMENT ON TABLE discovery_runs IS 'One row per discovery attempt. Records the search query used; RepoMeteor never scrapes HTML.';

-- Conditional-request validators keyed by logical request. Never stores
-- authorization headers or response bodies.
CREATE TABLE github_request_cache (
    request_key         TEXT        PRIMARY KEY,
    etag                TEXT,
    last_modified       TEXT,
    last_status         INTEGER,
    last_checked_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    rate_limit_resource TEXT,
    response_hash       TEXT,

    CONSTRAINT github_request_cache_status_valid CHECK (last_status IS NULL OR (last_status >= 100 AND last_status < 600))
);

CREATE INDEX github_request_cache_last_checked_idx ON github_request_cache (last_checked_at);

COMMENT ON TABLE github_request_cache IS 'ETag/Last-Modified validators for conditional GitHub requests. Contains no credentials and no response bodies.';

-- Per-client counters for public write endpoints. Keyed by a salted hash of the
-- client address, never the address itself.
CREATE TABLE rate_limit_counters (
    bucket_key   TEXT        NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    hits         INTEGER     NOT NULL DEFAULT 0,

    PRIMARY KEY (bucket_key, window_start),
    CONSTRAINT rate_limit_counters_hits_nonnegative CHECK (hits >= 0)
);

CREATE INDEX rate_limit_counters_window_start_idx ON rate_limit_counters (window_start);

COMMENT ON TABLE rate_limit_counters IS 'Durable rate-limit counters keyed by a salted hash of the client address. Rows older than the window are deleted; raw IP addresses are never stored.';
