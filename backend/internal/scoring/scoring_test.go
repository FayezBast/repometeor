package scoring

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)

// buildInput constructs a repository whose stars grow linearly from `start` to
// `end` across `points` hourly snapshots ending at refTime.
func buildInput(id int64, name string, start, end, points int) Input {
	snapshots := make([]Snapshot, 0, points)
	for i := range points {
		progress := float64(i) / float64(max(points-1, 1))
		stars := start + int(math.Round(float64(end-start)*progress))
		snapshots = append(snapshots, Snapshot{
			At:    refTime.Add(-time.Duration(points-1-i) * time.Hour),
			Stars: stars,
			Forks: stars / 10,
		})
	}

	pushed := refTime.Add(-2 * time.Hour)
	return Input{
		RepositoryID: id,
		FullName:     name,
		Description:  "A fictional repository used to exercise scoring",
		CreatedAt:    refTime.Add(-365 * 24 * time.Hour),
		PushedAt:     &pushed,
		Active:       true,
		FirstSeenAt:  refTime.Add(-90 * 24 * time.Hour),
		Snapshots:    snapshots,
	}
}

func cohort() []Input {
	return []Input{
		buildInput(1, "orbit-labs/signal-kit", 900, 1240, 25),    // strong growth
		buildInput(2, "northstar-dev/terminal-ai", 540, 680, 25), // moderate
		buildInput(3, "comet-tools/local-indexer", 140, 213, 25), // small but fast
		buildInput(4, "steady-co/slow-mover", 5000, 5010, 25),    // large, slow
		buildInput(5, "quiet-labs/flat-line", 300, 300, 25),      // no growth
	}
}

func TestRunProducesRankedResults(t *testing.T) {
	out := Run(refTime, Window24h, cohort(), DefaultOptions())

	if len(out.Ranked) == 0 {
		t.Fatal("no repositories were ranked")
	}
	if out.Window != Window24h.Name {
		t.Errorf("Window = %q, want 24h", out.Window)
	}
	if out.FormulaVersion != FormulaVersion {
		t.Errorf("FormulaVersion = %q, want %q", out.FormulaVersion, FormulaVersion)
	}

	for i, r := range out.Ranked {
		if r.Rank != i+1 {
			t.Errorf("Ranked[%d].Rank = %d, want %d", i, r.Rank, i+1)
		}
		if r.Score < 0 || r.Score > 100 {
			t.Errorf("%s score = %v, want it inside [0, 100]", r.FullName, r.Score)
		}
		if i > 0 && out.Ranked[i-1].Score < r.Score {
			t.Errorf("results are not in descending score order at index %d", i)
		}
	}
}

func TestRunIsDeterministic(t *testing.T) {
	first := Run(refTime, Window24h, cohort(), DefaultOptions())

	for trial := range 5 {
		// Reverse the input order to prove ordering does not leak into results.
		inputs := cohort()
		slices.Reverse(inputs)

		second := Run(refTime, Window24h, inputs, DefaultOptions())
		if len(first.Ranked) != len(second.Ranked) {
			t.Fatalf("trial %d: ranked count %d != %d", trial, len(second.Ranked), len(first.Ranked))
		}
		for i := range first.Ranked {
			a, b := first.Ranked[i], second.Ranked[i]
			if a.RepositoryID != b.RepositoryID || a.Score != b.Score || a.Rank != b.Rank {
				t.Fatalf("trial %d position %d: got %d/%v/%d, want %d/%v/%d",
					trial, i, b.RepositoryID, b.Score, b.Rank, a.RepositoryID, a.Score, a.Rank)
			}
		}
	}
}

func TestScoreIsClampedToRange(t *testing.T) {
	// An extreme outlier must not push any score above 100.
	inputs := cohort()
	inputs = append(inputs, buildInput(99, "extreme/outlier", 10, 5_000_000, 25))

	out := Run(refTime, Window24h, inputs, DefaultOptions())
	for _, r := range out.All() {
		if r.Score < 0 || r.Score > 100 {
			t.Fatalf("%s score = %v, want it inside [0, 100]", r.FullName, r.Score)
		}
	}
}

