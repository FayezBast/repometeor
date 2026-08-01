//go:build integration

package api

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/repometeor/repometeor/backend/internal/clock"
	"github.com/repometeor/repometeor/backend/internal/config"
	"github.com/repometeor/repometeor/backend/internal/database"
	"github.com/repometeor/repometeor/backend/internal/demo"
	"github.com/repometeor/repometeor/backend/internal/jobs"
	"github.com/repometeor/repometeor/backend/internal/reponame"
	"github.com/repometeor/repometeor/backend/internal/repository"
	"github.com/repometeor/repometeor/backend/internal/service"
	"github.com/repometeor/repometeor/backend/internal/telemetry"
)

// The integration suite drives the API against a real PostgreSQL instance
// seeded with the demo dataset and scored by the real scoring engine. It is the
// test that proves the whole vertical slice works: migrations, seeding,
// scoring, querying and rendering.

var integrationRef = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

type harness struct {
	server *Server
	db     *database.DB
	cfg    *config.Config
}

func TestMain(m *testing.M) {
	url := os.Getenv("REPOMETEOR_TEST_DATABASE_URL")
	if url != "" {
		cfg := config.DatabaseConfig{URL: url, ConnectTimeout: 10 * time.Second}
		if _, err := database.MigrateUp(context.Background(), cfg, telemetry.Discard()); err != nil {
			fmt.Fprintf(os.Stderr, "migrate test database: %v\n", err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// newHarness seeds a fresh database and returns a fully wired API server.
func newHarness(t *testing.T) *harness {
	t.Helper()

	url := os.Getenv("REPOMETEOR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("REPOMETEOR_TEST_DATABASE_URL is not set; skipping API integration tests")
	}

	cfg, err := config.LoadFromMap(map[string]string{
		"DEMO_MODE":                           "true",
		"DATABASE_URL":                        url,
		"API_PUBLIC_URL":                      "https://api.example.test",
		"WEB_PUBLIC_URL":                      "https://example.test",
		"CORS_ALLOWED_ORIGINS":                "https://example.test",
		"PUBLIC_TRACKING_RATE_LIMIT_PER_HOUR": "3",
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	ctx := context.Background()
	log := telemetry.Discard()

	db, err := database.Connect(ctx, cfg.Database, log)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(db.Close)

	if _, err := db.Pool().Exec(ctx, `
        TRUNCATE repositories, repository_aliases, repository_snapshots,
                 momentum_scores, scoring_runs, crawl_jobs, discovery_runs,
                 github_request_cache, rate_limit_counters
        RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	repos := repository.NewRepositories(db)
	snaps := repository.NewSnapshots(db)
	scores := repository.NewScores(db)
	queue := repository.NewJobs(db)
	limits := repository.NewRateLimits(db, []byte("integration-test-key"))

	fake := clock.NewFake(integrationRef)

	// Seed and score exactly as `make seed && make score` would.
	seeder := demo.NewSeeder(repos, snaps, fake, log)
	if _, err := seeder.Seed(ctx, integrationRef, cfg.Tracking.SnapshotBucketInterval); err != nil {
		t.Fatalf("seed: %v", err)
	}

	scorer := jobs.NewScorer(repos, snaps, scores, cfg.Tracking, fake, log, telemetry.NewMetrics("test"))
	if err := scorer.Handle(ctx, repository.Job{Kind: repository.JobScoreRepositories}); err != nil {
		t.Fatalf("score: %v", err)
	}

	svc := service.New(repos, snaps, scores, queue, limits, cfg, fake, log)

	return &harness{
		server: New(Options{
			Config: cfg, Service: svc, DB: db,
			Log: log, Metrics: telemetry.NewMetrics("test"), Clock: fake,
		}),
		db:  db,
		cfg: cfg,
	}
}

func (h *harness) get(t *testing.T, target string, headers ...map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = "203.0.113.5:1234"
	for _, set := range headers {
		for k, v := range set {
			req.Header.Set(k, v)
		}
	}

	rec := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(rec, req)
	return rec
}

func (h *harness) post(t *testing.T, target, body, client string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = client + ":1234"

	rec := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(rec, req)
	return rec
}

func decodeInto[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()

	if rec.Code != http.StatusOK && rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 200/202: %s", rec.Code, rec.Body.String())
	}

	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body.String())
	}
	return out
}

// --- readiness ---------------------------------------------------------------

func TestReadyzWithDatabase(t *testing.T) {
	h := newHarness(t)

	rec := h.get(t, "/readyz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	body := decodeInto[ReadyResponse](t, rec)
	if body.Checks["database"] != "ok" {
		t.Errorf("database check = %q", body.Checks["database"])
	}
	if !body.DemoMode {
		t.Error("demoMode should be reported as true")
	}
}

// --- trending ----------------------------------------------------------------

func TestTrendingReturnsRankedResults(t *testing.T) {
	h := newHarness(t)

	body := decodeInto[TrendingResponse](t, h.get(t, "/v1/trending?window=24h&limit=10"))

	if len(body.Data) == 0 {
		t.Fatal("no trending results")
	}
	if body.Meta.Window != "24h" || body.Meta.Collection != "daily" {
		t.Errorf("meta = %+v", body.Meta)
	}
	if !body.Meta.DemoMode {
		t.Error("demoMode must be true so the UI can label the data")
	}
	if body.Meta.FormulaVersion == "" {
		t.Error("formulaVersion is empty")
	}

	for i, item := range body.Data {
		if item.Momentum.Rank == nil {
			t.Errorf("item %d has no rank", i)
			continue
		}
		if *item.Momentum.Rank != i+1 {
			t.Errorf("item %d rank = %d, want %d", i, *item.Momentum.Rank, i+1)
		}
		if item.Repository.FullName == "" {
			t.Errorf("item %d has no repository name", i)
		}
		if len(item.Momentum.Reasons) == 0 {
			t.Errorf("%s has no explanation; every ranked repository must say why",
				item.Repository.FullName)
		}
		if item.Repository.Stars == 0 {
			t.Errorf("%s has no star count", item.Repository.FullName)
		}
		if len(item.Sparkline) == 0 {
			t.Errorf("%s has no sparkline", item.Repository.FullName)
		}
		if i > 0 && body.Data[i-1].Momentum.Score < item.Momentum.Score {
			t.Errorf("results are not in descending score order at index %d", i)
		}
	}
}

func TestTrendingTimestampsAreUTC(t *testing.T) {
	h := newHarness(t)

	// The raw JSON is inspected rather than the decoded struct, because
	// time.Time comparison would hide a non-UTC offset in the wire format.
	rec := h.get(t, "/v1/trending?limit=5")
	raw := rec.Body.String()

	for _, field := range []string{"createdAt", "pushedAt", "generatedAt", "scoredAt"} {
		index := strings.Index(raw, `"`+field+`":"`)
		if index < 0 {
			continue
		}
		value := raw[index+len(field)+4:]
		value = value[:strings.Index(value, `"`)]
		if !strings.HasSuffix(value, "Z") {
			t.Errorf("%s = %q, want a UTC timestamp ending in Z", field, value)
		}
	}
}

func TestTrendingFiltersByLanguage(t *testing.T) {
	h := newHarness(t)

	body := decodeInto[TrendingResponse](t, h.get(t, "/v1/trending?window=24h&language=Go&limit=50"))
	if len(body.Data) == 0 {
		t.Fatal("no Go repositories returned")
	}
	for _, item := range body.Data {
		if item.Repository.Language == nil || *item.Repository.Language != "Go" {
			t.Errorf("%s has language %v, want Go", item.Repository.FullName, item.Repository.Language)
		}
	}
}

func TestTrendingFiltersByTopicAndStars(t *testing.T) {
	h := newHarness(t)

	byTopic := decodeInto[TrendingResponse](t, h.get(t, "/v1/trending?topic=developer-tools&limit=50"))
	if len(byTopic.Data) == 0 {
		t.Fatal("no developer-tools repositories returned")
	}
	for _, item := range byTopic.Data {
		var found bool
		for _, topic := range item.Repository.Topics {
			if topic == "developer-tools" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s does not carry the developer-tools topic: %v",
				item.Repository.FullName, item.Repository.Topics)
		}
	}

	byStars := decodeInto[TrendingResponse](t, h.get(t, "/v1/trending?minStars=1000&maxStars=5000&limit=50"))
	for _, item := range byStars.Data {
		if item.Repository.Stars < 1000 || item.Repository.Stars > 5000 {
			t.Errorf("%s has %d stars, outside the requested 1000..5000",
				item.Repository.FullName, item.Repository.Stars)
		}
	}
}

func TestTrendingFiltersByConfidence(t *testing.T) {
	h := newHarness(t)

	body := decodeInto[TrendingResponse](t, h.get(t, "/v1/trending?confidence=high&limit=50"))
	for _, item := range body.Data {
		if item.Momentum.ConfidenceLabel != "high" {
			t.Errorf("%s has confidence %q, want high",
				item.Repository.FullName, item.Momentum.ConfidenceLabel)
		}
	}
}

func TestTrendingCombinedFilters(t *testing.T) {
	h := newHarness(t)

	body := decodeInto[TrendingResponse](t,
		h.get(t, "/v1/trending?window=24h&language=Go&topic=developer-tools&minStars=100&limit=50"))

	for _, item := range body.Data {
		if item.Repository.Language == nil || *item.Repository.Language != "Go" {
			t.Errorf("%s failed the language filter", item.Repository.FullName)
		}
		if item.Repository.Stars < 100 {
			t.Errorf("%s failed the minStars filter", item.Repository.FullName)
		}
	}
}

func TestTrendingCursorPagination(t *testing.T) {
	h := newHarness(t)

	first := decodeInto[TrendingResponse](t, h.get(t, "/v1/trending?window=24h&limit=5"))
	if len(first.Data) != 5 {
		t.Fatalf("first page has %d items, want 5", len(first.Data))
	}
	if first.Links.NextCursor == nil {
		t.Fatal("first page has no next cursor")
	}

	second := decodeInto[TrendingResponse](t,
		h.get(t, "/v1/trending?window=24h&limit=5&cursor="+*first.Links.NextCursor))
	if len(second.Data) == 0 {
		t.Fatal("second page is empty")
	}

	// The pages must not overlap, and ordering must continue downwards.
	seen := map[string]bool{}
	for _, item := range first.Data {
		seen[item.Repository.FullName] = true
	}
	for _, item := range second.Data {
		if seen[item.Repository.FullName] {
			t.Errorf("%s appears on both pages", item.Repository.FullName)
		}
	}

	lastOfFirst := first.Data[len(first.Data)-1].Momentum.Score
	firstOfSecond := second.Data[0].Momentum.Score
	if firstOfSecond > lastOfFirst {
		t.Errorf("page two starts at score %v, above page one's last %v", firstOfSecond, lastOfFirst)
	}
}

func TestTrendingCursorPaginationWalksTheWholeList(t *testing.T) {
	h := newHarness(t)

	seen := map[string]int{}
	cursor := ""

	for page := range 20 {
		target := "/v1/trending?window=24h&limit=7"
		if cursor != "" {
			target += "&cursor=" + cursor
		}

		body := decodeInto[TrendingResponse](t, h.get(t, target))
		for _, item := range body.Data {
			seen[item.Repository.FullName]++
			if seen[item.Repository.FullName] > 1 {
				t.Fatalf("page %d repeated %s", page, item.Repository.FullName)
			}
		}
		if body.Links.NextCursor == nil {
			break
		}
		cursor = *body.Links.NextCursor
	}

	// The demo dataset ranks around 50 repositories in the 24h window.
	if len(seen) < 40 {
		t.Fatalf("pagination surfaced only %d repositories, want the full ranked set", len(seen))
	}
}

func TestTrendingRejectsTamperedCursor(t *testing.T) {
	h := newHarness(t)

	for _, cursor := range []string{"not-base64!!", "YWJj", "eyJzIjo5OTk5OTl9"} {
		rec := h.get(t, "/v1/trending?cursor="+cursor)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("cursor %q returned %d, want 400", cursor, rec.Code)
		}
	}
}

func TestTrendingCollections(t *testing.T) {
	h := newHarness(t)

	for _, collection := range []string{"rising", "daily", "weekly", "hidden-gems", "new-entrants", "established"} {
		t.Run(collection, func(t *testing.T) {
			body := decodeInto[TrendingResponse](t, h.get(t, "/v1/trending?collection="+collection+"&limit=50"))
			if len(body.Data) == 0 {
				t.Fatalf("collection %q is empty in the demo dataset", collection)
			}
			for _, item := range body.Data {
				var found bool
				for _, c := range item.Momentum.Collections {
					if c == collection {
						found = true
					}
				}
				if !found {
					t.Errorf("%s was returned for %q but its collections are %v",
						item.Repository.FullName, collection, item.Momentum.Collections)
				}
			}
		})
	}
}

func TestHiddenGemsRespectTheirStarBounds(t *testing.T) {
	h := newHarness(t)

	body := decodeInto[TrendingResponse](t, h.get(t, "/v1/trending?collection=hidden-gems&limit=50"))
	for _, item := range body.Data {
		if item.Repository.Stars < 10 || item.Repository.Stars > 1000 {
			t.Errorf("%s has %d stars, outside the hidden-gem range",
				item.Repository.FullName, item.Repository.Stars)
		}
	}
}

func TestNewEntrantsAreLowConfidence(t *testing.T) {
	h := newHarness(t)

	body := decodeInto[TrendingResponse](t, h.get(t, "/v1/trending?collection=new-entrants&limit=50"))
	for _, item := range body.Data {
		if item.Momentum.ConfidenceLabel != "low" {
			t.Errorf("%s is a new entrant with confidence %q, want low",
				item.Repository.FullName, item.Momentum.ConfidenceLabel)
		}
		if len(item.Momentum.Reasons) == 0 || !strings.Contains(item.Momentum.Reasons[0], "incomplete") {
			t.Errorf("%s should lead with its incomplete-history caveat, got %v",
				item.Repository.FullName, item.Momentum.Reasons)
		}
	}
}

func TestExcludedRepositoriesNeverAppear(t *testing.T) {
	h := newHarness(t)

	// Every window, every collection: an archived, forked or disabled
	// repository must never surface.
	forbidden := map[string]bool{
		"museum-piece/archived-toolkit": true,
		"copycat-dev/vessel":            true,
		"shuttered/disabled-service":    true,
		"sparse-docs/nodesc":            true,
		"tinystart/two-stars":           true,
	}

	for _, window := range []string{"6h", "24h", "7d"} {
		body := decodeInto[TrendingResponse](t, h.get(t, "/v1/trending?window="+window+"&limit=100"))
		for _, item := range body.Data {
			if forbidden[item.Repository.FullName] {
				t.Errorf("window %s returned the excluded repository %s", window, item.Repository.FullName)
			}
			if item.Repository.IsArchived || item.Repository.IsFork {
				t.Errorf("window %s returned archived/fork %s", window, item.Repository.FullName)
			}
		}
	}
}

func TestTrendingConditionalRequest(t *testing.T) {
	h := newHarness(t)

	first := h.get(t, "/v1/trending?window=24h&limit=5")
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	if cc := first.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=60") {
		t.Errorf("Cache-Control = %q", cc)
	}

	second := h.get(t, "/v1/trending?window=24h&limit=5", map[string]string{"If-None-Match": etag})
	if second.Code != http.StatusNotModified {
		t.Fatalf("conditional request returned %d, want 304", second.Code)
	}
}

// --- repository detail --------------------------------------------------------

func TestRepositoryDetail(t *testing.T) {
	h := newHarness(t)

	body := decodeInto[RepositoryDetailResponse](t, h.get(t, "/v1/repositories/orbit-labs/signal-kit"))

	if body.Repository.FullName != "orbit-labs/signal-kit" {
		t.Errorf("fullName = %q", body.Repository.FullName)
	}
	for _, window := range []string{"6h", "24h", "7d"} {
		if _, ok := body.Momentum[window]; !ok {
			t.Errorf("no %s momentum", window)
		}
	}
	if body.Tracking.TrackingSince == nil {
		t.Error("trackingSince is nil; the UI must be able to state when history begins")
	}
	if body.Tracking.State != "active" {
		t.Errorf("state = %q", body.Tracking.State)
	}
	if !strings.Contains(body.Links.BadgeMarkdown, "https://api.example.test") {
		t.Errorf("badge snippet does not use the configured public URL: %q", body.Links.BadgeMarkdown)
	}
	if !strings.Contains(body.Links.Web, "https://example.test/r/") {
		t.Errorf("web link = %q", body.Links.Web)
	}
}

func TestRepositoryDetailIsCaseInsensitive(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{
		"/v1/repositories/orbit-labs/signal-kit",
		"/v1/repositories/ORBIT-LABS/SIGNAL-KIT",
		"/v1/repositories/Orbit-Labs/Signal-Kit",
	} {
		body := decodeInto[RepositoryDetailResponse](t, h.get(t, path))
		if body.Repository.FullName != "orbit-labs/signal-kit" {
			t.Errorf("GET %s returned %q", path, body.Repository.FullName)
		}
	}
}

func TestRepositoryDetailNotFound(t *testing.T) {
	h := newHarness(t)

	rec := h.get(t, "/v1/repositories/nobody/nothing")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}

	var problem Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if problem.Type != ProblemNotFound {
		t.Errorf("type = %q", problem.Type)
	}
	if !strings.Contains(problem.Detail, "/submit") {
		t.Errorf("detail should point at the submit page: %q", problem.Detail)
	}
}

func TestRenamedRepositoryResolvesThroughItsAlias(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	repos := repository.NewRepositories(h.db)
	original, err := repos.GetByName(ctx, reponame.MustParse("orbit-labs/signal-kit"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Rename on GitHub: same numeric ID, new owner/name.
	if _, err := repos.Upsert(ctx, repository.UpsertRepositoryInput{
		GitHubID:    original.GitHubID,
		OwnerLogin:  "orbit-labs",
		Name:        "signal-kit-v2",
		FullName:    "orbit-labs/signal-kit-v2",
		Description: original.Description,
		HTMLURL:     "https://github.com/orbit-labs/signal-kit-v2",
		CreatedAt:   original.CreatedAt,
	}); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// The old URL must still resolve, and must say what the canonical name is.
	rec := h.get(t, "/v1/repositories/orbit-labs/signal-kit")
	body := decodeInto[RepositoryDetailResponse](t, rec)

	if body.Repository.FullName != "orbit-labs/signal-kit-v2" {
		t.Errorf("fullName = %q, want the canonical name", body.Repository.FullName)
	}
	if body.Meta.CanonicalFullName == nil || *body.Meta.CanonicalFullName != "orbit-labs/signal-kit-v2" {
		t.Errorf("canonicalFullName = %v, want it set so the client can redirect", body.Meta.CanonicalFullName)
	}
}

// --- history -----------------------------------------------------------------

func TestHistoryReturnsStoredSnapshots(t *testing.T) {
	h := newHarness(t)

	body := decodeInto[HistoryResponse](t,
		h.get(t, "/v1/repositories/orbit-labs/signal-kit/history?range=7d&resolution=1h"))

	if len(body.Data) < 20 {
		t.Fatalf("got %d history points, want a dense series", len(body.Data))
	}
	if body.Meta.TrackingSince == nil {
		t.Error("trackingSince is nil")
	}
	if body.Meta.Resolution != "1h" {
		t.Errorf("resolution = %q", body.Meta.Resolution)
	}

	for i := 1; i < len(body.Data); i++ {
		if !body.Data[i].At.After(body.Data[i-1].At) {
			t.Fatalf("history point %d is not after its predecessor", i)
		}
		wantDelta := body.Data[i].Stars - body.Data[i-1].Stars
		if body.Data[i].StarDelta != wantDelta {
			t.Errorf("point %d delta = %d, want %d", i, body.Data[i].StarDelta, wantDelta)
		}
	}
}

func TestHistoryDownsamplesLongerRanges(t *testing.T) {
	h := newHarness(t)

	hourly := decodeInto[HistoryResponse](t,
		h.get(t, "/v1/repositories/orbit-labs/signal-kit/history?range=7d&resolution=1h"))
	daily := decodeInto[HistoryResponse](t,
		h.get(t, "/v1/repositories/orbit-labs/signal-kit/history?range=7d&resolution=1d"))

	if len(daily.Data) >= len(hourly.Data) {
		t.Fatalf("daily resolution returned %d points, want fewer than hourly's %d",
			len(daily.Data), len(hourly.Data))
	}
	if len(daily.Data) > 10 {
		t.Errorf("a 7-day daily series returned %d points, want roughly one per day", len(daily.Data))
	}
}

func TestHistoryNeverImpliesDataOlderThanTracking(t *testing.T) {
	h := newHarness(t)

	// A 90-day request against 8 days of stored history must return only what
	// exists, and must state where history begins.
	body := decodeInto[HistoryResponse](t,
		h.get(t, "/v1/repositories/orbit-labs/signal-kit/history?range=90d"))

	if body.Meta.TrackingSince == nil {
		t.Fatal("trackingSince is nil; the UI cannot otherwise say where history begins")
	}

	// A daily series labels each point with the start of its bucket, so the
	// first label may precede tracking start by up to one bucket. What must
	// never happen is a point from a bucket that begins before the day tracking
	// started — that would be data RepoMeteor never observed.
	bucket := 24 * time.Hour
	earliestPermitted := body.Meta.TrackingSince.Truncate(bucket)

	for _, point := range body.Data {
		if point.At.Before(earliestPermitted) {
			t.Errorf("history point at %v predates the first tracked bucket %v",
				point.At, earliestPermitted)
		}
	}

	// The request asked for 90 days but only ~8 exist, so the response must be
	// bounded by what was actually collected rather than padded out.
	if len(body.Data) > 12 {
		t.Errorf("a 90-day request over 8 days of history returned %d points; "+
			"the response must not invent coverage", len(body.Data))
	}
}

// --- compare -----------------------------------------------------------------

func TestCompare(t *testing.T) {
	h := newHarness(t)

	body := decodeInto[CompareResponse](t,
		h.get(t, "/v1/compare?repositories=orbit-labs/signal-kit,driftwood/schema-drift&window=24h&range=7d"))

	if len(body.Data) != 2 {
		t.Fatalf("got %d entries, want 2", len(body.Data))
	}
	for _, entry := range body.Data {
		if !entry.Available {
			t.Errorf("%s is unavailable: %v", entry.Repository.FullName, entry.Note)
		}
		if len(entry.Points) < 2 {
			t.Errorf("%s has %d points", entry.Repository.FullName, len(entry.Points))
		}
		if entry.Summary.AbsoluteGrowth <= 0 {
			t.Errorf("%s reports no growth", entry.Repository.FullName)
		}
		if entry.Momentum == nil {
			t.Errorf("%s has no momentum for the window", entry.Repository.FullName)
		}
	}

	// The small repository must show far higher relative growth.
	if body.Data[1].Summary.RelativeGrowth <= body.Data[0].Summary.RelativeGrowth {
		t.Errorf("relative growth %v (small) should exceed %v (large)",
			body.Data[1].Summary.RelativeGrowth, body.Data[0].Summary.RelativeGrowth)
	}
}

func TestCompareExplainsUntrackedRepositories(t *testing.T) {
	h := newHarness(t)

	body := decodeInto[CompareResponse](t,
		h.get(t, "/v1/compare?repositories=orbit-labs/signal-kit,nobody/nothing"))

	if len(body.Data) != 2 {
		t.Fatalf("got %d entries, want 2", len(body.Data))
	}

	missing := body.Data[1]
	if missing.Available {
		t.Error("an untracked repository must not be reported as available")
	}
	if missing.Note == nil || *missing.Note == "" {
		t.Error("an unavailable entry must explain why rather than silently vanishing")
	}
	if missing.Repository.FullName != "nobody/nothing" {
		t.Errorf("fullName = %q, want the requested name to be echoed", missing.Repository.FullName)
	}
}

// --- search and facets --------------------------------------------------------

func TestSearch(t *testing.T) {
	h := newHarness(t)

	body := decodeInto[SearchResponse](t, h.get(t, "/v1/search?q=signal"))
	if len(body.Data) == 0 {
		t.Fatal("no search results")
	}
	for _, repo := range body.Data {
		if !strings.Contains(strings.ToLower(repo.FullName), "signal") {
			t.Errorf("%s does not match the query", repo.FullName)
		}
	}
}

func TestSearchAcceptsAFullURL(t *testing.T) {
	h := newHarness(t)

	body := decodeInto[SearchResponse](t,
		h.get(t, "/v1/search?q=https%3A%2F%2Fgithub.com%2Forbit-labs%2Fsignal-kit"))
	if len(body.Data) != 1 || body.Data[0].FullName != "orbit-labs/signal-kit" {
		t.Fatalf("pasting a GitHub URL should resolve directly, got %+v", body.Data)
	}
}

func TestFacets(t *testing.T) {
	h := newHarness(t)

	languages := decodeInto[FacetsResponse](t, h.get(t, "/v1/languages"))
	if len(languages.Data) < 8 {
		t.Errorf("got %d languages, want at least 8", len(languages.Data))
	}
	for i := 1; i < len(languages.Data); i++ {
		if languages.Data[i-1].Count < languages.Data[i].Count {
			t.Error("language facets are not ordered by count")
			break
		}
	}

	topics := decodeInto[FacetsResponse](t, h.get(t, "/v1/topics?limit=20"))
	if len(topics.Data) == 0 || len(topics.Data) > 20 {
		t.Errorf("got %d topics, want 1..20", len(topics.Data))
	}
}

// --- badges and feeds ---------------------------------------------------------

func TestBadgeForATrackedRepository(t *testing.T) {
	h := newHarness(t)

	rec := h.get(t, "/v1/badges/orbit-labs/signal-kit.svg?window=24h&metric=momentum")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
		t.Errorf("Content-Type = %q", ct)
	}

	var parsed any
	if err := xml.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("badge is not valid XML: %v", err)
	}
	if !strings.Contains(rec.Body.String(), "momentum 24h") {
		t.Error("badge does not show the window")
	}
}

func TestBadgeMetrics(t *testing.T) {
	h := newHarness(t)

	for _, metric := range []string{"momentum", "stars", "delta"} {
		rec := h.get(t, "/v1/badges/orbit-labs/signal-kit.svg?metric="+metric)
		if rec.Code != http.StatusOK {
			t.Errorf("metric %s: status = %d", metric, rec.Code)
			continue
		}
		var parsed any
		if err := xml.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
			t.Errorf("metric %s: invalid SVG: %v", metric, err)
		}
	}
}

func TestBadgeConditionalRequest(t *testing.T) {
	h := newHarness(t)

	first := h.get(t, "/v1/badges/orbit-labs/signal-kit.svg")
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}

	second := h.get(t, "/v1/badges/orbit-labs/signal-kit.svg", map[string]string{"If-None-Match": etag})
	if second.Code != http.StatusNotModified {
		t.Fatalf("conditional badge request returned %d, want 304", second.Code)
	}
}

