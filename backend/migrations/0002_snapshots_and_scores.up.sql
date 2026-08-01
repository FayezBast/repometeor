-- 0002_snapshots_and_scores: RepoMeteor's own time series and derived rankings.

-- One aggregate observation per repository per time bucket. The bucket is
-- computed in UTC by application code (clock.Bucket) so that the width stays
-- configurable and identical across the API and worker; the unique constraint
-- below is what makes collection idempotent.
CREATE TABLE repository_snapshots (
    id                BIGSERIAL PRIMARY KEY,
    repository_id     BIGINT       NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,

    captured_at       TIMESTAMPTZ  NOT NULL,
    bucket_start      TIMESTAMPTZ  NOT NULL,

    stars             INTEGER      NOT NULL,
    forks             INTEGER      NOT NULL,
    open_issues       INTEGER      NOT NULL,
    subscribers       INTEGER,

    latest_release_at TIMESTAMPTZ,

    source            TEXT         NOT NULL DEFAULT 'github_api',
    raw_metadata      JSONB        NOT NULL DEFAULT '{}',

    CONSTRAINT repository_snapshots_stars_nonnegative       CHECK (stars >= 0),
    CONSTRAINT repository_snapshots_forks_nonnegative       CHECK (forks >= 0),
    CONSTRAINT repository_snapshots_open_issues_nonnegative CHECK (open_issues >= 0),
    CONSTRAINT repository_snapshots_subscribers_nonnegative CHECK (subscribers IS NULL OR subscribers >= 0),
    CONSTRAINT repository_snapshots_source_valid            CHECK (source IN ('github_api', 'demo_seed', 'backfill')),
    CONSTRAINT repository_snapshots_bucket_key              UNIQUE (repository_id, bucket_start)
);

CREATE INDEX repository_snapshots_repo_captured_idx
    ON repository_snapshots (repository_id, captured_at DESC);

CREATE INDEX repository_snapshots_bucket_start_idx
    ON repository_snapshots (bucket_start DESC);

CREATE INDEX repository_snapshots_stars_idx
    ON repository_snapshots (stars DESC);

COMMENT ON TABLE  repository_snapshots IS 'Aggregate star/fork/issue counts over time. Contains no individual stargazer information.';
COMMENT ON COLUMN repository_snapshots.bucket_start IS 'UTC-truncated bucket used for idempotent upserts; computed by application code.';
COMMENT ON COLUMN repository_snapshots.source IS 'github_api = live collection; demo_seed = deterministic fictional data; backfill = administrative import.';

-- Derived momentum rankings, one row per repository per window per scoring run.
CREATE TABLE momentum_scores (
    id                BIGSERIAL PRIMARY KEY,
    repository_id     BIGINT        NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,

    window_name       TEXT          NOT NULL,
    scored_at         TIMESTAMPTZ   NOT NULL,

    score             NUMERIC(6,3)  NOT NULL,
    rank              INTEGER,

    stars_total       INTEGER       NOT NULL,
    star_delta        INTEGER       NOT NULL,
    star_velocity     NUMERIC       NOT NULL,
    relative_growth   NUMERIC       NOT NULL,
    acceleration      NUMERIC       NOT NULL,
    fork_delta        INTEGER       NOT NULL,
    fork_velocity     NUMERIC       NOT NULL,
    activity_score    NUMERIC       NOT NULL,
    freshness_score   NUMERIC       NOT NULL,
    confidence        NUMERIC       NOT NULL,

    collections       TEXT[]        NOT NULL DEFAULT '{}',
    reason_codes      TEXT[]        NOT NULL DEFAULT '{}',
    anomaly_flags     TEXT[]        NOT NULL DEFAULT '{}',
    feature_data      JSONB         NOT NULL DEFAULT '{}',
    formula_version   TEXT          NOT NULL DEFAULT 'momentum-v1',

    CONSTRAINT momentum_scores_window_valid     CHECK (window_name IN ('6h', '24h', '7d')),
    CONSTRAINT momentum_scores_score_range      CHECK (score >= 0 AND score <= 100),
    CONSTRAINT momentum_scores_confidence_range CHECK (confidence >= 0 AND confidence <= 1),
    CONSTRAINT momentum_scores_stars_nonnegative CHECK (stars_total >= 0),
    CONSTRAINT momentum_scores_rank_positive    CHECK (rank IS NULL OR rank > 0),
    CONSTRAINT momentum_scores_run_key          UNIQUE (repository_id, window_name, scored_at)
);

-- Serves the ranking query: newest run for a window, ordered by score.
CREATE INDEX momentum_scores_window_scored_score_idx
    ON momentum_scores (window_name, scored_at DESC, score DESC);

-- Serves the repository detail query: latest score per window for one repo.
CREATE INDEX momentum_scores_repo_window_scored_idx
    ON momentum_scores (repository_id, window_name, scored_at DESC);

CREATE INDEX momentum_scores_collections_gin_idx
    ON momentum_scores USING GIN (collections);

COMMENT ON TABLE  momentum_scores IS 'Transparent momentum rankings derived only from stored aggregate snapshots.';
COMMENT ON COLUMN momentum_scores.collections IS 'Deterministic collection memberships: rising, daily, weekly, hidden-gems, new-entrants, established.';
COMMENT ON COLUMN momentum_scores.formula_version IS 'Scoring formula identifier; bump when weights or features change.';

-- Bookkeeping for the most recent completed scoring run per window. The API
-- reads this instead of running MAX(scored_at) on the large scores table.
CREATE TABLE scoring_runs (
    id              BIGSERIAL PRIMARY KEY,
    window_name     TEXT        NOT NULL,
    scored_at       TIMESTAMPTZ NOT NULL,
    repositories    INTEGER     NOT NULL DEFAULT 0,
    eligible        INTEGER     NOT NULL DEFAULT 0,
    duration_ms     INTEGER     NOT NULL DEFAULT 0,
    formula_version TEXT        NOT NULL DEFAULT 'momentum-v1',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT scoring_runs_window_valid CHECK (window_name IN ('6h', '24h', '7d')),
    CONSTRAINT scoring_runs_key          UNIQUE (window_name, scored_at)
);

CREATE INDEX scoring_runs_window_scored_idx ON scoring_runs (window_name, scored_at DESC);

COMMENT ON TABLE scoring_runs IS 'One row per completed scoring run per window; identifies the current ranking generation.';
