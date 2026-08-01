package scoring

import (
	"math"
	"time"
)

// Confidence weights. They sum to 1.0, asserted by a unit test.
const (
	confidenceCoverageWeight = 0.45
	confidenceDensityWeight  = 0.25
	confidenceMidpointWeight = 0.15
	confidenceRecencyWeight  = 0.15
)

// Confidence penalties applied multiplicatively after the weighted sum.
const (
	penaltyRecentlyDiscovered = 0.85
	penaltyLargeGap           = 0.85
	penaltyProvisional        = 0.55

	// largeGapFraction is the share of the window a single missing stretch may
	// occupy before it counts as a gap.
	largeGapFraction = 0.34
)

// ConfidenceBreakdown explains how a confidence value was reached. It is stored
// in feature_data and surfaced on the repository page, because a number a user
// cannot interrogate is not transparency.
type ConfidenceBreakdown struct {
	Coverage           float64 `json:"coverage"`
	Density            float64 `json:"density"`
	Midpoint           float64 `json:"midpoint"`
	Recency            float64 `json:"recency"`
	RecentlyDiscovered bool    `json:"recentlyDiscovered"`
	LargeGap           bool    `json:"largeGap"`
	Provisional        bool    `json:"provisional"`
	Value              float64 `json:"value"`
}

// ComputeConfidence measures data quality, never project quality.
//
// A confidence of 0.4 says "RepoMeteor has not watched this repository long
// enough to be sure", not "this project is weak". Four observable properties
// contribute:
//
//   - coverage: how much of the requested window was actually observed;
//   - density: how many of the expected snapshots exist;
//   - midpoint: whether the acceleration comparison could be made at all;
//   - recency: how fresh the newest snapshot is.
//
// Two situations then apply a multiplicative penalty: a repository RepoMeteor
// only started tracking inside this window, and a single large gap in history.
func ComputeConfidence(now time.Time, w Window, f Features, in Input, eligibility Eligibility) ConfidenceBreakdown {
	b := ConfidenceBreakdown{}

	windowHours := w.Hours()
	if windowHours <= 0 {
		return b
	}

	b.Coverage = clampFloat(f.ObservedHours/windowHours, 0, 1)

	if f.ExpectedCount > 0 {
		b.Density = clampFloat(float64(f.ObservedCount)/float64(f.ExpectedCount), 0, 1)
	}

	if f.MidpointAvailable {
		b.Midpoint = 1
	}

	// Recency decays over the window's staleness allowance: a snapshot taken
	// just now scores 1, one at the staleness limit scores 0.
	if staleHours := w.StaleAfter.Hours(); staleHours > 0 {
		b.Recency = clampFloat(1-f.LatestAgeHours/staleHours, 0, 1)
	}

	value := confidenceCoverageWeight*b.Coverage +
		confidenceDensityWeight*b.Density +
		confidenceMidpointWeight*b.Midpoint +
		confidenceRecencyWeight*b.Recency

	if !in.FirstSeenAt.IsZero() && now.Sub(in.FirstSeenAt) < w.Duration {
		b.RecentlyDiscovered = true
		value *= penaltyRecentlyDiscovered
	}
	if f.LargestGapHours > windowHours*largeGapFraction {
		b.LargeGap = true
		value *= penaltyLargeGap
	}
	if eligibility == EligibleProvisional {
		b.Provisional = true
		value *= penaltyProvisional
	}

	b.Value = clampFloat(round(value, 4), 0, 1)
	return b
}

// DetectAnomalies returns neutral flags describing unusual data patterns.
//
// These are observations about the data, not accusations about the project.
// They lower confidence and are explained to the reader; they do not remove a
// repository from the rankings, because an unusual pattern is not evidence of
// wrongdoing.
func DetectAnomalies(w Window, f Features, in Input, eligibility Eligibility, now time.Time) []string {
	var flags []string

	// Substantial star growth with no fork growth and a quiet repository is an
	// unusual combination worth surfacing to the reader.
	if f.PositiveStarDelta >= 100 && f.ForkDelta == 0 && f.Activity < 0.2 {
		flags = append(flags, FlagUnusualStarForkRatio)
	}

	// A repository that has not been pushed to in months but is suddenly
	// gaining stars quickly.
	if in.PushedAt != nil && now.Sub(*in.PushedAt) > 90*24*time.Hour && f.PositiveStarDelta >= 50 {
		flags = append(flags, FlagStaleRepositorySpike)
	}

	if eligibility == EligibleProvisional {
		flags = append(flags, FlagInsufficientHistory)
	}

	// One interval accounting for most of the window's growth means the shape
	// of the growth is a step, not a trend.
	if f.PositiveStarDelta >= 50 && f.AccelerationKnown && f.PreviousVelocity == 0 && f.RecentVelocity > 0 {
		flags = append(flags, FlagLargeSingleInterval)
	}

	if f.LargestGapHours > w.Hours()*largeGapFraction {
		flags = append(flags, FlagSnapshotGap)
	}

	if !in.FirstSeenAt.IsZero() && now.Sub(in.FirstSeenAt) < w.Duration {
		flags = append(flags, FlagRecentlyDiscovered)
	}

	return flags
}

// round returns v rounded to `places` decimal places.
func round(v float64, places int) float64 {
	factor := math.Pow(10, float64(places))
	return math.Round(v*factor) / factor
}
