package github

import (
	"testing"
	"time"

	"github.com/repometeor/repometeor/backend/internal/clock"
)

var budgetNow = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

func newTestBudget(threshold int) (*Budget, *clock.Fake) {
	fake := clock.NewFake(budgetNow)
	return NewBudget(threshold, fake), fake
}

func coreSnapshot(limit, remaining int, reset time.Time) RateLimitSnapshot {
	return RateLimitSnapshot{
		Resource: "core", Limit: limit, Remaining: remaining,
		Used: limit - remaining, ResetAt: reset, Known: true,
	}
}

func TestBudgetAllowsWhenNothingObserved(t *testing.T) {
	budget, _ := newTestBudget(60)

	for _, purpose := range []Purpose{PurposeBackground, PurposeUserRequested, PurposeHealth} {
		if d := budget.Allow("core", purpose); !d.Allowed {
			t.Errorf("%s should be allowed before any observation: %v", purpose, d.Reason)
		}
	}
}

func TestBudgetThrottlesBackgroundAtThreshold(t *testing.T) {
	budget, _ := newTestBudget(60)
	reset := budgetNow.Add(30 * time.Minute)

	// 60% consumed leaves 2000 of 5000. The background floor is 40% = 2000, and
	// the check is "remaining <= floor", so this is exactly at the boundary.
	budget.Observe(coreSnapshot(5000, 2000, reset))

	if d := budget.Allow("core", PurposeBackground); d.Allowed {
		t.Error("background work should be refused at the threshold")
	}
	if d := budget.Allow("core", PurposeUserRequested); !d.Allowed {
		t.Errorf("user-requested work must still be allowed: %v", d.Reason)
	}
	if d := budget.Allow("core", PurposeHealth); !d.Allowed {
		t.Errorf("health work must still be allowed: %v", d.Reason)
	}
}

func TestBudgetAllowsBackgroundAboveThreshold(t *testing.T) {
	budget, _ := newTestBudget(60)
	budget.Observe(coreSnapshot(5000, 2500, budgetNow.Add(30*time.Minute)))

	if d := budget.Allow("core", PurposeBackground); !d.Allowed {
		t.Errorf("background work should be allowed with 50%% remaining: %v", d.Reason)
	}
}

func TestBudgetRefusesEverythingWhenExhausted(t *testing.T) {
	budget, _ := newTestBudget(60)
	reset := budgetNow.Add(20 * time.Minute)
	budget.Observe(coreSnapshot(5000, 0, reset))

	for _, purpose := range []Purpose{PurposeBackground, PurposeUserRequested, PurposeHealth} {
		d := budget.Allow("core", purpose)
		if d.Allowed {
			t.Errorf("%s should be refused when the budget is exhausted", purpose)
		}
		if !d.RetryAt.After(budgetNow) {
			t.Errorf("%s: RetryAt = %v, want a future time", purpose, d.RetryAt)
		}
		if d.RetryAt.Before(reset) {
			t.Errorf("%s: RetryAt = %v, want it at or after the reset %v", purpose, d.RetryAt, reset)
		}
	}
}

func TestBudgetRefusalNeverBusyLoops(t *testing.T) {
	budget, _ := newTestBudget(60)
	// Exhausted with no reset time supplied.
	budget.Observe(RateLimitSnapshot{Resource: "core", Limit: 5000, Remaining: 0, Known: true})

	d := budget.Allow("core", PurposeBackground)
	if d.Allowed {
		t.Fatal("expected a refusal")
	}
	if !d.RetryAt.After(budgetNow) {
		t.Fatalf("RetryAt = %v, want a future time so the caller waits rather than spinning", d.RetryAt)
	}
}

func TestBudgetRecoversAfterReset(t *testing.T) {
	budget, fake := newTestBudget(60)
	reset := budgetNow.Add(10 * time.Minute)
	budget.Observe(coreSnapshot(5000, 0, reset))

	if d := budget.Allow("core", PurposeBackground); d.Allowed {
		t.Fatal("expected a refusal before the reset")
	}

	fake.Advance(11 * time.Minute)
	if d := budget.Allow("core", PurposeBackground); !d.Allowed {
		t.Fatalf("expected the budget to recover after the reset: %v", d.Reason)
	}
}

