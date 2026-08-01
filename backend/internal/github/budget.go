package github

import (
	"sync"
	"time"

	"github.com/FayezBast/repometeor/backend/internal/clock"
)

// Purpose describes why a request is being made. The budget manager reserves
// headroom so that a user pressing "track this repository" still works even
// when background collection has spent most of the hour's allowance.
type Purpose string

const (
	// PurposeBackground is routine collection and discovery. It is the first
	// thing to be throttled.
	PurposeBackground Purpose = "background"
	// PurposeUserRequested is work a person is waiting for.
	PurposeUserRequested Purpose = "user_requested"
	// PurposeHealth is an operational check such as reading the rate limit.
	PurposeHealth Purpose = "health"
)

// Budget tracks the remaining GitHub rate-limit allowance per resource and
// decides whether a new request may be scheduled.
//
// It is fed by the headers of every response the client receives, so it stays
// current without spending requests on /rate_limit polling. It never busy-waits:
// callers ask Allow, and a refusal comes with the time at which to try again.
type Budget struct {
	mu sync.RWMutex

	// thresholdPercent is the share of the budget that background work may
	// consume. The remainder is reserved for user-requested and health work.
	thresholdPercent int

	core   RateLimitSnapshot
	search RateLimitSnapshot

	clock clock.Clock
}

// NewBudget constructs a budget manager. thresholdPercent must be 1..100.
func NewBudget(thresholdPercent int, c clock.Clock) *Budget {
	if thresholdPercent < 1 {
		thresholdPercent = 1
	}
	if thresholdPercent > 100 {
		thresholdPercent = 100
	}
	if c == nil {
		c = clock.New()
	}
	return &Budget{thresholdPercent: thresholdPercent, clock: c}
}

// Observe records a snapshot parsed from a response.
func (b *Budget) Observe(snapshot RateLimitSnapshot) {
	if !snapshot.Known {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	switch snapshot.Resource {
	case "search":
		b.search = snapshot
	case "core", "":
		b.core = snapshot
	default:
		// Unknown resources are ignored rather than misfiled under core.
	}
}

// ObserveState records a full /rate_limit response.
func (b *Budget) ObserveState(state RateLimitState) {
	b.Observe(state.Core)
	b.Observe(state.Search)
}

// Decision is the outcome of a budget check.
type Decision struct {
	Allowed bool
	// RetryAt is when the caller should try again. Zero when allowed.
	RetryAt time.Time
	// Reason is a short machine-readable explanation for logs and job errors.
	Reason string
	// Remaining is the observed remaining allowance, for logging.
	Remaining int
}

// Allow reports whether a request against `resource` may proceed now.
//
// Background work is refused once consumption passes the configured threshold,
// leaving the remainder for people. User-requested and health work may spend
// down to the last unit, and is refused only when the budget is genuinely
// exhausted.
func (b *Budget) Allow(resource string, purpose Purpose) Decision {
	b.mu.RLock()
	snapshot := b.core
	if resource == "search" {
		snapshot = b.search
	}
	threshold := b.thresholdPercent
	b.mu.RUnlock()

	// With no observation yet, allow the request: the response will teach the
	// budget what the real state is.
	if !snapshot.Known || snapshot.Limit <= 0 {
		return Decision{Allowed: true}
	}

	now := b.clock.Now()

	// A reset that has already passed means the observation is stale; the
	// allowance has refilled.
	if !snapshot.ResetAt.IsZero() && !snapshot.ResetAt.After(now) {
		return Decision{Allowed: true}
	}

	if snapshot.Remaining <= 0 {
		return Decision{
			Allowed:   false,
			RetryAt:   resetOrDefault(snapshot.ResetAt, now),
			Reason:    "rate limit exhausted",
			Remaining: 0,
		}
	}

	if purpose == PurposeBackground {
		// Background work may consume down to (100 - threshold)% remaining.
		floor := snapshot.Limit * (100 - threshold) / 100
		if snapshot.Remaining <= floor {
			return Decision{
				Allowed:   false,
				RetryAt:   resetOrDefault(snapshot.ResetAt, now),
				Reason:    "background rate budget reached; reserving the remainder for user requests",
				Remaining: snapshot.Remaining,
			}
		}
	}

	return Decision{Allowed: true, Remaining: snapshot.Remaining}
}

// Snapshot returns the current observed state for a resource.
func (b *Budget) Snapshot(resource string) RateLimitSnapshot {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if resource == "search" {
		return b.search
	}
	return b.core
}

// State returns both observed resources.
func (b *Budget) State() RateLimitState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return RateLimitState{Core: b.core, Search: b.search}
}

// resetOrDefault returns the reset time, or a conservative one minute ahead
// when GitHub did not supply one.
func resetOrDefault(resetAt, now time.Time) time.Time {
	if resetAt.IsZero() || !resetAt.After(now) {
		return now.Add(time.Minute)
	}
	// Add a second of slack so a request does not land exactly on the boundary.
	return resetAt.Add(time.Second)
}
