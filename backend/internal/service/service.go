// Package service holds RepoMeteor's read and write use cases.
//
// It sits between the HTTP handlers and the data-access layer so that the API,
// the CLI-facing endpoints and any future surface share one implementation of
// "what is trending" and "what does this repository look like".
package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/FayezBast/repometeor/backend/internal/clock"
	"github.com/FayezBast/repometeor/backend/internal/config"
	"github.com/FayezBast/repometeor/backend/internal/database"
	"github.com/FayezBast/repometeor/backend/internal/reponame"
	"github.com/FayezBast/repometeor/backend/internal/repository"
	"github.com/FayezBast/repometeor/backend/internal/scoring"
)

// Errors returned by the service layer. Handlers map them onto problem details.
var (
	// ErrNotFound means the repository is not tracked by RepoMeteor.
	ErrNotFound = errors.New("not tracked")
	// ErrNotRanked means the repository is tracked but has no score yet.
	ErrNotRanked = errors.New("not ranked")
	// ErrInvalidCursor means a pagination cursor could not be decoded.
	ErrInvalidCursor = errors.New("invalid cursor")
	// ErrTrackingDisabled means public submissions are switched off.
	ErrTrackingDisabled = errors.New("public tracking is disabled")
	// ErrDemoMode means the operation is meaningless without live collection.
	ErrDemoMode = errors.New("demo mode")
)

// Service is the application's use-case layer.
type Service struct {
	repositories *repository.Repositories
	snapshots    *repository.Snapshots
	scores       *repository.Scores
	jobs         *repository.Jobs
	limits       *repository.RateLimits
	cfg          *config.Config
	clock        clock.Clock
	log          *slog.Logger
}

// New constructs the service.
func New(
	repos *repository.Repositories,
	snaps *repository.Snapshots,
	scores *repository.Scores,
	jobs *repository.Jobs,
	limits *repository.RateLimits,
	cfg *config.Config,
	c clock.Clock,
	log *slog.Logger,
) *Service {
	return &Service{
		repositories: repos, snapshots: snaps, scores: scores, jobs: jobs,
		limits: limits, cfg: cfg, clock: c, log: log,
	}
}

// Config exposes the loaded configuration to handlers.
func (s *Service) Config() *config.Config { return s.cfg }

// DemoMode reports whether the instance is serving fictional data.
func (s *Service) DemoMode() bool { return s.cfg.App.DemoMode }

// --- trending ----------------------------------------------------------------

// TrendingQuery is a validated trending request.
type TrendingQuery struct {
	Window     string
	Collection string
	Language   string
	Topic      string
	MinStars   *int
	MaxStars   *int
	Confidence string
	Limit      int
	Cursor     string
}

// TrendingResult is one page of rankings.
type TrendingResult struct {
	Items       []repository.RankedRepository
	Window      string
	Collection  string
	GeneratedAt time.Time
	NextCursor  string
	ResultCount int
	DemoMode    bool
	// FormulaVersion identifies the scoring method behind these numbers.
	FormulaVersion string
}

