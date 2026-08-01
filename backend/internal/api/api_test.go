package api

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/repometeor/repometeor/backend/internal/clock"
	"github.com/repometeor/repometeor/backend/internal/config"
	"github.com/repometeor/repometeor/backend/internal/telemetry"
)

var apiRef = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

// newTestServer builds a Server with no database. Handlers that need data are
// exercised by the integration suite; everything here covers validation,
// headers, error shape and rendering, which is where the security-relevant
// behaviour lives and where a fast unit test is most valuable.
func newTestServer(t *testing.T) *Server {
	t.Helper()

	cfg, err := config.LoadFromMap(map[string]string{
		"DEMO_MODE":            "true",
		"DATABASE_URL":         "postgres://u:p@db:5432/repometeor",
		"API_PUBLIC_URL":       "https://api.example.test",
		"WEB_PUBLIC_URL":       "https://example.test",
		"CORS_ALLOWED_ORIGINS": "https://example.test",
		"METRICS_ENABLED":      "true",
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	return New(Options{
		Config:  cfg,
		Service: nil, // handlers that dereference it are covered by integration tests
		DB:      nil,
		Log:     telemetry.Discard(),
		Metrics: telemetry.NewMetrics("test"),
		Clock:   clock.NewFake(apiRef),
	})
}

func do(t *testing.T, s *Server, method, target string, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}

	req := httptest.NewRequest(method, target, reader)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// httptest.NewRequest sets a RemoteAddr; make it explicit and stable.
	req.RemoteAddr = "203.0.113.5:1234"

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) Problem {
	t.Helper()

	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, problemContentType) {
		t.Fatalf("Content-Type = %q, want %s", got, problemContentType)
	}

	var problem Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("problem body is not JSON: %v\n%s", err, rec.Body.String())
	}
	return problem
}

// --- health ------------------------------------------------------------------

