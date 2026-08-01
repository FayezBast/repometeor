package scoring

import (
	"sort"
	"time"
)

// Result is one repository's ranking for one window.
type Result struct {
	RepositoryID int64
	FullName     string
	Window       string
	ScoredAt     time.Time

	// Score is the final 0..100 momentum score after the confidence
	// adjustment. Rank is 1-based within the window; ranked results only.
	Score float64
	Rank  int

	Eligibility     Eligibility
	EligibilityCode string

	StarsTotal     int
	StarDelta      int
	StarVelocity   float64
	RelativeGrowth float64
	Acceleration   float64
	ForkDelta      int
	ForkVelocity   float64
	Activity       float64
	Freshness      float64

	Confidence      float64
	ConfidenceLabel string

	Collections  []string
	Reasons      []Explanation
	AnomalyFlags []string

	// Percentiles are the normalised feature values used by the formula.
	Percentiles Percentiles

	// Features holds the raw measurements, published so that any score can be
	// recomputed by hand from the API response.
	Features Features

	FormulaVersion string
}

// ReasonCodes returns just the machine-readable codes, in display order.
func (r Result) ReasonCodes() []string {
	out := make([]string, 0, len(r.Reasons))
	for _, e := range r.Reasons {
		out = append(out, e.Code)
	}
	return out
}

// ReasonTexts returns just the sentences, in display order.
func (r Result) ReasonTexts() []string {
	out := make([]string, 0, len(r.Reasons))
	for _, e := range r.Reasons {
		out = append(out, e.Text)
	}
	return out
}

// Percentiles are the cohort-normalised feature values in [0, 1].
type Percentiles struct {
	StarVelocity   float64 `json:"starVelocity"`
	RelativeGrowth float64 `json:"relativeGrowth"`
	Acceleration   float64 `json:"acceleration"`
	ForkVelocity   float64 `json:"forkVelocity"`
	Activity       float64 `json:"activity"`
	Freshness      float64 `json:"freshness"`
}

// RunResult is the outcome of scoring one window.
type RunResult struct {
	Window   string
	ScoredAt time.Time

	// Ranked holds fully eligible repositories in descending score order with
	// Rank assigned.
	Ranked []Result

	// Provisional holds repositories with too little history for a standard
	// ranking. They carry a score for ordering within New Entrants but are
	// never mixed into the main leaderboard.
	Provisional []Result

	// Excluded records why each remaining repository was left out, keyed by
	// repository ID. Useful for the repository page and for operators.
	Excluded map[int64]string

	CohortSize     int
	Considered     int
	FormulaVersion string
}

// All returns ranked and provisional results together, ranked first.
func (r RunResult) All() []Result {
	out := make([]Result, 0, len(r.Ranked)+len(r.Provisional))
	out = append(out, r.Ranked...)
	out = append(out, r.Provisional...)
	return out
}

// Run scores every input for one window.
//
// The run is deterministic: identical inputs and identical `now` always produce
// identical output, including tie ordering. Nothing iterates a map in a way
// that affects results, and every sort is total.
//
// Normalisation happens against the fully eligible cohort only. Provisional
// repositories are then ranked against that same cohort so their scores remain
// comparable, but they never widen or distort the distribution — a burst of
// newly discovered repositories cannot move the established leaderboard.
func Run(now time.Time, w Window, inputs []Input, opts Options) RunResult {
	now = now.UTC()

	result := RunResult{
		Window:         w.Name,
		ScoredAt:       now,
		Excluded:       make(map[int64]string),
		Considered:     len(inputs),
		FormulaVersion: FormulaVersion,
	}

	type entry struct {
		input       Input
		features    Features
		eligibility Eligibility
		code        string
	}

	entries := make([]entry, 0, len(inputs))
	for _, in := range inputs {
		f := ComputeFeatures(now, w, in, opts)
		eligibility, code := Classify(f, w, in, opts)
		if eligibility == NotEligible {
			result.Excluded[in.RepositoryID] = code
			continue
		}
		entries = append(entries, entry{input: in, features: f, eligibility: eligibility, code: code})
	}

	// Build the normalisation cohort from fully eligible repositories.
	cohort := make([]Features, 0, len(entries))
	for _, e := range entries {
		if e.eligibility == EligibleFull {
			cohort = append(cohort, e.features)
		}
	}
	result.CohortSize = len(cohort)

	norm := buildNormalisers(cohort, opts)

	for _, e := range entries {
		res := score(now, w, e.input, e.features, e.eligibility, e.code, norm, opts)
		if e.eligibility == EligibleFull {
			result.Ranked = append(result.Ranked, res)
		} else {
			result.Provisional = append(result.Provisional, res)
		}
	}

	assignRanks(result.Ranked)
	sortByScore(result.Provisional)

	// Collections depend on rank, so they are assigned after ordering.
	for i := range result.Ranked {
		result.Ranked[i].Collections = collectionsFor(w, result.Ranked[i])
	}
	for i := range result.Provisional {
		result.Provisional[i].Collections = collectionsFor(w, result.Provisional[i])
	}

	return result
}

type normalisers struct {
	starVelocity   *Normaliser
	relativeGrowth *Normaliser
	acceleration   *Normaliser
	forkVelocity   *Normaliser
	activity       *Normaliser
	freshness      *Normaliser
}