func TestSmallFastRepositoryOutranksLargeSlowOne(t *testing.T) {
	// This is the product's central promise: total star count must not dominate.
	// comet-tools grows 73 stars on a base of 140; steady-co grows 10 on 5000.
	out := Run(refTime, Window24h, cohort(), DefaultOptions())

	positions := map[string]int{}
	for i, r := range out.Ranked {
		positions[r.FullName] = i
	}

	small, okSmall := positions["comet-tools/local-indexer"]
	large, okLarge := positions["steady-co/slow-mover"]
	if !okSmall || !okLarge {
		t.Fatalf("expected both repositories to be ranked, got %v", positions)
	}
	if small >= large {
		t.Fatalf("the small fast repository ranked %d, behind the large slow one at %d", small, large)
	}
}

func TestZeroGrowthRepositoryRanksLast(t *testing.T) {
	out := Run(refTime, Window24h, cohort(), DefaultOptions())

	last := out.Ranked[len(out.Ranked)-1]
	if last.FullName != "quiet-labs/flat-line" {
		t.Fatalf("last place = %s, want quiet-labs/flat-line", last.FullName)
	}
	if last.StarDelta != 0 {
		t.Errorf("StarDelta = %d, want 0", last.StarDelta)
	}
}

func TestForkIsExcluded(t *testing.T) {
	inputs := cohort()
	fork := buildInput(10, "someone/forked-project", 100, 900, 25)
	fork.IsFork = true
	inputs = append(inputs, fork)

	out := Run(refTime, Window24h, inputs, DefaultOptions())
	assertExcluded(t, out, 10, ReasonFork)
}

func TestArchivedIsExcluded(t *testing.T) {
	inputs := cohort()
	archived := buildInput(11, "old-labs/archived-tool", 100, 900, 25)
	archived.IsArchived = true
	inputs = append(inputs, archived)

	out := Run(refTime, Window24h, inputs, DefaultOptions())
	assertExcluded(t, out, 11, ReasonArchived)
}

func TestDisabledAndInactiveAreExcluded(t *testing.T) {
	inputs := cohort()

	disabled := buildInput(12, "dmca/disabled-repo", 100, 900, 25)
	disabled.IsDisabled = true

	inactive := buildInput(13, "gone/missing-repo", 100, 900, 25)
	inactive.Active = false

	inputs = append(inputs, disabled, inactive)

	out := Run(refTime, Window24h, inputs, DefaultOptions())
	assertExcluded(t, out, 12, ReasonDisabled)
	assertExcluded(t, out, 13, ReasonInactive)
}

func TestBelowMinimumStarsIsExcluded(t *testing.T) {
	inputs := cohort()
	tiny := buildInput(14, "tiny/starter", 1, 4, 25)
	inputs = append(inputs, tiny)

	out := Run(refTime, Window24h, inputs, DefaultOptions())
	assertExcluded(t, out, 14, ReasonBelowMinStars)
}

func TestMissingDescriptionIsExcluded(t *testing.T) {
	inputs := cohort()
	blank := buildInput(15, "no-desc/project", 100, 900, 25)
	blank.Description = "   "
	inputs = append(inputs, blank)

	out := Run(refTime, Window24h, inputs, DefaultOptions())
	assertExcluded(t, out, 15, ReasonNoDescription)
}

func TestStaleDataIsExcluded(t *testing.T) {
	inputs := cohort()

	stale := buildInput(16, "stale/collector", 100, 900, 25)
	// Shift every snapshot 12h into the past: beyond the 24h window's 8h
	// staleness allowance.
	for i := range stale.Snapshots {
		stale.Snapshots[i].At = stale.Snapshots[i].At.Add(-12 * time.Hour)
	}
	inputs = append(inputs, stale)

	out := Run(refTime, Window24h, inputs, DefaultOptions())
	assertExcluded(t, out, 16, ReasonStaleData)
}

