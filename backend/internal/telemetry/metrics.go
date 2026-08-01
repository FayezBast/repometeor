package telemetry

import (
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics is the RepoMeteor Prometheus metric set.
//
// Every label used here is deliberately low cardinality. Repository names,
// owner logins, request paths with identifiers and error strings are never
// used as label values; route templates and coarse outcome classes are.
type Metrics struct {
	registry *prometheus.Registry

	httpRequests    *prometheus.CounterVec
	httpDuration    *prometheus.HistogramVec
	httpInFlight    prometheus.Gauge
	githubRequests  *prometheus.CounterVec
	githubCacheHits prometheus.Counter
	githubRetries   prometheus.Counter
	githubRemaining *prometheus.GaugeVec
	jobsProcessed   *prometheus.CounterVec
	jobDuration     *prometheus.HistogramVec
	jobQueueDepth   *prometheus.GaugeVec
	snapshotsStored prometheus.Counter
	scoreRunSeconds *prometheus.HistogramVec
	scoredRepos     *prometheus.GaugeVec
	discoveryFound  *prometheus.CounterVec
	dbQuerySeconds  *prometheus.HistogramVec
	buildInfo       *prometheus.GaugeVec
}

// NewMetrics constructs the metric set with its own registry, plus the standard
// Go runtime and process collectors.
func NewMetrics(version string) *Metrics {
	registry := prometheus.NewRegistry()

	m := &Metrics{
		registry: registry,
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "repometeor_http_requests_total",
			Help: "Total HTTP requests handled, by route template, method and status class.",
		}, []string{"route", "method", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "repometeor_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds, by route template.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"route", "method"}),
		httpInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "repometeor_http_requests_in_flight",
			Help: "HTTP requests currently being served.",
		}),
		githubRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "repometeor_github_requests_total",
			Help: "GitHub API requests, by operation and outcome class.",
		}, []string{"operation", "outcome"}),
		githubCacheHits: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "repometeor_github_cache_hits_total",
			Help: "GitHub responses served as 304 Not Modified via conditional requests.",
		}),
		githubRetries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "repometeor_github_retries_total",
			Help: "GitHub requests retried after a transient failure.",
		}),
		githubRemaining: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "repometeor_github_rate_limit_remaining",
			Help: "Remaining GitHub rate-limit units, by resource.",
		}, []string{"resource"}),
		jobsProcessed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "repometeor_jobs_processed_total",
			Help: "Crawl jobs processed, by job kind and outcome.",
		}, []string{"kind", "outcome"}),
		jobDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "repometeor_job_duration_seconds",
			Help:    "Crawl job handler duration in seconds, by job kind.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 15, 60, 300},
		}, []string{"kind"}),
		jobQueueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "repometeor_job_queue_depth",
			Help: "Crawl jobs currently in each status.",
		}, []string{"status"}),
		snapshotsStored: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "repometeor_snapshots_stored_total",
			Help: "Repository snapshots written (inserts and in-bucket updates).",
		}),
		scoreRunSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "repometeor_score_run_duration_seconds",
			Help:    "Duration of a full scoring run, by window.",
			Buckets: []float64{0.05, 0.1, 0.5, 1, 5, 15, 60, 300},
		}, []string{"window"}),
		scoredRepos: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "repometeor_repositories_scored",
			Help: "Repositories that received a score in the most recent run, by window.",
		}, []string{"window"}),
		discoveryFound: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "repometeor_discovery_repositories_total",
			Help: "Repositories seen during discovery, by outcome (candidate or accepted).",
		}, []string{"outcome"}),
		dbQuerySeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "repometeor_database_query_duration_seconds",
			Help:    "Database query duration in seconds, by named operation.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
		}, []string{"operation"}),
		buildInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "repometeor_build_info",
			Help: "Build information; always 1.",
		}, []string{"version"}),
	}

	registry.MustRegister(
		m.httpRequests, m.httpDuration, m.httpInFlight,
		m.githubRequests, m.githubCacheHits, m.githubRetries, m.githubRemaining,
		m.jobsProcessed, m.jobDuration, m.jobQueueDepth,
		m.snapshotsStored, m.scoreRunSeconds, m.scoredRepos, m.discoveryFound,
		m.dbQuerySeconds, m.buildInfo,
	)
	m.buildInfo.WithLabelValues(version).Set(1)

	return m
}

