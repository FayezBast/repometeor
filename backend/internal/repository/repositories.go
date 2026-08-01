package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FayezBast/repometeor/backend/internal/database"
	"github.com/FayezBast/repometeor/backend/internal/reponame"
)

// Repositories reads and writes the repositories table.
type Repositories struct {
	db *database.DB
}

// NewRepositories constructs the store.
func NewRepositories(db *database.DB) *Repositories { return &Repositories{db: db} }

// repositoryColumnsTemplate is the single source of truth for the repository
// SELECT list; "{}" is replaced by a qualifier. Keeping one template guarantees
// that scanRepository stays aligned with every query, including the RETURNING
// clause of the upsert (which has no table alias available).
//
// NULLable text columns are coalesced to ” so that Go can scan them into
// plain strings; genuinely optional timestamps stay as *time.Time.
const repositoryColumnsTemplate = `
    {}id, {}github_id, COALESCE({}node_id, ''),
    {}owner_login, {}name, {}full_name,
    COALESCE({}description, ''), {}html_url, COALESCE({}homepage_url, ''),
    COALESCE({}avatar_url, ''), COALESCE({}default_branch, ''),
    COALESCE({}primary_language, ''), {}topics, COALESCE({}license_spdx, ''),
    {}is_fork, {}is_archived, {}is_disabled, {}is_template,
    {}created_at, {}github_updated_at, {}pushed_at,
    {}latest_release_at, COALESCE({}latest_release_tag, ''),
    {}first_seen_at, {}last_checked_at, {}next_check_at,
    {}tracking_tier, {}tracking_state, {}consecutive_failures,
    COALESCE({}etag, ''), COALESCE({}last_modified, ''),
    {}created_record_at, {}updated_record_at`

func repositoryColumnsQualified(qualifier string) string {
	return strings.ReplaceAll(repositoryColumnsTemplate, "{}", qualifier)
}

var (
	// repositoryColumns qualifies every column with the "r" table alias.
	repositoryColumns = repositoryColumnsQualified("r.")
	// repositoryColumnsReturning qualifies with the table name, for RETURNING.
	repositoryColumnsReturning = repositoryColumnsQualified("repositories.")
)

func scanRepository(row pgx.Row) (Repository, error) {
	var r Repository
	err := row.Scan(
		&r.ID, &r.GitHubID, &r.NodeID,
		&r.OwnerLogin, &r.Name, &r.FullName,
		&r.Description, &r.HTMLURL, &r.HomepageURL,
		&r.AvatarURL, &r.DefaultBranch,
		&r.PrimaryLanguage, &r.Topics, &r.LicenseSPDX,
		&r.IsFork, &r.IsArchived, &r.IsDisabled, &r.IsTemplate,
		&r.CreatedAt, &r.GitHubUpdatedAt, &r.PushedAt,
		&r.LatestReleaseAt, &r.LatestReleaseTag,
		&r.FirstSeenAt, &r.LastCheckedAt, &r.NextCheckAt,
		&r.TrackingTier, &r.TrackingState, &r.ConsecutiveFailures,
		&r.ETag, &r.LastModified,
		&r.CreatedRecordAt, &r.UpdatedRecordAt,
	)
	if err != nil {
		return Repository{}, database.NormaliseNotFound(err)
	}
	normaliseRepositoryTimes(&r)
	return r, nil
}

// normaliseRepositoryTimes converts every timestamp to UTC.
//
// pgx returns timestamptz values in the connection's location, which is not
// necessarily UTC on a developer machine. RepoMeteor's contract is that every
// timestamp it emits is UTC RFC 3339, so normalisation happens once here rather
// than being repeated at each serialisation site.
func normaliseRepositoryTimes(r *Repository) {
	r.CreatedAt = r.CreatedAt.UTC()
	r.FirstSeenAt = r.FirstSeenAt.UTC()
	r.CreatedRecordAt = r.CreatedRecordAt.UTC()
	r.UpdatedRecordAt = r.UpdatedRecordAt.UTC()
	r.GitHubUpdatedAt = utcPtr(r.GitHubUpdatedAt)
	r.PushedAt = utcPtr(r.PushedAt)
	r.LatestReleaseAt = utcPtr(r.LatestReleaseAt)
	r.LastCheckedAt = utcPtr(r.LastCheckedAt)
	r.NextCheckAt = utcPtr(r.NextCheckAt)
}

