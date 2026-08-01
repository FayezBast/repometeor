package scoring

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestNormaliserRanksMonotonically(t *testing.T) {
	n := NewNormaliser([]float64{1, 2, 3, 4, 5}, false, 0)

	previous := -1.0
	for _, v := range []float64{1, 2, 3, 4, 5} {
		rank := n.Rank(v)
		if rank <= previous {
			t.Fatalf("Rank(%v) = %v, expected it to exceed the previous rank %v", v, rank, previous)
		}
		previous = rank
	}
}

func TestNormaliserRankBounds(t *testing.T) {
	n := NewNormaliser([]float64{10, 20, 30, 40}, false, 0)

	for _, v := range []float64{-100, 0, 25, 1000} {
		if rank := n.Rank(v); rank < 0 || rank > 1 {
			t.Fatalf("Rank(%v) = %v, want a value in [0, 1]", v, rank)
		}
	}
}

func TestNormaliserMidRankForTies(t *testing.T) {
	// Four values, two of which are equal. The tied pair occupies positions 2
	// and 3, so both receive the mid-rank (1 + 0.5*2) / 4 = 0.5.
	n := NewNormaliser([]float64{1, 5, 5, 9}, false, 0)

	first := n.Rank(5)
	second := n.Rank(5)
	if first != second {
		t.Fatalf("tied values received different ranks: %v and %v", first, second)
	}
	if math.Abs(first-0.5) > 1e-9 {
		t.Fatalf("Rank(5) = %v, want 0.5", first)
	}
}

func TestNormaliserAllTiedValues(t *testing.T) {
	n := NewNormaliser([]float64{7, 7, 7, 7}, false, 0)
	if rank := n.Rank(7); math.Abs(rank-0.5) > 1e-9 {
		t.Fatalf("Rank in an all-tied cohort = %v, want 0.5", rank)
	}
}

func TestNormaliserEmptyAndSingletonCohorts(t *testing.T) {
	empty := NewNormaliser(nil, false, 0.01)
	if rank := empty.Rank(42); rank != 0.5 {
		t.Errorf("empty cohort Rank = %v, want the neutral 0.5", rank)
	}

	single := NewNormaliser([]float64{42}, false, 0.01)
	if rank := single.Rank(42); rank != 0.5 {
		t.Errorf("single-member cohort Rank = %v, want the neutral 0.5", rank)
	}
}

func TestNormaliserIsOrderIndependent(t *testing.T) {
	values := []float64{3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5}

	reference := NewNormaliser(values, true, 0.01)
	want := reference.Rank(5)

	for trial := range 20 {
		shuffled := make([]float64, len(values))
		copy(shuffled, values)
		//nolint:gosec // deterministic shuffle for a test, not cryptography
		rng := rand.New(rand.NewPCG(uint64(trial), 99))
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

		got := NewNormaliser(shuffled, true, 0.01).Rank(5)
		if got != want {
			t.Fatalf("shuffle %d produced Rank = %v, want %v; ranking must not depend on input order", trial, got, want)
		}
	}
}

func TestNormaliserLog1pCompressesSkew(t *testing.T) {
	// One extreme value alongside a tight cluster. With log1p the cluster keeps
	// meaningful separation instead of collapsing.
	values := []float64{1, 2, 3, 4, 100000}

	linear := NewNormaliser(values, false, 0)
	logged := NewNormaliser(values, true, 0)

	// Both preserve ordering; the point is that the transform is applied
	// consistently to query values too.
	if logged.Rank(2) >= logged.Rank(3) {
		t.Fatal("log1p ranking must stay monotonic")
	}
	if linear.Rank(100000) != logged.Rank(100000) {
		t.Errorf("the maximum should rank identically under either transform: %v vs %v",
			linear.Rank(100000), logged.Rank(100000))
	}
}

func TestNormaliserAppliesLog1pToQueryValues(t *testing.T) {
	values := []float64{0, 1, 10, 100, 1000}
	n := NewNormaliser(values, true, 0)

	// A query value must be transformed the same way the cohort was. If it were
	// not, Rank(1000) would land in the wrong part of the log-scaled cohort.
	if rank := n.Rank(1000); rank < 0.75 {
		t.Fatalf("Rank(1000) = %v, want the top of the distribution; the query transform is inconsistent", rank)
	}
	if rank := n.Rank(0); rank > 0.25 {
		t.Fatalf("Rank(0) = %v, want the bottom of the distribution", rank)
	}
}

func TestWinsorisationClipsExtremes(t *testing.T) {
	// A cohort of 100 with one absurd outlier.
	values := make([]float64, 0, 100)
	for i := range 99 {
		values = append(values, float64(i))
	}
	values = append(values, 1e9)

	n := NewNormaliser(values, false, 0.01)
	lower, upper := n.Bounds()

	if upper >= 1e9 {
		t.Fatalf("upper bound = %v, want the outlier to be clipped", upper)
	}
	if lower > 5 {
		t.Fatalf("lower bound = %v, want it near the bottom of the distribution", lower)
	}
	// The outlier and the clipping bound now rank identically.
	if n.Rank(1e9) != n.Rank(upper) {
		t.Error("a winsorised outlier should rank the same as the clipping bound")
	}
}

func TestWinsorisationWithZeroPercentileIsANoOp(t *testing.T) {
	values := []float64{1, 2, 3, 1000}
	n := NewNormaliser(values, false, 0)

	lower, upper := n.Bounds()
	if lower != 1 || upper != 1000 {
		t.Fatalf("bounds = (%v, %v), want the untouched (1, 1000)", lower, upper)
	}
}

func TestQuantile(t *testing.T) {
	sorted := []float64{0, 10, 20, 30, 40}

	tests := map[float64]float64{
		0:    0,
		0.5:  20,
		1:    40,
		0.25: 10,
	}
	for p, want := range tests {
		if got := quantile(sorted, p); math.Abs(got-want) > 1e-9 {
			t.Errorf("quantile(%v) = %v, want %v", p, got, want)
		}
	}

	if got := quantile(nil, 0.5); got != 0 {
		t.Errorf("quantile(empty) = %v, want 0", got)
	}
	if got := quantile([]float64{7}, 0.9); got != 7 {
		t.Errorf("quantile(single) = %v, want 7", got)
	}
}

func TestTransformHandlesNonFiniteValues(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := transform(v, true); got != 0 {
			t.Errorf("transform(%v) = %v, want 0", v, got)
		}
		if got := transform(v, false); got != 0 {
			t.Errorf("transform(%v, linear) = %v, want 0", v, got)
		}
	}
}

func TestTransformClampsNegativesBeforeLog1p(t *testing.T) {
	// log1p is undefined at or below -1; negatives are clamped to 0 first.
	if got := transform(-5, true); got != 0 {
		t.Errorf("transform(-5, log) = %v, want 0", got)
	}
}

func TestClampFloat(t *testing.T) {
	if got := clampFloat(math.NaN(), 0, 1); got != 0 {
		t.Errorf("clampFloat(NaN) = %v, want the lower bound", got)
	}
	if got := clampFloat(-1, 0, 1); got != 0 {
		t.Errorf("clampFloat(-1) = %v, want 0", got)
	}
	if got := clampFloat(2, 0, 1); got != 1 {
		t.Errorf("clampFloat(2) = %v, want 1", got)
	}
	if got := clampFloat(0.5, 0, 1); got != 0.5 {
		t.Errorf("clampFloat(0.5) = %v, want 0.5", got)
	}
}