// Trending returns one ranked page.
//
// Sparklines and latest snapshots are loaded in two batch queries after the
// page is fetched, rather than per row: a 100-item page therefore costs three
// queries in total regardless of its size.
func (s *Service) Trending(ctx context.Context, q TrendingQuery) (TrendingResult, error) {
	filter := repository.TrendingFilter{
		Window:     q.Window,
		Collection: q.Collection,
		Language:   q.Language,
		Topic:      q.Topic,
		MinStars:   q.MinStars,
		MaxStars:   q.MaxStars,
		// One extra row reveals whether a further page exists without a count.
		Limit: q.Limit + 1,
	}

	if q.Confidence != "" {
		low, high, ok := scoring.ConfidenceBounds(q.Confidence)
		if !ok {
			return TrendingResult{}, fmt.Errorf("unknown confidence label %q", q.Confidence)
		}
		filter.MinConfidence = &low
		filter.MaxConfidence = &high
	}

	if q.Cursor != "" {
		cursor, err := DecodeCursor(q.Cursor)
		if err != nil {
			return TrendingResult{}, err
		}
		filter.Cursor = cursor
	}

	items, run, err := s.scores.Trending(ctx, filter)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			// No scoring run has completed yet. That is an empty result, not an
			// error: a fresh instance should render an honest empty state.
			return TrendingResult{
				Items:          nil,
				Window:         q.Window,
				Collection:     q.Collection,
				GeneratedAt:    s.clock.Now(),
				DemoMode:       s.DemoMode(),
				FormulaVersion: scoring.FormulaVersion,
			}, nil
		}
		return TrendingResult{}, err
	}

	result := TrendingResult{
		Window:         q.Window,
		Collection:     q.Collection,
		GeneratedAt:    run.ScoredAt,
		DemoMode:       s.DemoMode(),
		FormulaVersion: run.FormulaVersion,
	}

	if len(items) > q.Limit {
		last := items[q.Limit-1]
		result.NextCursor = EncodeCursor(repository.Cursor{
			Score:        last.Score.Score,
			Rank:         derefRank(last.Score.Rank),
			RepositoryID: last.Repository.ID,
		})
		items = items[:q.Limit]
	}

	if err := s.decorate(ctx, items); err != nil {
		return TrendingResult{}, err
	}

	result.Items = items
	result.ResultCount = len(items)
	return result, nil
}

// decorate attaches the latest snapshot and a sparkline to each row.
func (s *Service) decorate(ctx context.Context, items []repository.RankedRepository) error {
	if len(items) == 0 {
		return nil
	}

	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.Repository.ID)
	}

	latest, err := s.snapshots.LatestForMany(ctx, ids)
	if err != nil {
		return fmt.Errorf("load latest snapshots: %w", err)
	}
	sparklines, err := s.snapshots.SparklinesForMany(ctx, ids, 24)
	if err != nil {
		return fmt.Errorf("load sparklines: %w", err)
	}

	for i := range items {
		if snapshot, ok := latest[items[i].Repository.ID]; ok {
			items[i].Snapshot = &snapshot
		}
		items[i].Sparkline = sparklines[items[i].Repository.ID]
	}
	return nil
}

// --- repository detail -------------------------------------------------------

// RepositoryDetail is everything the repository page needs.
type RepositoryDetail struct {
	Repository repository.Repository
	Latest     *repository.Snapshot
	Scores     map[string]repository.MomentumScore
	// TrackingSince is when RepoMeteor's own history begins. It is deliberately
	// distinct from the repository's GitHub creation date: the UI must never
	// imply that older data exists.
	TrackingSince *time.Time
	// CanonicalName is set when the request used a former name, so the caller
	// can redirect.
	CanonicalName  string
	Redirected     bool
	DemoMode       bool
	FormulaVersion string
}

// RepositoryDetail resolves one repository, following renames.
func (s *Service) RepositoryDetail(ctx context.Context, name reponame.Name) (RepositoryDetail, error) {
	repo, redirected, err := s.resolve(ctx, name)
	if err != nil {
		return RepositoryDetail{}, err
	}

	detail := RepositoryDetail{
		Repository:     repo,
		DemoMode:       s.DemoMode(),
		Redirected:     redirected,
		FormulaVersion: scoring.FormulaVersion,
	}
	if redirected {
		detail.CanonicalName = repo.FullName
	}

	if latest, err := s.snapshots.Latest(ctx, repo.ID); err == nil {
		detail.Latest = &latest
	} else if !errors.Is(err, database.ErrNotFound) {
		return RepositoryDetail{}, fmt.Errorf("load latest snapshot: %w", err)
	}

	if since, err := s.snapshots.EarliestCapture(ctx, repo.ID); err == nil {
		detail.TrackingSince = &since
	} else if !errors.Is(err, database.ErrNotFound) {
		return RepositoryDetail{}, fmt.Errorf("load tracking start: %w", err)
	}

	scores, err := s.scores.LatestForRepository(ctx, repo.ID)
	if err != nil {
		return RepositoryDetail{}, fmt.Errorf("load scores: %w", err)
	}
	detail.Scores = scores

	return detail, nil
}

