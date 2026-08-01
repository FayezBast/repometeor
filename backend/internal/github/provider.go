// Package github talks to the GitHub REST API.
//
// Only public, aggregate repository information is requested. RepoMeteor never
// calls the stargazers endpoint, never asks for user profiles, and never stores
// who starred anything — its history comes exclusively from its own periodic
// snapshots of the aggregate counters. See docs/PRIVACY.md.
package github

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Provider is the interface every GitHub implementation satisfies.
//
// It exists so that demo mode and the entire test suite can run with a fixture
// implementation and no network access at all.
type Provider interface {
	// SearchRepositories runs one partitioned repository search.
	SearchRepositories(ctx context.Context, input SearchInput) (SearchResult, error)

	// GetRepository fetches aggregate metadata for one repository. Validators
	// enable a conditional request; a 304 response is reported through
	// RepositoryResult.NotModified rather than as an error.
	GetRepository(ctx context.Context, owner, name string, validators Validators) (RepositoryResult, error)

	// GetLatestRelease fetches the most recent published release, if any.
	GetLatestRelease(ctx context.Context, owner, name string, validators Validators) (ReleaseResult, error)

	// GetRateLimit reports the current budget for every resource.
	GetRateLimit(ctx context.Context) (RateLimitState, error)
}

// Validators carry conditional-request headers from a previous response.
type Validators struct {
	ETag         string
	LastModified string
}

// Empty reports whether a conditional request is possible.
func (v Validators) Empty() bool { return v.ETag == "" && v.LastModified == "" }

// Repository is the aggregate public metadata RepoMeteor collects.
//
// The field set is deliberately closed: adding anything user-identifying here
// would be a privacy regression, and the test suite asserts the shape.
type Repository struct {
	ID              int64
	NodeID          string
	Owner           string
	Name            string
	FullName        string
	Description     string
	HTMLURL         string
	HomepageURL     string
	AvatarURL       string
	DefaultBranch   string
	PrimaryLanguage string
	Topics          []string
	LicenseSPDX     string

	Stars       int
	Forks       int
	OpenIssues  int
	Subscribers int

	IsFork     bool
	IsArchived bool
	IsDisabled bool
	IsTemplate bool
	IsPrivate  bool

	CreatedAt time.Time
	UpdatedAt *time.Time
	PushedAt  *time.Time
}

// Release is the minimal published-release metadata used by the activity score.
type Release struct {
	TagName     string
	PublishedAt *time.Time
}

// RepositoryResult wraps a repository response together with cache metadata.
type RepositoryResult struct {
	Repository  Repository
	NotModified bool
	Validators  Validators
	RateLimit   RateLimitSnapshot
	// CanonicalOwner and CanonicalName are set when GitHub redirected the
	// request, meaning the repository was renamed or transferred.
	CanonicalOwner string
	CanonicalName  string
	Redirected     bool
}

// ReleaseResult wraps a release response.
type ReleaseResult struct {
	Release     Release
	Found       bool
	NotModified bool
	Validators  Validators
	RateLimit   RateLimitSnapshot
}

// SearchInput describes one partitioned repository search.
//
// The caller supplies structured criteria rather than a raw query string, so a
// user-controlled value can never reach GitHub's query grammar.
type SearchInput struct {
	// Language filters by primary language, e.g. "Go".
	Language string
	// CreatedAfter and CreatedBefore bound the repository creation date.
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
	// PushedAfter bounds the most recent push.
	PushedAfter *time.Time
	// MinStars is the lower bound on stargazer count.
	MinStars int
	// MaxStars, when positive, bounds the search from above so that large
	// result sets can be partitioned into slices GitHub will fully return.
	MaxStars int
	// ExcludeForks and ExcludeArchived add the corresponding qualifiers.
	ExcludeForks    bool
	ExcludeArchived bool

	Sort    string // "stars" or "updated"
	Order   string // "desc" or "asc"
	Page    int
	PerPage int
}

// SearchResult is one page of search results.
type SearchResult struct {
	Repositories []Repository
	TotalCount   int
	// IncompleteResults mirrors GitHub's flag: the search timed out server-side
	// and the page may be missing entries.
	IncompleteResults bool
	Query             string
	RateLimit         RateLimitSnapshot
	HasNextPage       bool
}

