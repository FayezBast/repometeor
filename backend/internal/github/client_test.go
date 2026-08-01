package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/repometeor/repometeor/backend/internal/config"
	"github.com/repometeor/repometeor/backend/internal/telemetry"
)

const testToken = "ghp_testtoken_do_not_use" //nolint:gosec // fixture value

func testClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cfg := config.GitHubConfig{
		Token:            testToken,
		BaseURL:          server.URL,
		APIVersion:       "2022-11-28",
		UserAgent:        "RepoMeteor/test",
		RequestTimeout:   5 * time.Second,
		MaxRetries:       3,
		Concurrency:      1,
		MaxResponseBytes: 1 << 20,
	}

	client := NewClient(cfg, telemetry.Discard(), nil)
	// Never actually wait during retry tests.
	client.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return client, server
}

func repositoryJSON(overrides map[string]any) []byte {
	payload := map[string]any{
		"id":                123456,
		"node_id":           "R_kgDOABCD",
		"name":              "signal-kit",
		"full_name":         "orbit-labs/signal-kit",
		"owner":             map[string]any{"login": "orbit-labs", "avatar_url": "https://example.test/a.png"},
		"description":       "A fictional demo repository",
		"html_url":          "https://github.com/orbit-labs/signal-kit",
		"homepage":          "https://example.test",
		"default_branch":    "main",
		"language":          "Go",
		"topics":            []string{"developer-tools", "cli"},
		"license":           map[string]any{"spdx_id": "Apache-2.0"},
		"stargazers_count":  1240,
		"forks_count":       81,
		"open_issues_count": 12,
		"subscribers_count": 34,
		"fork":              false,
		"archived":          false,
		"disabled":          false,
		"is_template":       false,
		"private":           false,
		"created_at":        "2026-07-01T00:00:00Z",
		"updated_at":        "2026-08-01T08:00:00Z",
		"pushed_at":         "2026-08-01T08:00:00Z",
	}
	for k, v := range overrides {
		payload[k] = v
	}
	encoded, _ := json.Marshal(payload)
	return encoded
}

func TestGetRepositorySendsRequiredHeaders(t *testing.T) {
	var captured http.Header

	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.Header().Set("ETag", `W/"abc123"`)
		w.Header().Set("Last-Modified", "Fri, 01 Aug 2026 08:00:00 GMT")
		_, _ = w.Write(repositoryJSON(nil))
	}))

	if _, err := client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{}); err != nil {
		t.Fatalf("GetRepository() error = %v", err)
	}

	tests := map[string]string{
		"Accept":               "application/vnd.github+json",
		"X-Github-Api-Version": "2022-11-28",
		"User-Agent":           "RepoMeteor/test",
		"Authorization":        "Bearer " + testToken,
	}
	for header, want := range tests {
		if got := captured.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestGetRepositoryOmitsAuthorizationWithoutToken(t *testing.T) {
	var captured http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		_, _ = w.Write(repositoryJSON(nil))
	}))
	defer server.Close()

	client := NewClient(config.GitHubConfig{
		BaseURL: server.URL, APIVersion: "2022-11-28", UserAgent: "RepoMeteor/test",
		RequestTimeout: 5 * time.Second, MaxRetries: 0, Concurrency: 1, MaxResponseBytes: 1 << 20,
	}, telemetry.Discard(), nil)

	if _, err := client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{}); err != nil {
		t.Fatalf("GetRepository() error = %v", err)
	}
	if got := captured.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want it absent without a token", got)
	}
}