func TestHealthzDoesNotDependOnTheDatabase(t *testing.T) {
	s := newTestServer(t)

	rec := do(t, s, http.MethodGet, "/healthz", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; liveness must not fail because PostgreSQL is down", rec.Code)
	}

	var body HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" || body.Service != "repometeor-api" {
		t.Errorf("body = %+v", body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestReadyzFailsWithoutADatabase(t *testing.T) {
	s := newTestServer(t)

	rec := do(t, s, http.MethodGet, "/readyz", "", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when the database is unavailable", rec.Code)
	}

	problem := decodeProblem(t, rec)
	if problem.Type != ProblemNotReady {
		t.Errorf("type = %q, want %q", problem.Type, ProblemNotReady)
	}
}

func TestMetricsEndpointIsExposed(t *testing.T) {
	s := newTestServer(t)

	rec := do(t, s, http.MethodGet, "/metrics", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "repometeor_build_info") {
		t.Error("metrics output is missing repometeor_build_info")
	}
}

func TestMetricsCanBeDisabled(t *testing.T) {
	cfg, err := config.LoadFromMap(map[string]string{
		"DEMO_MODE":       "true",
		"DATABASE_URL":    "postgres://u:p@db:5432/repometeor",
		"METRICS_ENABLED": "false",
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	s := New(Options{
		Config: cfg, Log: telemetry.Discard(),
		Metrics: telemetry.NewMetrics("test"), Clock: clock.NewFake(apiRef),
	})

	if rec := do(t, s, http.MethodGet, "/metrics", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when metrics are disabled", rec.Code)
	}
}

// --- request identity and headers --------------------------------------------

func TestRequestIDIsGeneratedAndEchoed(t *testing.T) {
	s := newTestServer(t)

	rec := do(t, s, http.MethodGet, "/healthz", "", nil)
	if got := rec.Header().Get(RequestIDHeader); got == "" {
		t.Fatal("no request ID was returned")
	}
}

func TestClientRequestIDIsHonouredWhenSafe(t *testing.T) {
	s := newTestServer(t)

	rec := do(t, s, http.MethodGet, "/healthz", "", map[string]string{RequestIDHeader: "trace-abc123"})
	if got := rec.Header().Get(RequestIDHeader); got != "trace-abc123" {
		t.Fatalf("request ID = %q, want the supplied trace-abc123", got)
	}
}

func TestUnsafeRequestIDIsReplaced(t *testing.T) {
	s := newTestServer(t)

	// A header value containing CR/LF or other punctuation could inject a
	// second header or corrupt a log line, so it must be discarded.
	for _, unsafe := range []string{
		"trace with spaces",
		"trace\r\nX-Injected: yes",
		strings.Repeat("a", 200),
		"trace;drop",
		"<script>",
	} {
		rec := do(t, s, http.MethodGet, "/healthz", "", map[string]string{RequestIDHeader: unsafe})
		got := rec.Header().Get(RequestIDHeader)
		if got == unsafe {
			t.Errorf("unsafe request ID %q was echoed back", unsafe)
		}
		if strings.ContainsAny(got, "\r\n <>;") {
			t.Errorf("generated request ID %q contains unsafe characters", got)
		}
	}
}

func TestSecurityHeadersArePresent(t *testing.T) {
	s := newTestServer(t)
	rec := do(t, s, http.MethodGet, "/healthz", "", nil)

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for header, value := range want {
		if got := rec.Header().Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("Content-Security-Policy is missing")
	}
}

// --- CORS --------------------------------------------------------------------

func TestCORSAllowsConfiguredOrigin(t *testing.T) {
	s := newTestServer(t)

	rec := do(t, s, http.MethodGet, "/healthz", "", map[string]string{"Origin": "https://example.test"})
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.test" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want the configured origin", got)
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Origin") {
		t.Error("Vary must include Origin so caches do not share cross-origin responses")
	}
}

func TestCORSRejectsUnknownOrigin(t *testing.T) {
	s := newTestServer(t)

	rec := do(t, s, http.MethodGet, "/healthz", "", map[string]string{"Origin": "https://evil.test"})
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want it absent for an unlisted origin", got)
	}
}

func TestCORSPreflight(t *testing.T) {
	s := newTestServer(t)

	rec := do(t, s, http.MethodOptions, "/v1/trending", "", map[string]string{
		"Origin":                        "https://example.test",
		"Access-Control-Request-Method": "GET",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "GET") {
		t.Errorf("Access-Control-Allow-Methods = %q", got)
	}
}

// --- routing and problem shape ------------------------------------------------

func TestUnknownRouteReturnsProblemJSON(t *testing.T) {
	s := newTestServer(t)

	rec := do(t, s, http.MethodGet, "/v1/does-not-exist", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}

	problem := decodeProblem(t, rec)
	if problem.Type != ProblemNotFound {
		t.Errorf("type = %q", problem.Type)
	}
	if problem.RequestID == "" {
		t.Error("problem is missing a request ID")
	}
	if problem.Instance != "/v1/does-not-exist" {
		t.Errorf("instance = %q", problem.Instance)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store; errors must not be cached", got)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	s := newTestServer(t)

	rec := do(t, s, http.MethodDelete, "/v1/trending", "", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	decodeProblem(t, rec)
}

func TestHeadIsSupportedOnReadRoutes(t *testing.T) {
	s := newTestServer(t)

	// Image proxies and monitors probe with HEAD; a 405 there would break
	// badges embedded in READMEs.
	for _, path := range []string{
		"/healthz", "/readyz", "/metrics",
		"/v1/badges/orbit-labs/signal-kit.svg", "/v1/methodology",
	} {
		rec := do(t, s, http.MethodHead, path, "", nil)
		if rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("HEAD %s returned 405", path)
		}
	}
}

// --- trending validation ------------------------------------------------------

func TestTrendingRejectsInvalidParameters(t *testing.T) {
	s := newTestServer(t)

	tests := []struct {
		name  string
		query string
		field string
	}{
		{"unknown window", "?window=12h", "window"},
		{"unknown collection", "?collection=everything", "collection"},
		{"limit too large", "?limit=1000", "limit"},
		{"limit zero", "?limit=0", "limit"},
		{"limit not a number", "?limit=abc", "limit"},
		{"negative minStars", "?minStars=-5", "minStars"},
		{"unknown confidence", "?confidence=perfect", "confidence"},
		{"topic with punctuation", "?topic=a%3Cb%3E", "topic"},
		{"minStars above maxStars", "?minStars=100&maxStars=10", "minStars"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, s, http.MethodGet, "/v1/trending"+tc.query, "", nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}

			problem := decodeProblem(t, rec)
			if problem.Type != ProblemValidation {
				t.Fatalf("type = %q, want %q", problem.Type, ProblemValidation)
			}

			var found bool
			for _, fieldErr := range problem.Errors {
				if fieldErr.Field == tc.field {
					found = true
				}
			}
			if !found {
				t.Fatalf("errors = %+v, want one for field %q", problem.Errors, tc.field)
			}
		})
	}
}

func TestTrendingQueryDefaults(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/trending", nil)
	c := ginContextFor(req)

	query, problem := parseTrendingQuery(c)
	if problem != nil {
		t.Fatalf("unexpected problem: %+v", problem)
	}
	if query.Window != "24h" {
		t.Errorf("window = %q, want the 24h default", query.Window)
	}
	if query.Collection != "daily" {
		t.Errorf("collection = %q, want daily for the 24h window", query.Collection)
	}
	if query.Limit != defaultLimit {
		t.Errorf("limit = %d, want %d", query.Limit, defaultLimit)
	}
}

func TestCollectionImpliesItsDefaultWindow(t *testing.T) {
	cases := map[string]string{
		"rising":       "6h",
		"weekly":       "7d",
		"hidden-gems":  "24h",
		"new-entrants": "24h",
		"established":  "24h",
	}
	for collection, wantWindow := range cases {
		req := httptest.NewRequest(http.MethodGet, "/v1/trending?collection="+collection, nil)
		query, problem := parseTrendingQuery(ginContextFor(req))
		if problem != nil {
			t.Fatalf("%s: unexpected problem %+v", collection, problem)
		}
		if query.Window != wantWindow {
			t.Errorf("collection %s implied window %q, want %q", collection, query.Window, wantWindow)
		}
	}
}

func TestExplicitWindowOverridesCollectionDefault(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/trending?collection=hidden-gems&window=7d", nil)
	query, problem := parseTrendingQuery(ginContextFor(req))
	if problem != nil {
		t.Fatalf("unexpected problem: %+v", problem)
	}
	if query.Window != "7d" {
		t.Errorf("window = %q, want the explicitly requested 7d", query.Window)
	}
	if query.Collection != "hidden-gems" {
		t.Errorf("collection = %q", query.Collection)
	}
}

// --- repository path validation -----------------------------------------------

func TestRepositoryPathValidation(t *testing.T) {
	s := newTestServer(t)

	// The router only matches two path segments, so traversal and injection
	// attempts either miss the route entirely or fail validation. Either way
	// they must never reach the service layer.
	paths := []string{
		"/v1/repositories/-bad/repo",
		"/v1/repositories/owner/re%20po",
		"/v1/repositories/owner/repo;drop",
		"/v1/repositories/" + strings.Repeat("a", 60) + "/repo",
	}
	for _, path := range paths {
		rec := do(t, s, http.MethodGet, path, "", nil)
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
			t.Errorf("GET %s returned %d, want 400 or 404", path, rec.Code)
		}
	}
}

