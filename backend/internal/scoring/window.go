// Package scoring computes RepoMeteor's momentum rankings.
//
// The package is deliberately pure: it has no HTTP, database or GitHub
// dependency and performs no I/O. Callers pass in snapshots and repository
// metadata and receive ranked results. That makes the product's most important
// logic exhaustively testable with fixed inputs and a fixed clock, and it means
// the published methodology can be read as one self-contained unit.
//
// The formula is documented in docs/SCORING.md and versioned by FormulaVersion.
package scoring

import (
	"fmt"
	"time"
)

// FormulaVersion identifies the scoring method. Bump it whenever weights,
// features or eligibility rules change, and record the change in
// docs/SCORING.md and docs/DECISIONS.md.
const FormulaVersion = "momentum-v1"

// Window describes one ranking horizon.
type Window struct {
	// Name is the public identifier: "6h", "24h" or "7d".
	Name string

	// Duration is the nominal horizon.
	Duration time.Duration

	// BaselineTolerance is how far from the ideal window start a snapshot may
	// sit and still be used as the baseline. Real collection lands on a poll
	// cadence, not on exact boundaries, so an exact-match requirement would
	// discard most usable history.
	BaselineTolerance time.Duration

	// MinObservedHistory is the shortest observed span that still permits a
	// standard ranking. Below it a repository is provisional: it can appear in
	// New Entrants but is never compared head-to-head with fully observed
	// repositories.
	MinObservedHistory time.Duration

	// StaleAfter is how old the newest snapshot may be before the repository
	// drops out of this window's ranking entirely.
	StaleAfter time.Duration
}

// Hours returns the window duration in hours.
func (w Window) Hours() float64 { return w.Duration.Hours() }

// The three shipped windows.
var (
	Window6h = Window{
		Name:               "6h",
		Duration:           6 * time.Hour,
		BaselineTolerance:  2 * time.Hour,
		MinObservedHistory: 4 * time.Hour,
		StaleAfter:         3 * time.Hour,
	}
	Window24h = Window{
		Name:               "24h",
		Duration:           24 * time.Hour,
		BaselineTolerance:  6 * time.Hour,
		MinObservedHistory: 18 * time.Hour,
		StaleAfter:         8 * time.Hour,
	}
	Window7d = Window{
		Name:               "7d",
		Duration:           7 * 24 * time.Hour,
		BaselineTolerance:  24 * time.Hour,
		MinObservedHistory: 5 * 24 * time.Hour,
		StaleAfter:         48 * time.Hour,
	}
)

// Windows lists every shipped window in ascending order.
var Windows = []Window{Window6h, Window24h, Window7d}

// WindowByName resolves a public window identifier.
func WindowByName(name string) (Window, error) {
	for _, w := range Windows {
		if w.Name == name {
			return w, nil
		}
	}
	return Window{}, fmt.Errorf("unknown window %q (expected 6h, 24h or 7d)", name)
}

// WindowNames returns the valid window identifiers.
func WindowNames() []string {
	out := make([]string, 0, len(Windows))
	for _, w := range Windows {
		out = append(out, w.Name)
	}
	return out
}

// Weights are the momentum-v1 feature weights. They sum to 1.0, which is
// asserted by a unit test so the formula cannot silently drift.
type Weights struct {
	StarVelocity   float64
	RelativeGrowth float64
	Acceleration   float64
	ForkVelocity   float64
	Activity       float64
	Freshness      float64
}

// DefaultWeights is the momentum-v1 weighting.
//
// Star velocity leads because it is the clearest momentum signal, but it holds
// well under half the total so that a large repository cannot dominate on
// absolute numbers alone. Relative growth deliberately ranks second: it is what
// gives small repositories a genuine chance. Freshness is intentionally small —
// it should surface new projects, not exclude established ones.
var DefaultWeights = Weights{
	StarVelocity:   0.32,
	RelativeGrowth: 0.23,
	Acceleration:   0.17,
	ForkVelocity:   0.10,
	Activity:       0.13,
	Freshness:      0.05,
}

