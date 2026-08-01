package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/repometeor/repometeor/backend/internal/clock"
	"github.com/repometeor/repometeor/backend/internal/config"
	"github.com/repometeor/repometeor/backend/internal/database"
	"github.com/repometeor/repometeor/backend/internal/github"
	"github.com/repometeor/repometeor/backend/internal/reponame"
	"github.com/repometeor/repometeor/backend/internal/repository"
	"github.com/repometeor/repometeor/backend/internal/telemetry"
)

// missingConfirmations is how many consecutive 404s mark a repository missing.
// One 404 can be a transient GitHub inconsistency; three in a row across
// separate polls is a real disappearance.
const missingConfirmations = 3

// CollectPayload is the collect_repository job payload. It is an alias for the
// shared type so that producers and this consumer cannot drift apart.
type CollectPayload = repository.CollectPayload

// Collector polls one repository and stores an aggregate snapshot.
type Collector struct {
	repositories *repository.Repositories
	snapshots    *repository.Snapshots
	jobs         *repository.Jobs
	cache        *repository.Cache
	provider     github.Provider
	budget       *github.Budget
	tracking     config.TrackingConfig
	clock        clock.Clock
	log          *slog.Logger
	metrics      *telemetry.Metrics
}

// NewCollector constructs the collection handler.
func NewCollector(
	repos *repository.Repositories,
	snaps *repository.Snapshots,
	jobs *repository.Jobs,
	cache *repository.Cache,
	provider github.Provider,
	budget *github.Budget,
	tracking config.TrackingConfig,
	c clock.Clock,
	log *slog.Logger,
	metrics *telemetry.Metrics,
) *Collector {
	return &Collector{
		repositories: repos, snapshots: snaps, jobs: jobs, cache: cache,
		provider: provider, budget: budget, tracking: tracking,
		clock: c, log: log, metrics: metrics,
	}
}

// Kind implements Handler.
func (c *Collector) Kind() repository.JobKind { return repository.JobCollectRepository }

// Handle implements Handler.
//
// The handler is idempotent by construction: the repository upsert is keyed on
// GitHub's stable ID and the snapshot upsert on (repository, bucket), so
// running the same job twice inside one bucket updates rather than duplicates.
func (c *Collector) Handle(ctx context.Context, job repository.Job) error {
	payload, err := repository.DecodeCollectPayload(job.Payload)
	if err != nil {
		// A payload that cannot be decoded will never decode; retrying is
		// pointless, so fail straight to the dead-letter path.
		return err
	}

	purpose := github.PurposeBackground
	if payload.UserRequested {
		purpose = github.PurposeUserRequested
	}

	if decision := c.budget.Allow("core", purpose); !decision.Allowed {
		return Deferred(decision.RetryAt, decision.Reason)
	}

	// Reuse stored validators so an unchanged repository costs almost nothing.
	validators := github.Validators{}
	existing, err := c.repositories.GetByName(ctx, name(payload.Owner, payload.Name))
	haveExisting := err == nil
	switch {
	case haveExisting:
		validators = github.Validators{ETag: existing.ETag, LastModified: existing.LastModified}
	case errors.Is(err, database.ErrNotFound):
		// First time seeing this repository.
	default:
		return fmt.Errorf("look up repository: %w", err)
	}

	result, err := c.provider.GetRepository(ctx, payload.Owner, payload.Name, validators)
	if err != nil {
		return c.handleFetchError(ctx, payload, existing, haveExisting, err)
	}

	c.budget.Observe(result.RateLimit)

	if result.NotModified {
		return c.handleNotModified(ctx, existing, haveExisting, result)
	}

	return c.store(ctx, payload, result)
}

// handleNotModified records a cache hit without inventing a data point.
//
// A 304 means the counts are unchanged, not that they are zero. The snapshot is
// re-written for the current bucket using the last known counts so the series
// stays dense, and the check timestamp advances.
func (c *Collector) handleNotModified(
	ctx context.Context,
	existing repository.Repository,
	haveExisting bool,
	result github.RepositoryResult,
) error {
	if !haveExisting {
		// A 304 without a stored repository should be impossible, because
		// validators only exist for repositories already stored.
		return errors.New("received 304 for a repository with no stored record")
	}

	now := c.clock.Now()

	latest, err := c.snapshots.Latest(ctx, existing.ID)
	switch {
	case err == nil:
		bucket := clock.Bucket(now, c.tracking.SnapshotBucketInterval)
		if !bucket.Equal(latest.BucketStart) {
			// Carry the last known counts forward into the new bucket. This is
			// an observation ("we checked, nothing changed"), not a fabrication.
			if _, err := c.snapshots.Upsert(ctx, repository.Snapshot{
				RepositoryID:    existing.ID,
				CapturedAt:      now,
				BucketStart:     bucket,
				Stars:           latest.Stars,
				Forks:           latest.Forks,
				OpenIssues:      latest.OpenIssues,
				Subscribers:     latest.Subscribers,
				LatestReleaseAt: latest.LatestReleaseAt,
				Source:          repository.SourceGitHubAPI,
			}); err != nil {
				return fmt.Errorf("carry snapshot forward: %w", err)
			}
			c.metrics.AddSnapshots(1)
		}
	case errors.Is(err, database.ErrNotFound):
		// Nothing stored yet and nothing returned: nothing to record.
	default:
		return fmt.Errorf("read latest snapshot: %w", err)
	}

	nextCheck := c.nextCheckAt(existing.TrackingTier, now)
	if err := c.repositories.MarkChecked(ctx, existing.ID, now, nextCheck,
		result.Validators.ETag, result.Validators.LastModified); err != nil {
		return fmt.Errorf("mark checked: %w", err)
	}

	c.log.DebugContext(ctx, "repository unchanged since last check",
		slog.String("repository", existing.FullName))
	return nil
}