// RateLimitSnapshot is the budget state parsed from one response's headers.
type RateLimitSnapshot struct {
	Resource  string
	Limit     int
	Remaining int
	Used      int
	ResetAt   time.Time
	// Known is false when the response carried no rate-limit headers.
	Known bool
}

// PercentRemaining returns the share of the budget still available, 0..100.
func (s RateLimitSnapshot) PercentRemaining() int {
	if !s.Known || s.Limit <= 0 {
		return 100
	}
	return int(float64(s.Remaining) / float64(s.Limit) * 100)
}

// RateLimitState is the full budget across resources.
type RateLimitState struct {
	Core    RateLimitSnapshot
	Search  RateLimitSnapshot
	GraphQL RateLimitSnapshot
}

// ErrorKind classifies a GitHub failure so that callers can react correctly:
// retry, back off, mark missing, or give up.
type ErrorKind string

const (
	// ErrNotFound: the repository does not exist or is private.
	ErrNotFound ErrorKind = "not_found"
	// ErrRenamed: GitHub redirected to a new owner/name.
	ErrRenamed ErrorKind = "renamed"
	// ErrArchived: the repository exists but is archived.
	ErrArchived ErrorKind = "archived"
	// ErrDisabled: the repository exists but is disabled.
	ErrDisabled ErrorKind = "disabled"
	// ErrUnauthorized: the token is missing, invalid or revoked.
	ErrUnauthorized ErrorKind = "unauthorized"
	// ErrForbidden: access is denied for a reason other than rate limiting.
	ErrForbidden ErrorKind = "forbidden"
	// ErrRateLimited: the primary rate limit is exhausted.
	ErrRateLimited ErrorKind = "rate_limited"
	// ErrSecondaryRateLimit: GitHub asked the client to slow down.
	ErrSecondaryRateLimit ErrorKind = "secondary_rate_limit"
	// ErrServer: a 5xx response.
	ErrServer ErrorKind = "server_error"
	// ErrTimeout: the request exceeded its deadline.
	ErrTimeout ErrorKind = "timeout"
	// ErrNetwork: a transport-level failure.
	ErrNetwork ErrorKind = "network_error"
	// ErrMalformed: the response body could not be decoded.
	ErrMalformed ErrorKind = "malformed_response"
	// ErrValidation: GitHub rejected the request as invalid (422).
	ErrValidation ErrorKind = "validation_failed"
	// ErrUnknown: anything unclassified.
	ErrUnknown ErrorKind = "unknown"
)

// Retryable reports whether an error of this kind is worth another attempt.
// Permanent 4xx failures are never retried; retrying them would only burn
// budget and delay the queue.
func (k ErrorKind) Retryable() bool {
	switch k {
	case ErrServer, ErrTimeout, ErrNetwork, ErrSecondaryRateLimit:
		return true
	default:
		return false
	}
}

// Error is a classified GitHub failure.
type Error struct {
	Kind       ErrorKind
	StatusCode int
	Message    string
	// RetryAfter is set when GitHub told the client how long to wait.
	RetryAfter time.Duration
	// CanonicalOwner and CanonicalName are set for ErrRenamed.
	CanonicalOwner string
	CanonicalName  string
	RateLimit      RateLimitSnapshot
	cause          error
}

func (e *Error) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("github %s (status %d): %s", e.Kind, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("github %s: %s", e.Kind, e.Message)
}

// Unwrap exposes the underlying transport error, if any.
func (e *Error) Unwrap() error { return e.cause }

// Is supports errors.Is against a bare ErrorKind sentinel.
func (e *Error) Is(target error) bool {
	var other *Error
	if errors.As(target, &other) {
		return e.Kind == other.Kind
	}
	return false
}

// KindOf extracts the classification from an error, or ErrUnknown.
func KindOf(err error) ErrorKind {
	var ghErr *Error
	if errors.As(err, &ghErr) {
		return ghErr.Kind
	}
	if err == nil {
		return ""
	}
	return ErrUnknown
}

// AsError returns the typed error if err is one.
func AsError(err error) (*Error, bool) {
	var ghErr *Error
	ok := errors.As(err, &ghErr)
	return ghErr, ok
}

// newError builds a classified error.
func newError(kind ErrorKind, status int, message string, cause error) *Error {
	return &Error{Kind: kind, StatusCode: status, Message: message, cause: cause}
}
