package scoring

import (
	"math"
	"testing"
	"time"
)

var refTime = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

// hourly builds a snapshot series ending at `now`, spaced one hour apart, with
// the supplied star counts oldest first. Forks stay flat unless overridden.
func hourly(now time.Time, stars ...int) []Snapshot {
	out := make([]Snapshot, 0, len(stars))
	for i, s := range stars {
		at := now.Add(-time.Duration(len(stars)-1-i) * time.Hour)
		out = append(out, Snapshot{At: at, Stars: s, Forks: 10})
	}
	return out
}

func baseInput(snapshots []Snapshot) Input {
	pushed := refTime.Add(-2 * time.Hour)
	return Input{
		RepositoryID: 1,
		FullName:     "orbit-labs/signal-kit",
		Description:  "A fictional demo repository for testing",
		CreatedAt:    refTime.Add(-200 * 24 * time.Hour),
		PushedAt:     &pushed,
		Active:       true,
		FirstSeenAt:  refTime.Add(-60 * 24 * time.Hour),
		Snapshots:    snapshots,
	}
}

func approx(t *testing.T, got, want, tolerance float64, label string) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Errorf("%s = %v, want %v (±%v)", label, got, want, tolerance)
	}
}

func TestComputeFeaturesExactWindowBaseline(t *testing.T) {
	// Seven hourly snapshots: the oldest sits exactly 6h before now.
	in := baseInput(hourly(refTime, 100, 110, 120, 130, 140, 150, 160))
	f := ComputeFeatures(refTime, Window6h, in, DefaultOptions())

	if !f.BaselineAvailable {
		t.Fatal("BaselineAvailable = false, want true")
	}
	if f.BaselineStars != 100 {
		t.Errorf("BaselineStars = %d, want 100", f.BaselineStars)
	}
	if f.LatestStars != 160 {
		t.Errorf("LatestStars = %d, want 160", f.LatestStars)
	}
	if f.StarDelta != 60 {
		t.Errorf("StarDelta = %d, want 60", f.StarDelta)
	}
	approx(t, f.ObservedHours, 6, 0.001, "ObservedHours")
	approx(t, f.StarVelocity, 10, 0.001, "StarVelocity")
}

func TestComputeFeaturesToleranceWindowBaseline(t *testing.T) {
	// Only two snapshots exist for a 24h window: one 20h ago (inside the 6h
	// tolerance around the 24h mark) and one now.
	in := baseInput([]Snapshot{
		{At: refTime.Add(-20 * time.Hour), Stars: 500, Forks: 40},
		{At: refTime, Stars: 700, Forks: 52},
	})
	f := ComputeFeatures(refTime, Window24h, in, DefaultOptions())

	if !f.BaselineAvailable {
		t.Fatal("a snapshot 20h old should satisfy the 24h window's 6h tolerance")
	}
	if f.BaselineStars != 500 {
		t.Errorf("BaselineStars = %d, want 500", f.BaselineStars)
	}
	approx(t, f.ObservedHours, 20, 0.001, "ObservedHours")
	approx(t, f.StarVelocity, 10, 0.001, "StarVelocity")
}

func TestComputeFeaturesBaselineOutsideToleranceIsUnavailable(t *testing.T) {
	// A snapshot 2h old cannot serve as the baseline for a 24h window: the
	// tolerance is 6h around the 24h mark, so 2h is far too recent.
	in := baseInput([]Snapshot{
		{At: refTime.Add(-2 * time.Hour), Stars: 500, Forks: 40},
		{At: refTime, Stars: 900, Forks: 41},
	})
	f := ComputeFeatures(refTime, Window24h, in, DefaultOptions())

	if f.BaselineAvailable {
		t.Fatal("BaselineAvailable = true; a 2h-old snapshot must not stand in for a 24h baseline")
	}

	// The measurement falls back to the span that genuinely exists, and reports
	// that span honestly. Crucially the 400 stars are attributed to 2 observed
	// hours, not scaled up to the 24h window.
	if f.StarDelta != 400 {
		t.Errorf("StarDelta = %d, want the 400 actually observed", f.StarDelta)
	}
	approx(t, f.ObservedHours, 2, 0.001, "ObservedHours")
	approx(t, f.StarVelocity, 200, 0.001, "StarVelocity")
}

