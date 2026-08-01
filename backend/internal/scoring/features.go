package scoring

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Snapshot is one aggregate observation. Scoring never sees anything beyond
// these counts and a timestamp — no stargazer identities exist in this package.
type Snapshot struct {
	At    time.Time
	Stars int
	Forks int
}

// Input is everything scoring needs to know about one repository.
type Input struct {
	RepositoryID int64

	// Identity, used only for deterministic tie-breaking and explanations.
	FullName string

	Description     string
	CreatedAt       time.Time
	PushedAt        *time.Time
	LatestReleaseAt *time.Time

	IsFork     bool
	IsArchived bool
	IsDisabled bool

	// Active reports whether the repository is currently tracked. Missing,
	// paused and excluded repositories are never ranked.
	Active bool

	// FirstSeenAt is when RepoMeteor started tracking. It drives the
	// "recently discovered" signal and the confidence penalty for short history.
	FirstSeenAt time.Time

	// Snapshots must be sorted oldest first. Callers that cannot guarantee the
	// order should sort before calling; Run sorts defensively anyway.
	Snapshots []Snapshot
}

// Eligibility classifies whether a repository may be ranked.
type Eligibility string

const (
	// EligibleFull means the repository has enough observed history for a
	// standard head-to-head ranking.
	EligibleFull Eligibility = "eligible"
	// EligibleProvisional means the repository moved measurably but has not
	// been observed for long enough to be compared fairly. It appears only in
	// New Entrants, always with a low-confidence label.
	EligibleProvisional Eligibility = "provisional"
	// NotEligible means the repository is excluded from all rankings.
	NotEligible Eligibility = "excluded"
)

// Exclusion reason codes. These are stable identifiers, not display strings.
const (
	ReasonFork             = "is_fork"
	ReasonArchived         = "is_archived"
	ReasonDisabled         = "is_disabled"
	ReasonInactive         = "not_active"
	ReasonBelowMinStars    = "below_min_stars"
	ReasonNoDescription    = "no_meaningful_description"
	ReasonNoSnapshots      = "no_snapshots"
	ReasonStaleData        = "stale_data"
	ReasonNoBaseline       = "no_baseline_snapshot"
	ReasonShortHistory     = "insufficient_history"
	ReasonNoMeasuredChange = "no_measured_change"
)

// Anomaly flags. Wording is deliberately neutral: RepoMeteor describes an
// unusual data pattern, it never alleges manipulation.
const (
	FlagUnusualStarForkRatio = "unusual_star_fork_ratio"
	FlagStaleRepositorySpike = "stale_repository_spike"
	FlagInsufficientHistory  = "insufficient_history"
	FlagLargeSingleInterval  = "large_single_interval_change"
	FlagSnapshotGap          = "snapshot_gap"
	FlagRecentlyDiscovered   = "recently_discovered"
)

// Features holds the raw, un-normalised measurements for one repository and
// window, together with the availability flags that drive confidence.
type Features struct {
	// Observed span and sampling.
	ObservedHours   float64
	ObservedCount   int
	ExpectedCount   int
	LargestGapHours float64
	LatestAgeHours  float64

	// Baseline and latest values.
	BaselineStars int
	BaselineForks int
	LatestStars   int
	LatestForks   int

	// Deltas and rates. StarDelta may be negative; PositiveStarDelta is the
	// clamped value used for ranking, because losing stars is not momentum.
	StarDelta         int
	PositiveStarDelta int
	StarVelocity      float64
	RelativeGrowth    float64
	ForkDelta         int
	PositiveForkDelta int
	ForkVelocity      float64

	// Acceleration compares the two halves of the window.
	Acceleration      float64
	RecentVelocity    float64
	PreviousVelocity  float64
	AccelerationKnown bool

	// Deterministic metadata features.
	PushRecency    float64
	ReleaseRecency float64
	Activity       float64
	Freshness      float64

	// Availability.
	BaselineAvailable bool
	MidpointAvailable bool

	// AgeDays is the repository's age at scoring time.
	AgeDays float64
}

