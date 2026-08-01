package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/repometeor/repometeor/backend/internal/config"
	"github.com/repometeor/repometeor/backend/internal/reponame"
)

// Client is the live GitHub REST provider.
type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
	apiVersion string
	userAgent  string
	maxRetries int
	maxBody    int64
	log        *slog.Logger

	// semaphore bounds concurrent in-flight requests. The default of one is
	// deliberately conservative: RepoMeteor is a background collector and has
	// no reason to hammer the API.
	semaphore chan struct{}

	// observer receives per-request telemetry. Optional.
	observer Observer

	// sleep is injectable so retry tests do not actually wait.
	sleep func(context.Context, time.Duration) error
}

// Observer receives request-level telemetry.
type Observer interface {
	ObserveGitHubRequest(operation, outcome string)
	IncGitHubCacheHit()
	IncGitHubRetry()
	SetGitHubRemaining(resource string, remaining int)
}

// NewClient builds a live provider from configuration.
func NewClient(cfg config.GitHubConfig, log *slog.Logger, observer Observer) *Client {
	concurrency := cfg.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	return &Client{
		httpClient: &http.Client{
			Timeout: cfg.RequestTimeout,
			// Never follow a redirect automatically: a 301 from GitHub means
			// the repository was renamed, which is information the collector
			// must record rather than silently absorb.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		token:      cfg.Token,
		apiVersion: cfg.APIVersion,
		userAgent:  cfg.UserAgent,
		maxRetries: cfg.MaxRetries,
		maxBody:    cfg.MaxResponseBytes,
		log:        log,
		semaphore:  make(chan struct{}, concurrency),
		observer:   observer,
		sleep:      sleepContext,
	}
}

var _ Provider = (*Client)(nil)

// GetRepository fetches aggregate repository metadata.
func (c *Client) GetRepository(ctx context.Context, owner, name string, validators Validators) (RepositoryResult, error) {
	// The path is rebuilt from validated components. A caller cannot supply a
	// URL, so there is no way to point this request at another host.
	validated, err := reponame.Parse(owner + "/" + name)
	if err != nil {
		return RepositoryResult{}, newError(ErrValidation, 0, err.Error(), err)
	}

	path := "/repos/" + url.PathEscape(validated.Owner) + "/" + url.PathEscape(validated.Repository)

	resp, err := c.do(ctx, "get_repository", path, validators)
	if err != nil {
		return RepositoryResult{}, err
	}

	result := RepositoryResult{
		NotModified: resp.notModified,
		Validators:  resp.validators,
		RateLimit:   resp.rateLimit,
	}
	if resp.notModified {
		return result, nil
	}

	var payload repositoryPayload
	if err := json.Unmarshal(resp.body, &payload); err != nil {
		return RepositoryResult{}, newError(ErrMalformed, resp.statusCode, "repository response could not be decoded", err)
	}
	repo, err := payload.toRepository()
	if err != nil {
		return RepositoryResult{}, newError(ErrMalformed, resp.statusCode, err.Error(), err)
	}

	result.Repository = repo
	if !strings.EqualFold(repo.Owner, validated.Owner) || !strings.EqualFold(repo.Name, validated.Repository) {
		// GitHub answered with a different canonical name than was requested,
		// which happens after a rename even without a redirect status.
		result.Redirected = true
		result.CanonicalOwner = repo.Owner
		result.CanonicalName = repo.Name
	}
	return result, nil
}

// GetLatestRelease fetches the most recent published release.
//
// A repository with no releases returns Found=false rather than an error: that
// is an ordinary and expected state, not a failure.
func (c *Client) GetLatestRelease(ctx context.Context, owner, name string, validators Validators) (ReleaseResult, error) {
	validated, err := reponame.Parse(owner + "/" + name)
	if err != nil {
		return ReleaseResult{}, newError(ErrValidation, 0, err.Error(), err)
	}

	path := "/repos/" + url.PathEscape(validated.Owner) + "/" + url.PathEscape(validated.Repository) + "/releases/latest"

	resp, err := c.do(ctx, "get_latest_release", path, validators)
	if err != nil {
		if KindOf(err) == ErrNotFound {
			return ReleaseResult{Found: false}, nil
		}
		return ReleaseResult{}, err
	}

	result := ReleaseResult{
		NotModified: resp.notModified,
		Validators:  resp.validators,
		RateLimit:   resp.rateLimit,
	}
	if resp.notModified {
		result.Found = true
		return result, nil
	}

	var payload releasePayload
	if err := json.Unmarshal(resp.body, &payload); err != nil {
		return ReleaseResult{}, newError(ErrMalformed, resp.statusCode, "release response could not be decoded", err)
	}

	result.Found = true
	result.Release = Release{TagName: payload.TagName, PublishedAt: payload.PublishedAt}
	return result, nil
}

// SearchRepositories runs one page of a partitioned repository search.
func (c *Client) SearchRepositories(ctx context.Context, input SearchInput) (SearchResult, error) {
	query := BuildSearchQuery(input)

	params := url.Values{}
	params.Set("q", query)
	params.Set("sort", normaliseSort(input.Sort))
	params.Set("order", normaliseOrder(input.Order))
	params.Set("per_page", strconv.Itoa(clampInt(input.PerPage, 1, 100)))
	params.Set("page", strconv.Itoa(clampInt(input.Page, 1, 10)))

	resp, err := c.do(ctx, "search_repositories", "/search/repositories?"+params.Encode(), Validators{})
	if err != nil {
		return SearchResult{}, err
	}

	var payload searchPayload
	if err := json.Unmarshal(resp.body, &payload); err != nil {
		return SearchResult{}, newError(ErrMalformed, resp.statusCode, "search response could not be decoded", err)
	}

	out := SearchResult{
		TotalCount:        payload.TotalCount,
		IncompleteResults: payload.IncompleteResults,
		Query:             query,
		RateLimit:         resp.rateLimit,
	}
	for _, item := range payload.Items {
		repo, err := item.toRepository()
		if err != nil {
			// One malformed entry must not discard an otherwise good page.
			c.log.WarnContext(ctx, "skipping malformed search result", slog.String("error", err.Error()))
			continue
		}
		out.Repositories = append(out.Repositories, repo)
	}

	perPage := clampInt(input.PerPage, 1, 100)
	page := clampInt(input.Page, 1, 10)
	// GitHub search caps results at 1000 regardless of total_count.
	out.HasNextPage = len(out.Repositories) == perPage && page*perPage < min(payload.TotalCount, 1000)

	return out, nil
}

// GetRateLimit reports the current budget across resources.
func (c *Client) GetRateLimit(ctx context.Context) (RateLimitState, error) {
	resp, err := c.do(ctx, "get_rate_limit", "/rate_limit", Validators{})
	if err != nil {
		return RateLimitState{}, err
	}

	var payload rateLimitPayload
	if err := json.Unmarshal(resp.body, &payload); err != nil {
		return RateLimitState{}, newError(ErrMalformed, resp.statusCode, "rate limit response could not be decoded", err)
	}

	return RateLimitState{
		Core:    payload.Resources.Core.toSnapshot("core"),
		Search:  payload.Resources.Search.toSnapshot("search"),
		GraphQL: payload.Resources.GraphQL.toSnapshot("graphql"),
	}, nil
}

// --- transport ---------------------------------------------------------------

type response struct {
	statusCode  int
	body        []byte
	validators  Validators
	rateLimit   RateLimitSnapshot
	notModified bool
}

// do performs a request with retries, conditional headers and classification.
func (c *Client) do(ctx context.Context, operation, path string, validators Validators) (*response, error) {
	var lastErr error

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			delay := backoffDelay(attempt, lastErr)
			c.log.DebugContext(ctx, "retrying github request",
				slog.String("operation", operation),
				slog.Int("attempt", attempt),
				slog.Int64("delayMs", delay.Milliseconds()),
			)
			if c.observer != nil {
				c.observer.IncGitHubRetry()
			}
			if err := c.sleep(ctx, delay); err != nil {
				return nil, newError(ErrTimeout, 0, "context cancelled while backing off", err)
			}
		}

		resp, err := c.attempt(ctx, operation, path, validators)
		if err == nil {
			c.observe(operation, outcomeFor(resp))
			return resp, nil
		}

		lastErr = err
		if !KindOf(err).Retryable() {
			c.observe(operation, string(KindOf(err)))
			return nil, err
		}
	}

	c.observe(operation, string(KindOf(lastErr))+"_exhausted")
	return nil, fmt.Errorf("github request failed after %d attempts: %w", c.maxRetries+1, lastErr)
}

