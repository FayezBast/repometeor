package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/repometeor/repometeor/backend/internal/database"
)

// Scores reads and writes momentum rankings.
type Scores struct {
	db *database.DB
}

// NewScores constructs the store.
func NewScores(db *database.DB) *Scores { return &Scores{db: db} }

const scoreColumns = `
    m.id, m.repository_id, m.window_name, m.scored_at,
    m.score, m.rank,
    m.stars_total, m.star_delta, m.star_velocity, m.relative_growth, m.acceleration,
    m.fork_delta, m.fork_velocity, m.activity_score, m.freshness_score, m.confidence,
    m.collections, m.reason_codes, m.anomaly_flags, m.feature_data, m.formula_version`

func scanScore(row pgx.Row) (MomentumScore, error) {
	var (
		s        MomentumScore
		features []byte
	)
	err := row.Scan(
		&s.ID, &s.RepositoryID, &s.Window, &s.ScoredAt,
		&s.Score, &s.Rank,
		&s.StarsTotal, &s.StarDelta, &s.StarVelocity, &s.RelativeGrowth, &s.Acceleration,
		&s.ForkDelta, &s.ForkVelocity, &s.ActivityScore, &s.FreshnessScore, &s.Confidence,
		&s.Collections, &s.ReasonCodes, &s.AnomalyFlags, &features, &s.FormulaVersion,
	)
	if err != nil {
		return MomentumScore{}, database.NormaliseNotFound(err)
	}
	if len(features) > 0 {
		if err := json.Unmarshal(features, &s.FeatureData); err != nil {
			return MomentumScore{}, fmt.Errorf("decode feature_data: %w", err)
		}
	}
	s.ScoredAt = s.ScoredAt.UTC()
	return s, nil
}

// SaveRun persists an entire scoring run for one window in a single
// transaction, then records the run so that readers can identify the current
// ranking generation atomically. Readers therefore never see a half-written
// leaderboard.
func (s *Scores) SaveRun(ctx context.Context, run ScoringRun, scores []MomentumScore) error {
	ctx, cancel := context.WithTimeout(ctx, 5*s.db.QueryTimeout())
	defer cancel()

	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		for _, sc := range scores {
			features, err := json.Marshal(sc.FeatureData)
			if err != nil {
				return fmt.Errorf("encode feature_data: %w", err)
			}
			if _, err := tx.Exec(ctx, `
                INSERT INTO momentum_scores (
                    repository_id, window_name, scored_at,
                    score, rank,
                    stars_total, star_delta, star_velocity, relative_growth, acceleration,
                    fork_delta, fork_velocity, activity_score, freshness_score, confidence,
                    collections, reason_codes, anomaly_flags, feature_data, formula_version
                ) VALUES (
                    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
                    $11, $12, $13, $14, $15, $16, $17, $18, $19, $20
                )
                ON CONFLICT (repository_id, window_name, scored_at) DO UPDATE SET
                    score           = EXCLUDED.score,
                    rank            = EXCLUDED.rank,
                    stars_total     = EXCLUDED.stars_total,
                    star_delta      = EXCLUDED.star_delta,
                    star_velocity   = EXCLUDED.star_velocity,
                    relative_growth = EXCLUDED.relative_growth,
                    acceleration    = EXCLUDED.acceleration,
                    fork_delta      = EXCLUDED.fork_delta,
                    fork_velocity   = EXCLUDED.fork_velocity,
                    activity_score  = EXCLUDED.activity_score,
                    freshness_score = EXCLUDED.freshness_score,
                    confidence      = EXCLUDED.confidence,
                    collections     = EXCLUDED.collections,
                    reason_codes    = EXCLUDED.reason_codes,
                    anomaly_flags   = EXCLUDED.anomaly_flags,
                    feature_data    = EXCLUDED.feature_data,
                    formula_version = EXCLUDED.formula_version`,
				sc.RepositoryID, sc.Window, run.ScoredAt.UTC(),
				sc.Score, sc.Rank,
				sc.StarsTotal, sc.StarDelta, sc.StarVelocity, sc.RelativeGrowth, sc.Acceleration,
				sc.ForkDelta, sc.ForkVelocity, sc.ActivityScore, sc.FreshnessScore, sc.Confidence,
				nonNilStrings(sc.Collections), nonNilStrings(sc.ReasonCodes), nonNilStrings(sc.AnomalyFlags),
				features, sc.FormulaVersion,
			); err != nil {
				return fmt.Errorf("insert score for repository %d: %w", sc.RepositoryID, err)
			}
		}

		if _, err := tx.Exec(ctx, `
            INSERT INTO scoring_runs (window_name, scored_at, repositories, eligible, duration_ms, formula_version)
            VALUES ($1, $2, $3, $4, $5, $6)
            ON CONFLICT (window_name, scored_at) DO UPDATE SET
                repositories = EXCLUDED.repositories,
                eligible     = EXCLUDED.eligible,
                duration_ms  = EXCLUDED.duration_ms`,
			run.Window, run.ScoredAt.UTC(), run.Repositories, run.Eligible, run.DurationMillis, run.FormulaVersion,
		); err != nil {
			return fmt.Errorf("record scoring run: %w", err)
		}
		return nil
	})
}

