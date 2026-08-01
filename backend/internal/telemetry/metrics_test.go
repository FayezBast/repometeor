package telemetry

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestStatusClass(t *testing.T) {
	tests := map[int]string{
		100: "1xx", 200: "2xx", 201: "2xx", 304: "3xx",
		400: "4xx", 404: "4xx", 429: "4xx", 500: "5xx", 503: "5xx", 0: "unknown",
	}
	for status, want := range tests {
		if got := StatusClass(status); got != want {
			t.Errorf("StatusClass(%d) = %q, want %q", status, got, want)
		}
	}
}

func TestObserveHTTPUsesRouteTemplateLabels(t *testing.T) {
	m := NewMetrics("test")
	m.ObserveHTTP("/v1/repositories/:owner/:repository", "GET", 200, 12*time.Millisecond)
	m.ObserveHTTP("/v1/repositories/:owner/:repository", "GET", 404, 3*time.Millisecond)

	got := testutil.CollectAndCount(m.httpRequests)
	if got != 2 {
		t.Fatalf("counter series = %d, want 2 (one per status class)", got)
	}

	dumped := dump(t, m)
	if strings.Contains(dumped, "orbit-labs") {
		t.Fatal("metrics must not contain repository identifiers")
	}
	if !strings.Contains(dumped, `route="/v1/repositories/:owner/:repository"`) {
		t.Fatalf("expected a route template label, got:\n%s", dumped)
	}
}

func TestGitHubMetrics(t *testing.T) {
	m := NewMetrics("test")
	m.ObserveGitHubRequest("get_repository", "ok")
	m.ObserveGitHubRequest("get_repository", "not_found")
	m.IncGitHubCacheHit()
	m.IncGitHubRetry()
	m.SetGitHubRemaining("core", 4321)

	if got := testutil.ToFloat64(m.githubCacheHits); got != 1 {
		t.Errorf("cache hits = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.githubRemaining.WithLabelValues("core")); got != 4321 {
		t.Errorf("remaining = %v, want 4321", got)
	}
}

func TestJobAndScoringMetrics(t *testing.T) {
	m := NewMetrics("test")
	m.ObserveJob("collect_repository", "completed", 50*time.Millisecond)
	m.ObserveJob("collect_repository", "failed", 10*time.Millisecond)
	m.SetJobQueueDepth("queued", 7)
	m.AddSnapshots(3)
	m.ObserveScoreRun("24h", 128, 400*time.Millisecond)
	m.AddDiscovery("accepted", 12)
	m.ObserveDBQuery("trending", 2*time.Millisecond)

	if got := testutil.ToFloat64(m.jobQueueDepth.WithLabelValues("queued")); got != 7 {
		t.Errorf("queue depth = %v, want 7", got)
	}
	if got := testutil.ToFloat64(m.snapshotsStored); got != 3 {
		t.Errorf("snapshots = %v, want 3", got)
	}
	if got := testutil.ToFloat64(m.scoredRepos.WithLabelValues("24h")); got != 128 {
		t.Errorf("scored repositories = %v, want 128", got)
	}
}

func TestBuildInfoIsRegistered(t *testing.T) {
	m := NewMetrics("0.1.0")
	if got := testutil.ToFloat64(m.buildInfo.WithLabelValues("0.1.0")); got != 1 {
		t.Fatalf("build_info = %v, want 1", got)
	}
}

func TestNilMetricsIsSafe(t *testing.T) {
	var m *Metrics
	// None of these should panic; services may run with metrics disabled.
	m.ObserveHTTP("/x", "GET", 200, time.Second)
	m.IncInFlight()
	m.DecInFlight()
	m.ObserveGitHubRequest("op", "ok")
	m.IncGitHubCacheHit()
	m.IncGitHubRetry()
	m.SetGitHubRemaining("core", 1)
	m.ObserveJob("k", "completed", time.Second)
	m.SetJobQueueDepth("queued", 1)
	m.AddSnapshots(1)
	m.ObserveScoreRun("24h", 1, time.Second)
	m.AddDiscovery("accepted", 1)
	m.ObserveDBQuery("op", time.Second)
}

func dump(t *testing.T, m *Metrics) string {
	t.Helper()
	families, err := m.registry.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	var b strings.Builder
	for _, f := range families {
		for _, metric := range f.GetMetric() {
			b.WriteString(f.GetName())
			for _, label := range metric.GetLabel() {
				b.WriteString(" " + label.GetName() + `="` + label.GetValue() + `"`)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}