// store writes the repository metadata and one snapshot.
func (c *Collector) store(ctx context.Context, payload CollectPayload, result github.RepositoryResult) error {
	repo := result.Repository
	now := c.clock.Now()

	// A repository that has become private must not be tracked further.
	if repo.IsPrivate {
		if existing, err := c.repositories.GetByGitHubID(ctx, repo.ID); err == nil {
			return c.repositories.SetTrackingState(ctx, existing.ID, repository.StateMissing)
		}
		return nil
	}

	// The release call costs a second request, so it is only made when the
	// budget allows and the repository does not already have fresh release data.
	var (
		latestReleaseAt  *time.Time
		latestReleaseTag string
	)
	if decision := c.budget.Allow("core", github.PurposeBackground); decision.Allowed {
		release, err := c.provider.GetLatestRelease(ctx, repo.Owner, repo.Name, github.Validators{})
		if err != nil {
			// Release metadata is a nice-to-have; its absence must not fail the
			// snapshot that is the actual product.
			c.log.DebugContext(ctx, "could not read latest release",
				slog.String("repository", repo.FullName),
				slog.String("error", err.Error()))
		} else if release.Found {
			c.budget.Observe(release.RateLimit)
			latestReleaseAt = release.Release.PublishedAt
			latestReleaseTag = release.Release.TagName
		}
	}

	stored, err := c.repositories.Upsert(ctx, repository.UpsertRepositoryInput{
		GitHubID:         repo.ID,
		NodeID:           repo.NodeID,
		OwnerLogin:       repo.Owner,
		Name:             repo.Name,
		FullName:         repo.FullName,
		Description:      repo.Description,
		HTMLURL:          repo.HTMLURL,
		HomepageURL:      repo.HomepageURL,
		AvatarURL:        repo.AvatarURL,
		DefaultBranch:    repo.DefaultBranch,
		PrimaryLanguage:  repo.PrimaryLanguage,
		Topics:           repo.Topics,
		LicenseSPDX:      repo.LicenseSPDX,
		IsFork:           repo.IsFork,
		IsArchived:       repo.IsArchived,
		IsDisabled:       repo.IsDisabled,
		IsTemplate:       repo.IsTemplate,
		CreatedAt:        repo.CreatedAt,
		GitHubUpdatedAt:  repo.UpdatedAt,
		PushedAt:         repo.PushedAt,
		LatestReleaseAt:  latestReleaseAt,
		LatestReleaseTag: latestReleaseTag,
	})
	if err != nil {
		return fmt.Errorf("upsert repository: %w", err)
	}

	subscribers := repo.Subscribers
	if _, err := c.snapshots.Upsert(ctx, repository.Snapshot{
		RepositoryID:    stored.ID,
		CapturedAt:      now,
		BucketStart:     clock.Bucket(now, c.tracking.SnapshotBucketInterval),
		Stars:           repo.Stars,
		Forks:           repo.Forks,
		OpenIssues:      repo.OpenIssues,
		Subscribers:     &subscribers,
		LatestReleaseAt: latestReleaseAt,
		Source:          repository.SourceGitHubAPI,
	}); err != nil {
		return fmt.Errorf("upsert snapshot: %w", err)
	}
	c.metrics.AddSnapshots(1)

	// Structurally ineligible repositories are still stored — their history
	// stays meaningful — but they stop consuming polling budget.
	state := repository.StateActive
	if repo.IsFork || repo.IsArchived || repo.IsDisabled {
		state = repository.StateExcluded
	}
	if err := c.repositories.SetTrackingState(ctx, stored.ID, state); err != nil {
		return fmt.Errorf("set tracking state: %w", err)
	}

	nextCheck := c.nextCheckAt(stored.TrackingTier, now)
	if err := c.repositories.MarkChecked(ctx, stored.ID, now, nextCheck,
		result.Validators.ETag, result.Validators.LastModified); err != nil {
		return fmt.Errorf("mark checked: %w", err)
	}

	if result.Redirected {
		c.log.InfoContext(ctx, "repository was renamed",
			slog.String("requested", payload.Owner+"/"+payload.Name),
			slog.String("canonical", repo.FullName),
		)
	}

	c.log.DebugContext(ctx, "collected repository snapshot",
		slog.String("repository", repo.FullName),
		slog.Int("stars", repo.Stars),
		slog.Int("forks", repo.Forks),
	)
	return nil
}