// LatestRun returns the most recent completed scoring run for a window.
func (s *Scores) LatestRun(ctx context.Context, window string) (ScoringRun, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	var run ScoringRun
	err := s.db.Pool().QueryRow(ctx, `
        SELECT id, window_name, scored_at, repositories, eligible, duration_ms, formula_version
        FROM scoring_runs
        WHERE window_name = $1
        ORDER BY scored_at DESC
        LIMIT 1`, window).Scan(
		&run.ID, &run.Window, &run.ScoredAt, &run.Repositories, &run.Eligible, &run.DurationMillis, &run.FormulaVersion)
	if err != nil {
		return ScoringRun{}, database.NormaliseNotFound(err)
	}
	run.ScoredAt = run.ScoredAt.UTC()
	return run, nil
}

// LatestForRepository returns the newest score for each window for one
// repository, keyed by window name.
func (s *Scores) LatestForRepository(ctx context.Context, repositoryID int64) (map[string]MomentumScore, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	rows, err := s.db.Pool().Query(ctx, `
        SELECT DISTINCT ON (m.window_name) `+scoreColumns+`
        FROM momentum_scores m
        WHERE m.repository_id = $1
        ORDER BY m.window_name, m.scored_at DESC`, repositoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]MomentumScore, 3)
	for rows.Next() {
		sc, err := scanScore(rows)
		if err != nil {
			return nil, err
		}
		out[sc.Window] = sc
	}
	return out, rows.Err()
}

// TrendingFilter describes a trending query. Every field is validated by the
// API layer before it reaches here; the SQL below binds all of them.
type TrendingFilter struct {
	Window        string
	Collection    string
	Language      string
	Topic         string
	MinStars      *int
	MaxStars      *int
	MinConfidence *float64
	MaxConfidence *float64
	Limit         int
	Cursor        *Cursor
}

// Cursor is the opaque pagination position: the score, rank and repository ID
// of the last row on the previous page. Using the full tuple makes the ordering
// total, so pages cannot repeat or skip rows when scores tie.
type Cursor struct {
	Score        float64
	Rank         int
	RepositoryID int64
}