// resolve finds a repository by its current name, falling back to aliases.
func (s *Service) resolve(ctx context.Context, name reponame.Name) (repository.Repository, bool, error) {
	repo, err := s.repositories.GetByName(ctx, name)
	if err == nil {
		return repo, false, nil
	}
	if !errors.Is(err, database.ErrNotFound) {
		return repository.Repository{}, false, err
	}

	// The name may be a former one. Following the alias keeps old RepoMeteor
	// links, badges and feeds working after a repository is renamed on GitHub.
	repo, aliasErr := s.repositories.ResolveAlias(ctx, name)
	if aliasErr == nil {
		return repo, true, nil
	}
	if errors.Is(aliasErr, database.ErrNotFound) {
		return repository.Repository{}, false, ErrNotFound
	}
	return repository.Repository{}, false, aliasErr
}

// --- history -----------------------------------------------------------------

// HistoryQuery is a validated history request.
type HistoryQuery struct {
	Range      time.Duration
	Resolution repository.Resolution
	Window     string
}

// HistoryResult is a repository's stored history.
type HistoryResult struct {
	Repository repository.Repository
	Points     []repository.HistoryPoint
	Resolution repository.Resolution
	From       time.Time
	To         time.Time
	// TrackingSince states when RepoMeteor's history actually begins.
	TrackingSince *time.Time
	DemoMode      bool
}

// History returns downsampled history for a repository.
func (s *Service) History(ctx context.Context, name reponame.Name, q HistoryQuery) (HistoryResult, error) {
	repo, _, err := s.resolve(ctx, name)
	if err != nil {
		return HistoryResult{}, err
	}

	now := s.clock.Now()
	from := now.Add(-q.Range)

	points, err := s.snapshots.History(ctx, repo.ID, from, q.Resolution)
	if err != nil {
		return HistoryResult{}, fmt.Errorf("load history: %w", err)
	}

	result := HistoryResult{
		Repository: repo,
		Points:     points,
		Resolution: q.Resolution,
		From:       from,
		To:         now,
		DemoMode:   s.DemoMode(),
	}

	if since, err := s.snapshots.EarliestCapture(ctx, repo.ID); err == nil {
		result.TrackingSince = &since
	}

	// Overlay momentum scores when a window was requested.
	if q.Window != "" {
		scoresByTime, err := s.scores.ScoreHistory(ctx, repo.ID, q.Window, from)
		if err != nil {
			return HistoryResult{}, fmt.Errorf("load score history: %w", err)
		}
		attachScores(result.Points, scoresByTime)
	}

	return result, nil
}

// attachScores maps score timestamps onto the nearest history point.
func attachScores(points []repository.HistoryPoint, scores map[time.Time]float64) {
	if len(points) == 0 || len(scores) == 0 {
		return
	}

	for at, score := range scores {
		nearest := -1
		var bestDiff time.Duration = 1<<62 - 1

		for i := range points {
			diff := points[i].At.Sub(at)
			if diff < 0 {
				diff = -diff
			}
			if diff < bestDiff {
				bestDiff, nearest = diff, i
			}
		}
		// Only attach when the score genuinely belongs near that point.
		if nearest >= 0 && bestDiff <= time.Hour {
			value := score
			points[nearest].Score = &value
		}
	}
}

// --- compare -----------------------------------------------------------------

// CompareEntry is one repository in a comparison.
type CompareEntry struct {
	Repository repository.Repository
	Points     []repository.HistoryPoint
	Score      *repository.MomentumScore
	// Available is false when RepoMeteor has no usable history for the shared
	// range, which the UI explains rather than silently omitting.
	Available   bool
	Unavailable string
	// TrackingSince marks where this repository's own history starts.
	TrackingSince *time.Time
}