func TestGetRepositoryDecodesPayload(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `W/"abc123"`)
		_, _ = w.Write(repositoryJSON(nil))
	}))

	result, err := client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{})
	if err != nil {
		t.Fatalf("GetRepository() error = %v", err)
	}

	repo := result.Repository
	if repo.ID != 123456 {
		t.Errorf("ID = %d, want 123456", repo.ID)
	}
	if repo.FullName != "orbit-labs/signal-kit" {
		t.Errorf("FullName = %q", repo.FullName)
	}
	if repo.Stars != 1240 || repo.Forks != 81 || repo.OpenIssues != 12 {
		t.Errorf("counts = %d/%d/%d, want 1240/81/12", repo.Stars, repo.Forks, repo.OpenIssues)
	}
	if repo.LicenseSPDX != "Apache-2.0" {
		t.Errorf("LicenseSPDX = %q", repo.LicenseSPDX)
	}
	if len(repo.Topics) != 2 {
		t.Errorf("Topics = %v, want 2 entries", repo.Topics)
	}
	if result.Validators.ETag != `W/"abc123"` {
		t.Errorf("ETag = %q", result.Validators.ETag)
	}
	if repo.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt is not UTC: %v", repo.CreatedAt.Location())
	}
}

func TestConditionalRequestSendsIfNoneMatch(t *testing.T) {
	var captured http.Header

	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.WriteHeader(http.StatusNotModified)
	}))

	result, err := client.GetRepository(context.Background(), "orbit-labs", "signal-kit",
		Validators{ETag: `W/"abc123"`})
	if err != nil {
		t.Fatalf("GetRepository() error = %v", err)
	}

	if got := captured.Get("If-None-Match"); got != `W/"abc123"` {
		t.Errorf("If-None-Match = %q, want the stored ETag", got)
	}
	if !result.NotModified {
		t.Error("NotModified = false, want true for a 304")
	}
	if result.Validators.ETag != `W/"abc123"` {
		t.Errorf("ETag = %q, want the validator to be preserved across a 304", result.Validators.ETag)
	}
	if result.Repository.ID != 0 {
		t.Error("a 304 must not populate repository data")
	}
}

func TestConditionalRequestFallsBackToIfModifiedSince(t *testing.T) {
	var captured http.Header

	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.WriteHeader(http.StatusNotModified)
	}))

	_, err := client.GetRepository(context.Background(), "orbit-labs", "signal-kit",
		Validators{LastModified: "Fri, 01 Aug 2026 08:00:00 GMT"})
	if err != nil {
		t.Fatalf("GetRepository() error = %v", err)
	}

	if got := captured.Get("If-Modified-Since"); got != "Fri, 01 Aug 2026 08:00:00 GMT" {
		t.Errorf("If-Modified-Since = %q", got)
	}
	if got := captured.Get("If-None-Match"); got != "" {
		t.Errorf("If-None-Match = %q, want it absent when only Last-Modified is known", got)
	}
}

func TestErrorClassification(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		headers  map[string]string
		body     string
		wantKind ErrorKind
	}{
		{"not found", 404, nil, `{"message":"Not Found"}`, ErrNotFound},
		{"unauthorized", 401, nil, `{"message":"Bad credentials"}`, ErrUnauthorized},
		{"validation", 422, nil, `{"message":"Validation Failed"}`, ErrValidation},
		{"server error", 500, nil, `{"message":"oops"}`, ErrServer},
		{"bad gateway", 502, nil, ``, ErrServer},
		{"service unavailable", 503, nil, ``, ErrServer},
		{
			name:     "primary rate limit",
			status:   403,
			headers:  map[string]string{"X-RateLimit-Limit": "5000", "X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1785000000"},
			body:     `{"message":"API rate limit exceeded"}`,
			wantKind: ErrRateLimited,
		},
		{
			name:     "secondary rate limit via retry-after",
			status:   403,
			headers:  map[string]string{"Retry-After": "60"},
			body:     `{"message":"You have exceeded a secondary rate limit"}`,
			wantKind: ErrSecondaryRateLimit,
		},
		{
			name:     "secondary rate limit via message",
			status:   403,
			body:     `{"message":"You have triggered an abuse detection mechanism"}`,
			wantKind: ErrSecondaryRateLimit,
		},
		{
			name:     "plain forbidden",
			status:   403,
			body:     `{"message":"Repository access blocked"}`,
			wantKind: ErrForbidden,
		},
		{
			name:     "too many requests",
			status:   429,
			headers:  map[string]string{"Retry-After": "30"},
			body:     `{"message":"Too many requests"}`,
			wantKind: ErrSecondaryRateLimit,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			client.maxRetries = 0

			_, err := client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{})
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := KindOf(err); got != tc.wantKind {
				t.Fatalf("kind = %q, want %q (error: %v)", got, tc.wantKind, err)
			}
		})
	}
}