func TestShortHistoryBecomesProvisional(t *testing.T) {
	// Six hours of history against a 24h window: measurable movement, but far
	// short of the 18h minimum.
	newcomer := buildInput(20, "brand-new/rocket", 40, 95, 7)
	newcomer.FirstSeenAt = refTime.Add(-6 * time.Hour)

	inputs := append(cohort(), newcomer)
	out := Run(refTime, Window24h, inputs, DefaultOptions())

	var found *Result
	for i := range out.Provisional {
		if out.Provisional[i].RepositoryID == 20 {
			found = &out.Provisional[i]
		}
	}
	if found == nil {
		t.Fatalf("repository 20 is not provisional; excluded=%v", out.Excluded)
	}
	if found.Eligibility != EligibleProvisional {
		t.Errorf("Eligibility = %q, want provisional", found.Eligibility)
	}
	if !slices.Contains(found.Collections, CollectionNewEntrants) {
		t.Errorf("Collections = %v, want it to include %s", found.Collections, CollectionNewEntrants)
	}
	if found.ConfidenceLabel != "low" {
		t.Errorf("ConfidenceLabel = %q, want low for a provisional entry", found.ConfidenceLabel)
	}

	// Provisional entries must never appear in the main leaderboard.
	for _, r := range out.Ranked {
		if r.RepositoryID == 20 {
			t.Fatal("a provisional repository leaked into the ranked leaderboard")
		}
	}
}

func TestShortHistoryWithoutMovementIsExcluded(t *testing.T) {
	flat := buildInput(21, "brand-new/quiet", 40, 40, 5)
	flat.FirstSeenAt = refTime.Add(-4 * time.Hour)

	out := Run(refTime, Window24h, append(cohort(), flat), DefaultOptions())
	assertExcluded(t, out, 21, ReasonNoMeasuredChange)
}

func TestProvisionalDoesNotDistortTheCohort(t *testing.T) {
	// Adding many provisional newcomers must not shift the established ranking.
	base := Run(refTime, Window24h, cohort(), DefaultOptions())

	inputs := cohort()
	for i := range 20 {
		newcomer := buildInput(int64(100+i), "flood/newcomer", 50, 5000, 5)
		newcomer.FirstSeenAt = refTime.Add(-4 * time.Hour)
		inputs = append(inputs, newcomer)
	}
	flooded := Run(refTime, Window24h, inputs, DefaultOptions())

	if len(base.Ranked) != len(flooded.Ranked) {
		t.Fatalf("ranked count changed from %d to %d", len(base.Ranked), len(flooded.Ranked))
	}
	for i := range base.Ranked {
		if base.Ranked[i].RepositoryID != flooded.Ranked[i].RepositoryID {
			t.Fatalf("position %d changed from %d to %d; provisional entries must not enter the cohort",
				i, base.Ranked[i].RepositoryID, flooded.Ranked[i].RepositoryID)
		}
		if base.Ranked[i].Score != flooded.Ranked[i].Score {
			t.Fatalf("%s score changed from %v to %v",
				base.Ranked[i].FullName, base.Ranked[i].Score, flooded.Ranked[i].Score)
		}
	}
}

func TestHiddenGemsClassification(t *testing.T) {
	out := Run(refTime, Window24h, cohort(), DefaultOptions())

	for _, r := range out.Ranked {
		inCollection := slices.Contains(r.Collections, CollectionHiddenGems)
		qualifies := r.StarsTotal >= HiddenGemMinStars &&
			r.StarsTotal <= HiddenGemMaxStars &&
			r.Confidence >= ConfidenceMediumThreshold &&
			r.StarDelta > 0

		if inCollection != qualifies {
			t.Errorf("%s (stars=%d confidence=%.2f delta=%d): hidden-gems=%v, want %v",
				r.FullName, r.StarsTotal, r.Confidence, r.StarDelta, inCollection, qualifies)
		}
	}

	// comet-tools sits at 213 stars with real growth, so it must qualify.
	for _, r := range out.Ranked {
		if r.FullName == "comet-tools/local-indexer" && !slices.Contains(r.Collections, CollectionHiddenGems) {
			t.Errorf("comet-tools/local-indexer should be a hidden gem; collections=%v confidence=%v",
				r.Collections, r.Confidence)
		}
	}
}