func buildNormalisers(cohort []Features, opts Options) normalisers {
	starVelocity := make([]float64, 0, len(cohort))
	relativeGrowth := make([]float64, 0, len(cohort))
	acceleration := make([]float64, 0, len(cohort))
	forkVelocity := make([]float64, 0, len(cohort))
	activity := make([]float64, 0, len(cohort))
	freshness := make([]float64, 0, len(cohort))

	for _, f := range cohort {
		starVelocity = append(starVelocity, f.StarVelocity)
		relativeGrowth = append(relativeGrowth, f.RelativeGrowth)
		forkVelocity = append(forkVelocity, f.ForkVelocity)
		activity = append(activity, f.Activity)
		freshness = append(freshness, f.Freshness)
		// Only repositories with a usable midpoint contribute to the
		// acceleration distribution; including fabricated zeros would make
		// unmeasured repositories look decelerating.
		if f.AccelerationKnown {
			acceleration = append(acceleration, f.Acceleration)
		}
	}

	p := opts.WinsorPercentile
	return normalisers{
		// Velocities and growth ratios are heavily right-skewed, so they are
		// log1p-transformed before ranking. Activity and freshness are already
		// bounded in [0, 1] and are ranked directly.
		starVelocity:   NewNormaliser(starVelocity, true, p),
		relativeGrowth: NewNormaliser(relativeGrowth, true, p),
		acceleration:   NewNormaliser(acceleration, true, p),
		forkVelocity:   NewNormaliser(forkVelocity, true, p),
		activity:       NewNormaliser(activity, false, p),
		freshness:      NewNormaliser(freshness, false, p),
	}
}

func score(
	now time.Time,
	w Window,
	in Input,
	f Features,
	eligibility Eligibility,
	code string,
	norm normalisers,
	opts Options,
) Result {
	p := Percentiles{
		StarVelocity:   norm.starVelocity.Rank(f.StarVelocity),
		RelativeGrowth: norm.relativeGrowth.Rank(f.RelativeGrowth),
		ForkVelocity:   norm.forkVelocity.Rank(f.ForkVelocity),
		Activity:       norm.activity.Rank(f.Activity),
		Freshness:      norm.freshness.Rank(f.Freshness),
	}

	// An unmeasurable acceleration takes the neutral 0.5 rather than 0: the
	// repository is not penalised for a gap in RepoMeteor's own history, and
	// the missing measurement is instead reflected in confidence.
	if f.AccelerationKnown {
		p.Acceleration = norm.acceleration.Rank(f.Acceleration)
	} else {
		p.Acceleration = 0.5
	}

	raw := 100 * (opts.Weights.StarVelocity*p.StarVelocity +
		opts.Weights.RelativeGrowth*p.RelativeGrowth +
		opts.Weights.Acceleration*p.Acceleration +
		opts.Weights.ForkVelocity*p.ForkVelocity +
		opts.Weights.Activity*p.Activity +
		opts.Weights.Freshness*p.Freshness)

	confidence := ComputeConfidence(now, w, f, in, eligibility)

	// The confidence adjustment scales a score into [0.75, 1.0] of its raw
	// value. A well-observed repository keeps its full score; a barely observed
	// one is held back rather than removed, which keeps the ranking honest
	// without hiding new projects entirely.
	final := clampFloat(raw*(0.75+0.25*confidence.Value), 0, 100)

	features := f
	return Result{
		RepositoryID:    in.RepositoryID,
		FullName:        in.FullName,
		Window:          w.Name,
		ScoredAt:        now,
		Score:           round(final, 3),
		Eligibility:     eligibility,
		EligibilityCode: code,

		StarsTotal:     f.LatestStars,
		StarDelta:      f.StarDelta,
		StarVelocity:   round(f.StarVelocity, 6),
		RelativeGrowth: round(f.RelativeGrowth, 6),
		Acceleration:   round(f.Acceleration, 6),
		ForkDelta:      f.ForkDelta,
		ForkVelocity:   round(f.ForkVelocity, 6),
		Activity:       round(f.Activity, 6),
		Freshness:      round(f.Freshness, 6),

		Confidence:      confidence.Value,
		ConfidenceLabel: ConfidenceLabel(confidence.Value),

		Reasons:      Explain(now, w, f, in, eligibility),
		AnomalyFlags: DetectAnomalies(w, f, in, eligibility, now),

		Percentiles:    p,
		Features:       features,
		FormulaVersion: FormulaVersion,
	}
}

// sortByScore orders results deterministically: score descending, then star
// delta descending, then repository ID ascending. The final key guarantees a
// total order, so two repositories that tie on every measurement still appear
// in the same sequence on every run.
func sortByScore(results []Result) {
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].StarDelta != results[j].StarDelta {
			return results[i].StarDelta > results[j].StarDelta
		}
		return results[i].RepositoryID < results[j].RepositoryID
	})
}

func assignRanks(results []Result) {
	sortByScore(results)
	for i := range results {
		results[i].Rank = i + 1
	}
}

// collectionsFor assigns deterministic collection membership.
//
// Every collection is computed in every window using that window's own score
// and confidence, so a request such as window=7d&collection=hidden-gems returns
// a meaningful answer instead of an empty list. The API's default window per
// collection is what reproduces the headline definitions.
func collectionsFor(w Window, r Result) []string {
	var out []string

	if r.Eligibility == EligibleFull {
		out = append(out, PrimaryCollectionForWindow(w.Name))

		if r.StarsTotal >= HiddenGemMinStars &&
			r.StarsTotal <= HiddenGemMaxStars &&
			r.Confidence >= ConfidenceMediumThreshold &&
			r.StarDelta > 0 {
			out = append(out, CollectionHiddenGems)
		}

		if r.StarsTotal > EstablishedMinStars &&
			r.Confidence >= ConfidenceMediumThreshold &&
			r.Percentiles.StarVelocity >= 0.60 {
			out = append(out, CollectionEstablished)
		}
	}

	// New Entrants is exactly the provisional set with measured movement.
	if r.Eligibility == EligibleProvisional && (r.StarDelta > 0 || r.ForkDelta > 0) {
		out = append(out, CollectionNewEntrants)
	}

	if out == nil {
		out = []string{}
	}
	return out
}