func TestRenamedRepositoryIsClassifiedWithCanonicalName(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://api.github.com/repositories/123456")
		w.Header().Set("Location", "https://api.github.com/repos/orbit-labs/signal-kit-v2")
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	client.maxRetries = 0

	_, err := client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{})
	if err == nil {
		t.Fatal("expected an error")
	}

	ghErr, ok := AsError(err)
	if !ok {
		t.Fatalf("error is not a *github.Error: %v", err)
	}
	if ghErr.Kind != ErrRenamed {
		t.Fatalf("kind = %q, want %q", ghErr.Kind, ErrRenamed)
	}
	if ghErr.CanonicalOwner != "orbit-labs" || ghErr.CanonicalName != "signal-kit-v2" {
		t.Fatalf("canonical = %s/%s, want orbit-labs/signal-kit-v2", ghErr.CanonicalOwner, ghErr.CanonicalName)
	}
}

func TestRedirectIsNotFollowedAutomatically(t *testing.T) {
	var requestPaths []string

	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPaths = append(requestPaths, r.URL.Path)
		w.Header().Set("Location", "/repos/orbit-labs/signal-kit-v2")
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	client.maxRetries = 0

	_, _ = client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{})

	if len(requestPaths) != 1 {
		t.Fatalf("made %d requests (%v); a redirect must be reported, not followed", len(requestPaths), requestPaths)
	}
}

func TestPermanentErrorsAreNotRetried(t *testing.T) {
	var attempts atomic.Int32

	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))

	_, err := client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{})
	if KindOf(err) != ErrNotFound {
		t.Fatalf("kind = %q, want not_found", KindOf(err))
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("made %d attempts, want 1; a 404 must never be retried", got)
	}
}

func TestTransientErrorsAreRetriedThenSucceed(t *testing.T) {
	var attempts atomic.Int32

	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(repositoryJSON(nil))
	}))
	// Allow the retry loop to sleep instantly.
	client.sleep = func(context.Context, time.Duration) error { return nil }

	result, err := client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{})
	if err != nil {
		t.Fatalf("GetRepository() error = %v", err)
	}
	if result.Repository.ID != 123456 {
		t.Errorf("ID = %d, want the repository from the third attempt", result.Repository.ID)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("made %d attempts, want 3", got)
	}
}

func TestRetriesAreBoundedByMaxRetries(t *testing.T) {
	var attempts atomic.Int32

	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	client.maxRetries = 2
	client.sleep = func(context.Context, time.Duration) error { return nil }

	if _, err := client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{}); err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("made %d attempts, want 3 (initial + 2 retries)", got)
	}
}