// GetByName resolves a repository case-insensitively by owner and name.
func (s *Repositories) GetByName(ctx context.Context, name reponame.Name) (Repository, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	row := s.db.Pool().QueryRow(ctx, `
        SELECT `+repositoryColumns+`
        FROM repositories r
        WHERE lower(r.owner_login) = lower($1) AND lower(r.name) = lower($2)`,
		name.Owner, name.Repository)
	return scanRepository(row)
}

// GetByID resolves a repository by its internal identifier.
func (s *Repositories) GetByID(ctx context.Context, id int64) (Repository, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	row := s.db.Pool().QueryRow(ctx, `SELECT `+repositoryColumns+` FROM repositories r WHERE r.id = $1`, id)
	return scanRepository(row)
}

// GetByGitHubID resolves a repository by GitHub's stable numeric identifier,
// which survives renames.
func (s *Repositories) GetByGitHubID(ctx context.Context, githubID int64) (Repository, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	row := s.db.Pool().QueryRow(ctx, `SELECT `+repositoryColumns+` FROM repositories r WHERE r.github_id = $1`, githubID)
	return scanRepository(row)
}

// ResolveAlias returns the repository a former owner/name pair now points at.
func (s *Repositories) ResolveAlias(ctx context.Context, name reponame.Name) (Repository, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	row := s.db.Pool().QueryRow(ctx, `
        SELECT `+repositoryColumns+`
        FROM repository_aliases a
        JOIN repositories r ON r.id = a.repository_id
        WHERE lower(a.owner_login) = lower($1) AND lower(a.name) = lower($2)`,
		name.Owner, name.Repository)
	return scanRepository(row)
}

// Upsert inserts or updates a repository from collected GitHub metadata.
//
// Only GitHub-sourced columns are written: tracking tier, state, next_check_at
// and the conditional-request validators are owned by the collector and are
// updated through their own methods. When the owner or name has changed, the
// previous pair is recorded as an alias inside the same transaction so old
// RepoMeteor URLs keep resolving.
func (s *Repositories) Upsert(ctx context.Context, in UpsertRepositoryInput) (Repository, error) {
	if in.GitHubID <= 0 {
		return Repository{}, fmt.Errorf("invalid github id %d", in.GitHubID)
	}
	if in.Topics == nil {
		in.Topics = []string{}
	}

	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	var out Repository
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		var (
			existingID    int64
			existingOwner string
			existingName  string
		)
		err := tx.QueryRow(ctx,
			`SELECT id, owner_login, name FROM repositories WHERE github_id = $1 FOR UPDATE`,
			in.GitHubID,
		).Scan(&existingID, &existingOwner, &existingName)
		switch {
		case err == nil:
			// Repository is known. Record a rename if one happened.
			if !strings.EqualFold(existingOwner, in.OwnerLogin) || !strings.EqualFold(existingName, in.Name) {
				if _, err := tx.Exec(ctx, `
                    INSERT INTO repository_aliases (repository_id, owner_login, name)
                    VALUES ($1, $2, $3)
                    ON CONFLICT (lower(owner_login), lower(name)) DO NOTHING`,
					existingID, existingOwner, existingName,
				); err != nil {
					return fmt.Errorf("record rename alias: %w", err)
				}
			}
		case errors.Is(err, pgx.ErrNoRows):
			// New repository; nothing to do before the upsert.
		default:
			return fmt.Errorf("lock repository: %w", err)
		}

		row := tx.QueryRow(ctx, `
            INSERT INTO repositories (
                github_id, node_id, owner_login, name, full_name,
                description, html_url, homepage_url, avatar_url, default_branch,
                primary_language, topics, license_spdx,
                is_fork, is_archived, is_disabled, is_template,
                created_at, github_updated_at, pushed_at,
                latest_release_at, latest_release_tag
            ) VALUES (
                $1, NULLIF($2, ''), $3, $4, $5,
                NULLIF($6, ''), $7, NULLIF($8, ''), NULLIF($9, ''), NULLIF($10, ''),
                NULLIF($11, ''), $12, NULLIF($13, ''),
                $14, $15, $16, $17,
                $18, $19, $20,
                $21, NULLIF($22, '')
            )
            ON CONFLICT (github_id) DO UPDATE SET
                node_id            = EXCLUDED.node_id,
                owner_login        = EXCLUDED.owner_login,
                name               = EXCLUDED.name,
                full_name          = EXCLUDED.full_name,
                description        = EXCLUDED.description,
                html_url           = EXCLUDED.html_url,
                homepage_url       = EXCLUDED.homepage_url,
                avatar_url         = EXCLUDED.avatar_url,
                default_branch     = EXCLUDED.default_branch,
                primary_language   = EXCLUDED.primary_language,
                topics             = EXCLUDED.topics,
                license_spdx       = EXCLUDED.license_spdx,
                is_fork            = EXCLUDED.is_fork,
                is_archived        = EXCLUDED.is_archived,
                is_disabled        = EXCLUDED.is_disabled,
                is_template        = EXCLUDED.is_template,
                created_at         = EXCLUDED.created_at,
                github_updated_at  = EXCLUDED.github_updated_at,
                pushed_at          = EXCLUDED.pushed_at,
                latest_release_at  = COALESCE(EXCLUDED.latest_release_at, repositories.latest_release_at),
                latest_release_tag = COALESCE(EXCLUDED.latest_release_tag, repositories.latest_release_tag),
                updated_record_at  = NOW()
            RETURNING `+repositoryColumnsReturning,
			in.GitHubID, in.NodeID, in.OwnerLogin, in.Name, in.FullName,
			in.Description, in.HTMLURL, in.HomepageURL, in.AvatarURL, in.DefaultBranch,
			in.PrimaryLanguage, in.Topics, in.LicenseSPDX,
			in.IsFork, in.IsArchived, in.IsDisabled, in.IsTemplate,
			in.CreatedAt.UTC(), utcPtr(in.GitHubUpdatedAt), utcPtr(in.PushedAt),
			utcPtr(in.LatestReleaseAt), in.LatestReleaseTag,
		)

		out, err = scanRepository(row)
		return err
	})
	return out, err
}