func (c *Client) attempt(ctx context.Context, operation, path string, validators Validators) (*response, error) {
	// Bound concurrency before doing any work.
	select {
	case c.semaphore <- struct{}{}:
		defer func() { <-c.semaphore }()
	case <-ctx.Done():
		return nil, newError(ErrTimeout, 0, "context cancelled while waiting for a request slot", ctx.Err())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, newError(ErrValidation, 0, "could not build request", err)
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", c.apiVersion)
	req.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if validators.ETag != "" {
		req.Header.Set("If-None-Match", validators.ETag)
	} else if validators.LastModified != "" {
		req.Header.Set("If-Modified-Since", validators.LastModified)
	}

	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || isTimeout(err) {
			return nil, newError(ErrTimeout, 0, "request timed out", err)
		}
		return nil, newError(ErrNetwork, 0, "request failed", err)
	}
	defer func() {
		// Drain a bounded amount so the connection can be reused, then close.
		_, _ = io.Copy(io.Discard, io.LimitReader(httpResp.Body, 64*1024))
		_ = httpResp.Body.Close()
	}()

	rateLimit := parseRateLimit(httpResp.Header)
	if rateLimit.Known && c.observer != nil {
		c.observer.SetGitHubRemaining(rateLimit.Resource, rateLimit.Remaining)
	}

	out := &response{
		statusCode: httpResp.StatusCode,
		rateLimit:  rateLimit,
		validators: Validators{
			ETag:         httpResp.Header.Get("ETag"),
			LastModified: httpResp.Header.Get("Last-Modified"),
		},
	}

	if httpResp.StatusCode == http.StatusNotModified {
		// Preserve the previous validators: a 304 does not always resend them.
		if out.validators.ETag == "" {
			out.validators.ETag = validators.ETag
		}
		if out.validators.LastModified == "" {
			out.validators.LastModified = validators.LastModified
		}
		out.notModified = true
		if c.observer != nil {
			c.observer.IncGitHubCacheHit()
		}
		return out, nil
	}

	if httpResp.StatusCode >= 200 && httpResp.StatusCode < 300 {
		body, err := io.ReadAll(io.LimitReader(httpResp.Body, c.maxBody))
		if err != nil {
			return nil, newError(ErrNetwork, httpResp.StatusCode, "could not read response body", err)
		}
		out.body = body
		return out, nil
	}

	return nil, c.classify(httpResp, rateLimit)
}

