package demo

import (
	"strings"
	"testing"
	"time"

	"github.com/repometeor/repometeor/backend/internal/scoring"
)

var demoReference = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

func TestDatasetMeetsItsDocumentedShape(t *testing.T) {
	summary := Summarise(Build(demoReference))

	if summary.Repositories < 50 {
		t.Errorf("got %d repositories, want at least 50", summary.Repositories)
	}
	if len(summary.Languages) < 8 {
		t.Errorf("got %d languages (%v), want at least 8", len(summary.Languages), summary.Languages)
	}
	if len(summary.Topics) < 12 {
		t.Errorf("got %d topics, want at least 12", len(summary.Topics))
	}
	if summary.MinStars > 5 {
		t.Errorf("minimum star count = %d, want a repository at or below 5", summary.MinStars)
	}
	if summary.MaxStars < 50000 {
		t.Errorf("maximum star count = %d, want at least 50,000", summary.MaxStars)
	}
	if summary.Archived < 1 {
		t.Error("the dataset needs an archived repository")
	}
	if summary.Forks < 1 {
		t.Error("the dataset needs a forked repository")
	}
	if summary.Disabled < 1 {
		t.Error("the dataset needs a disabled repository")
	}
	if summary.NoRelease < 1 {
		t.Error("the dataset needs a repository with no releases")
	}
}

func TestDatasetCoversEveryPatternAndCoverage(t *testing.T) {
	summary := Summarise(Build(demoReference))

	for _, pattern := range []Pattern{
		PatternLinear, PatternAccelerating, PatternDecelerating,
		PatternFlat, PatternDeclining, PatternSpike,
	} {
		if summary.Patterns[pattern] == 0 {
			t.Errorf("no repository uses the %s pattern", pattern)
		}
	}
	for _, coverage := range []Coverage{CoverageFull, CoverageGap, CoverageShort, CoverageStale} {
		if summary.Coverages[coverage] == 0 {
			t.Errorf("no repository uses %s coverage", coverage)
		}
	}
}

func TestDatasetIsDeterministic(t *testing.T) {
	first := Build(demoReference)

	for trial := range 5 {
		second := Build(demoReference)

		if len(first.Repositories) != len(second.Repositories) {
			t.Fatalf("trial %d: repository count changed", trial)
		}
		for i := range first.Repositories {
			if first.Repositories[i].GitHubID != second.Repositories[i].GitHubID {
				t.Fatalf("trial %d: repository %d id changed", trial, i)
			}
			if len(first.Snapshots[i]) != len(second.Snapshots[i]) {
				t.Fatalf("trial %d: %s snapshot count changed from %d to %d",
					trial, first.Repositories[i].FullName(), len(first.Snapshots[i]), len(second.Snapshots[i]))
			}
			for j := range first.Snapshots[i] {
				a, b := first.Snapshots[i][j], second.Snapshots[i][j]
				if a.Stars != b.Stars || a.Forks != b.Forks || !a.At.Equal(b.At) {
					t.Fatalf("trial %d: %s snapshot %d differs: %+v vs %+v",
						trial, first.Repositories[i].FullName(), j, a, b)
				}
			}
		}
	}
}

func TestRepositoryIdentifiersAreUnique(t *testing.T) {
	dataset := Build(demoReference)

	ids := map[int64]string{}
	names := map[string]struct{}{}

	for _, repo := range dataset.Repositories {
		if existing, dup := ids[repo.GitHubID]; dup {
			t.Errorf("github id %d is used by both %s and %s", repo.GitHubID, existing, repo.FullName())
		}
		ids[repo.GitHubID] = repo.FullName()

		key := strings.ToLower(repo.FullName())
		if _, dup := names[key]; dup {
			t.Errorf("duplicate repository name %s", repo.FullName())
		}
		names[key] = struct{}{}
	}
}

func TestSnapshotsAreOrderedAndNonNegative(t *testing.T) {
	dataset := Build(demoReference)

	for i, snapshots := range dataset.Snapshots {
		name := dataset.Repositories[i].FullName()
		for j, s := range snapshots {
			if s.Stars < 0 || s.Forks < 0 || s.Issues < 0 {
				t.Errorf("%s snapshot %d has a negative count: %+v", name, j, s)
			}
			if s.At.After(demoReference) {
				t.Errorf("%s snapshot %d is in the future: %v", name, j, s.At)
			}
			if j > 0 && !s.At.After(snapshots[j-1].At) {
				t.Errorf("%s snapshot %d is not after its predecessor", name, j)
			}
			if s.At.Location() != time.UTC {
				t.Errorf("%s snapshot %d is not UTC", name, j)
			}
		}
	}
}

