package github

import (
	"context"
	"strings"
	"sync"
	"time"
)

// FixtureProvider is an in-memory Provider that never touches the network.
//
// It backs demo mode and the whole test suite. Because it satisfies the same
// interface as the live client, the collector, scoring and API code paths that
// run in demo mode are the very same code paths that run in production — demo
// mode is a different data source, not a different program.
type FixtureProvider struct {
	mu sync.RWMutex

	// repositories is keyed by lower-cased "owner/name".
	repositories map[string]Repository
	releases     map[string]Release

	// renames maps a lower-cased old "owner/name" to its canonical form.
	renames map[string]string

	// missing holds lower-cased names that should return ErrNotFound.
	missing map[string]struct{}

	// failures lets a test force a specific error for one repository.
	failures map[string]*Error

	// etags simulates conditional requests. When a request supplies the stored
	// ETag, the provider answers 304.
	etags map[string]string

	rateLimit RateLimitState

	// Calls records every request for assertions in tests.
	calls []string
}

// NewFixtureProvider constructs an empty fixture provider.
func NewFixtureProvider() *FixtureProvider {
	return &FixtureProvider{
		repositories: make(map[string]Repository),
		releases:     make(map[string]Release),
		renames:      make(map[string]string),
		missing:      make(map[string]struct{}),
		failures:     make(map[string]*Error),
		etags:        make(map[string]string),
		rateLimit: RateLimitState{
			Core:   RateLimitSnapshot{Resource: "core", Limit: 5000, Remaining: 5000, Known: true},
			Search: RateLimitSnapshot{Resource: "search", Limit: 30, Remaining: 30, Known: true},
		},
	}
}

var _ Provider = (*FixtureProvider)(nil)

func key(owner, name string) string {
	return strings.ToLower(owner + "/" + name)
}

// AddRepository registers a repository.
func (f *FixtureProvider) AddRepository(repo Repository) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repositories[key(repo.Owner, repo.Name)] = repo
}

// AddRelease registers a repository's latest release.
func (f *FixtureProvider) AddRelease(owner, name string, release Release) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases[key(owner, name)] = release
}

// AddRename makes an old name resolve to a canonical one.
func (f *FixtureProvider) AddRename(oldOwner, oldName, newOwner, newName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renames[key(oldOwner, oldName)] = newOwner + "/" + newName
}

// MarkMissing makes a repository return ErrNotFound.
func (f *FixtureProvider) MarkMissing(owner, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.missing[key(owner, name)] = struct{}{}
}

// SetETag makes a repository answer 304 when the matching validator is sent.
func (f *FixtureProvider) SetETag(owner, name, etag string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.etags[key(owner, name)] = etag
}

// FailWith makes a repository return a specific classified error.
func (f *FixtureProvider) FailWith(owner, name string, err *Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[key(owner, name)] = err
}

// SetRateLimit overrides the reported budget.
func (f *FixtureProvider) SetRateLimit(state RateLimitState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rateLimit = state
}

// Calls returns every recorded request, in order.
func (f *FixtureProvider) Calls() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// Repositories returns every registered repository, for seeding.
func (f *FixtureProvider) Repositories() []Repository {
	f.mu.RLock()
	defer f.mu.RUnlock()

	out := make([]Repository, 0, len(f.repositories))
	for _, r := range f.repositories {
		out = append(out, r)
	}
	return out
}

func (f *FixtureProvider) record(call string) {
	f.calls = append(f.calls, call)
}