func TestFeedIsValidAtom(t *testing.T) {
	h := newHarness(t)

	rec := h.get(t, "/v1/feeds/trending.xml?window=24h&limit=10")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/atom+xml") {
		t.Errorf("Content-Type = %q", ct)
	}

	var feed atomFeed
	if err := xml.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("feed is not valid Atom: %v", err)
	}

	if feed.Title == "" || feed.ID == "" || feed.Updated == "" {
		t.Errorf("feed is missing required elements: %+v", feed)
	}
	if len(feed.Entries) == 0 {
		t.Fatal("feed has no entries")
	}
	for i, entry := range feed.Entries {
		if entry.ID == "" || entry.Title == "" || entry.Updated == "" {
			t.Errorf("entry %d is missing required elements: %+v", i, entry)
		}
		if len(entry.Links) == 0 {
			t.Errorf("entry %d has no link", i)
		}
	}

	// Demo mode must be unmistakable in a syndicated feed too.
	if !strings.Contains(feed.Subtitle, "DEMO") {
		t.Errorf("feed subtitle does not mark demo data: %q", feed.Subtitle)
	}
}

func TestFeedEntryIDsAreUnique(t *testing.T) {
	h := newHarness(t)

	var feed atomFeed
	if err := xml.Unmarshal(h.get(t, "/v1/feeds/trending.xml?limit=20").Body.Bytes(), &feed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	seen := map[string]bool{}
	for _, entry := range feed.Entries {
		if seen[entry.ID] {
			t.Errorf("duplicate entry ID %q; readers would collapse the entries", entry.ID)
		}
		seen[entry.ID] = true
	}
}

// --- tracking submissions ------------------------------------------------------

func TestTrackIsRefusedInDemoMode(t *testing.T) {
	h := newHarness(t)

	rec := h.post(t, "/v1/repositories/track", `{"repository":"someone/newthing"}`, "198.51.100.1")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 in demo mode: %s", rec.Code, rec.Body.String())
	}

	var problem Problem
	_ = json.Unmarshal(rec.Body.Bytes(), &problem)
	if !strings.Contains(strings.ToLower(problem.Detail), "demo") {
		t.Errorf("detail should explain demo mode: %q", problem.Detail)
	}
}