// handleFetchError applies the right policy for each classified failure.
func (c *Collector) handleFetchError(
	ctx context.Context,
	payload CollectPayload,
	existing repository.Repository,
	haveExisting bool,
	fetchErr error,
) error {
	now := c.clock.Now()
	kind := github.KindOf(fetchErr)

	switch kind {
	case github.ErrNotFound:
		if !haveExisting {
			// Never tracked and not on GitHub: nothing to record.
			return fmt.Errorf("repository %s/%s was not found", payload.Owner, payload.Name)
		}

		failures, err := c.repositories.MarkFailure(ctx, existing.ID,
			now.Add(c.tracking.ColdPollInterval), "not found on github")
		if err != nil {
			return fmt.Errorf("record 404: %w", err)
		}

		if failures >= missingConfirmations {
			// Confirmed gone. History is deliberately retained: the snapshots
			// already collected remain true statements about the past.
			if err := c.repositories.SetTrackingState(ctx, existing.ID, repository.StateMissing); err != nil {
				return fmt.Errorf("mark missing: %w", err)
			}
			c.log.InfoContext(ctx, "repository marked missing after repeated 404s",
				slog.String("repository", existing.FullName),
				slog.Int("consecutiveFailures", failures),
			)
			return nil
		}

		return fmt.Errorf("repository %s returned 404 (%d/%d confirmations)",
			existing.FullName, failures, missingConfirmations)

	case github.ErrRenamed:
		ghErr, _ := github.AsError(fetchErr)
		if ghErr == nil || ghErr.CanonicalName == "" {
			return fetchErr
		}

		// Re-queue against the canonical name. The upsert keys on GitHub's
		// stable numeric ID, so the existing row and all its history are
		// preserved and the old name is recorded as an alias.
		if _, err := c.jobs.Enqueue(ctx, repository.EnqueueInput{
			Kind:        repository.JobCollectRepository,
			Priority:    40,
			RunAfter:    now,
			MaxAttempts: 3,
			Payload: repository.CollectPayload{
				Owner:         ghErr.CanonicalOwner,
				Name:          ghErr.CanonicalName,
				UserRequested: payload.UserRequested,
			}.Encode(),
			DedupeKey: "collect:" + ghErr.CanonicalOwner + "/" + ghErr.CanonicalName,
		}); err != nil && !errors.Is(err, repository.ErrDuplicateJob) {
			return fmt.Errorf("re-queue renamed repository: %w", err)
		}

		c.log.InfoContext(ctx, "repository renamed; re-queued under its canonical name",
			slog.String("from", payload.Owner+"/"+payload.Name),
			slog.String("to", ghErr.CanonicalOwner+"/"+ghErr.CanonicalName),
		)
		return nil

	case github.ErrRateLimited, github.ErrSecondaryRateLimit:
		ghErr, _ := github.AsError(fetchErr)
		if ghErr != nil {
			c.budget.Observe(ghErr.RateLimit)
		}

		retryAt := now.Add(5 * time.Minute)
		if ghErr != nil && ghErr.RetryAfter > 0 {
			retryAt = now.Add(ghErr.RetryAfter)
		} else if ghErr != nil && !ghErr.RateLimit.ResetAt.IsZero() {
			retryAt = ghErr.RateLimit.ResetAt.Add(time.Second)
		}
		return Deferred(retryAt, "github rate limit reached")

	case github.ErrUnauthorized:
		// A bad token affects every job; retrying this one will not help, and
		// the error needs to be loud.
		c.log.ErrorContext(ctx, "github rejected the configured token; collection cannot continue")
		return fetchErr

	default:
		if haveExisting {
			if _, err := c.repositories.MarkFailure(ctx, existing.ID,
				now.Add(c.tracking.WarmPollInterval), string(kind)); err != nil {
				c.log.WarnContext(ctx, "could not record collection failure",
					slog.String("error", err.Error()))
			}
		}
		return fetchErr
	}
}

// nextCheckAt schedules the next poll for a tier.
func (c *Collector) nextCheckAt(tier repository.TrackingTier, now time.Time) time.Time {
	switch tier {
	case repository.TierHot:
		return now.Add(c.tracking.HotPollInterval)
	case repository.TierWarm:
		return now.Add(c.tracking.WarmPollInterval)
	default:
		return now.Add(c.tracking.ColdPollInterval)
	}
}

// name builds a reponame.Name for lookups. Validation happens again inside the
// GitHub client before any request is constructed.
func name(owner, repo string) reponame.Name {
	return reponame.Name{Owner: owner, Repository: repo}
}