// --- compare validation -------------------------------------------------------

func TestCompareValidation(t *testing.T) {
	s := newTestServer(t)

	tests := map[string]string{
		"missing parameter": "/v1/compare",
		"only one":          "/v1/compare?repositories=owner/a",
		"too many":          "/v1/compare?repositories=o/a,o/b,o/c,o/d,o/e",
		"invalid host":      "/v1/compare?repositories=owner/a,https://gitlab.com/o/b",
		"duplicate":         "/v1/compare?repositories=owner/a,Owner/A",
		"bad window":        "/v1/compare?repositories=o/a,o/b&window=99h",
		"bad range":         "/v1/compare?repositories=o/a,o/b&range=5y",
	}
	for name, target := range tests {
		t.Run(name, func(t *testing.T) {
			rec := do(t, s, http.MethodGet, target, "", nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			decodeProblem(t, rec)
		})
	}
}

// --- history validation -------------------------------------------------------

func TestHistoryValidation(t *testing.T) {
	s := newTestServer(t)

	for _, target := range []string{
		"/v1/repositories/owner/repo/history?range=5y",
		"/v1/repositories/owner/repo/history?resolution=every-second",
		"/v1/repositories/owner/repo/history?window=99h",
	} {
		rec := do(t, s, http.MethodGet, target, "", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s returned %d, want 400", target, rec.Code)
		}
	}
}

func TestHistoryResolutionDefaults(t *testing.T) {
	cases := map[string]string{
		"24h": "1h",
		"7d":  "1h",
		"30d": "1d",
		"90d": "1d",
	}
	for rangeKey, wantResolution := range cases {
		req := httptest.NewRequest(http.MethodGet, "/x?range="+rangeKey, nil)
		_, resolution, problem := parseHistoryParams(ginContextFor(req))
		if problem != nil {
			t.Fatalf("%s: unexpected problem %+v", rangeKey, problem)
		}
		if string(resolution) != wantResolution {
			t.Errorf("range %s defaulted to resolution %q, want %q", rangeKey, resolution, wantResolution)
		}
	}
}

// --- track endpoint -----------------------------------------------------------

func TestTrackRequiresJSONContentType(t *testing.T) {
	s := newTestServer(t)

	rec := do(t, s, http.MethodPost, "/v1/repositories/track", `{"repository":"o/r"}`,
		map[string]string{"Content-Type": "text/plain"})
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
	decodeProblem(t, rec)
}

func TestTrackRejectsInvalidRepositories(t *testing.T) {
	s := newTestServer(t)

	bodies := []string{
		`{"repository":"https://gitlab.com/o/r"}`,
		`{"repository":"../../etc/passwd"}`,
		`{"repository":"owner"}`,
		`{"repository":""}`,
		`{"repository":"https://github.com/o/r/issues"}`,
		`{"repository":"o/r\nX: y"}`,
		`{}`,
	}
	for _, body := range bodies {
		rec := do(t, s, http.MethodPost, "/v1/repositories/track", body,
			map[string]string{"Content-Type": "application/json"})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s returned %d, want 400", body, rec.Code)
			continue
		}
		problem := decodeProblem(t, rec)
		if problem.Type != ProblemValidation {
			t.Errorf("body %s: type = %q", body, problem.Type)
		}
	}
}

