package demo

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/repometeor/repometeor/backend/internal/clock"
	"github.com/repometeor/repometeor/backend/internal/repository"
)

// SeedResult reports what a seed run wrote.
type SeedResult struct {
	Repositories  int
	Snapshots     int
	Duration      time.Duration
	ReferenceTime time.Time
}

// Seeder writes the demo dataset into PostgreSQL.
type Seeder struct {
	repositories *repository.Repositories
	snapshots    *repository.Snapshots
	clock        clock.Clock
	log          *slog.Logger
}

// NewSeeder constructs a seeder.
func NewSeeder(repos *repository.Repositories, snaps *repository.Snapshots, c clock.Clock, log *slog.Logger) *Seeder {
	return &Seeder{repositories: repos, snapshots: snaps, clock: c, log: log}
}

// Seed writes the dataset, generated against the supplied reference time.
//
// The whole operation is idempotent: repositories are upserted by their
// fictional GitHub ID and snapshots by (repository, bucket), so running the
// seeder twice produces exactly the same rows rather than duplicates. That
// matters because Docker Compose runs it on every start.
//
// Every snapshot is written with source='demo_seed'. Nothing in the pipeline
// can subsequently mistake this data for a real observation.
func (s *Seeder) Seed(ctx context.Context, referenceTime time.Time, bucketInterval time.Duration) (SeedResult, error) {
	start := time.Now()

	if referenceTime.IsZero() {
		referenceTime = s.clock.Now()
	}
	dataset := Build(referenceTime)

	result := SeedResult{ReferenceTime: dataset.ReferenceTime}

	for i, demoRepo := range dataset.Repositories {
		stored, err := s.repositories.Upsert(ctx, repository.UpsertRepositoryInput{
			GitHubID:         demoRepo.GitHubID,
			NodeID:           fmt.Sprintf("R_demo_%d", demoRepo.GitHubID),
			OwnerLogin:       demoRepo.Owner,
			Name:             demoRepo.Name,
			FullName:         demoRepo.FullName(),
			Description:      demoRepo.Description,
			HTMLURL:          demoRepo.HTMLURL(),
			HomepageURL:      demoRepo.HomepageURL,
			AvatarURL:        demoRepo.AvatarURL(),
			DefaultBranch:    demoRepo.DefaultBranch,
			PrimaryLanguage:  demoRepo.Language,
			Topics:           demoRepo.Topics,
			LicenseSPDX:      demoRepo.LicenseSPDX,
			IsFork:           demoRepo.IsFork,
			IsArchived:       demoRepo.IsArchived,
			IsDisabled:       demoRepo.IsDisabled,
			IsTemplate:       demoRepo.IsTemplate,
			CreatedAt:        dataset.ReferenceTime.Add(-time.Duration(demoRepo.AgeDays) * 24 * time.Hour),
			GitHubUpdatedAt:  timePtr(dataset.ReferenceTime.Add(-time.Duration(demoRepo.PushedHoursAgo) * time.Hour)),
			PushedAt:         timePtr(dataset.ReferenceTime.Add(-time.Duration(demoRepo.PushedHoursAgo) * time.Hour)),
			LatestReleaseAt:  releaseTime(dataset.ReferenceTime, demoRepo.ReleaseDaysAgo),
			LatestReleaseTag: releaseTag(demoRepo.ReleaseDaysAgo),
		})
		if err != nil {
			return result, fmt.Errorf("seed repository %s: %w", demoRepo.FullName(), err)
		}
		result.Repositories++

		// Structurally ineligible repositories are still tracked and still have
		// history; they are simply never ranked. Marking them excluded here
		// mirrors what the live collector would do.
		state := repository.StateActive
		if demoRepo.IsArchived || demoRepo.IsDisabled || demoRepo.IsFork {
			state = repository.StateExcluded
		}
		if err := s.repositories.SetTrackingState(ctx, stored.ID, state); err != nil {
			return result, fmt.Errorf("set tracking state for %s: %w", demoRepo.FullName(), err)
		}

		// Tracking must appear to have begun when this repository's history
		// begins, not when the seeder ran. Otherwise every demo repository
		// would carry the "recently discovered" flag and a depressed
		// confidence, which would misrepresent what the rankings look like in
		// a real deployment.
		if snapshots := dataset.Snapshots[i]; len(snapshots) > 0 {
			if err := s.repositories.SetFirstSeenAt(ctx, stored.ID, snapshots[0].At); err != nil {
				return result, fmt.Errorf("set first seen for %s: %w", demoRepo.FullName(), err)
			}
		}

		for _, snapshot := range dataset.Snapshots[i] {
			if _, err := s.snapshots.Upsert(ctx, repository.Snapshot{
				RepositoryID:    stored.ID,
				CapturedAt:      snapshot.At,
				BucketStart:     clock.Bucket(snapshot.At, bucketInterval),
				Stars:           snapshot.Stars,
				Forks:           snapshot.Forks,
				OpenIssues:      snapshot.Issues,
				LatestReleaseAt: releaseTime(dataset.ReferenceTime, demoRepo.ReleaseDaysAgo),
				Source:          repository.SourceDemoSeed,
			}); err != nil {
				return result, fmt.Errorf("seed snapshot for %s: %w", demoRepo.FullName(), err)
			}
			result.Snapshots++
		}

		// A freshly discovered repository must look freshly discovered, so that
		// the New Entrants collection and the confidence penalty both engage.
		if demoRepo.Coverage == CoverageShort {
			continue
		}

		if err := s.repositories.MarkChecked(ctx, stored.ID,
			dataset.ReferenceTime, dataset.ReferenceTime.Add(time.Hour), "", ""); err != nil {
			return result, fmt.Errorf("mark checked for %s: %w", demoRepo.FullName(), err)
		}
	}

	result.Duration = time.Since(start)

	s.log.InfoContext(ctx, "seeded demo dataset",
		slog.Int("repositories", result.Repositories),
		slog.Int("snapshots", result.Snapshots),
		slog.String("referenceTime", result.ReferenceTime.Format(time.RFC3339)),
		slog.Int64("durationMs", result.Duration.Milliseconds()),
	)
	return result, nil
}

func timePtr(t time.Time) *time.Time {
	u := t.UTC()
	return &u
}

func releaseTime(reference time.Time, daysAgo int) *time.Time {
	if daysAgo < 0 {
		return nil
	}
	return timePtr(reference.Add(-time.Duration(daysAgo) * 24 * time.Hour))
}

func releaseTag(daysAgo int) string {
	if daysAgo < 0 {
		return ""
	}
	return "v1.0.0"
}