func TestHiddenGemExcludesRepositoriesAboveTheCeiling(t *testing.T) {
	out := Run(refTime, Window24h, cohort(), DefaultOptions())
	for _, r := range out.Ranked {
		if r.StarsTotal > HiddenGemMaxStars && slices.Contains(r.Collections, CollectionHiddenGems) {
			t.Errorf("%s has %d stars, above the %d ceiling, but is a hidden gem",
				r.FullName, r.StarsTotal, HiddenGemMaxStars)
		}
	}
}

func TestEstablishedMoversClassification(t *testing.T) {
	// A large repository with strong absolute velocity.
	big := buildInput(30, "mega-corp/framework", 40000, 42000, 25)
	out := Run(refTime, Window24h, append(cohort(), big), DefaultOptions())

	var found *Result
	for i := range out.Ranked {
		if out.Ranked[i].RepositoryID == 30 {
			found = &out.Ranked[i]
		}
	}
	if found == nil {
		t.Fatal("the large repository was not ranked")
	}
	if !slices.Contains(found.Collections, CollectionEstablished) {
		t.Errorf("collections = %v, want it to include %s (stars=%d velocityPercentile=%.2f confidence=%.2f)",
			found.Collections, CollectionEstablished, found.StarsTotal, found.Percentiles.StarVelocity, found.Confidence)
	}
	// It is far too large to be a hidden gem.
	if slices.Contains(found.Collections, CollectionHiddenGems) {
		t.Error("a 42,000-star repository must not be a hidden gem")
	}
}

func TestPrimaryCollectionMatchesWindow(t *testing.T) {
	tests := map[Window]string{
		Window6h:  CollectionRising,
		Window24h: CollectionDaily,
		Window7d:  CollectionWeekly,
	}
	for w, want := range tests {
		inputs := cohort()
		if w.Name == Window7d.Name {
			// Give every repository a week of history for the 7d window.
			inputs = weekLongCohort()
		}
		out := Run(refTime, w, inputs, DefaultOptions())
		if len(out.Ranked) == 0 {
			t.Fatalf("%s: nothing ranked; excluded=%v", w.Name, out.Excluded)
		}
		for _, r := range out.Ranked {
			if !slices.Contains(r.Collections, want) {
				t.Errorf("%s in window %s: collections = %v, want it to include %s",
					r.FullName, w.Name, r.Collections, want)
			}
		}
	}
}

func weekLongCohort() []Input {
	// 8 daily snapshots covering more than the 7d window.
	build := func(id int64, name string, start, end int) Input {
		in := buildInput(id, name, start, end, 8)
		for i := range in.Snapshots {
			in.Snapshots[i].At = refTime.Add(-time.Duration(7-i) * 24 * time.Hour)
		}
		return in
	}
	return []Input{
		build(1, "orbit-labs/signal-kit", 700, 1240),
		build(2, "northstar-dev/terminal-ai", 400, 680),
		build(3, "comet-tools/local-indexer", 90, 213),
		build(4, "steady-co/slow-mover", 4900, 5010),
	}
}

func TestConfidenceReflectsObservationQuality(t *testing.T) {
	full := buildInput(40, "well-observed/repo", 100, 400, 25)
	sparse := buildInput(41, "sparsely-observed/repo", 100, 400, 3)
	// Spread the three snapshots across the whole 24h window.
	sparse.Snapshots[0].At = refTime.Add(-24 * time.Hour)
	sparse.Snapshots[1].At = refTime.Add(-12 * time.Hour)
	sparse.Snapshots[2].At = refTime

	out := Run(refTime, Window24h, []Input{full, sparse}, DefaultOptions())

	byID := map[int64]Result{}
	for _, r := range out.Ranked {
		byID[r.RepositoryID] = r
	}
	if len(byID) != 2 {
		t.Fatalf("expected both to rank, got %d; excluded=%v", len(byID), out.Excluded)
	}
	if byID[40].Confidence <= byID[41].Confidence {
		t.Errorf("densely observed confidence %v should exceed sparse %v",
			byID[40].Confidence, byID[41].Confidence)
	}
}

func TestConfidenceIsBounded(t *testing.T) {
	out := Run(refTime, Window24h, cohort(), DefaultOptions())
	for _, r := range out.All() {
		if r.Confidence < 0 || r.Confidence > 1 {
			t.Errorf("%s confidence = %v, want it inside [0, 1]", r.FullName, r.Confidence)
		}
	}
}