func TestSnapshotEndpointsMatchTheDeclaredRange(t *testing.T) {
	dataset := Build(demoReference)

	for i, repo := range dataset.Repositories {
		snapshots := dataset.Snapshots[i]
		if len(snapshots) == 0 {
			t.Errorf("%s has no snapshots", repo.FullName())
			continue
		}
		// Only full coverage runs the whole period, so only it is guaranteed to
		// hit both declared endpoints.
		if repo.Coverage != CoverageFull {
			continue
		}
		if got := snapshots[0].Stars; got != repo.StartStars {
			t.Errorf("%s first snapshot = %d stars, want %d", repo.FullName(), got, repo.StartStars)
		}
		if got := snapshots[len(snapshots)-1].Stars; got != repo.EndStars {
			t.Errorf("%s last snapshot = %d stars, want %d", repo.FullName(), got, repo.EndStars)
		}
	}
}

func TestShortCoverageProducesOnlyRecentHistory(t *testing.T) {
	dataset := Build(demoReference)

	for i, repo := range dataset.Repositories {
		if repo.Coverage != CoverageShort {
			continue
		}
		snapshots := dataset.Snapshots[i]
		if len(snapshots) == 0 {
			t.Fatalf("%s has no snapshots", repo.FullName())
		}
		span := snapshots[len(snapshots)-1].At.Sub(snapshots[0].At)
		if span > 6*time.Hour {
			t.Errorf("%s spans %v, want at most 6h for short coverage", repo.FullName(), span)
		}
	}
}

func TestStaleCoverageStopsWellBeforeNow(t *testing.T) {
	dataset := Build(demoReference)

	for i, repo := range dataset.Repositories {
		if repo.Coverage != CoverageStale {
			continue
		}
		snapshots := dataset.Snapshots[i]
		newest := snapshots[len(snapshots)-1].At

		// The newest snapshot must be older than the 24h window's staleness
		// allowance, but recent enough that snapshots still exist inside the
		// window — otherwise the repository would be excluded for having no
		// data at all rather than for having stale data.
		age := demoReference.Sub(newest)
		if age <= scoring.Window24h.StaleAfter {
			t.Errorf("%s newest snapshot is %v old, want it beyond the %v staleness allowance",
				repo.FullName(), age, scoring.Window24h.StaleAfter)
		}
		if age >= scoring.Window24h.Duration {
			t.Errorf("%s newest snapshot is %v old, want it inside the %v window so stale_data applies",
				repo.FullName(), age, scoring.Window24h.Duration)
		}
	}
}

func TestGapCoverageProducesAMeasurableGap(t *testing.T) {
	dataset := Build(demoReference)

	for i, repo := range dataset.Repositories {
		if repo.Coverage != CoverageGap {
			continue
		}
		snapshots := dataset.Snapshots[i]

		var largest time.Duration
		for j := 1; j < len(snapshots); j++ {
			if gap := snapshots[j].At.Sub(snapshots[j-1].At); gap > largest {
				largest = gap
			}
		}
		if largest < 8*time.Hour {
			t.Errorf("%s largest gap = %v, want a gap big enough to flag", repo.FullName(), largest)
		}
	}
}

// The dataset exists to demonstrate the product, so its behaviour under the
// real scoring engine is part of its contract.

func toScoringInputs(d Dataset) []scoring.Input {
	inputs := make([]scoring.Input, 0, len(d.Repositories))
	for i, repo := range d.Repositories {
		snapshots := make([]scoring.Snapshot, 0, len(d.Snapshots[i]))
		for _, s := range d.Snapshots[i] {
			snapshots = append(snapshots, scoring.Snapshot{At: s.At, Stars: s.Stars, Forks: s.Forks})
		}

		pushed := d.ReferenceTime.Add(-time.Duration(repo.PushedHoursAgo) * time.Hour)
		in := scoring.Input{
			RepositoryID: int64(i + 1),
			FullName:     repo.FullName(),
			Description:  repo.Description,
			CreatedAt:    d.ReferenceTime.Add(-time.Duration(repo.AgeDays) * 24 * time.Hour),
			PushedAt:     &pushed,
			IsFork:       repo.IsFork,
			IsArchived:   repo.IsArchived,
			IsDisabled:   repo.IsDisabled,
			Active:       true,
			FirstSeenAt:  d.ReferenceTime.Add(-Period),
			Snapshots:    snapshots,
		}
		if repo.ReleaseDaysAgo >= 0 {
			release := d.ReferenceTime.Add(-time.Duration(repo.ReleaseDaysAgo) * 24 * time.Hour)
			in.LatestReleaseAt = &release
		}
		if repo.Coverage == CoverageShort {
			in.FirstSeenAt = d.ReferenceTime.Add(-5 * time.Hour)
		}
		inputs = append(inputs, in)
	}
	return inputs
}