// Sum returns the total weight, which must be 1.0.
func (w Weights) Sum() float64 {
	return w.StarVelocity + w.RelativeGrowth + w.Acceleration + w.ForkVelocity + w.Activity + w.Freshness
}

// Options tune a scoring run. The zero value is not valid; use DefaultOptions.
type Options struct {
	Weights Weights

	// SnapshotInterval is the expected gap between snapshots. Confidence uses
	// it to judge how complete a window's history is.
	SnapshotInterval time.Duration

	// RelativeGrowthFloor prevents a repository with almost no stars from
	// producing an unbounded relative growth ratio.
	RelativeGrowthFloor int

	// MinStars is the lowest star count that may be ranked.
	MinStars int

	// MinDescriptionLength is the shortest description considered meaningful.
	MinDescriptionLength int

	// WinsorPercentile trims the extreme tails before percentile ranking.
	// 0.01 clips the top and bottom 1% of each raw feature.
	WinsorPercentile float64

	// PushRecencyHalfLife and ReleaseRecencyHalfLife control the exponential
	// decay of the activity components.
	PushRecencyScale    time.Duration
	ReleaseRecencyScale time.Duration

	// FreshnessScale controls how quickly repository age decays.
	FreshnessScale time.Duration
}

// DefaultOptions returns the shipped configuration.
func DefaultOptions() Options {
	return Options{
		Weights:              DefaultWeights,
		SnapshotInterval:     time.Hour,
		RelativeGrowthFloor:  20,
		MinStars:             5,
		MinDescriptionLength: 8,
		WinsorPercentile:     0.01,
		PushRecencyScale:     168 * time.Hour, // one week
		ReleaseRecencyScale:  30 * 24 * time.Hour,
		FreshnessScale:       730 * 24 * time.Hour, // two years
	}
}

// Collection names. These are the deterministic groupings the UI and API
// expose; membership is decided by explicit rules, never by an opaque model.
const (
	CollectionRising      = "rising"
	CollectionDaily       = "daily"
	CollectionWeekly      = "weekly"
	CollectionHiddenGems  = "hidden-gems"
	CollectionNewEntrants = "new-entrants"
	CollectionEstablished = "established"
)

// CollectionNames lists every collection.
func CollectionNames() []string {
	return []string{
		CollectionRising, CollectionDaily, CollectionWeekly,
		CollectionHiddenGems, CollectionNewEntrants, CollectionEstablished,
	}
}

// DefaultWindowForCollection maps a collection to the window it is most
// meaningful in, used when a caller supplies a collection but no window.
func DefaultWindowForCollection(collection string) string {
	switch collection {
	case CollectionRising:
		return Window6h.Name
	case CollectionWeekly:
		return Window7d.Name
	default:
		return Window24h.Name
	}
}

// PrimaryCollectionForWindow returns the collection that a window's ranking is
// presented as by default.
func PrimaryCollectionForWindow(window string) string {
	switch window {
	case Window6h.Name:
		return CollectionRising
	case Window7d.Name:
		return CollectionWeekly
	default:
		return CollectionDaily
	}
}

// Hidden Gems bounds.
const (
	HiddenGemMinStars = 10
	HiddenGemMaxStars = 1000
)

// EstablishedMinStars is the floor for the Established Movers collection.
const EstablishedMinStars = 10000

// Confidence label thresholds.
const (
	ConfidenceHighThreshold   = 0.85
	ConfidenceMediumThreshold = 0.60
)

// ConfidenceLabel maps a confidence value to its public label.
func ConfidenceLabel(confidence float64) string {
	switch {
	case confidence >= ConfidenceHighThreshold:
		return "high"
	case confidence >= ConfidenceMediumThreshold:
		return "medium"
	default:
		return "low"
	}
}

// ConfidenceBounds returns the inclusive-exclusive range for a label, used by
// the API to translate a confidence filter into numeric bounds.
func ConfidenceBounds(label string) (minimum, maximum float64, ok bool) {
	switch label {
	case "high":
		return ConfidenceHighThreshold, 1.0001, true
	case "medium":
		return ConfidenceMediumThreshold, ConfidenceHighThreshold, true
	case "low":
		return 0, ConfidenceMediumThreshold, true
	default:
		return 0, 0, false
	}
}