func TestComputeFeaturesDoesNotExtrapolateShortHistory(t *testing.T) {
	// A repository observed for 2 hours must never be presented as if the gain
	// covered the whole 24h window. Coverage — and therefore confidence — has
	// to reflect the 2 hours actually seen.
	in := baseInput([]Snapshot{
		{At: refTime.Add(-2 * time.Hour), Stars: 500, Forks: 40},
		{At: refTime, Stars: 900, Forks: 41},
	})
	f := ComputeFeatures(refTime, Window24h, in, DefaultOptions())

	eligibility, code := Classify(f, Window24h, in, DefaultOptions())
	if eligibility != EligibleProvisional {
		t.Fatalf("Eligibility = %q (%s), want provisional", eligibility, code)
	}

	confidence := ComputeConfidence(refTime, Window24h, f, in, eligibility)
	approx(t, confidence.Coverage, 2.0/24.0, 0.001, "Coverage")
	if confidence.Value >= ConfidenceMediumThreshold {
		t.Errorf("confidence = %v, want it below %v for two hours of a 24h window",
			confidence.Value, ConfidenceMediumThreshold)
	}
}

func TestSingleSnapshotCannotBeMeasured(t *testing.T) {
	in := baseInput([]Snapshot{{At: refTime, Stars: 500, Forks: 40}})
	f := ComputeFeatures(refTime, Window24h, in, DefaultOptions())

	if f.BaselineAvailable {
		t.Error("BaselineAvailable = true with one snapshot")
	}
	if f.StarDelta != 0 {
		t.Errorf("StarDelta = %d, want 0; one observation measures no change", f.StarDelta)
	}

	eligibility, code := Classify(f, Window24h, in, DefaultOptions())
	if eligibility != NotEligible || code != ReasonNoBaseline {
		t.Fatalf("Classify() = %q/%q, want excluded/%s", eligibility, code, ReasonNoBaseline)
	}
}

func TestComputeFeaturesNegativeStarDelta(t *testing.T) {
	in := baseInput(hourly(refTime, 200, 195, 190, 185, 180, 175, 170))
	f := ComputeFeatures(refTime, Window6h, in, DefaultOptions())

	if f.StarDelta != -30 {
		t.Errorf("StarDelta = %d, want -30 (raw delta is preserved)", f.StarDelta)
	}
	if f.PositiveStarDelta != 0 {
		t.Errorf("PositiveStarDelta = %d, want 0", f.PositiveStarDelta)
	}
	if f.StarVelocity != 0 {
		t.Errorf("StarVelocity = %v, want 0 (losing stars is not momentum)", f.StarVelocity)
	}
	if f.RelativeGrowth != 0 {
		t.Errorf("RelativeGrowth = %v, want 0", f.RelativeGrowth)
	}
}

func TestRelativeGrowthFloor(t *testing.T) {
	// A repository going from 5 to 15 stars gains 10. Without a floor the
	// relative growth would be 2.0; the floor of 20 holds it to 0.5 so that
	// tiny repositories cannot dominate purely by arithmetic.
	in := baseInput(hourly(refTime, 5, 7, 9, 11, 13, 14, 15))
	f := ComputeFeatures(refTime, Window6h, in, DefaultOptions())

	approx(t, f.RelativeGrowth, 0.5, 0.001, "RelativeGrowth")
}

func TestRelativeGrowthUsesBaselineWhenAboveFloor(t *testing.T) {
	in := baseInput(hourly(refTime, 100, 105, 110, 115, 120, 125, 130))
	f := ComputeFeatures(refTime, Window6h, in, DefaultOptions())

	// 30 gained on a base of 100.
	approx(t, f.RelativeGrowth, 0.30, 0.001, "RelativeGrowth")
}