func TestTrackRateLimitAppliesBeforeTheDemoCheck(t *testing.T) {
	h := newHarness(t)

	// The configured limit is three per hour. The fourth submission from the
	// same client must be refused with 429 regardless of what follows.
	var last *httptest.ResponseRecorder
	for range 4 {
		last = h.post(t, "/v1/repositories/track", `{"repository":"someone/newthing"}`, "198.51.100.2")
	}

	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: %s", last.Code, last.Body.String())
	}
	if last.Header().Get("Retry-After") == "" {
		t.Error("a 429 must carry Retry-After")
	}
	if last.Header().Get("X-RateLimit-Limit") != "3" {
		t.Errorf("X-RateLimit-Limit = %q, want 3", last.Header().Get("X-RateLimit-Limit"))
	}
}

func TestTrackRateLimitIsolatesClients(t *testing.T) {
	h := newHarness(t)

	for range 4 {
		h.post(t, "/v1/repositories/track", `{"repository":"someone/newthing"}`, "198.51.100.3")
	}

	// A different address must have its own allowance.
	other := h.post(t, "/v1/repositories/track", `{"repository":"someone/newthing"}`, "198.51.100.4")
	if other.Code == http.StatusTooManyRequests {
		t.Fatal("a second client was throttled by the first client's usage")
	}
}