// ComputeFeatures derives the raw feature set for one repository and window.
//
// It never invents data: when a baseline or midpoint snapshot is missing the
// corresponding availability flag is false and the caller reduces confidence,
// rather than a zero being substituted and silently treated as a real
// observation.
func ComputeFeatures(now time.Time, w Window, in Input, opts Options) Features {
	f := Features{
		ExpectedCount: expectedSnapshots(w, opts.SnapshotInterval),
		AgeDays:       math.Max(now.Sub(in.CreatedAt).Hours()/24, 0),
	}

	snapshots := windowSnapshots(now, w, in.Snapshots)
	f.ObservedCount = len(snapshots)
	if len(snapshots) == 0 {
		return f
	}

	latest := snapshots[len(snapshots)-1]
	f.LatestStars = latest.Stars
	f.LatestForks = latest.Forks
	f.LatestAgeHours = math.Max(now.Sub(latest.At).Hours(), 0)

	// Baseline: the earliest snapshot within tolerance of the ideal window
	// start. Preferring the earliest acceptable snapshot maximises the observed
	// span, which is what the window is meant to measure.
	windowStart := now.Add(-w.Duration)
	baseline, baselineOK := findBaseline(snapshots, windowStart, w.BaselineTolerance)
	f.BaselineAvailable = baselineOK

	if !baselineOK {
		// No snapshot sits near the window start, so this repository has not
		// been observed across the window at all. Rather than reporting a delta
		// of zero — which would read as "it did not move" — measure over the
		// span that genuinely exists and let ObservedHours state how short it
		// is. Callers treat BaselineAvailable=false as provisional, so this
		// number is never compared against a fully observed repository, and it
		// is never scaled up to the window length.
		if len(snapshots) < 2 {
			return f
		}
		baseline = snapshots[0]
	}

	f.BaselineStars = baseline.Stars
	f.BaselineForks = baseline.Forks
	f.ObservedHours = math.Max(latest.At.Sub(baseline.At).Hours(), 0)
	f.LargestGapHours = largestGapHours(snapshots)

	f.StarDelta = latest.Stars - baseline.Stars
	f.PositiveStarDelta = max(f.StarDelta, 0)
	f.ForkDelta = latest.Forks - baseline.Forks
	f.PositiveForkDelta = max(f.ForkDelta, 0)

	if f.ObservedHours > 0 {
		f.StarVelocity = float64(f.PositiveStarDelta) / f.ObservedHours
		f.ForkVelocity = float64(f.PositiveForkDelta) / f.ObservedHours
	}

	floor := float64(max(f.BaselineStars, opts.RelativeGrowthFloor))
	f.RelativeGrowth = float64(f.PositiveStarDelta) / floor

	// Acceleration over the two halves of the observed span.
	midpointTime := baseline.At.Add(latest.At.Sub(baseline.At) / 2)
	if mid, ok := findNearest(snapshots, midpointTime, latest.At.Sub(baseline.At)/4); ok {
		previousHours := mid.At.Sub(baseline.At).Hours()
		recentHours := latest.At.Sub(mid.At).Hours()
		if previousHours > 0 && recentHours > 0 {
			f.PreviousVelocity = math.Max(float64(mid.Stars-baseline.Stars), 0) / previousHours
			f.RecentVelocity = math.Max(float64(latest.Stars-mid.Stars), 0) / recentHours
			f.Acceleration = math.Max(f.RecentVelocity-f.PreviousVelocity, 0)
			f.AccelerationKnown = true
			f.MidpointAvailable = true
		}
	}

	f.PushRecency = decay(now, in.PushedAt, opts.PushRecencyScale)
	f.ReleaseRecency = decay(now, in.LatestReleaseAt, opts.ReleaseRecencyScale)
	f.Activity = 0.70*f.PushRecency + 0.30*f.ReleaseRecency
	f.Freshness = math.Exp(-f.AgeDays / (opts.FreshnessScale.Hours() / 24))

	return f
}