// MarkChecked records a successful poll and schedules the next one.
func (s *Repositories) MarkChecked(ctx context.Context, id int64, checkedAt, nextCheckAt time.Time, etag, lastModified string) error {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	_, err := s.db.Pool().Exec(ctx, `
        UPDATE repositories SET
            last_checked_at      = $2,
            next_check_at        = $3,
            etag                 = NULLIF($4, ''),
            last_modified        = NULLIF($5, ''),
            consecutive_failures = 0,
            updated_record_at    = NOW()
        WHERE id = $1`,
		id, checkedAt.UTC(), nextCheckAt.UTC(), etag, lastModified)
	return err
}

// MarkFailure increments the failure counter and backs off the next check.
// It returns the new failure count so the caller can decide when to give up.
func (s *Repositories) MarkFailure(ctx context.Context, id int64, nextCheckAt time.Time, reason string) (int, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	var failures int
	err := s.db.Pool().QueryRow(ctx, `
        UPDATE repositories SET
            consecutive_failures = consecutive_failures + 1,
            next_check_at        = $2,
            last_checked_at      = NOW(),
            metadata             = jsonb_set(metadata, '{lastFailure}', to_jsonb($3::text), true),
            updated_record_at    = NOW()
        WHERE id = $1
        RETURNING consecutive_failures`,
		id, nextCheckAt.UTC(), truncateForStorage(reason, 500)).Scan(&failures)
	return failures, err
}

// SetFirstSeenAt overrides when RepoMeteor started tracking a repository.
//
// It exists for the demo seeder and for administrative backfills, where history
// is written for a period before the row was created. Live collection never
// calls it: for a real repository, first_seen_at is exactly when the row was
// inserted, and moving it would misrepresent how much history actually exists.
func (s *Repositories) SetFirstSeenAt(ctx context.Context, id int64, firstSeenAt time.Time) error {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	_, err := s.db.Pool().Exec(ctx, `
        UPDATE repositories
        SET first_seen_at = $2, updated_record_at = NOW()
        WHERE id = $1`, id, firstSeenAt.UTC())
	return err
}

// SetTrackingState transitions a repository's lifecycle state.
func (s *Repositories) SetTrackingState(ctx context.Context, id int64, state TrackingState) error {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	_, err := s.db.Pool().Exec(ctx, `
        UPDATE repositories
        SET tracking_state = $2, updated_record_at = NOW()
        WHERE id = $1`, id, string(state))
	return err
}

// SetTrackingTier moves a repository between polling tiers and reschedules it.
func (s *Repositories) SetTrackingTier(ctx context.Context, id int64, tier TrackingTier, nextCheckAt time.Time) error {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	_, err := s.db.Pool().Exec(ctx, `
        UPDATE repositories
        SET tracking_tier = $2, next_check_at = $3, updated_record_at = NOW()
        WHERE id = $1`, id, string(tier), nextCheckAt.UTC())
	return err
}