func TestAccelerationIsMeasuredAcrossHalfWindows(t *testing.T) {
	// First half gains 10 stars over 3h; second half gains 40 over 3h.
	in := baseInput(hourly(refTime, 100, 103, 107, 110, 125, 140, 150))
	f := ComputeFeatures(refTime, Window6h, in, DefaultOptions())

	if !f.AccelerationKnown {
		t.Fatal("AccelerationKnown = false, want true")
	}
	approx(t, f.PreviousVelocity, 10.0/3.0, 0.001, "PreviousVelocity")
	approx(t, f.RecentVelocity, 40.0/3.0, 0.001, "RecentVelocity")
	approx(t, f.Acceleration, 10.0, 0.001, "Acceleration")
}

func TestDecelerationClampsAccelerationToZero(t *testing.T) {
	// First half gains 40, second half gains 10.
	in := baseInput(hourly(refTime, 100, 115, 130, 140, 143, 147, 150))
	f := ComputeFeatures(refTime, Window6h, in, DefaultOptions())

	if !f.AccelerationKnown {
		t.Fatal("AccelerationKnown = false, want true")
	}
	if f.Acceleration != 0 {
		t.Errorf("Acceleration = %v, want 0 for a decelerating repository", f.Acceleration)
	}
	if f.RecentVelocity >= f.PreviousVelocity {
		t.Errorf("recent (%v) should be below previous (%v)", f.RecentVelocity, f.PreviousVelocity)
	}
}

func TestMissingMidpointLeavesAccelerationUnknown(t *testing.T) {
	// Only the two endpoints exist: nothing sits near the midpoint.
	in := baseInput([]Snapshot{
		{At: refTime.Add(-6 * time.Hour), Stars: 100, Forks: 10},
		{At: refTime, Stars: 200, Forks: 12},
	})
	f := ComputeFeatures(refTime, Window6h, in, DefaultOptions())

	if !f.BaselineAvailable {
		t.Fatal("baseline should be available")
	}
	if f.AccelerationKnown {
		t.Fatal("AccelerationKnown = true; acceleration must not be fabricated from two points")
	}
	if f.MidpointAvailable {
		t.Fatal("MidpointAvailable = true, want false")
	}
	if f.Acceleration != 0 {
		t.Errorf("Acceleration = %v, want the neutral zero", f.Acceleration)
	}
}

func TestActivityCombinesPushAndRelease(t *testing.T) {
	opts := DefaultOptions()

	pushed := refTime // pushed right now
	release := refTime
	in := baseInput(hourly(refTime, 100, 110, 120, 130, 140, 150, 160))
	in.PushedAt = &pushed
	in.LatestReleaseAt = &release

	f := ComputeFeatures(refTime, Window6h, in, opts)
	approx(t, f.PushRecency, 1.0, 0.001, "PushRecency")
	approx(t, f.ReleaseRecency, 1.0, 0.001, "ReleaseRecency")
	approx(t, f.Activity, 1.0, 0.001, "Activity")
}

func TestActivityWithoutReleaseScoresZeroOnReleaseComponent(t *testing.T) {
	pushed := refTime
	in := baseInput(hourly(refTime, 100, 110, 120, 130, 140, 150, 160))
	in.PushedAt = &pushed
	in.LatestReleaseAt = nil

	f := ComputeFeatures(refTime, Window6h, in, DefaultOptions())
	if f.ReleaseRecency != 0 {
		t.Errorf("ReleaseRecency = %v, want 0 when no release exists", f.ReleaseRecency)
	}
	approx(t, f.Activity, 0.70, 0.001, "Activity")
}

func TestPushRecencyDecaysOverOneWeek(t *testing.T) {
	pushed := refTime.Add(-168 * time.Hour) // exactly one scale length
	in := baseInput(hourly(refTime, 100, 110, 120, 130, 140, 150, 160))
	in.PushedAt = &pushed

	f := ComputeFeatures(refTime, Window6h, in, DefaultOptions())
	approx(t, f.PushRecency, math.Exp(-1), 0.001, "PushRecency at one scale length")
}