// Registry exposes the underlying registry so the API can mount a handler.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// ObserveHTTP records one served HTTP request. route must be a template such as
// "/v1/repositories/:owner/:repository", never a concrete path.
func (m *Metrics) ObserveHTTP(route, method string, status int, d time.Duration) {
	if m == nil {
		return
	}
	m.httpRequests.WithLabelValues(route, method, statusClass(status)).Inc()
	m.httpDuration.WithLabelValues(route, method).Observe(d.Seconds())
}

// IncInFlight and DecInFlight bracket a request being served.
func (m *Metrics) IncInFlight() {
	if m != nil {
		m.httpInFlight.Inc()
	}
}

// DecInFlight decrements the in-flight request gauge.
func (m *Metrics) DecInFlight() {
	if m != nil {
		m.httpInFlight.Dec()
	}
}

// ObserveGitHubRequest records a GitHub API call outcome.
func (m *Metrics) ObserveGitHubRequest(operation, outcome string) {
	if m != nil {
		m.githubRequests.WithLabelValues(operation, outcome).Inc()
	}
}

// IncGitHubCacheHit records a 304 Not Modified response.
func (m *Metrics) IncGitHubCacheHit() {
	if m != nil {
		m.githubCacheHits.Inc()
	}
}

// IncGitHubRetry records a retried GitHub request.
func (m *Metrics) IncGitHubRetry() {
	if m != nil {
		m.githubRetries.Inc()
	}
}

// SetGitHubRemaining publishes the remaining budget for a rate-limit resource.
func (m *Metrics) SetGitHubRemaining(resource string, remaining int) {
	if m != nil {
		m.githubRemaining.WithLabelValues(resource).Set(float64(remaining))
	}
}

// ObserveJob records a completed job attempt.
func (m *Metrics) ObserveJob(kind, outcome string, d time.Duration) {
	if m == nil {
		return
	}
	m.jobsProcessed.WithLabelValues(kind, outcome).Inc()
	m.jobDuration.WithLabelValues(kind).Observe(d.Seconds())
}

// SetJobQueueDepth publishes the number of jobs in a status.
func (m *Metrics) SetJobQueueDepth(status string, depth int) {
	if m != nil {
		m.jobQueueDepth.WithLabelValues(status).Set(float64(depth))
	}
}

// AddSnapshots records snapshot writes.
func (m *Metrics) AddSnapshots(n int) {
	if m != nil && n > 0 {
		m.snapshotsStored.Add(float64(n))
	}
}

// ObserveScoreRun records the duration and size of a scoring run.
func (m *Metrics) ObserveScoreRun(window string, repositories int, d time.Duration) {
	if m == nil {
		return
	}
	m.scoreRunSeconds.WithLabelValues(window).Observe(d.Seconds())
	m.scoredRepos.WithLabelValues(window).Set(float64(repositories))
}

// AddDiscovery records discovery candidates and acceptances.
func (m *Metrics) AddDiscovery(outcome string, n int) {
	if m != nil && n > 0 {
		m.discoveryFound.WithLabelValues(outcome).Add(float64(n))
	}
}

// ObserveDBQuery records a named database operation's duration.
func (m *Metrics) ObserveDBQuery(operation string, d time.Duration) {
	if m != nil {
		m.dbQuerySeconds.WithLabelValues(operation).Observe(d.Seconds())
	}
}

// statusClass collapses a status code to a class such as "2xx" so that the
// status label stays bounded.
func statusClass(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	case status >= 200:
		return "2xx"
	case status >= 100:
		return "1xx"
	default:
		return "unknown"
	}
}

// StatusClass is the exported form of statusClass, used by tests and handlers.
func StatusClass(status int) string { return statusClass(status) }

// StatusText renders a status code for log attributes.
func StatusText(status int) string { return strconv.Itoa(status) }

// noopOnce guards lazily created no-op metrics in tests.
var (
	noopOnce sync.Once
	noop     *Metrics
)

// Noop returns a shared metric set backed by a throwaway registry. Handlers and
// jobs can always call metric methods without nil checks in tests.
func Noop() *Metrics {
	noopOnce.Do(func() { noop = NewMetrics("test") })
	return noop
}