func TestRetryAfterIsHonoured(t *testing.T) {
	var (
		attempts atomic.Int32
		observed []time.Duration
	)

	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "42")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"secondary rate limit"}`))
			return
		}
		_, _ = w.Write(repositoryJSON(nil))
	}))
	client.sleep = func(_ context.Context, d time.Duration) error {
		observed = append(observed, d)
		return nil
	}

	if _, err := client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{}); err != nil {
		t.Fatalf("GetRepository() error = %v", err)
	}
	if len(observed) != 1 {
		t.Fatalf("slept %d times, want 1", len(observed))
	}
	if observed[0] != 42*time.Second {
		t.Fatalf("slept %v, want the 42s from Retry-After", observed[0])
	}
}

func TestRetryAfterIsCapped(t *testing.T) {
	err := &Error{Kind: ErrSecondaryRateLimit, RetryAfter: time.Hour}
	if got := backoffDelay(1, err); got != 5*time.Minute {
		t.Fatalf("backoffDelay = %v, want it capped at 5m", got)
	}
}

func TestBackoffGrowsAndStaysBounded(t *testing.T) {
	var previousMax time.Duration
	for attempt := 1; attempt <= 8; attempt++ {
		d := backoffDelay(attempt, nil)
		if d <= 0 {
			t.Fatalf("attempt %d: delay = %v, want a positive duration", attempt, d)
		}
		if d > 61*time.Second {
			t.Fatalf("attempt %d: delay = %v, want it capped near 60s", attempt, d)
		}
		if attempt <= 5 && d < previousMax/4 {
			t.Fatalf("attempt %d: delay %v collapsed relative to %v", attempt, d, previousMax)
		}
		previousMax = d
	}
}

func TestRateLimitHeadersAreParsed(t *testing.T) {
	reset := time.Now().Add(time.Hour).Unix()

	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4321")
		w.Header().Set("X-RateLimit-Used", "679")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
		w.Header().Set("X-RateLimit-Resource", "core")
		_, _ = w.Write(repositoryJSON(nil))
	}))

	result, err := client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{})
	if err != nil {
		t.Fatalf("GetRepository() error = %v", err)
	}

	rl := result.RateLimit
	if !rl.Known {
		t.Fatal("Known = false, want true")
	}
	if rl.Limit != 5000 || rl.Remaining != 4321 || rl.Used != 679 {
		t.Errorf("limit/remaining/used = %d/%d/%d, want 5000/4321/679", rl.Limit, rl.Remaining, rl.Used)
	}
	if rl.Resource != "core" {
		t.Errorf("Resource = %q, want core", rl.Resource)
	}
	if got := rl.PercentRemaining(); got != 86 {
		t.Errorf("PercentRemaining() = %d, want 86", got)
	}
}

func TestMissingRateLimitHeadersAreUnknown(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(repositoryJSON(nil))
	}))

	result, _ := client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{})
	if result.RateLimit.Known {
		t.Error("Known = true without rate-limit headers")
	}
	if got := result.RateLimit.PercentRemaining(); got != 100 {
		t.Errorf("PercentRemaining() = %d, want 100 when unknown", got)
	}
}

func TestGetLatestReleaseFound(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.4.0","published_at":"2026-07-29T10:00:00Z"}`))
	}))

	result, err := client.GetLatestRelease(context.Background(), "orbit-labs", "signal-kit", Validators{})
	if err != nil {
		t.Fatalf("GetLatestRelease() error = %v", err)
	}
	if !result.Found {
		t.Fatal("Found = false, want true")
	}
	if result.Release.TagName != "v1.4.0" {
		t.Errorf("TagName = %q", result.Release.TagName)
	}
	if result.Release.PublishedAt == nil {
		t.Fatal("PublishedAt is nil")
	}
}

func TestGetLatestReleaseTreatsMissingAsNotAnError(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	client.maxRetries = 0

	result, err := client.GetLatestRelease(context.Background(), "orbit-labs", "signal-kit", Validators{})
	if err != nil {
		t.Fatalf("a repository without releases must not be an error, got %v", err)
	}
	if result.Found {
		t.Error("Found = true, want false")
	}
}