// CompareResult is an aligned comparison.
type CompareResult struct {
	Entries    []CompareEntry
	Window     string
	Range      time.Duration
	Resolution repository.Resolution
	From       time.Time
	To         time.Time
	DemoMode   bool
}

// Compare aligns two to four repositories over a shared range.
func (s *Service) Compare(ctx context.Context, names []reponame.Name, window string, rangeDuration time.Duration) (CompareResult, error) {
	now := s.clock.Now()
	from := now.Add(-rangeDuration)
	resolution := resolutionFor(rangeDuration)

	result := CompareResult{
		Window:     window,
		Range:      rangeDuration,
		Resolution: resolution,
		From:       from,
		To:         now,
		DemoMode:   s.DemoMode(),
	}

	for _, name := range names {
		entry := CompareEntry{}

		repo, _, err := s.resolve(ctx, name)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				entry.Repository = repository.Repository{
					OwnerLogin: name.Owner, Name: name.Repository, FullName: name.String(),
				}
				entry.Unavailable = "RepoMeteor is not tracking this repository yet."
				result.Entries = append(result.Entries, entry)
				continue
			}
			return CompareResult{}, err
		}

		entry.Repository = repo

		points, err := s.snapshots.History(ctx, repo.ID, from, resolution)
		if err != nil {
			return CompareResult{}, fmt.Errorf("load history for %s: %w", repo.FullName, err)
		}
		entry.Points = points

		if since, err := s.snapshots.EarliestCapture(ctx, repo.ID); err == nil {
			entry.TrackingSince = &since
		}

		scores, err := s.scores.LatestForRepository(ctx, repo.ID)
		if err != nil {
			return CompareResult{}, fmt.Errorf("load score for %s: %w", repo.FullName, err)
		}
		if score, ok := scores[window]; ok {
			entry.Score = &score
		}

		switch {
		case len(points) < 2:
			entry.Unavailable = "Not enough stored history to plot this range yet."
		case entry.TrackingSince != nil && entry.TrackingSince.After(from):
			entry.Available = true
			entry.Unavailable = fmt.Sprintf("Tracking began on %s, so earlier points are unavailable.",
				entry.TrackingSince.Format("2 January 2006"))
		default:
			entry.Available = true
		}

		result.Entries = append(result.Entries, entry)
	}

	return result, nil
}

// resolutionFor picks a downsampling resolution that keeps payloads bounded.
func resolutionFor(rangeDuration time.Duration) repository.Resolution {
	if rangeDuration > 14*24*time.Hour {
		return repository.ResolutionDaily
	}
	return repository.ResolutionHourly
}

// --- search ------------------------------------------------------------------

// Search finds tracked repositories by name.
//
// It queries RepoMeteor's own database only. A live GitHub search per keystroke
// would exhaust the rate budget within minutes and expose the service to abuse.
func (s *Service) Search(ctx context.Context, query string, limit int) ([]repository.Repository, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	// A full owner/name or URL resolves directly, which is what a user pasting
	// a link expects.
	if name, err := reponame.Parse(query); err == nil {
		if repo, _, err := s.resolve(ctx, name); err == nil {
			return []repository.Repository{repo}, nil
		}
	}

	return s.repositories.Search(ctx, query, limit)
}

// --- facets ------------------------------------------------------------------

// Languages returns filter facets.
func (s *Service) Languages(ctx context.Context) ([]repository.LanguageCount, error) {
	return s.repositories.Languages(ctx)
}

// Topics returns filter facets.
func (s *Service) Topics(ctx context.Context, limit int) ([]repository.TopicCount, error) {
	return s.repositories.Topics(ctx, limit)
}

// --- tracking ----------------------------------------------------------------

// TrackOutcome describes the result of a tracking submission.
type TrackOutcome string

const (
	// TrackQueued means a collection job was enqueued.
	TrackQueued TrackOutcome = "queued"
	// TrackAlreadyTracked means RepoMeteor already follows the repository.
	TrackAlreadyTracked TrackOutcome = "already_tracked"
)

