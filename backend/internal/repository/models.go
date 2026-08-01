// Package repository is RepoMeteor's data-access layer.
//
// Every statement here is hand-written SQL with bind parameters. Sort and
// filter inputs that cannot be bound (column names, directions) are resolved
// through fixed allowlists, never interpolated from request data.
package repository

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/FayezBast/repometeor/backend/internal/reponame"
)

// TrackingTier controls how often a repository is polled.
type TrackingTier string

const (
	TierHot  TrackingTier = "hot"
	TierWarm TrackingTier = "warm"
	TierCold TrackingTier = "cold"
)

// TrackingState is the lifecycle state of a tracked repository.
type TrackingState string

const (
	// StateActive repositories are polled and ranked.
	StateActive TrackingState = "active"
	// StatePaused repositories are temporarily not polled.
	StatePaused TrackingState = "paused"
	// StateExcluded repositories are never ranked (forks, archived, disabled).
	StateExcluded TrackingState = "excluded"
	// StateMissing repositories return 404 on GitHub. History is retained.
	StateMissing TrackingState = "missing"
)

// SnapshotSource records where a snapshot's numbers came from.
type SnapshotSource string

const (
	SourceGitHubAPI SnapshotSource = "github_api"
	SourceDemoSeed  SnapshotSource = "demo_seed"
	SourceBackfill  SnapshotSource = "backfill"
)

// Repository is a tracked public GitHub repository.
type Repository struct {
	ID       int64
	GitHubID int64
	NodeID   string

	OwnerLogin string
	Name       string
	FullName   string

	Description   string
	HTMLURL       string
	HomepageURL   string
	AvatarURL     string
	DefaultBranch string

	PrimaryLanguage string
	Topics          []string
	LicenseSPDX     string

	IsFork     bool
	IsArchived bool
	IsDisabled bool
	IsTemplate bool

	CreatedAt        time.Time
	GitHubUpdatedAt  *time.Time
	PushedAt         *time.Time
	LatestReleaseAt  *time.Time
	LatestReleaseTag string

	FirstSeenAt         time.Time
	LastCheckedAt       *time.Time
	NextCheckAt         *time.Time
	TrackingTier        TrackingTier
	TrackingState       TrackingState
	ConsecutiveFailures int

	ETag         string
	LastModified string

	CreatedRecordAt time.Time
	UpdatedRecordAt time.Time
}

// RepoName returns the validated owner/name pair for this repository.
func (r Repository) RepoName() reponame.Name {
	return reponame.Name{Owner: r.OwnerLogin, Repository: r.Name}
}

// Snapshot is one aggregate observation of a repository at a point in time.
type Snapshot struct {
	ID           int64
	RepositoryID int64

	CapturedAt  time.Time
	BucketStart time.Time

	Stars       int
	Forks       int
	OpenIssues  int
	Subscribers *int

	LatestReleaseAt *time.Time
	Source          SnapshotSource
}

// UpsertRepositoryInput carries the fields collected from GitHub. It is
// deliberately separate from Repository so that collection never overwrites
// RepoMeteor's own bookkeeping columns by accident.
type UpsertRepositoryInput struct {
	GitHubID         int64
	NodeID           string
	OwnerLogin       string
	Name             string
	FullName         string
	Description      string
	HTMLURL          string
	HomepageURL      string
	AvatarURL        string
	DefaultBranch    string
	PrimaryLanguage  string
	Topics           []string
	LicenseSPDX      string
	IsFork           bool
	IsArchived       bool
	IsDisabled       bool
	IsTemplate       bool
	CreatedAt        time.Time
	GitHubUpdatedAt  *time.Time
	PushedAt         *time.Time
	LatestReleaseAt  *time.Time
	LatestReleaseTag string
}

// MomentumScore is a persisted ranking row for one repository and window.
type MomentumScore struct {
	ID           int64
	RepositoryID int64

	Window   string
	ScoredAt time.Time

	Score float64
	Rank  *int

	StarsTotal     int
	StarDelta      int
	StarVelocity   float64
	RelativeGrowth float64
	Acceleration   float64
	ForkDelta      int
	ForkVelocity   float64
	ActivityScore  float64
	FreshnessScore float64
	Confidence     float64

	Collections    []string
	ReasonCodes    []string
	AnomalyFlags   []string
	FeatureData    map[string]any
	FormulaVersion string
}