// classify turns a non-success response into a typed error.
func (c *Client) classify(resp *http.Response, rateLimit RateLimitSnapshot) *Error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
	message := extractMessage(body)

	err := &Error{StatusCode: resp.StatusCode, Message: message, RateLimit: rateLimit}
	err.RetryAfter = parseRetryAfter(resp.Header)

	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusPermanentRedirect, http.StatusTemporaryRedirect, http.StatusFound:
		err.Kind = ErrRenamed
		if owner, name, ok := parseRedirectTarget(resp.Header.Get("Location")); ok {
			err.CanonicalOwner, err.CanonicalName = owner, name
			err.Message = fmt.Sprintf("repository moved to %s/%s", owner, name)
		} else {
			err.Message = "repository moved but the new location could not be parsed"
		}

	case http.StatusNotFound:
		err.Kind = ErrNotFound
		if message == "" {
			err.Message = "repository not found or not public"
		}

	case http.StatusUnauthorized:
		err.Kind = ErrUnauthorized
		err.Message = "github rejected the credentials"

	case http.StatusForbidden, http.StatusTooManyRequests:
		// 403 is overloaded: it covers both rate limiting and plain denial.
		// The headers are what distinguish them.
		switch {
		case rateLimit.Known && rateLimit.Remaining == 0:
			err.Kind = ErrRateLimited
			err.Message = "primary rate limit exhausted"
			if !rateLimit.ResetAt.IsZero() {
				err.RetryAfter = time.Until(rateLimit.ResetAt)
			}
		case err.RetryAfter > 0 || mentionsSecondaryLimit(message):
			err.Kind = ErrSecondaryRateLimit
			if err.Message == "" {
				err.Message = "secondary rate limit triggered"
			}
		default:
			err.Kind = ErrForbidden
			if err.Message == "" {
				err.Message = "access denied"
			}
		}

	case http.StatusUnprocessableEntity:
		err.Kind = ErrValidation

	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout,
		http.StatusInternalServerError:
		err.Kind = ErrServer
		if err.Message == "" {
			err.Message = "github returned a server error"
		}

	default:
		if resp.StatusCode >= 500 {
			err.Kind = ErrServer
		} else {
			err.Kind = ErrUnknown
		}
	}

	// Never let a token or header value reach the error string.
	err.Message = sanitiseMessage(err.Message)
	return err
}

func (c *Client) observe(operation, outcome string) {
	if c.observer != nil {
		c.observer.ObserveGitHubRequest(operation, outcome)
	}
}