func TestTrackRejectsMalformedJSON(t *testing.T) {
	s := newTestServer(t)

	rec := do(t, s, http.MethodPost, "/v1/repositories/track", `{not json`,
		map[string]string{"Content-Type": "application/json"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestRequestBodyIsCapped(t *testing.T) {
	s := newTestServer(t)

	huge := `{"repository":"` + strings.Repeat("a", 200_000) + `"}`
	rec := do(t, s, http.MethodPost, "/v1/repositories/track", huge,
		map[string]string{"Content-Type": "application/json"})

	// Either the body limit or the validation rejects it; what matters is that
	// an oversized body never succeeds and never crashes the handler.
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 400 or 413", rec.Code)
	}
}

// --- badges -------------------------------------------------------------------

func TestBadgeRendersValidSVG(t *testing.T) {
	svg := renderBadge("momentum 24h", "92.4", themeLight, false)

	var parsed any
	if err := xml.Unmarshal(svg, &parsed); err != nil {
		t.Fatalf("badge is not well-formed XML: %v\n%s", err, svg)
	}

	content := string(svg)
	for _, want := range []string{`<title>`, `role="img"`, `aria-label=`, "momentum 24h", "92.4"} {
		if !strings.Contains(content, want) {
			t.Errorf("badge is missing %q", want)
		}
	}
}

func TestBadgeEscapesDynamicValues(t *testing.T) {
	// A repository name or metric value must never be able to inject markup.
	svg := string(renderBadge(`</text><script>alert(1)</script>`, `"&<>`, themeLight, false))

	if strings.Contains(svg, "<script>") {
		t.Fatalf("badge contains an unescaped script tag:\n%s", svg)
	}
	for _, raw := range []string{"</text><script>", `"&<>`} {
		if strings.Contains(svg, raw) {
			t.Fatalf("badge contains unescaped input %q:\n%s", raw, svg)
		}
	}

	var parsed any
	if err := xml.Unmarshal([]byte(svg), &parsed); err != nil {
		t.Fatalf("badge with hostile input is not well-formed: %v", err)
	}
}

func TestBadgeReferencesNoExternalResources(t *testing.T) {
	// A badge is rendered inside a README by GitHub's image proxy. Anything it
	// fetched would leak the reader's request to a third party, so the document
	// must be entirely self-contained.
	svg := string(renderBadge("momentum", "92.4", themeAuto, false))

	// Mechanisms that could load something remote.
	for _, forbidden := range []string{
		"xlink:href", "<image", "<use", "@import", "url(http", "url(//", "<script", "<foreignObject",
	} {
		if strings.Contains(svg, forbidden) {
			t.Errorf("badge could load an external resource via %q", forbidden)
		}
	}

	// The SVG namespace declaration is the only URI permitted in the document;
	// every other absolute reference would be a fetch.
	if strings.Count(svg, "http://www.w3.org/2000/svg") != 1 {
		t.Error("expected exactly one namespace declaration")
	}
	withoutNamespace := strings.ReplaceAll(svg, "http://www.w3.org/2000/svg", "")
	for _, scheme := range []string{"http://", "https://", "//cdn", "data:"} {
		if strings.Contains(withoutNamespace, scheme) {
			t.Errorf("badge contains an absolute reference (%q) beyond the namespace declaration", scheme)
		}
	}

	// url(#fragment) is a same-document reference and is expected.
	if !strings.Contains(svg, "url(#r)") {
		t.Error("expected the clip path to be referenced by fragment")
	}
}

func TestBadgeThemes(t *testing.T) {
	light := string(renderBadge("momentum", "50", themeLight, false))
	dark := string(renderBadge("momentum", "50", themeDark, false))
	auto := string(renderBadge("momentum", "50", themeAuto, false))

	if light == dark {
		t.Error("light and dark badges should differ")
	}
	if !strings.Contains(auto, "prefers-color-scheme") {
		t.Error("the auto theme should adapt via a media query")
	}
	if strings.Contains(light, "prefers-color-scheme") {
		t.Error("an explicitly chosen theme must not be overridden by a media query")
	}
}

func TestBadgeWidthGrowsWithContent(t *testing.T) {
	short := renderBadge("m", "1", themeLight, false)
	long := renderBadge("momentum 24h", "1,234,567", themeLight, false)

	if len(long) <= len(short) {
		t.Fatal("a longer badge should produce a wider SVG")
	}
	if !strings.Contains(string(long), `width="`) {
		t.Fatal("badge is missing a width attribute")
	}
}

func TestBadgeInvalidParametersStillRenderAnImage(t *testing.T) {
	s := newTestServer(t)

	// An <img> tag cannot display a problem document, so invalid input must
	// still produce a valid SVG rather than JSON.
	for _, target := range []string{
		"/v1/badges/owner/repo.svg?metric=nonsense",
		"/v1/badges/owner/repo.svg?window=99h",
	} {
		rec := do(t, s, http.MethodGet, target, "", nil)
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/svg+xml") {
			t.Errorf("GET %s: Content-Type = %q, want an SVG", target, got)
		}

		var parsed any
		if err := xml.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
			t.Errorf("GET %s: body is not valid SVG: %v", target, err)
		}
	}
}