func TestSearchRepositories(t *testing.T) {
	var capturedQuery string

	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query().Get("q")
		body := fmt.Sprintf(`{"total_count":1,"incomplete_results":false,"items":[%s]}`, repositoryJSON(nil))
		_, _ = w.Write([]byte(body))
	}))

	after := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	result, err := client.SearchRepositories(context.Background(), SearchInput{
		Language:        "Go",
		CreatedAfter:    &after,
		MinStars:        5,
		ExcludeForks:    true,
		ExcludeArchived: true,
		PerPage:         100,
		Page:            1,
	})
	if err != nil {
		t.Fatalf("SearchRepositories() error = %v", err)
	}
	if len(result.Repositories) != 1 {
		t.Fatalf("got %d repositories, want 1", len(result.Repositories))
	}
	for _, fragment := range []string{`language:"Go"`, "created:>=2026-07-01", "stars:>=5", "fork:false", "archived:false", "is:public"} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Errorf("query %q is missing %q", capturedQuery, fragment)
		}
	}
}

func TestSearchSkipsMalformedItemsWithoutLosingThePage(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := fmt.Sprintf(`{"total_count":2,"items":[{"id":0,"name":"broken"},%s]}`, repositoryJSON(nil))
		_, _ = w.Write([]byte(body))
	}))

	result, err := client.SearchRepositories(context.Background(), SearchInput{Language: "Go", PerPage: 100, Page: 1})
	if err != nil {
		t.Fatalf("SearchRepositories() error = %v", err)
	}
	if len(result.Repositories) != 1 {
		t.Fatalf("got %d repositories, want the 1 valid entry", len(result.Repositories))
	}
}

func TestMalformedJSONIsClassified(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{not json`))
	}))

	_, err := client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{})
	if KindOf(err) != ErrMalformed {
		t.Fatalf("kind = %q, want malformed_response (error: %v)", KindOf(err), err)
	}
}

func TestResponseBodyIsCapped(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Far larger than the configured cap.
		_, _ = w.Write([]byte(`{"id":1,"name":"x","owner":{"login":"y"},"description":"` + strings.Repeat("a", 4096) + `"}`))
	}))
	client.maxBody = 128

	_, err := client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{})
	// Truncation makes the JSON invalid, which is exactly the safe outcome:
	// a malformed classification rather than unbounded memory use.
	if KindOf(err) != ErrMalformed {
		t.Fatalf("kind = %q, want malformed_response from a truncated body", KindOf(err))
	}
}

func TestContextCancellationIsClassifiedAsTimeout(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(repositoryJSON(nil))
	}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.GetRepository(ctx, "orbit-labs", "signal-kit", Validators{})
	if KindOf(err) != ErrTimeout {
		t.Fatalf("kind = %q, want timeout", KindOf(err))
	}
}

func TestInvalidRepositoryNameNeverReachesTheNetwork(t *testing.T) {
	var reached atomic.Bool

	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Store(true)
		_, _ = w.Write(repositoryJSON(nil))
	}))

	for _, name := range []string{"../../etc", "owner/extra", "bad name", ""} {
		_, err := client.GetRepository(context.Background(), name, "repo", Validators{})
		if KindOf(err) != ErrValidation {
			t.Errorf("owner %q: kind = %q, want validation_failed", name, KindOf(err))
		}
	}
	if reached.Load() {
		t.Fatal("an invalid repository name reached the network; validation must happen first")
	}
}

func TestErrorMessagesNeverLeakCredentials(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Bad credentials for token ghp_leakedsecretvalue"}`))
	}))
	client.maxRetries = 0

	_, err := client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "ghp_leakedsecretvalue") {
		t.Fatalf("error leaked a token: %v", err)
	}
}

func TestErrorMessagesAreBoundedAndSingleLine(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		message := strings.Repeat("x", 5000) + "\nInjected: header"
		encoded, _ := json.Marshal(map[string]string{"message": message})
		_, _ = w.Write(encoded)
	}))
	client.maxRetries = 0

	_, err := client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "\n") {
		t.Error("error message contains a newline, which would allow log injection")
	}
	if len(err.Error()) > 500 {
		t.Errorf("error message is %d characters, want it bounded", len(err.Error()))
	}
}

