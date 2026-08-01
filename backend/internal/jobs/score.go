package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/repometeor/repometeor/backend/internal/clock"
	"github.com/repometeor/repometeor/backend/internal/config"
	"github.com/repometeor/repometeor/backend/internal/repository"
	"github.com/repometeor/repometeor/backend/internal/scoring"
	"github.com/repometeor/repometeor/backend/internal/telemetry"
)

// maxScoredRepositories bounds one scoring run. Beyond this the run would take
// long enough to overlap the next schedule; the cap is deliberately far above
// any realistic single-instance dataset.
const maxScoredRepositories = 50_000

// Scorer recomputes momentum rankings for every window.
type Scorer struct {
	repositories *repository.Repositories
	snapshots    *repository.Snapshots
	scores       *repository.Scores
	tracking     config.TrackingConfig
	clock        clock.Clock
	log          *slog.Logger
	metrics      *telemetry.Metrics
}

// NewScorer constructs the scoring handler.
func NewScorer(
	repos *repository.Repositories,
	snaps *repository.Snapshots,
	scores *repository.Scores,
	tracking config.TrackingConfig,
	c clock.Clock,
	log *slog.Logger,
	metrics *telemetry.Metrics,
) *Scorer {
	return &Scorer{
		repositories: repos, snapshots: snaps, scores: scores,
		tracking: tracking, clock: c, log: log, metrics: metrics,
	}
}

// Kind implements Handler.
func (s *Scorer) Kind() repository.JobKind { return repository.JobScoreRepositories }

// Handle implements Handler.
//
// One pass loads every active repository and the history needed for the longest
// window, then scores all three windows from that single in-memory set. Loading
// history once rather than per window keeps the run to two queries regardless
// of how many repositories are tracked.
func (s *Scorer) Handle(ctx context.Context, _ repository.Job) error {
	now := s.clock.Now()

	repos, err := s.repositories.ListActive(ctx, maxScoredRepositories)
	if err != nil {
		return fmt.Errorf("list repositories for scoring: %w", err)
	}
	if len(repos) == 0 {
		s.log.InfoContext(ctx, "scoring run skipped: no active repositories")
		return nil
	}

	ids := make([]int64, 0, len(repos))
	for _, r := range repos {
		ids = append(ids, r.ID)
	}

	// The longest window plus its baseline tolerance is the most history any
	// window can need.
	longest := scoring.Window7d
	from := now.Add(-longest.Duration - longest.BaselineTolerance)

	history, err := s.snapshots.SinceForMany(ctx, ids, from)
	if err != nil {
		return fmt.Errorf("load snapshot history: %w", err)
	}

	inputs := buildScoringInputs(repos, history)

	opts := scoring.DefaultOptions()
	opts.SnapshotInterval = s.tracking.SnapshotBucketInterval

	for _, window := range scoring.Windows {
		start := time.Now()

		out := scoring.Run(now, window, inputs, opts)
		persisted := toStoredScores(out)

		if err := s.scores.SaveRun(ctx, repository.ScoringRun{
			Window:         window.Name,
			ScoredAt:       out.ScoredAt,
			Repositories:   len(persisted),
			Eligible:       len(out.Ranked),
			DurationMillis: int(time.Since(start).Milliseconds()),
			FormulaVersion: out.FormulaVersion,
		}, persisted); err != nil {
			return fmt.Errorf("persist %s scoring run: %w", window.Name, err)
		}

		s.metrics.ObserveScoreRun(window.Name, len(persisted), time.Since(start))
		s.log.InfoContext(ctx, "scoring run complete",
			slog.String("window", window.Name),
			slog.Int("ranked", len(out.Ranked)),
			slog.Int("provisional", len(out.Provisional)),
			slog.Int("excluded", len(out.Excluded)),
			slog.Int("cohortSize", out.CohortSize),
			slog.Int64("durationMs", time.Since(start).Milliseconds()),
		)
	}

	// Old generations are pruned so momentum_scores does not grow without
	// bound. The current run for each window is always kept.
	if pruned, err := s.scores.PruneOlderThan(ctx, now.Add(-14*24*time.Hour)); err != nil {
		s.log.WarnContext(ctx, "could not prune old scores", slog.String("error", err.Error()))
	} else if pruned > 0 {
		s.log.DebugContext(ctx, "pruned superseded score rows", slog.Int("rows", pruned))
	}

	return nil
}

// buildScoringInputs maps stored rows onto the pure scoring package's types.
func buildScoringInputs(repos []repository.Repository, history map[int64][]repository.Snapshot) []scoring.Input {
	inputs := make([]scoring.Input, 0, len(repos))

	for _, r := range repos {
		stored := history[r.ID]
		snapshots := make([]scoring.Snapshot, 0, len(stored))
		for _, s := range stored {
			snapshots = append(snapshots, scoring.Snapshot{At: s.CapturedAt, Stars: s.Stars, Forks: s.Forks})
		}

		inputs = append(inputs, scoring.Input{
			RepositoryID:    r.ID,
			FullName:        r.FullName,
			Description:     r.Description,
			CreatedAt:       r.CreatedAt,
			PushedAt:        r.PushedAt,
			LatestReleaseAt: r.LatestReleaseAt,
			IsFork:          r.IsFork,
			IsArchived:      r.IsArchived,
			IsDisabled:      r.IsDisabled,
			Active:          r.TrackingState == repository.StateActive,
			FirstSeenAt:     r.FirstSeenAt,
			Snapshots:       snapshots,
		})
	}
	return inputs
}