// windowSnapshots returns the snapshots inside the window, oldest first, with a
// small lead-in so that a baseline slightly older than the window start can
// still be found within tolerance.
func windowSnapshots(now time.Time, w Window, all []Snapshot) []Snapshot {
	if len(all) == 0 {
		return nil
	}
	sorted := make([]Snapshot, len(all))
	copy(sorted, all)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })

	from := now.Add(-w.Duration).Add(-w.BaselineTolerance)
	out := make([]Snapshot, 0, len(sorted))
	for _, s := range sorted {
		if s.At.Before(from) || s.At.After(now) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// findBaseline picks the earliest snapshot within tolerance of the ideal window
// start. Snapshots are already sorted oldest first.
func findBaseline(snapshots []Snapshot, windowStart time.Time, tolerance time.Duration) (Snapshot, bool) {
	earliest := windowStart.Add(-tolerance)
	latestAcceptable := windowStart.Add(tolerance)

	for _, s := range snapshots {
		if s.At.Before(earliest) {
			continue
		}
		if s.At.After(latestAcceptable) {
			break
		}
		return s, true
	}
	return Snapshot{}, false
}

// findNearest returns the snapshot closest to target within tolerance.
func findNearest(snapshots []Snapshot, target time.Time, tolerance time.Duration) (Snapshot, bool) {
	if tolerance <= 0 {
		return Snapshot{}, false
	}

	var (
		best     Snapshot
		bestDiff = time.Duration(math.MaxInt64)
		found    bool
	)
	for _, s := range snapshots {
		diff := s.At.Sub(target)
		if diff < 0 {
			diff = -diff
		}
		if diff <= tolerance && diff < bestDiff {
			best, bestDiff, found = s, diff, true
		}
	}
	return best, found
}

// largestGapHours reports the biggest interval between consecutive snapshots.
func largestGapHours(snapshots []Snapshot) float64 {
	var largest float64
	for i := 1; i < len(snapshots); i++ {
		if gap := snapshots[i].At.Sub(snapshots[i-1].At).Hours(); gap > largest {
			largest = gap
		}
	}
	return largest
}

func expectedSnapshots(w Window, interval time.Duration) int {
	if interval <= 0 {
		interval = time.Hour
	}
	n := int(w.Duration / interval)
	if n < 2 {
		n = 2
	}
	return n
}

// decay returns exp(-elapsed/scale), or 0 when the timestamp is absent.
// A repository with no release scores 0 on release recency by design.
func decay(now time.Time, at *time.Time, scale time.Duration) float64 {
	if at == nil || at.IsZero() || scale <= 0 {
		return 0
	}
	elapsed := now.Sub(*at)
	if elapsed < 0 {
		// A future timestamp is treated as "just now" rather than amplified.
		return 1
	}
	return math.Exp(-elapsed.Hours() / scale.Hours())
}

// Classify decides whether a repository may be ranked in this window.
//
// The checks run in a fixed order and the first failure wins, so the returned
// reason code is stable and directly explainable to a user.
//
// There are two levels of "yes". EligibleFull repositories are compared
// head-to-head. EligibleProvisional repositories moved measurably but have not
// been observed long enough for a fair comparison — RepoMeteor shows them only
// under New Entrants and never extrapolates a short observation into a longer
// window.
func Classify(f Features, w Window, in Input, opts Options) (Eligibility, string) {
	switch {
	case in.IsFork:
		return NotEligible, ReasonFork
	case in.IsArchived:
		return NotEligible, ReasonArchived
	case in.IsDisabled:
		return NotEligible, ReasonDisabled
	case !in.Active:
		return NotEligible, ReasonInactive
	case f.ObservedCount == 0:
		return NotEligible, ReasonNoSnapshots
	case f.LatestStars < opts.MinStars:
		return NotEligible, ReasonBelowMinStars
	case len(strings.TrimSpace(in.Description)) < opts.MinDescriptionLength:
		return NotEligible, ReasonNoDescription
	}

	// Two observations are the minimum needed to measure any change at all.
	if f.ObservedCount < 2 {
		return NotEligible, ReasonNoBaseline
	}

	// A repository whose newest snapshot is old cannot be ranked honestly: the
	// numbers on screen would not describe the requested window.
	if f.LatestAgeHours > w.StaleAfter.Hours() {
		return NotEligible, ReasonStaleData
	}

	// Full eligibility requires both a genuine window baseline and enough
	// observed span. Either shortfall makes a head-to-head comparison unfair,
	// because the repository's numbers would cover less ground than its rivals'.
	if !f.BaselineAvailable || f.ObservedHours < w.MinObservedHistory.Hours() {
		// Enough to say "this is moving", not enough to say "this is moving
		// faster than that". Provisional entries need actual movement to be
		// worth showing at all.
		if f.PositiveStarDelta > 0 || f.PositiveForkDelta > 0 {
			if !f.BaselineAvailable {
				return EligibleProvisional, ReasonNoBaseline
			}
			return EligibleProvisional, ReasonShortHistory
		}
		return NotEligible, ReasonNoMeasuredChange
	}

	return EligibleFull, ""
}