func TestGetRateLimit(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"resources":{
            "core":{"limit":5000,"remaining":4000,"used":1000,"reset":1785000000},
            "search":{"limit":30,"remaining":28,"used":2,"reset":1785000060},
            "graphql":{"limit":5000,"remaining":5000,"used":0,"reset":1785000000}}}`))
	}))

	state, err := client.GetRateLimit(context.Background())
	if err != nil {
		t.Fatalf("GetRateLimit() error = %v", err)
	}
	if state.Core.Remaining != 4000 || state.Search.Remaining != 28 {
		t.Errorf("core/search remaining = %d/%d, want 4000/28", state.Core.Remaining, state.Search.Remaining)
	}
	if state.Core.Resource != "core" || state.Search.Resource != "search" {
		t.Errorf("resources = %q/%q", state.Core.Resource, state.Search.Resource)
	}
}

func TestParseRetryAfter(t *testing.T) {
	h := http.Header{}

	h.Set("Retry-After", "60")
	if got := parseRetryAfter(h); got != time.Minute {
		t.Errorf("seconds form = %v, want 1m", got)
	}

	h.Set("Retry-After", "-5")
	if got := parseRetryAfter(h); got != 0 {
		t.Errorf("negative form = %v, want 0", got)
	}

	h.Set("Retry-After", http.TimeFormat)
	if got := parseRetryAfter(h); got < 0 {
		t.Errorf("date form = %v, want a non-negative duration", got)
	}

	h.Del("Retry-After")
	if got := parseRetryAfter(h); got != 0 {
		t.Errorf("absent = %v, want 0", got)
	}
}

func TestParseRedirectTarget(t *testing.T) {
	tests := map[string]struct {
		owner, name string
		ok          bool
	}{
		"https://api.github.com/repos/orbit-labs/signal-kit": {"orbit-labs", "signal-kit", true},
		"/repos/orbit-labs/signal-kit":                       {"orbit-labs", "signal-kit", true},
		"https://api.github.com/repositories/123":            {"", "", false},
		"":                     {"", "", false},
		"not a url at all %%%": {"", "", false},
	}
	for location, want := range tests {
		owner, name, ok := parseRedirectTarget(location)
		if ok != want.ok || owner != want.owner || name != want.name {
			t.Errorf("parseRedirectTarget(%q) = %q/%q/%v, want %q/%q/%v",
				location, owner, name, ok, want.owner, want.name, want.ok)
		}
	}
}

func TestErrorKindRetryability(t *testing.T) {
	retryable := []ErrorKind{ErrServer, ErrTimeout, ErrNetwork, ErrSecondaryRateLimit}
	permanent := []ErrorKind{ErrNotFound, ErrUnauthorized, ErrForbidden, ErrValidation, ErrRenamed, ErrMalformed, ErrRateLimited}

	for _, kind := range retryable {
		if !kind.Retryable() {
			t.Errorf("%s should be retryable", kind)
		}
	}
	for _, kind := range permanent {
		if kind.Retryable() {
			t.Errorf("%s should not be retryable", kind)
		}
	}
}

func TestTopicsAreNormalised(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(repositoryJSON(map[string]any{
			"topics": []string{"Developer-Tools", "developer-tools", "  CLI  ", "<script>", "", "ok"},
		}))
	}))

	result, err := client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{})
	if err != nil {
		t.Fatalf("GetRepository() error = %v", err)
	}

	want := []string{"developer-tools", "cli", "ok"}
	got := result.Repository.Topics
	if len(got) != len(want) {
		t.Fatalf("topics = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("topics = %v, want %v", got, want)
		}
	}
}

func TestNoAssertionLicenceIsDropped(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(repositoryJSON(map[string]any{
			"license": map[string]any{"spdx_id": "NOASSERTION"},
		}))
	}))

	result, _ := client.GetRepository(context.Background(), "orbit-labs", "signal-kit", Validators{})
	if result.Repository.LicenseSPDX != "" {
		t.Errorf("LicenseSPDX = %q, want it empty for NOASSERTION", result.Repository.LicenseSPDX)
	}
}