func TestConfidenceLabels(t *testing.T) {
	tests := map[float64]string{
		1.00: "high",
		0.85: "high",
		0.84: "medium",
		0.60: "medium",
		0.59: "low",
		0.00: "low",
	}
	for value, want := range tests {
		if got := ConfidenceLabel(value); got != want {
			t.Errorf("ConfidenceLabel(%v) = %q, want %q", value, got, want)
		}
	}
}

func TestConfidenceBounds(t *testing.T) {
	for _, label := range []string{"high", "medium", "low"} {
		low, high, ok := ConfidenceBounds(label)
		if !ok {
			t.Fatalf("ConfidenceBounds(%q) reported not ok", label)
		}
		if low >= high {
			t.Errorf("ConfidenceBounds(%q) = (%v, %v), want a non-empty range", label, low, high)
		}
	}
	if _, _, ok := ConfidenceBounds("excellent"); ok {
		t.Error("an unknown confidence label should be rejected")
	}
}

func TestRecentlyDiscoveredReducesConfidence(t *testing.T) {
	established := buildInput(50, "known/repo", 100, 400, 25)
	fresh := buildInput(51, "just-found/repo", 100, 400, 25)
	fresh.FirstSeenAt = refTime.Add(-3 * time.Hour)

	out := Run(refTime, Window24h, []Input{established, fresh}, DefaultOptions())

	byID := map[int64]Result{}
	for _, r := range out.Ranked {
		byID[r.RepositoryID] = r
	}
	if byID[50].Confidence <= byID[51].Confidence {
		t.Errorf("established confidence %v should exceed recently discovered %v",
			byID[50].Confidence, byID[51].Confidence)
	}
	if !slices.Contains(byID[51].AnomalyFlags, FlagRecentlyDiscovered) {
		t.Errorf("flags = %v, want %s", byID[51].AnomalyFlags, FlagRecentlyDiscovered)
	}
}

func TestUnusualStarForkRatioFlag(t *testing.T) {
	// Big star gain, no forks, no recent push.
	unusual := buildInput(60, "sudden/spike", 100, 400, 25)
	for i := range unusual.Snapshots {
		unusual.Snapshots[i].Forks = 5
	}
	oldPush := refTime.Add(-200 * 24 * time.Hour)
	unusual.PushedAt = &oldPush

	out := Run(refTime, Window24h, append(cohort(), unusual), DefaultOptions())

	var found *Result
	for i := range out.Ranked {
		if out.Ranked[i].RepositoryID == 60 {
			found = &out.Ranked[i]
		}
	}
	if found == nil {
		t.Fatalf("repository 60 was not ranked; excluded=%v", out.Excluded)
	}
	if !slices.Contains(found.AnomalyFlags, FlagUnusualStarForkRatio) {
		t.Errorf("flags = %v, want %s", found.AnomalyFlags, FlagUnusualStarForkRatio)
	}
	if !slices.Contains(found.AnomalyFlags, FlagStaleRepositorySpike) {
		t.Errorf("flags = %v, want %s", found.AnomalyFlags, FlagStaleRepositorySpike)
	}
}

func TestAnomalyFlagsDoNotRemoveARepository(t *testing.T) {
	unusual := buildInput(61, "flagged/but-present", 100, 400, 25)
	for i := range unusual.Snapshots {
		unusual.Snapshots[i].Forks = 5
	}
	oldPush := refTime.Add(-200 * 24 * time.Hour)
	unusual.PushedAt = &oldPush

	out := Run(refTime, Window24h, append(cohort(), unusual), DefaultOptions())

	if _, excluded := out.Excluded[61]; excluded {
		t.Fatal("a flagged repository must stay in the rankings; flags affect confidence, not eligibility")
	}
}