// TrackResult reports what happened to a submission.
type TrackResult struct {
	Outcome    TrackOutcome
	Repository *repository.Repository
	Name       reponame.Name
}

// Track submits a repository for tracking.
//
// The submission is never processed inline: it enqueues a high-priority job and
// returns immediately, because a live GitHub call inside a public endpoint
// would let anyone drive RepoMeteor's outbound traffic. Rate limiting happens
// in the handler, before this is reached.
func (s *Service) Track(ctx context.Context, name reponame.Name) (TrackResult, error) {
	if !s.cfg.Limits.PublicTrackingEnabled {
		return TrackResult{}, ErrTrackingDisabled
	}
	if s.cfg.App.DemoMode {
		return TrackResult{}, ErrDemoMode
	}

	result := TrackResult{Name: name}

	repo, _, err := s.resolve(ctx, name)
	switch {
	case err == nil:
		result.Outcome = TrackAlreadyTracked
		result.Repository = &repo
		return result, nil
	case errors.Is(err, ErrNotFound):
		// Fall through to enqueue.
	default:
		return TrackResult{}, err
	}

	_, err = s.jobs.Enqueue(ctx, repository.EnqueueInput{
		Kind:        repository.JobCollectRepository,
		Priority:    100, // A person is waiting for this one.
		RunAfter:    s.clock.Now(),
		MaxAttempts: 3,
		Payload: repository.CollectPayload{
			Owner:         name.Owner,
			Name:          name.Repository,
			UserRequested: true,
		}.Encode(),
		DedupeKey: "collect:" + name.Lower().String(),
	})
	if err != nil && !errors.Is(err, repository.ErrDuplicateJob) {
		return TrackResult{}, fmt.Errorf("enqueue tracking job: %w", err)
	}

	result.Outcome = TrackQueued
	return result, nil
}

// --- cursors -----------------------------------------------------------------

// cursorPayload is the wire form of a pagination cursor.
type cursorPayload struct {
	Score        float64 `json:"s"`
	Rank         int     `json:"r"`
	RepositoryID int64   `json:"i"`
}

// EncodeCursor renders an opaque pagination cursor.
//
// It is base64url of a tiny JSON object. The cursor is opaque to clients but
// deliberately not signed: it carries no privileged information, and every
// field it can influence is bounds-checked when it is used.
func EncodeCursor(c repository.Cursor) string {
	encoded, err := json.Marshal(cursorPayload{Score: c.Score, Rank: c.Rank, RepositoryID: c.RepositoryID})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(encoded)
}

// DecodeCursor parses a pagination cursor, rejecting anything malformed or out
// of range.
func DecodeCursor(raw string) (*repository.Cursor, error) {
	if len(raw) > 256 {
		return nil, ErrInvalidCursor
	}

	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, ErrInvalidCursor
	}

	var payload cursorPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return nil, ErrInvalidCursor
	}
	if payload.Score < 0 || payload.Score > 100 {
		return nil, ErrInvalidCursor
	}
	if payload.Rank < 0 || payload.RepositoryID < 0 {
		return nil, ErrInvalidCursor
	}

	return &repository.Cursor{
		Score:        payload.Score,
		Rank:         payload.Rank,
		RepositoryID: payload.RepositoryID,
	}, nil
}

func derefRank(rank *int) int {
	if rank == nil {
		return 0
	}
	return *rank
}

// AllowTracking applies the durable per-client rate limit for submissions.
//
// The limit lives in PostgreSQL rather than in process memory because tracking
// submissions cause outbound GitHub requests: the allowance has to hold across
// restarts and across multiple API instances, or it would not be a limit at all.
func (s *Service) AllowTracking(ctx context.Context, client string, now time.Time) (allowed bool, remaining int, resetAt time.Time, err error) {
	return s.limits.Allow(ctx, "track", client,
		s.cfg.Limits.PublicTrackingPerHour, time.Hour, now)
}