func TestDatasetProducesRankingsInEveryWindow(t *testing.T) {
	inputs := toScoringInputs(Build(demoReference))

	for _, w := range scoring.Windows {
		out := scoring.Run(demoReference, w, inputs, scoring.DefaultOptions())
		if len(out.Ranked) < 10 {
			t.Errorf("window %s ranked only %d repositories, want at least 10 for a useful demo",
				w.Name, len(out.Ranked))
		}
	}
}

func TestDatasetPopulatesEveryCollection(t *testing.T) {
	inputs := toScoringInputs(Build(demoReference))
	out := scoring.Run(demoReference, scoring.Window24h, inputs, scoring.DefaultOptions())

	counts := map[string]int{}
	for _, r := range out.All() {
		for _, collection := range r.Collections {
			counts[collection]++
		}
	}

	for _, collection := range []string{
		scoring.CollectionDaily,
		scoring.CollectionHiddenGems,
		scoring.CollectionNewEntrants,
		scoring.CollectionEstablished,
	} {
		if counts[collection] == 0 {
			t.Errorf("collection %q is empty in the demo dataset; the first run would show nothing", collection)
		}
	}
}

func TestDatasetExcludesTheStructurallyIneligible(t *testing.T) {
	dataset := Build(demoReference)
	inputs := toScoringInputs(dataset)
	out := scoring.Run(demoReference, scoring.Window24h, inputs, scoring.DefaultOptions())

	expectations := map[string]string{
		"museum-piece/archived-toolkit":  scoring.ReasonArchived,
		"copycat-dev/vessel":             scoring.ReasonFork,
		"shuttered/disabled-service":     scoring.ReasonDisabled,
		"sparse-docs/nodesc":             scoring.ReasonNoDescription,
		"tinystart/two-stars":            scoring.ReasonBelowMinStars,
		"twilight-works/abandoned-cache": scoring.ReasonStaleData,
	}

	byName := map[string]int64{}
	for i, repo := range dataset.Repositories {
		byName[repo.FullName()] = int64(i + 1)
	}

	for name, wantReason := range expectations {
		id, ok := byName[name]
		if !ok {
			t.Fatalf("demo dataset is missing %s", name)
		}
		reason, excluded := out.Excluded[id]
		if !excluded {
			t.Errorf("%s should be excluded (%s) but was ranked", name, wantReason)
			continue
		}
		if reason != wantReason {
			t.Errorf("%s excluded for %q, want %q", name, reason, wantReason)
		}
	}
}

func TestDatasetDemonstratesSmallBeatingLarge(t *testing.T) {
	dataset := Build(demoReference)
	inputs := toScoringInputs(dataset)
	out := scoring.Run(demoReference, scoring.Window24h, inputs, scoring.DefaultOptions())

	positions := map[string]int{}
	for i, r := range out.Ranked {
		positions[r.FullName] = i
	}

	small, okSmall := positions["driftwood/schema-drift"]
	large, okLarge := positions["stonebridge/atlas-ui"]
	if !okSmall || !okLarge {
		t.Fatalf("expected both repositories to rank; got %v / %v", okSmall, okLarge)
	}
	if small >= large {
		t.Fatalf("the fast 148-star project ranked %d, behind the slow 24,000-star project at %d;"+
			" the demo must show total stars not dominating", small, large)
	}
}

func TestDatasetProducesAnUnusualGrowthFlag(t *testing.T) {
	dataset := Build(demoReference)
	inputs := toScoringInputs(dataset)
	out := scoring.Run(demoReference, scoring.Window24h, inputs, scoring.DefaultOptions())

	var found bool
	for _, r := range out.All() {
		if r.FullName != "brightfield/sudden-spark" {
			continue
		}
		found = true
		if len(r.AnomalyFlags) == 0 {
			t.Errorf("%s carries no anomaly flags; the demo should show what one looks like", r.FullName)
		}
	}
	if !found {
		t.Fatal("brightfield/sudden-spark was not scored at all")
	}
}

func TestEveryRepositoryDocumentsWhatItDemonstrates(t *testing.T) {
	for _, repo := range Build(demoReference).Repositories {
		if strings.TrimSpace(repo.Note) == "" {
			t.Errorf("%s has no note explaining why it is in the dataset", repo.FullName())
		}
	}
}

func TestDemoDataUsesNoExternalAssets(t *testing.T) {
	// A demo page must not make third-party requests, and must not reference a
	// real domain that could later resolve to something unexpected.
	for _, repo := range Build(demoReference).Repositories {
		if repo.AvatarURL() != "" {
			t.Errorf("%s has an avatar URL; the demo dataset must not load external images", repo.FullName())
		}
		if repo.HomepageURL != "" && !strings.Contains(repo.HomepageURL, ".invalid") {
			t.Errorf("%s homepage %q should use the reserved .invalid TLD", repo.FullName(), repo.HomepageURL)
		}
	}
}