func outcomeFor(resp *response) string {
	if resp.notModified {
		return "not_modified"
	}
	return "ok"
}

// backoffDelay computes the wait before a retry.
//
// GitHub's Retry-After always wins when present. Otherwise the delay grows
// exponentially from one second and carries ±25% jitter, so that a fleet of
// workers recovering from the same outage does not resynchronise.
func backoffDelay(attempt int, lastErr error) time.Duration {
	const (
		base = time.Second
		cap_ = 60 * time.Second
	)

	if ghErr, ok := AsError(lastErr); ok && ghErr.RetryAfter > 0 {
		if ghErr.RetryAfter > 5*time.Minute {
			return 5 * time.Minute
		}
		return ghErr.RetryAfter
	}

	exponent := min(attempt-1, 10)
	delay := time.Duration(float64(base) * math.Pow(2, float64(exponent)))
	if delay > cap_ || delay <= 0 {
		delay = cap_
	}

	jitter := (rand.Float64()*0.5 - 0.25) * float64(delay) //nolint:gosec // jitter, not cryptography
	withJitter := time.Duration(float64(delay) + jitter)

	// Clamp after jitter so the documented cap is a real upper bound.
	return clampDuration(withJitter, 100*time.Millisecond, cap_)
}

func clampDuration(d, low, high time.Duration) time.Duration {
	if d < low {
		return low
	}
	if d > high {
		return high
	}
	return d
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func isTimeout(err error) bool {
	var timeoutErr interface{ Timeout() bool }
	return errors.As(err, &timeoutErr) && timeoutErr.Timeout()
}

// parseRateLimit reads the standard GitHub budget headers.
func parseRateLimit(h http.Header) RateLimitSnapshot {
	limit := h.Get("X-RateLimit-Limit")
	if limit == "" {
		return RateLimitSnapshot{}
	}

	snapshot := RateLimitSnapshot{
		Resource: h.Get("X-RateLimit-Resource"),
		Known:    true,
	}
	if snapshot.Resource == "" {
		snapshot.Resource = "core"
	}
	snapshot.Limit = atoiOrZero(limit)
	snapshot.Remaining = atoiOrZero(h.Get("X-RateLimit-Remaining"))
	snapshot.Used = atoiOrZero(h.Get("X-RateLimit-Used"))
	if reset := atoiOrZero(h.Get("X-RateLimit-Reset")); reset > 0 {
		snapshot.ResetAt = time.Unix(int64(reset), 0).UTC()
	}
	return snapshot
}

// parseRetryAfter reads Retry-After in both its seconds and HTTP-date forms.
func parseRetryAfter(h http.Header) time.Duration {
	value := strings.TrimSpace(h.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if d := time.Until(at); d > 0 {
			return d
		}
	}
	return 0
}

// parseRedirectTarget extracts owner/name from a GitHub API Location header.
func parseRedirectTarget(location string) (owner, name string, ok bool) {
	if location == "" {
		return "", "", false
	}
	u, err := url.Parse(location)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	// Expect .../repos/{owner}/{name}
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] == "repos" {
			candidate, err := reponame.Parse(parts[i+1] + "/" + parts[i+2])
			if err != nil {
				return "", "", false
			}
			return candidate.Owner, candidate.Repository, true
		}
	}
	return "", "", false
}

func mentionsSecondaryLimit(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "secondary rate limit") ||
		strings.Contains(lower, "abuse detection") ||
		strings.Contains(lower, "please slow down") ||
		strings.Contains(lower, "retry your request again")
}

// extractMessage pulls the human-readable message out of a GitHub error body.
func extractMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return payload.Message
}

// sanitiseMessage guards against a token or header ever reaching a log line or
// an HTTP response through an upstream error string.
func sanitiseMessage(message string) string {
	message = strings.ReplaceAll(message, "\n", " ")
	message = strings.ReplaceAll(message, "\r", " ")
	if len(message) > 300 {
		message = message[:300] + "…"
	}

	lower := strings.ToLower(message)
	for _, marker := range []string{"bearer ", "token ", "ghp_", "gho_", "ghs_", "github_pat_"} {
		if strings.Contains(lower, marker) {
			return "github returned an error (message withheld because it may contain a credential)"
		}
	}
	return message
}

func atoiOrZero(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

func clampInt(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func normaliseSort(sort string) string {
	switch sort {
	case "stars", "updated", "forks", "help-wanted-issues":
		return sort
	default:
		return "stars"
	}
}

func normaliseOrder(order string) string {
	if order == "asc" {
		return "asc"
	}
	return "desc"
}