func TestBudgetTracksSearchSeparately(t *testing.T) {
	budget, _ := newTestBudget(60)
	reset := budgetNow.Add(time.Minute)

	budget.Observe(coreSnapshot(5000, 5000, reset))
	budget.Observe(RateLimitSnapshot{Resource: "search", Limit: 30, Remaining: 1, ResetAt: reset, Known: true})

	if d := budget.Allow("core", PurposeBackground); !d.Allowed {
		t.Error("core should still be allowed")
	}
	if d := budget.Allow("search", PurposeBackground); d.Allowed {
		t.Error("search should be throttled independently of core")
	}
}

func TestBudgetIgnoresUnknownSnapshots(t *testing.T) {
	budget, _ := newTestBudget(60)
	budget.Observe(coreSnapshot(5000, 100, budgetNow.Add(time.Hour)))
	budget.Observe(RateLimitSnapshot{Known: false, Remaining: 9999})

	if got := budget.Snapshot("core").Remaining; got != 100 {
		t.Fatalf("Remaining = %d, want the last known observation of 100", got)
	}
}

func TestBudgetIgnoresUnrecognisedResources(t *testing.T) {
	budget, _ := newTestBudget(60)
	budget.Observe(coreSnapshot(5000, 4000, budgetNow.Add(time.Hour)))
	budget.Observe(RateLimitSnapshot{Resource: "code_search", Limit: 10, Remaining: 0, Known: true})

	if got := budget.Snapshot("core").Remaining; got != 4000 {
		t.Fatalf("core Remaining = %d; an unrecognised resource must not be filed under core", got)
	}
}

func TestBudgetObserveState(t *testing.T) {
	budget, _ := newTestBudget(60)
	budget.ObserveState(RateLimitState{
		Core:   coreSnapshot(5000, 3000, budgetNow.Add(time.Hour)),
		Search: RateLimitSnapshot{Resource: "search", Limit: 30, Remaining: 20, Known: true},
	})

	state := budget.State()
	if state.Core.Remaining != 3000 || state.Search.Remaining != 20 {
		t.Fatalf("state = %d/%d, want 3000/20", state.Core.Remaining, state.Search.Remaining)
	}
}

func TestBudgetThresholdIsClamped(t *testing.T) {
	for _, threshold := range []int{-10, 0, 500} {
		budget := NewBudget(threshold, clock.NewFake(budgetNow))
		budget.Observe(coreSnapshot(5000, 2500, budgetNow.Add(time.Hour)))
		// Should not panic and should still produce a decision.
		_ = budget.Allow("core", PurposeBackground)
	}
}

func TestBudgetIsConcurrencySafe(t *testing.T) {
	budget, _ := newTestBudget(60)

	done := make(chan struct{})
	for range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := range 200 {
				budget.Observe(coreSnapshot(5000, 5000-i, budgetNow.Add(time.Hour)))
				_ = budget.Allow("core", PurposeBackground)
				_ = budget.State()
			}
		}()
	}
	for range 8 {
		<-done
	}
}

func TestPercentRemaining(t *testing.T) {
	tests := []struct {
		snapshot RateLimitSnapshot
		want     int
	}{
		{coreSnapshot(5000, 5000, budgetNow), 100},
		{coreSnapshot(5000, 2500, budgetNow), 50},
		{coreSnapshot(5000, 0, budgetNow), 0},
		{RateLimitSnapshot{}, 100},
		{RateLimitSnapshot{Known: true, Limit: 0}, 100},
	}
	for _, tc := range tests {
		if got := tc.snapshot.PercentRemaining(); got != tc.want {
			t.Errorf("PercentRemaining(%+v) = %d, want %d", tc.snapshot, got, tc.want)
		}
	}
}
