-- 0001_repositories: the canonical registry of tracked public repositories.
--
-- One row per GitHub repository RepoMeteor has ever seen. Rows are never
-- deleted by the application: a repository that disappears from GitHub moves to
-- tracking_state 'missing' so its historical snapshots stay meaningful.

CREATE TABLE repositories (
    id                BIGSERIAL PRIMARY KEY,

    -- GitHub identity. github_id is the stable key across renames.
    github_id         BIGINT       NOT NULL UNIQUE,
    node_id           TEXT,

    -- Canonical capitalisation exactly as GitHub reports it.
    owner_login       TEXT         NOT NULL,
    name              TEXT         NOT NULL,
    full_name         TEXT         NOT NULL,

    description       TEXT,
    html_url          TEXT         NOT NULL,
    homepage_url      TEXT,
    avatar_url        TEXT,
    default_branch    TEXT,

    primary_language  TEXT,
    topics            TEXT[]       NOT NULL DEFAULT '{}',
    license_spdx      TEXT,

    is_fork           BOOLEAN      NOT NULL DEFAULT FALSE,
    is_archived       BOOLEAN      NOT NULL DEFAULT FALSE,
    is_disabled       BOOLEAN      NOT NULL DEFAULT FALSE,
    is_template       BOOLEAN      NOT NULL DEFAULT FALSE,

    -- Timestamps reported by GitHub.
    created_at        TIMESTAMPTZ  NOT NULL,
    github_updated_at TIMESTAMPTZ,
    pushed_at         TIMESTAMPTZ,
    latest_release_at TIMESTAMPTZ,
    latest_release_tag TEXT,

    -- RepoMeteor tracking bookkeeping.
    first_seen_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_checked_at   TIMESTAMPTZ,
    next_check_at     TIMESTAMPTZ,
    tracking_tier     TEXT         NOT NULL DEFAULT 'cold',
    tracking_state    TEXT         NOT NULL DEFAULT 'active',
    consecutive_failures INTEGER   NOT NULL DEFAULT 0,

    -- Conditional-request validators for the repository resource.
    etag              TEXT,
    last_modified     TEXT,

    metadata          JSONB        NOT NULL DEFAULT '{}',

    created_record_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_record_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT repositories_github_id_positive   CHECK (github_id > 0),
    CONSTRAINT repositories_owner_login_nonempty CHECK (length(owner_login) > 0),
    CONSTRAINT repositories_name_nonempty        CHECK (length(name) > 0),
    CONSTRAINT repositories_tracking_tier_valid  CHECK (tracking_tier IN ('hot', 'warm', 'cold')),
    CONSTRAINT repositories_tracking_state_valid CHECK (tracking_state IN ('active', 'paused', 'excluded', 'missing')),
    CONSTRAINT repositories_failures_nonnegative CHECK (consecutive_failures >= 0)
);

-- GitHub treats owner/name case-insensitively; so does RepoMeteor. The stored
-- values keep GitHub's capitalisation, but only one row may exist per
-- case-folded pair.
CREATE UNIQUE INDEX repositories_owner_name_lower_key
    ON repositories (lower(owner_login), lower(name));

CREATE UNIQUE INDEX repositories_full_name_lower_key
    ON repositories (lower(full_name));

CREATE INDEX repositories_primary_language_idx
    ON repositories (primary_language)
    WHERE primary_language IS NOT NULL;

CREATE INDEX repositories_topics_gin_idx
    ON repositories USING GIN (topics);

-- The collector's work queue: "which active repository is due next?".
CREATE INDEX repositories_next_check_at_idx
    ON repositories (next_check_at)
    WHERE tracking_state = 'active';

CREATE INDEX repositories_tracking_tier_idx  ON repositories (tracking_tier);
CREATE INDEX repositories_pushed_at_idx      ON repositories (pushed_at DESC NULLS LAST);
CREATE INDEX repositories_first_seen_at_idx  ON repositories (first_seen_at DESC);
CREATE INDEX repositories_created_at_idx     ON repositories (created_at DESC);

COMMENT ON TABLE  repositories IS 'Public GitHub repositories tracked by RepoMeteor. Aggregate metadata only; no individual user data.';
COMMENT ON COLUMN repositories.github_id IS 'Stable GitHub numeric ID; survives repository renames.';
COMMENT ON COLUMN repositories.tracking_state IS 'active = polled; paused = temporarily skipped; excluded = never ranked; missing = 404 on GitHub, history retained.';
COMMENT ON COLUMN repositories.next_check_at IS 'Earliest UTC time the collector may poll this repository again.';

-- Aliases record former owner/name pairs so that RepoMeteor detail URLs keep
-- resolving after a repository is renamed on GitHub.
CREATE TABLE repository_aliases (
    id            BIGSERIAL PRIMARY KEY,
    repository_id BIGINT      NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    owner_login   TEXT        NOT NULL,
    name          TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT repository_aliases_owner_nonempty CHECK (length(owner_login) > 0),
    CONSTRAINT repository_aliases_name_nonempty  CHECK (length(name) > 0)
);

CREATE UNIQUE INDEX repository_aliases_lower_key
    ON repository_aliases (lower(owner_login), lower(name));

CREATE INDEX repository_aliases_repository_id_idx
    ON repository_aliases (repository_id);

COMMENT ON TABLE repository_aliases IS 'Historical owner/name pairs, used to redirect old RepoMeteor URLs after a GitHub rename.';