func TestSnapshotGapFlag(t *testing.T) {
	gapped := buildInput(62, "gappy/collector", 100, 400, 25)
	// Remove the middle stretch to create a 12h hole.
	var kept []Snapshot
	for _, s := range gapped.Snapshots {
		age := refTime.Sub(s.At)
		if age > 6*time.Hour && age < 18*time.Hour {
			continue
		}
		kept = append(kept, s)
	}
	gapped.Snapshots = kept

	out := Run(refTime, Window24h, append(cohort(), gapped), DefaultOptions())

	var found *Result
	for i := range out.Ranked {
		if out.Ranked[i].RepositoryID == 62 {
			found = &out.Ranked[i]
		}
	}
	if found == nil {
		t.Fatalf("repository 62 was not ranked; excluded=%v", out.Excluded)
	}
	if !slices.Contains(found.AnomalyFlags, FlagSnapshotGap) {
		t.Errorf("flags = %v, want %s", found.AnomalyFlags, FlagSnapshotGap)
	}
}

func TestWeightsSumToOne(t *testing.T) {
	if got := DefaultWeights.Sum(); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("DefaultWeights.Sum() = %v, want exactly 1.0", got)
	}
}

func TestConfidenceWeightsSumToOne(t *testing.T) {
	sum := confidenceCoverageWeight + confidenceDensityWeight + confidenceMidpointWeight + confidenceRecencyWeight
	if math.Abs(sum-1.0) > 1e-9 {
		t.Fatalf("confidence weights sum to %v, want exactly 1.0", sum)
	}
}

func TestFormulaMatchesDocumentedWeights(t *testing.T) {
	// Recompute one result by hand from its published percentiles and
	// confidence. If the formula in the code and the formula in the docs ever
	// diverge, this fails.
	out := Run(refTime, Window24h, cohort(), DefaultOptions())
	r := out.Ranked[0]

	w := DefaultWeights
	raw := 100 * (w.StarVelocity*r.Percentiles.StarVelocity +
		w.RelativeGrowth*r.Percentiles.RelativeGrowth +
		w.Acceleration*r.Percentiles.Acceleration +
		w.ForkVelocity*r.Percentiles.ForkVelocity +
		w.Activity*r.Percentiles.Activity +
		w.Freshness*r.Percentiles.Freshness)
	want := raw * (0.75 + 0.25*r.Confidence)

	if math.Abs(r.Score-want) > 0.01 {
		t.Fatalf("score = %v, hand-computed = %v", r.Score, want)
	}
}

func TestConfidenceAdjustmentBoundsTheScore(t *testing.T) {
	// The adjustment scales a raw score into [0.75, 1.0] of its value.
	out := Run(refTime, Window24h, cohort(), DefaultOptions())
	for _, r := range out.Ranked {
		w := DefaultWeights
		raw := 100 * (w.StarVelocity*r.Percentiles.StarVelocity +
			w.RelativeGrowth*r.Percentiles.RelativeGrowth +
			w.Acceleration*r.Percentiles.Acceleration +
			w.ForkVelocity*r.Percentiles.ForkVelocity +
			w.Activity*r.Percentiles.Activity +
			w.Freshness*r.Percentiles.Freshness)
		if raw == 0 {
			continue
		}
		ratio := r.Score / raw
		if ratio < 0.7499 || ratio > 1.0001 {
			t.Errorf("%s: score/raw = %v, want it inside [0.75, 1.0]", r.FullName, ratio)
		}
	}
}

func TestAllResultsCarryFormulaVersion(t *testing.T) {
	out := Run(refTime, Window24h, cohort(), DefaultOptions())
	for _, r := range out.All() {
		if r.FormulaVersion != FormulaVersion {
			t.Errorf("%s FormulaVersion = %q, want %q", r.FullName, r.FormulaVersion, FormulaVersion)
		}
	}
}

func TestEmptyInputProducesEmptyRun(t *testing.T) {
	out := Run(refTime, Window24h, nil, DefaultOptions())

	if len(out.Ranked) != 0 || len(out.Provisional) != 0 {
		t.Fatal("an empty input should produce no results")
	}
	if out.CohortSize != 0 {
		t.Errorf("CohortSize = %d, want 0", out.CohortSize)
	}
}

func TestSingleRepositoryScoresNeutrally(t *testing.T) {
	out := Run(refTime, Window24h, []Input{buildInput(1, "solo/repo", 100, 400, 25)}, DefaultOptions())

	if len(out.Ranked) != 1 {
		t.Fatalf("got %d ranked, want 1", len(out.Ranked))
	}
	// Every percentile is the neutral 0.5 in a cohort of one, so the raw score
	// is 50 before the confidence adjustment.
	r := out.Ranked[0]
	if r.Score > 50.01 {
		t.Errorf("score = %v, want no more than 50 for a cohort of one", r.Score)
	}
}

