// Package clock provides an injectable time source.
//
// Every RepoMeteor code path that depends on "now" — snapshot bucketing, job
// scheduling, retry backoff, scoring windows, confidence, demo seeding — takes
// a Clock rather than calling time.Now directly. Tests use Fake to get
// deterministic results without sleeping.
package clock

import (
	"sync"
	"time"
)

// Clock is a source of the current time.
type Clock interface {
	// Now returns the current instant in UTC.
	Now() time.Time
}

// Real is the production clock. It always returns UTC.
type Real struct{}

// New returns the production clock.
func New() Clock { return Real{} }

// Now returns the current UTC instant.
func (Real) Now() time.Time { return time.Now().UTC() }

// Fake is a manually controlled clock for tests. It is safe for concurrent use.
type Fake struct {
	mu  sync.RWMutex
	now time.Time
}

// NewFake returns a Fake positioned at t (converted to UTC).
func NewFake(t time.Time) *Fake {
	return &Fake{now: t.UTC()}
}

// Now returns the fake clock's current instant.
func (f *Fake) Now() time.Time {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.now
}

// Advance moves the fake clock forward by d.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// Set moves the fake clock to t.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t.UTC()
}

// Bucket truncates t to the start of the interval-sized bucket containing it,
// in UTC. Snapshot deduplication relies on this being computed by application
// code rather than by the database, so that the bucket width is configurable
// and identical across services.
//
// interval must be positive; a non-positive interval falls back to one hour.
func Bucket(t time.Time, interval time.Duration) time.Time {
	if interval <= 0 {
		interval = time.Hour
	}
	return t.UTC().Truncate(interval)
}