// DueForCollection returns active repositories whose next check has arrived,
// most urgent tier first.
func (s *Repositories) DueForCollection(ctx context.Context, now time.Time, limit int) ([]Repository, error) {
	if limit <= 0 {
		return nil, nil
	}
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	rows, err := s.db.Pool().Query(ctx, `
        SELECT `+repositoryColumns+`
        FROM repositories r
        WHERE r.tracking_state = 'active'
          AND (r.next_check_at IS NULL OR r.next_check_at <= $1)
        ORDER BY
            CASE r.tracking_tier WHEN 'hot' THEN 0 WHEN 'warm' THEN 1 ELSE 2 END,
            r.next_check_at ASC NULLS FIRST,
            r.id ASC
        LIMIT $2`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectRepositories(rows)
}

// ListActive returns every repository eligible for scoring consideration.
func (s *Repositories) ListActive(ctx context.Context, limit int) ([]Repository, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	rows, err := s.db.Pool().Query(ctx, `
        SELECT `+repositoryColumns+`
        FROM repositories r
        WHERE r.tracking_state = 'active'
        ORDER BY r.id
        LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectRepositories(rows)
}

// Search finds tracked repositories by a prefix or substring of their full
// name. The pattern is bound as a parameter and its wildcards are escaped, so
// a caller cannot smuggle LIKE metacharacters into the query.
func (s *Repositories) Search(ctx context.Context, query string, limit int) ([]Repository, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	pattern := "%" + escapeLike(strings.ToLower(strings.TrimSpace(query))) + "%"

	rows, err := s.db.Pool().Query(ctx, `
        SELECT `+repositoryColumns+`
        FROM repositories r
        WHERE lower(r.full_name) LIKE $1 ESCAPE '\'
          AND r.tracking_state <> 'excluded'
        ORDER BY
            -- exact match first, then prefix match, then the rest
            CASE WHEN lower(r.full_name) = $2 THEN 0
                 WHEN lower(r.full_name) LIKE $3 ESCAPE '\' THEN 1
                 ELSE 2 END,
            r.full_name
        LIMIT $4`,
		pattern,
		strings.ToLower(strings.TrimSpace(query)),
		escapeLike(strings.ToLower(strings.TrimSpace(query)))+"%",
		limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectRepositories(rows)
}

// Languages returns filter facets for active, non-excluded repositories.
func (s *Repositories) Languages(ctx context.Context) ([]LanguageCount, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	rows, err := s.db.Pool().Query(ctx, `
        SELECT primary_language, COUNT(*)::int
        FROM repositories
        WHERE primary_language IS NOT NULL
          AND tracking_state = 'active'
          AND NOT is_archived AND NOT is_fork AND NOT is_disabled
        GROUP BY primary_language
        ORDER BY COUNT(*) DESC, primary_language ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LanguageCount
	for rows.Next() {
		var lc LanguageCount
		if err := rows.Scan(&lc.Language, &lc.Count); err != nil {
			return nil, err
		}
		out = append(out, lc)
	}
	return out, rows.Err()
}

// Topics returns topic facets, most common first.
func (s *Repositories) Topics(ctx context.Context, limit int) ([]TopicCount, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	rows, err := s.db.Pool().Query(ctx, `
        SELECT topic, COUNT(*)::int
        FROM repositories, unnest(topics) AS topic
        WHERE tracking_state = 'active'
          AND NOT is_archived AND NOT is_fork AND NOT is_disabled
        GROUP BY topic
        ORDER BY COUNT(*) DESC, topic ASC
        LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TopicCount
	for rows.Next() {
		var tc TopicCount
		if err := rows.Scan(&tc.Topic, &tc.Count); err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

// Count returns the number of repositories in a tracking state.
func (s *Repositories) Count(ctx context.Context, state TrackingState) (int, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	var n int
	err := s.db.Pool().QueryRow(ctx, `SELECT COUNT(*)::int FROM repositories WHERE tracking_state = $1`, string(state)).Scan(&n)
	return n, err
}

func collectRepositories(rows pgx.Rows) ([]Repository, error) {
	var out []Repository
	for rows.Next() {
		r, err := scanRepository(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// escapeLike neutralises LIKE metacharacters in user input. The result is still
// bound as a parameter; this only stops a user turning a search into a
// full-table wildcard scan.
func escapeLike(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(s)
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func truncateForStorage(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