func TestPercentilesStayWithinRange(t *testing.T) {
	out := Run(refTime, Window24h, cohort(), DefaultOptions())
	for _, r := range out.All() {
		p := r.Percentiles
		for name, value := range map[string]float64{
			"starVelocity": p.StarVelocity, "relativeGrowth": p.RelativeGrowth,
			"acceleration": p.Acceleration, "forkVelocity": p.ForkVelocity,
			"activity": p.Activity, "freshness": p.Freshness,
		} {
			if value < 0 || value > 1 {
				t.Errorf("%s %s percentile = %v, want it inside [0, 1]", r.FullName, name, value)
			}
		}
	}
}

func TestUnmeasurableAccelerationUsesNeutralPercentile(t *testing.T) {
	// Only two snapshots: no midpoint, so acceleration cannot be measured.
	twoPoint := Input{
		RepositoryID: 70,
		FullName:     "two/points",
		Description:  "Only two observations exist for this repository",
		CreatedAt:    refTime.Add(-100 * 24 * time.Hour),
		Active:       true,
		FirstSeenAt:  refTime.Add(-100 * 24 * time.Hour),
		Snapshots: []Snapshot{
			{At: refTime.Add(-24 * time.Hour), Stars: 100, Forks: 10},
			{At: refTime, Stars: 300, Forks: 20},
		},
	}

	out := Run(refTime, Window24h, []Input{twoPoint}, DefaultOptions())
	if len(out.Ranked) != 1 {
		t.Fatalf("got %d ranked, want 1; excluded=%v", len(out.Ranked), out.Excluded)
	}
	r := out.Ranked[0]
	if r.Features.AccelerationKnown {
		t.Fatal("acceleration should not be measurable from two points")
	}
	if r.Percentiles.Acceleration != 0.5 {
		t.Errorf("acceleration percentile = %v, want the neutral 0.5", r.Percentiles.Acceleration)
	}
}

func TestFeatureDataIsJSONSerialisable(t *testing.T) {
	out := Run(refTime, Window24h, cohort(), DefaultOptions())
	r := out.Ranked[0]

	for name, value := range map[string]any{"percentiles": r.Percentiles, "features": r.Features} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if strings.Contains(string(encoded), "NaN") || strings.Contains(string(encoded), "Inf") {
			t.Fatalf("%s contains a non-finite value: %s", name, encoded)
		}
	}
}

func TestWindowByName(t *testing.T) {
	for _, want := range Windows {
		got, err := WindowByName(want.Name)
		if err != nil {
			t.Fatalf("WindowByName(%q) error = %v", want.Name, err)
		}
		if got.Name != want.Name {
			t.Errorf("got %q, want %q", got.Name, want.Name)
		}
	}
	if _, err := WindowByName("12h"); err == nil {
		t.Error("expected an error for an unknown window")
	}
}

func TestDefaultWindowForCollection(t *testing.T) {
	tests := map[string]string{
		CollectionRising:      "6h",
		CollectionDaily:       "24h",
		CollectionWeekly:      "7d",
		CollectionHiddenGems:  "24h",
		CollectionNewEntrants: "24h",
		CollectionEstablished: "24h",
	}
	for collection, want := range tests {
		if got := DefaultWindowForCollection(collection); got != want {
			t.Errorf("DefaultWindowForCollection(%q) = %q, want %q", collection, got, want)
		}
	}
}

func assertExcluded(t *testing.T, out RunResult, id int64, wantReason string) {
	t.Helper()
	reason, ok := out.Excluded[id]
	if !ok {
		t.Fatalf("repository %d should have been excluded; excluded=%v", id, out.Excluded)
	}
	if reason != wantReason {
		t.Fatalf("repository %d excluded for %q, want %q", id, reason, wantReason)
	}
	for _, r := range out.All() {
		if r.RepositoryID == id {
			t.Fatalf("repository %d appears in results despite being excluded", id)
		}
	}
}