// Trending returns one ranked page for the current generation of a window.
//
// The query pins scored_at to the latest completed run before filtering, so a
// scoring run that lands mid-pagination cannot mix two generations into one
// result set.
func (s *Scores) Trending(ctx context.Context, f TrendingFilter) ([]RankedRepository, ScoringRun, error) {
	run, err := s.LatestRun(ctx, f.Window)
	if err != nil {
		return nil, ScoringRun{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, 2*s.db.QueryTimeout())
	defer cancel()

	var (
		conditions = []string{"m.window_name = $1", "m.scored_at = $2"}
		args       = []any{f.Window, run.ScoredAt}
	)
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}

	if f.Collection != "" {
		add("$%d = ANY(m.collections)", f.Collection)
	}
	if f.Language != "" {
		add("lower(r.primary_language) = lower($%d)", f.Language)
	}
	if f.Topic != "" {
		add("$%d = ANY(r.topics)", strings.ToLower(f.Topic))
	}
	if f.MinStars != nil {
		add("m.stars_total >= $%d", *f.MinStars)
	}
	if f.MaxStars != nil {
		add("m.stars_total <= $%d", *f.MaxStars)
	}
	if f.MinConfidence != nil {
		add("m.confidence >= $%d", *f.MinConfidence)
	}
	if f.MaxConfidence != nil {
		add("m.confidence < $%d", *f.MaxConfidence)
	}
	if f.Cursor != nil {
		// Row-wise comparison over the exact ORDER BY tuple.
		args = append(args, f.Cursor.Score, f.Cursor.Rank, f.Cursor.RepositoryID)
		conditions = append(conditions, fmt.Sprintf(
			"(m.score, m.rank, m.repository_id) < ($%d::numeric, $%d::int, $%d::bigint)",
			len(args)-2, len(args)-1, len(args)))
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	args = append(args, limit)

	query := `
        SELECT ` + scoreColumns + `, ` + repositoryColumns + `
        FROM momentum_scores m
        JOIN repositories r ON r.id = m.repository_id
        WHERE ` + strings.Join(conditions, "\n          AND ") + `
        ORDER BY m.score DESC, m.rank ASC, m.repository_id DESC
        LIMIT $` + fmt.Sprint(len(args))

	rows, err := s.db.Pool().Query(ctx, query, args...)
	if err != nil {
		return nil, ScoringRun{}, err
	}
	defer rows.Close()

	var out []RankedRepository
	for rows.Next() {
		var (
			sc       MomentumScore
			features []byte
			r        Repository
		)
		if err := rows.Scan(
			&sc.ID, &sc.RepositoryID, &sc.Window, &sc.ScoredAt,
			&sc.Score, &sc.Rank,
			&sc.StarsTotal, &sc.StarDelta, &sc.StarVelocity, &sc.RelativeGrowth, &sc.Acceleration,
			&sc.ForkDelta, &sc.ForkVelocity, &sc.ActivityScore, &sc.FreshnessScore, &sc.Confidence,
			&sc.Collections, &sc.ReasonCodes, &sc.AnomalyFlags, &features, &sc.FormulaVersion,

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
		); err != nil {
			return nil, ScoringRun{}, err
		}
		if len(features) > 0 {
			if err := json.Unmarshal(features, &sc.FeatureData); err != nil {
				return nil, ScoringRun{}, fmt.Errorf("decode feature_data: %w", err)
			}
		}
		sc.ScoredAt = sc.ScoredAt.UTC()
		normaliseRepositoryTimes(&r)
		out = append(out, RankedRepository{Repository: r, Score: sc})
	}
	if err := rows.Err(); err != nil {
		return nil, ScoringRun{}, err
	}
	return out, run, nil
}

// ScoreHistory returns score points for one repository and window, for
// overlaying momentum on the history chart.
func (s *Scores) ScoreHistory(ctx context.Context, repositoryID int64, window string, from time.Time) (map[time.Time]float64, error) {
	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	rows, err := s.db.Pool().Query(ctx, `
        SELECT scored_at, score
        FROM momentum_scores
        WHERE repository_id = $1 AND window_name = $2 AND scored_at >= $3
        ORDER BY scored_at ASC
        LIMIT 1000`, repositoryID, window, from.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[time.Time]float64)
	for rows.Next() {
		var (
			at    time.Time
			score float64
		)
		if err := rows.Scan(&at, &score); err != nil {
			return nil, err
		}
		out[at.UTC()] = score
	}
	return out, rows.Err()
}

// TopRepositoryIDs returns the highest-scoring repository IDs for a window in
// the current generation. The tier scheduler uses it to promote repositories
// into the hot and warm tiers.
func (s *Scores) TopRepositoryIDs(ctx context.Context, window string, limit int) ([]int64, error) {
	run, err := s.LatestRun(ctx, window)
	if err != nil {
		return nil, err
	}

	ctx, cancel := s.db.Context(ctx)
	defer cancel()

	rows, err := s.db.Pool().Query(ctx, `
        SELECT repository_id
        FROM momentum_scores
        WHERE window_name = $1 AND scored_at = $2
        ORDER BY score DESC, repository_id ASC
        LIMIT $3`, window, run.ScoredAt, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// PruneOlderThan deletes score rows from superseded generations, keeping the
// most recent run for every window regardless of age.
func (s *Scores) PruneOlderThan(ctx context.Context, before time.Time) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*s.db.QueryTimeout())
	defer cancel()

	tag, err := s.db.Pool().Exec(ctx, `
        DELETE FROM momentum_scores m
        WHERE m.scored_at < $1
          AND m.scored_at <> (
              SELECT MAX(scored_at) FROM scoring_runs WHERE window_name = m.window_name
          )`, before.UTC())
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