func TestTrackQueuesAJobInLiveMode(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Flip to live mode on the already-built server's configuration. Only the
	// tracking path reads this flag, so no other behaviour changes.
	h.cfg.App.DemoMode = false

	rec := h.post(t, "/v1/repositories/track", `{"repository":"someone/brand-new"}`, "198.51.100.5")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}

	body := decodeInto[TrackResponse](t, rec)
	if body.Status != "queued" {
		t.Errorf("status = %q", body.Status)
	}
	if !strings.Contains(body.Message, "cannot") {
		t.Errorf("the message should be explicit that history cannot be reconstructed: %q", body.Message)
	}

	// A high-priority collection job must now exist.
	queue := repository.NewJobs(h.db)
	claimed, err := queue.Claim(ctx, "test-worker", 10, integrationRef.Add(time.Minute))
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	var found bool
	for _, job := range claimed {
		// The payload is decoded rather than string-matched: PostgreSQL
		// normalises jsonb formatting, so substring assertions on the raw bytes
		// would be testing whitespace rather than behaviour.
		payload, err := repository.DecodeCollectPayload(job.Payload)
		if err != nil {
			t.Fatalf("stored payload does not decode into the shared type: %v (%s)", err, job.Payload)
		}
		if payload.Name != "brand-new" {
			continue
		}

		found = true
		if payload.Owner != "someone" {
			t.Errorf("owner = %q, want someone", payload.Owner)
		}
		if !payload.UserRequested {
			t.Error("the job is not marked user-requested, so it would be throttled like background work")
		}
		if job.Priority < 50 {
			t.Errorf("a user-submitted job has priority %d, want it prioritised", job.Priority)
		}
	}
	if !found {
		t.Fatal("no collection job was queued for the submitted repository")
	}
}

func TestTrackReportsAlreadyTracked(t *testing.T) {
	h := newHarness(t)
	h.cfg.App.DemoMode = false

	rec := h.post(t, "/v1/repositories/track", `{"repository":"orbit-labs/signal-kit"}`, "198.51.100.6")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an already-tracked repository: %s", rec.Code, rec.Body.String())
	}

	body := decodeInto[TrackResponse](t, rec)
	if body.Status != "already_tracked" {
		t.Errorf("status = %q", body.Status)
	}
	if body.WebURL == nil {
		t.Error("an already-tracked response should link to the repository page")
	}
}