// GetRepository implements Provider.
func (f *FixtureProvider) GetRepository(ctx context.Context, owner, name string, validators Validators) (RepositoryResult, error) {
	if err := ctx.Err(); err != nil {
		return RepositoryResult{}, newError(ErrTimeout, 0, "context cancelled", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	k := key(owner, name)
	f.record("GetRepository " + k)

	if err, ok := f.failures[k]; ok {
		return RepositoryResult{}, err
	}
	if _, gone := f.missing[k]; gone {
		return RepositoryResult{}, &Error{Kind: ErrNotFound, StatusCode: 404, Message: "Not Found"}
	}
	if canonical, renamed := f.renames[k]; renamed {
		newOwner, newName, _ := strings.Cut(canonical, "/")
		return RepositoryResult{}, &Error{
			Kind:           ErrRenamed,
			StatusCode:     301,
			Message:        "repository moved to " + canonical,
			CanonicalOwner: newOwner,
			CanonicalName:  newName,
		}
	}

	repo, ok := f.repositories[k]
	if !ok {
		return RepositoryResult{}, &Error{Kind: ErrNotFound, StatusCode: 404, Message: "Not Found"}
	}

	if etag, hasETag := f.etags[k]; hasETag && validators.ETag == etag {
		return RepositoryResult{
			NotModified: true,
			Validators:  Validators{ETag: etag},
			RateLimit:   f.rateLimit.Core,
		}, nil
	}

	result := RepositoryResult{
		Repository: repo,
		Validators: Validators{ETag: f.etags[k]},
		RateLimit:  f.rateLimit.Core,
	}
	return result, nil
}

// GetLatestRelease implements Provider.
func (f *FixtureProvider) GetLatestRelease(ctx context.Context, owner, name string, _ Validators) (ReleaseResult, error) {
	if err := ctx.Err(); err != nil {
		return ReleaseResult{}, newError(ErrTimeout, 0, "context cancelled", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	k := key(owner, name)
	f.record("GetLatestRelease " + k)

	release, ok := f.releases[k]
	if !ok {
		return ReleaseResult{Found: false, RateLimit: f.rateLimit.Core}, nil
	}
	return ReleaseResult{Release: release, Found: true, RateLimit: f.rateLimit.Core}, nil
}

// SearchRepositories implements Provider. It applies the same filters the live
// query expresses, so discovery logic can be tested end to end offline.
func (f *FixtureProvider) SearchRepositories(ctx context.Context, input SearchInput) (SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return SearchResult{}, newError(ErrTimeout, 0, "context cancelled", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	query := BuildSearchQuery(input)
	f.record("SearchRepositories " + query)

	var matched []Repository
	for _, repo := range f.repositories {
		if !matchesSearch(repo, input) {
			continue
		}
		matched = append(matched, repo)
	}

	// Deterministic ordering by full name so tests are reproducible.
	sortRepositoriesByName(matched)

	return SearchResult{
		Repositories: matched,
		TotalCount:   len(matched),
		Query:        query,
		RateLimit:    f.rateLimit.Search,
	}, nil
}

// GetRateLimit implements Provider.
func (f *FixtureProvider) GetRateLimit(ctx context.Context) (RateLimitState, error) {
	if err := ctx.Err(); err != nil {
		return RateLimitState{}, newError(ErrTimeout, 0, "context cancelled", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("GetRateLimit")
	return f.rateLimit, nil
}

func matchesSearch(repo Repository, input SearchInput) bool {
	if repo.IsPrivate {
		return false
	}
	if input.Language != "" && !strings.EqualFold(repo.PrimaryLanguage, input.Language) {
		return false
	}
	if input.ExcludeForks && repo.IsFork {
		return false
	}
	if input.ExcludeArchived && repo.IsArchived {
		return false
	}
	if input.MinStars > 0 && repo.Stars < input.MinStars {
		return false
	}
	if input.MaxStars > 0 && repo.Stars > input.MaxStars {
		return false
	}
	if input.CreatedAfter != nil && repo.CreatedAt.Before(*input.CreatedAfter) {
		return false
	}
	if input.CreatedBefore != nil && repo.CreatedAt.After(*input.CreatedBefore) {
		return false
	}
	if input.PushedAfter != nil {
		if repo.PushedAt == nil || repo.PushedAt.Before(*input.PushedAfter) {
			return false
		}
	}
	return true
}

func sortRepositoriesByName(repos []Repository) {
	// Insertion sort keeps the dependency surface at zero and the slice is
	// small in every fixture scenario.
	for i := 1; i < len(repos); i++ {
		for j := i; j > 0 && strings.ToLower(repos[j].FullName) < strings.ToLower(repos[j-1].FullName); j-- {
			repos[j], repos[j-1] = repos[j-1], repos[j]
		}
	}
}

// StaticTime is a helper for building fixture timestamps.
func StaticTime(year int, month time.Month, day, hour int) time.Time {
	return time.Date(year, month, day, hour, 0, 0, 0, time.UTC)
}