// toStoredScores converts scoring results into persistable rows.
//
// Both ranked and provisional results are stored: New Entrants is a real
// collection that the API serves, and keeping provisional rows means a
// repository's page can explain its own low confidence rather than showing
// nothing at all.
func toStoredScores(out scoring.RunResult) []repository.MomentumScore {
	all := out.All()
	stored := make([]repository.MomentumScore, 0, len(all))

	for _, r := range all {
		var rank *int
		if r.Rank > 0 {
			value := r.Rank
			rank = &value
		}

		stored = append(stored, repository.MomentumScore{
			RepositoryID:   r.RepositoryID,
			Window:         r.Window,
			ScoredAt:       r.ScoredAt,
			Score:          r.Score,
			Rank:           rank,
			StarsTotal:     r.StarsTotal,
			StarDelta:      r.StarDelta,
			StarVelocity:   r.StarVelocity,
			RelativeGrowth: r.RelativeGrowth,
			Acceleration:   r.Acceleration,
			ForkDelta:      r.ForkDelta,
			ForkVelocity:   r.ForkVelocity,
			ActivityScore:  r.Activity,
			FreshnessScore: r.Freshness,
			Confidence:     r.Confidence,
			Collections:    r.Collections,
			ReasonCodes:    r.ReasonCodes(),
			AnomalyFlags:   r.AnomalyFlags,
			FeatureData:    featureData(r),
			FormulaVersion: r.FormulaVersion,
		})
	}
	return stored
}

// featureData is the transparency payload. It carries the normalised
// percentiles, the raw measurements behind them and the reason sentences, so
// that any score in the API can be recomputed and checked by a reader.
func featureData(r scoring.Result) map[string]any {
	return map[string]any{
		"percentiles": r.Percentiles,
		"raw": map[string]any{
			"observedHours":     r.Features.ObservedHours,
			"observedCount":     r.Features.ObservedCount,
			"expectedCount":     r.Features.ExpectedCount,
			"largestGapHours":   r.Features.LargestGapHours,
			"latestAgeHours":    r.Features.LatestAgeHours,
			"baselineStars":     r.Features.BaselineStars,
			"baselineForks":     r.Features.BaselineForks,
			"recentVelocity":    r.Features.RecentVelocity,
			"previousVelocity":  r.Features.PreviousVelocity,
			"accelerationKnown": r.Features.AccelerationKnown,
			"baselineAvailable": r.Features.BaselineAvailable,
			"midpointAvailable": r.Features.MidpointAvailable,
			"pushRecency":       r.Features.PushRecency,
			"releaseRecency":    r.Features.ReleaseRecency,
			"ageDays":           r.Features.AgeDays,
		},
		"reasons":     r.Reasons,
		"eligibility": string(r.Eligibility),
		"weights":     scoring.DefaultWeights,
	}
}

// TierRefresher recalculates which repositories belong in which polling tier.
type TierRefresher struct {
	repositories *repository.Repositories
	scores       *repository.Scores
	tracking     config.TrackingConfig
	clock        clock.Clock
	log          *slog.Logger
}

// NewTierRefresher constructs the tier handler.
func NewTierRefresher(
	repos *repository.Repositories,
	scores *repository.Scores,
	tracking config.TrackingConfig,
	c clock.Clock,
	log *slog.Logger,
) *TierRefresher {
	return &TierRefresher{repositories: repos, scores: scores, tracking: tracking, clock: c, log: log}
}

// Kind implements Handler.
func (t *TierRefresher) Kind() repository.JobKind { return repository.JobRefreshTiers }

// Handle implements Handler.
//
// Tiers follow momentum: the highest-scoring repositories are polled hourly,
// the next band every four hours, and everything else twice a day. Promotion
// only reschedules a repository sooner; a demotion never pulls an already
// scheduled check forward, so tier churn cannot cause a burst of requests.
func (t *TierRefresher) Handle(ctx context.Context, _ repository.Job) error {
	now := t.clock.Now()

	hot, err := t.scores.TopRepositoryIDs(ctx, scoring.Window24h.Name, t.tracking.HotLimit)
	if err != nil {
		// Before the first scoring run there is nothing to rank by; that is an
		// expected state on a fresh database, not a failure.
		t.log.InfoContext(ctx, "tier refresh skipped: no scoring run yet",
			slog.String("reason", err.Error()))
		return nil
	}

	warm, err := t.scores.TopRepositoryIDs(ctx, scoring.Window24h.Name, t.tracking.HotLimit+t.tracking.WarmLimit)
	if err != nil {
		return fmt.Errorf("read warm tier candidates: %w", err)
	}

	hotSet := make(map[int64]struct{}, len(hot))
	for _, id := range hot {
		hotSet[id] = struct{}{}
	}

	promotedHot, promotedWarm := 0, 0

	for _, id := range hot {
		if err := t.repositories.SetTrackingTier(ctx, id, repository.TierHot,
			now.Add(t.tracking.HotPollInterval)); err != nil {
			return fmt.Errorf("promote repository %d to hot: %w", id, err)
		}
		promotedHot++
	}

	for _, id := range warm {
		if _, isHot := hotSet[id]; isHot {
			continue
		}
		if err := t.repositories.SetTrackingTier(ctx, id, repository.TierWarm,
			now.Add(t.tracking.WarmPollInterval)); err != nil {
			return fmt.Errorf("promote repository %d to warm: %w", id, err)
		}
		promotedWarm++
	}

	t.log.InfoContext(ctx, "tracking tiers refreshed",
		slog.Int("hot", promotedHot),
		slog.Int("warm", promotedWarm),
	)
	return nil
}