func TestEscapeXML(t *testing.T) {
	tests := map[string]string{
		"plain":    "plain",
		"a<b":      "a&lt;b",
		`say "hi"`: "say &#34;hi&#34;",
		"a&b":      "a&amp;b",
		"tom's":    "tom&#39;s",
		"<script>": "&lt;script&gt;",
	}
	for in, want := range tests {
		if got := escapeXML(in); got != want {
			t.Errorf("escapeXML(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- feeds --------------------------------------------------------------------

func TestFeedStructureIsValidAtom(t *testing.T) {
	feed := atomFeed{
		Title:   "RepoMeteor",
		ID:      "https://api.example.test/v1/feeds/trending.xml",
		Updated: apiRef.Format(time.RFC3339),
		Author:  atomAuthor{Name: "RepoMeteor"},
		Entries: []atomEntry{{
			Title:   "#1 · orbit-labs/signal-kit",
			ID:      "https://api.example.test/v1/repositories/orbit-labs/signal-kit#24h@1",
			Updated: apiRef.Format(time.RFC3339),
			Summary: atomContent{Type: "text", Body: "Score 92.4"},
		}},
	}

	encoded, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed any
	if err := xml.Unmarshal(encoded, &parsed); err != nil {
		t.Fatalf("feed is not well-formed XML: %v", err)
	}

	content := string(encoded)
	for _, want := range []string{
		`xmlns="http://www.w3.org/2005/Atom"`, "<title>", "<id>", "<updated>", "<entry>",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("feed is missing %q", want)
		}
	}
}

func TestFeedEscapesHostileContent(t *testing.T) {
	feed := atomFeed{
		Title:   `</title><script>alert(1)</script>`,
		ID:      "urn:test",
		Updated: apiRef.Format(time.RFC3339),
		Author:  atomAuthor{Name: "RepoMeteor"},
		Entries: []atomEntry{{
			Title:   `A & B <injected>`,
			ID:      "urn:entry",
			Updated: apiRef.Format(time.RFC3339),
			Content: atomContent{Type: "text", Body: `<img src=x onerror=alert(1)>`},
		}},
	}

	encoded, err := xml.Marshal(feed)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	content := string(encoded)
	if strings.Contains(content, "<script>") || strings.Contains(content, "<img src=x") {
		t.Fatalf("feed contains unescaped markup:\n%s", content)
	}

	var parsed any
	if err := xml.Unmarshal(encoded, &parsed); err != nil {
		t.Fatalf("feed with hostile input is not well-formed: %v", err)
	}
}

func TestEntryIDsAreStablePerGeneration(t *testing.T) {
	base := "https://api.example.test"
	first := entryID(base, "orbit-labs/signal-kit", "24h", apiRef)
	same := entryID(base, "orbit-labs/signal-kit", "24h", apiRef)
	later := entryID(base, "orbit-labs/signal-kit", "24h", apiRef.Add(time.Hour))

	if first != same {
		t.Error("the same generation must produce the same entry ID")
	}
	if first == later {
		t.Error("a new generation must produce a new entry ID so readers see an update")
	}
}

// --- ETag ---------------------------------------------------------------------

func TestETagMatches(t *testing.T) {
	const etag = `W/"abc123"`

	tests := map[string]bool{
		etag:                 true,
		"*":                  true,
		`W/"other", ` + etag: true,
		` ` + etag + ` `:     true,
		`W/"other"`:          false,
		"":                   false,
	}
	for header, want := range tests {
		if got := etagMatches(header, etag); got != want {
			t.Errorf("etagMatches(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestMethodologyIsCacheableAndConditional(t *testing.T) {
	s := newTestServer(t)

	first := do(t, s, http.MethodGet, "/v1/methodology", "", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", first.Code)
	}

	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag was returned")
	}
	if cc := first.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=3600") {
		t.Errorf("Cache-Control = %q, want a one-hour policy", cc)
	}

	second := do(t, s, http.MethodGet, "/v1/methodology", "", map[string]string{"If-None-Match": etag})
	if second.Code != http.StatusNotModified {
		t.Fatalf("conditional request returned %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Error("a 304 must have an empty body")
	}
}

func TestMethodologyPublishesTheCompleteFormula(t *testing.T) {
	s := newTestServer(t)

	rec := do(t, s, http.MethodGet, "/v1/methodology", "", nil)
	var body MethodologyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.FormulaVersion == "" {
		t.Error("formulaVersion is empty")
	}
	if body.Formula == "" {
		t.Error("the formula string is empty")
	}

	var sum float64
	for _, w := range body.Weights {
		sum += w
	}
	if sum < 0.9999 || sum > 1.0001 {
		t.Errorf("published weights sum to %v, want 1.0", sum)
	}

	if len(body.Windows) != 3 {
		t.Errorf("got %d windows, want 3", len(body.Windows))
	}
	if len(body.Collections) != 6 {
		t.Errorf("got %d collections, want 6", len(body.Collections))
	}
	if !strings.Contains(strings.ToLower(body.Snapshots.PrivacyNote), "stargazer") {
		t.Error("the privacy note should state that stargazer identities are not collected")
	}
}

// --- rate limiting -------------------------------------------------------------

func TestReadRateLimiterAllowsThenRefuses(t *testing.T) {
	fake := clock.NewFake(apiRef)
	limiter := newReadLimiter(3, time.Minute, fake)

	for i := 1; i <= 3; i++ {
		allowed, remaining, _ := limiter.allow("203.0.113.5")
		if !allowed {
			t.Fatalf("request %d was refused", i)
		}
		if want := 3 - i; remaining != want {
			t.Errorf("request %d remaining = %d, want %d", i, remaining, want)
		}
	}

	if allowed, _, _ := limiter.allow("203.0.113.5"); allowed {
		t.Fatal("the fourth request should be refused")
	}
}

func TestReadRateLimiterResetsInANewWindow(t *testing.T) {
	fake := clock.NewFake(apiRef)
	limiter := newReadLimiter(2, time.Minute, fake)

	limiter.allow("client")
	limiter.allow("client")
	if allowed, _, _ := limiter.allow("client"); allowed {
		t.Fatal("expected the limit to be reached")
	}

	fake.Advance(time.Minute)
	if allowed, _, _ := limiter.allow("client"); !allowed {
		t.Fatal("the allowance should reset in a new window")
	}
}

func TestReadRateLimiterIsolatesClients(t *testing.T) {
	limiter := newReadLimiter(1, time.Minute, clock.NewFake(apiRef))

	limiter.allow("a")
	if allowed, _, _ := limiter.allow("b"); !allowed {
		t.Fatal("a different client must have its own allowance")
	}
}

func TestReadRateLimiterReturnsHeaders(t *testing.T) {
	cfg, err := config.LoadFromMap(map[string]string{
		"DEMO_MODE":                      "true",
		"DATABASE_URL":                   "postgres://u:p@db:5432/repometeor",
		"API_READ_RATE_LIMIT_PER_MINUTE": "2",
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	s := New(Options{
		Config: cfg, Log: telemetry.Discard(),
		Metrics: telemetry.NewMetrics("test"), Clock: clock.NewFake(apiRef),
	})

	// The third request exceeds the configured limit of two.
	var last *httptest.ResponseRecorder
	for range 3 {
		last = do(t, s, http.MethodGet, "/v1/trending?window=bad", "", nil)
	}

	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", last.Code)
	}
	if last.Header().Get("Retry-After") == "" {
		t.Error("a 429 must carry Retry-After")
	}
	if last.Header().Get("X-RateLimit-Limit") != "2" {
		t.Errorf("X-RateLimit-Limit = %q, want 2", last.Header().Get("X-RateLimit-Limit"))
	}

	problem := decodeProblem(t, last)
	if problem.Type != ProblemRateLimited {
		t.Errorf("type = %q, want %q", problem.Type, ProblemRateLimited)
	}
}

func TestHealthEndpointsAreNotRateLimited(t *testing.T) {
	cfg, err := config.LoadFromMap(map[string]string{
		"DEMO_MODE":                      "true",
		"DATABASE_URL":                   "postgres://u:p@db:5432/repometeor",
		"API_READ_RATE_LIMIT_PER_MINUTE": "1",
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	s := New(Options{
		Config: cfg, Log: telemetry.Discard(),
		Metrics: telemetry.NewMetrics("test"), Clock: clock.NewFake(apiRef),
	})

	for i := range 10 {
		if rec := do(t, s, http.MethodGet, "/healthz", "", nil); rec.Code != http.StatusOK {
			t.Fatalf("probe %d returned %d; liveness must never be throttled", i, rec.Code)
		}
	}
}

// --- client address resolution --------------------------------------------------

func TestClientIPIgnoresForwardedHeadersFromUntrustedPeers(t *testing.T) {
	resolver := NewClientIPResolver(nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.7")

	if got := resolver.Resolve(req); got != "203.0.113.5" {
		t.Fatalf("resolved %q, want the socket peer; an untrusted client must not be able "+
			"to spoof its address and mint fresh rate-limit buckets", got)
	}
}

func TestClientIPUsesForwardedHeaderFromTrustedProxy(t *testing.T) {
	cfg, err := config.LoadFromMap(map[string]string{
		"DEMO_MODE":           "true",
		"DATABASE_URL":        "postgres://u:p@db:5432/repometeor",
		"TRUSTED_PROXY_CIDRS": "10.0.0.0/8",
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	resolver := NewClientIPResolver(cfg.API.TrustedProxyCIDRs)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.1.2.3:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 10.1.2.3")

	if got := resolver.Resolve(req); got != "198.51.100.7" {
		t.Fatalf("resolved %q, want the right-most non-proxy address 198.51.100.7", got)
	}
}

func TestClientIPFallsBackWhenForwardedHeaderIsUnusable(t *testing.T) {
	cfg, _ := config.LoadFromMap(map[string]string{
		"DEMO_MODE":           "true",
		"DATABASE_URL":        "postgres://u:p@db:5432/repometeor",
		"TRUSTED_PROXY_CIDRS": "10.0.0.0/8",
	})
	resolver := NewClientIPResolver(cfg.API.TrustedProxyCIDRs)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.1.2.3:1234"
	req.Header.Set("X-Forwarded-For", "not-an-ip, also-not")

	if got := resolver.Resolve(req); got != "10.1.2.3" {
		t.Fatalf("resolved %q, want the peer address when the header is unusable", got)
	}
}

// --- helpers ------------------------------------------------------------------

func TestParseBoundedInt(t *testing.T) {
	if _, err := parseBoundedInt("abc", 1, 10); err == nil {
		t.Error("expected an error for a non-integer")
	}
	if _, err := parseBoundedInt("0", 1, 10); err == nil {
		t.Error("expected an error below the lower bound")
	}
	if _, err := parseBoundedInt("11", 1, 10); err == nil {
		t.Error("expected an error above the upper bound")
	}
	if got, err := parseBoundedInt("5", 1, 10); err != nil || got != 5 {
		t.Errorf("parseBoundedInt(5) = %d, %v", got, err)
	}
}

func TestRoundHelpers(t *testing.T) {
	if got := round1(92.449); got != 92.4 {
		t.Errorf("round1(92.449) = %v, want 92.4", got)
	}
	if got := round1(92.45); got != 92.5 {
		t.Errorf("round1(92.45) = %v, want 92.5", got)
	}
	if got := round3(0.123456); got != 0.123 {
		t.Errorf("round3(0.123456) = %v, want 0.123", got)
	}
	if got := round1(-1.25); got != -1.3 {
		t.Errorf("round1(-1.25) = %v, want -1.3", got)
	}
}

func TestNonNilCollectionsSerialiseAsArrays(t *testing.T) {
	// A nil slice would render as JSON null, which forces every client to
	// null-check what should always be a list.
	encoded, err := json.Marshal(map[string]any{
		"strings": nonNilStrings(nil),
		"ints":    nonNilInts(nil),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "null") {
		t.Fatalf("empty collections serialised as null: %s", encoded)
	}
}

// ginContextFor builds a gin.Context around a request so that parameter
// parsers can be unit tested without routing.
func ginContextFor(req *http.Request) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return c
}

func TestRetryAfterSecondsUsesTheServiceClock(t *testing.T) {
	now := apiRef

	// A future reset rounds up to whole seconds.
	if got := retryAfterSeconds(now, now.Add(30*time.Second)); got != 31 {
		t.Errorf("retryAfterSeconds(+30s) = %d, want 31", got)
	}

	// A reset that has already passed — which happens whenever the service
	// clock and the wall clock disagree — must still yield a usable header
	// rather than a value that would be omitted entirely.
	for _, resetAt := range []time.Time{now, now.Add(-time.Hour), now.Add(-time.Millisecond)} {
		if got := retryAfterSeconds(now, resetAt); got < 1 {
			t.Errorf("retryAfterSeconds(reset=%v) = %d, want at least 1", resetAt, got)
		}
	}
}