// ScoringRun records a completed scoring pass for one window.
type ScoringRun struct {
	ID             int64
	Window         string
	ScoredAt       time.Time
	Repositories   int
	Eligible       int
	DurationMillis int
	FormulaVersion string
}

// JobKind enumerates the worker's job types.
type JobKind string

const (
	JobDiscoverRepositories JobKind = "discover_repositories"
	JobCollectRepository    JobKind = "collect_repository"
	JobScoreRepositories    JobKind = "score_repositories"
	JobRefreshTiers         JobKind = "refresh_tiers"
)

// JobStatus enumerates queue states.
type JobStatus string

const (
	JobQueued    JobStatus = "queued"
	JobRunning   JobStatus = "running"
	JobCompleted JobStatus = "completed"
	JobFailed    JobStatus = "failed"
	JobDead      JobStatus = "dead"
)

// Job is one unit of durable work.
type Job struct {
	ID           int64
	Kind         JobKind
	RepositoryID *int64
	Payload      []byte

	Status   JobStatus
	Priority int
	RunAfter time.Time

	Attempts    int
	MaxAttempts int

	LockedAt  *time.Time
	LockedBy  string
	LastError string
	DedupeKey string

	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
}

// EnqueueInput describes a job to add to the queue.
type EnqueueInput struct {
	Kind         JobKind
	RepositoryID *int64
	Payload      []byte
	Priority     int
	RunAfter     time.Time
	MaxAttempts  int
	// DedupeKey, when set, prevents a second queued or running job with the
	// same key from being created.
	DedupeKey string
}

// DiscoveryRun is an audit record for one discovery attempt.
type DiscoveryRun struct {
	ID                 int64
	StartedAt          time.Time
	FinishedAt         *time.Time
	Strategy           string
	Query              string
	PagesVisited       int
	CandidatesSeen     int
	CandidatesAccepted int
	RateLimitRemaining *int
	RateLimitResource  string
	Succeeded          bool
	ErrorSummary       string
}

// CacheEntry holds conditional-request validators for one logical GitHub
// request. It never contains credentials or response bodies.
type CacheEntry struct {
	RequestKey        string
	ETag              string
	LastModified      string
	LastStatus        int
	LastCheckedAt     time.Time
	RateLimitResource string
	ResponseHash      string
}

// LanguageCount is a filter facet.
type LanguageCount struct {
	Language string
	Count    int
}

// TopicCount is a filter facet.
type TopicCount struct {
	Topic string
	Count int
}

// RankedRepository joins a repository with its score for one window; it is what
// the trending endpoint returns.
type RankedRepository struct {
	Repository Repository
	Score      MomentumScore
	Snapshot   *Snapshot
	// Sparkline holds up to 24 recent star counts, oldest first.
	Sparkline []int
}

// HistoryPoint is one downsampled point on a repository's history chart.
type HistoryPoint struct {
	At        time.Time
	Stars     int
	Forks     int
	StarDelta int
	ForkDelta int
	Score     *float64
}

// derefTime returns the zero time for a nil pointer, for use in comparisons.
func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// CollectPayload is the wire format of a collect_repository job.
//
// It lives here, in the layer both the producer and the consumer already
// import, so that the API's tracking endpoint and the worker's collector cannot
// drift apart. Encoding it as an untyped map at either end would make a renamed
// field a silent runtime failure rather than a compile error.
type CollectPayload struct {
	// Owner and Name identify the repository. They are re-validated by the
	// GitHub client before any request is built.
	Owner string `json:"owner"`
	Name  string `json:"name"`
	// UserRequested marks work a person is waiting for, which may spend rate
	// budget that background collection may not.
	UserRequested bool `json:"userRequested,omitempty"`
}

// Encode renders the payload for storage.
func (p CollectPayload) Encode() []byte {
	encoded, err := json.Marshal(p)
	if err != nil {
		// The struct is three scalar fields; a failure would be impossible.
		return []byte(`{}`)
	}
	return encoded
}

// DecodeCollectPayload parses a stored collect_repository payload.
func DecodeCollectPayload(raw []byte) (CollectPayload, error) {
	var p CollectPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return CollectPayload{}, fmt.Errorf("invalid collect payload: %w", err)
	}
	return p, nil
}
