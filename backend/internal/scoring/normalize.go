package scoring

import (
	"math"
	"sort"
)

// Normaliser converts raw feature values into percentile ranks within the
// current cohort.
//
// Raw features carry incompatible units — stars per hour, a unitless growth
// ratio, a decayed activity score — so they cannot be summed directly. Ranking
// each feature within the cohort puts everything on a common 0..1 scale and
// makes the weighted sum meaningful. It also means the score answers "how does
// this repository compare with everything else RepoMeteor is watching right
// now", which is what a discovery ranking should answer.
type Normaliser struct {
	sorted []float64
	// logged records whether log1p was applied when the cohort was built, so
	// that Rank applies the identical transform to query values.
	logged bool
	// lower and upper are the winsorisation bounds applied before ranking.
	lower float64
	upper float64
}

// NewNormaliser builds a normaliser from the cohort's raw values.
//
// applyLog1p should be true for heavily right-skewed positive metrics such as
// velocities, where a handful of very large values would otherwise compress
// everything else into the bottom of the range.
func NewNormaliser(values []float64, applyLog1p bool, winsorPercentile float64) *Normaliser {
	sorted := make([]float64, 0, len(values))
	for _, v := range values {
		sorted = append(sorted, transform(v, applyLog1p))
	}
	sort.Float64s(sorted)

	n := &Normaliser{sorted: sorted, logged: applyLog1p}
	if len(sorted) == 0 {
		return n
	}
	n.lower = quantile(sorted, winsorPercentile)
	n.upper = quantile(sorted, 1-winsorPercentile)
	if n.upper < n.lower {
		n.lower, n.upper = n.upper, n.lower
	}

	// Apply winsorisation to the stored cohort so that ranks are computed
	// against the clipped distribution, not the raw one.
	for i := range n.sorted {
		n.sorted[i] = clampFloat(n.sorted[i], n.lower, n.upper)
	}
	return n
}

// Rank returns the percentile rank of v in [0, 1].
//
// The mid-rank definition is used: values strictly below v, plus half of the
// values equal to v, divided by the cohort size. Ties therefore receive an
// identical rank in a way that does not depend on input order, which is what
// makes a scoring run reproducible. An empty or single-member cohort yields
// 0.5, an honest "no comparison available" rather than a fabricated extreme.
func (n *Normaliser) Rank(v float64) float64 {
	size := len(n.sorted)
	if size == 0 {
		return 0.5
	}
	if size == 1 {
		return 0.5
	}

	value := clampFloat(transform(v, n.isLogged()), n.lower, n.upper)

	below := sort.SearchFloat64s(n.sorted, value)
	// Find the end of the run of equal values.
	equalEnd := below
	for equalEnd < size && n.sorted[equalEnd] == value {
		equalEnd++
	}
	equal := equalEnd - below

	rank := (float64(below) + 0.5*float64(equal)) / float64(size)
	return clampFloat(rank, 0, 1)
}

// isLogged reports whether this normaliser was built with log1p applied. It is
// recorded implicitly by the transform used at construction; Normaliser stores
// it so Rank applies the same transform to query values.
func (n *Normaliser) isLogged() bool { return n.logged }

// Size returns the cohort size.
func (n *Normaliser) Size() int { return len(n.sorted) }

// Bounds returns the winsorisation bounds, exposed for feature_data.
func (n *Normaliser) Bounds() (lower, upper float64) { return n.lower, n.upper }

func transform(v float64, applyLog1p bool) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	if !applyLog1p {
		return v
	}
	// log1p is only defined for v > -1; every metric it is applied to is
	// non-negative by construction, but clamp defensively.
	if v < 0 {
		v = 0
	}
	return math.Log1p(v)
}

// quantile returns the value at percentile p (0..1) of an ascending slice,
// using linear interpolation between neighbouring points.
func quantile(sorted []float64, p float64) float64 {
	size := len(sorted)
	if size == 0 {
		return 0
	}
	if size == 1 {
		return sorted[0]
	}
	p = clampFloat(p, 0, 1)

	position := p * float64(size-1)
	lowerIndex := int(math.Floor(position))
	upperIndex := int(math.Ceil(position))
	if lowerIndex == upperIndex {
		return sorted[lowerIndex]
	}
	weight := position - float64(lowerIndex)
	return sorted[lowerIndex]*(1-weight) + sorted[upperIndex]*weight
}

func clampFloat(v, low, high float64) float64 {
	if math.IsNaN(v) {
		return low
	}
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}