func TestFreshnessDecaysWithRepositoryAge(t *testing.T) {
	opts := DefaultOptions()

	newRepo := baseInput(hourly(refTime, 100, 110, 120, 130, 140, 150, 160))
	newRepo.CreatedAt = refTime.Add(-24 * time.Hour)

	oldRepo := baseInput(hourly(refTime, 100, 110, 120, 130, 140, 150, 160))
	oldRepo.CreatedAt = refTime.Add(-2000 * 24 * time.Hour)

	fNew := ComputeFeatures(refTime, Window6h, newRepo, opts)
	fOld := ComputeFeatures(refTime, Window6h, oldRepo, opts)

	if fNew.Freshness <= fOld.Freshness {
		t.Errorf("new repository freshness (%v) should exceed old (%v)", fNew.Freshness, fOld.Freshness)
	}
	// At two years (the scale length) freshness is exp(-1).
	twoYears := baseInput(hourly(refTime, 100, 110, 120, 130, 140, 150, 160))
	twoYears.CreatedAt = refTime.Add(-730 * 24 * time.Hour)
	approx(t, ComputeFeatures(refTime, Window6h, twoYears, opts).Freshness, math.Exp(-1), 0.01, "Freshness at two years")
}

func TestLargestGapIsMeasured(t *testing.T) {
	in := baseInput([]Snapshot{
		{At: refTime.Add(-24 * time.Hour), Stars: 100, Forks: 10},
		{At: refTime.Add(-23 * time.Hour), Stars: 105, Forks: 10},
		// A 20-hour gap.
		{At: refTime.Add(-3 * time.Hour), Stars: 200, Forks: 15},
		{At: refTime, Stars: 220, Forks: 16},
	})
	f := ComputeFeatures(refTime, Window24h, in, DefaultOptions())

	approx(t, f.LargestGapHours, 20, 0.001, "LargestGapHours")
}

func TestSnapshotsAreSortedDefensively(t *testing.T) {
	shuffled := []Snapshot{
		{At: refTime, Stars: 160, Forks: 12},
		{At: refTime.Add(-6 * time.Hour), Stars: 100, Forks: 10},
		{At: refTime.Add(-3 * time.Hour), Stars: 130, Forks: 11},
	}
	f := ComputeFeatures(refTime, Window6h, baseInput(shuffled), DefaultOptions())

	if f.BaselineStars != 100 || f.LatestStars != 160 {
		t.Fatalf("baseline/latest = %d/%d, want 100/160 from unsorted input", f.BaselineStars, f.LatestStars)
	}
}

func TestFutureSnapshotsAreIgnored(t *testing.T) {
	in := baseInput([]Snapshot{
		{At: refTime.Add(-6 * time.Hour), Stars: 100, Forks: 10},
		{At: refTime, Stars: 160, Forks: 12},
		{At: refTime.Add(time.Hour), Stars: 9999, Forks: 99},
	})
	f := ComputeFeatures(refTime, Window6h, in, DefaultOptions())

	if f.LatestStars != 160 {
		t.Fatalf("LatestStars = %d, want 160; a future-dated snapshot must be ignored", f.LatestStars)
	}
}

func TestExpectedSnapshotCount(t *testing.T) {
	tests := map[Window]int{
		Window6h:  6,
		Window24h: 24,
		Window7d:  168,
	}
	for w, want := range tests {
		if got := expectedSnapshots(w, time.Hour); got != want {
			t.Errorf("expectedSnapshots(%s) = %d, want %d", w.Name, got, want)
		}
	}
}

func TestDecayHandlesMissingAndFutureTimes(t *testing.T) {
	if got := decay(refTime, nil, time.Hour); got != 0 {
		t.Errorf("decay(nil) = %v, want 0", got)
	}
	future := refTime.Add(time.Hour)
	if got := decay(refTime, &future, time.Hour); got != 1 {
		t.Errorf("decay(future) = %v, want 1", got)
	}
}
