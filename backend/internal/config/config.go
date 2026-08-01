// Package config loads and validates RepoMeteor's runtime configuration.
//
// Configuration comes exclusively from environment variables. Every value is
// parsed into a typed field and validated at startup, so a misconfigured
// deployment fails immediately with an actionable message instead of
// misbehaving later. Secrets are never printed by String or Redacted.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Environment names the deployment environment.
type Environment string

const (
	EnvDevelopment Environment = "development"
	EnvStaging     Environment = "staging"
	EnvProduction  Environment = "production"
)

// IsProduction reports whether stricter production behaviour should apply.
func (e Environment) IsProduction() bool { return e == EnvProduction }

// Config is the fully validated application configuration.
type Config struct {
	App      AppConfig
	Database DatabaseConfig
	API      APIConfig
	GitHub   GitHubConfig
	Worker   WorkerConfig
	Tracking TrackingConfig
	Limits   LimitsConfig
	Metrics  MetricsConfig
}

// AppConfig holds process-wide settings.
type AppConfig struct {
	Environment Environment
	LogLevel    string
	DemoMode    bool
}

// DatabaseConfig holds PostgreSQL connection settings.
type DatabaseConfig struct {
	URL            string
	MaxConnections int32
	MinConnections int32
	ConnectTimeout time.Duration
	QueryTimeout   time.Duration
}

// APIConfig holds HTTP server settings for repometeor-api.
type APIConfig struct {
	Host              string
	Port              int
	PublicURL         string
	WebPublicURL      string
	AllowedOrigins    []string
	TrustedProxyCIDRs []*net.IPNet
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxRequestBytes   int64
}

// Address returns the host:port the API server binds to.
func (a APIConfig) Address() string {
	return net.JoinHostPort(a.Host, strconv.Itoa(a.Port))
}

// GitHubConfig holds GitHub REST client settings.
type GitHubConfig struct {
	Token             string
	BaseURL           string
	APIVersion        string
	UserAgent         string
	RequestTimeout    time.Duration
	MaxRetries        int
	Concurrency       int
	RateBudgetPercent int
	MaxResponseBytes  int64
}

// HasToken reports whether an API token is configured.
func (g GitHubConfig) HasToken() bool { return g.Token != "" }

// WorkerConfig holds repometeor-worker scheduling settings.
type WorkerConfig struct {
	DiscoveryEnabled         bool
	DiscoveryInterval        time.Duration
	DiscoveryCreatedLookback time.Duration
	DiscoveryPushedLookback  time.Duration
	ScoringInterval          time.Duration
	JobPollInterval          time.Duration
	JobBatchSize             int
	StaleJobTimeout          time.Duration
	ShutdownTimeout          time.Duration
}

// TrackingConfig holds tier sizing and polling cadence.
type TrackingConfig struct {
	HotLimit               int
	HotPollInterval        time.Duration
	WarmLimit              int
	WarmPollInterval       time.Duration
	ColdPollInterval       time.Duration
	SnapshotBucketInterval time.Duration
}

// LimitsConfig holds public request limits.
type LimitsConfig struct {
	PublicTrackingEnabled     bool
	PublicTrackingPerHour     int
	APIReadRateLimitPerMinute int
}

// MetricsConfig controls the Prometheus endpoint.
type MetricsConfig struct {
	Enabled bool
}

// Getenv resolves an environment variable. It exists so tests can supply a map
// instead of mutating the process environment.
type Getenv func(string) string

// Load reads configuration from the process environment.
func Load() (*Config, error) { return LoadFrom(os.Getenv) }

// LoadFromMap reads configuration from an explicit map. Missing keys fall back
// to defaults exactly as they would with an unset environment variable.
func LoadFromMap(env map[string]string) (*Config, error) {
	return LoadFrom(func(key string) string { return env[key] })
}

// LoadFrom reads configuration using the supplied lookup function. All
// validation problems are collected and reported together so an operator can
// fix an entire misconfiguration in one pass.
func LoadFrom(getenv Getenv) (*Config, error) {
	l := &loader{getenv: getenv}

	cfg := &Config{
		App: AppConfig{
			Environment: Environment(l.enum("APP_ENV", "development", "development", "staging", "production")),
			LogLevel:    l.enum("LOG_LEVEL", "info", "debug", "info", "warn", "error"),
			DemoMode:    l.boolean("DEMO_MODE", true),
		},
		Database: DatabaseConfig{
			URL:            l.databaseURL("DATABASE_URL", "postgres://repometeor:repometeor@localhost:5432/repometeor?sslmode=disable"),
			MaxConnections: int32(l.intRange("DATABASE_MAX_CONNECTIONS", 10, 1, 500)),
			MinConnections: int32(l.intRange("DATABASE_MIN_CONNECTIONS", 1, 0, 500)),
			ConnectTimeout: l.duration("DATABASE_CONNECT_TIMEOUT", 10*time.Second, time.Second, 2*time.Minute),
			QueryTimeout:   l.duration("DATABASE_QUERY_TIMEOUT", 8*time.Second, 100*time.Millisecond, 2*time.Minute),
		},
		API: APIConfig{
			Host:              l.str("API_HOST", "0.0.0.0"),
			Port:              l.intRange("API_PORT", 8080, 1, 65535),
			PublicURL:         l.publicURL("API_PUBLIC_URL", "http://localhost:8080"),
			WebPublicURL:      l.publicURL("WEB_PUBLIC_URL", "http://localhost:3000"),
			AllowedOrigins:    l.origins("CORS_ALLOWED_ORIGINS", "http://localhost:3000"),
			TrustedProxyCIDRs: l.cidrs("TRUSTED_PROXY_CIDRS"),
			ReadHeaderTimeout: l.duration("API_READ_HEADER_TIMEOUT", 5*time.Second, time.Second, time.Minute),
			ReadTimeout:       l.duration("API_READ_TIMEOUT", 15*time.Second, time.Second, 5*time.Minute),
			WriteTimeout:      l.duration("API_WRITE_TIMEOUT", 30*time.Second, time.Second, 5*time.Minute),
			IdleTimeout:       l.duration("API_IDLE_TIMEOUT", 60*time.Second, time.Second, 30*time.Minute),
			ShutdownTimeout:   l.duration("API_SHUTDOWN_TIMEOUT", 15*time.Second, time.Second, 5*time.Minute),
			MaxRequestBytes:   int64(l.intRange("API_MAX_REQUEST_BYTES", 65536, 1024, 10*1024*1024)),
		},
		GitHub: GitHubConfig{
			Token:             strings.TrimSpace(l.getenv("GITHUB_TOKEN")),
			BaseURL:           l.publicURL("GITHUB_API_BASE_URL", "https://api.github.com"),
			APIVersion:        l.str("GITHUB_API_VERSION", "2022-11-28"),
			UserAgent:         l.str("GITHUB_USER_AGENT", "RepoMeteor/0.1"),
			RequestTimeout:    l.duration("GITHUB_REQUEST_TIMEOUT", 15*time.Second, time.Second, 2*time.Minute),
			MaxRetries:        l.intRange("GITHUB_MAX_RETRIES", 4, 0, 10),
			Concurrency:       l.intRange("GITHUB_REQUEST_CONCURRENCY", 1, 1, 16),
			RateBudgetPercent: l.intRange("GITHUB_RATE_BUDGET_PERCENT", 60, 1, 100),
			MaxResponseBytes:  int64(l.intRange("GITHUB_MAX_RESPONSE_BYTES", 4*1024*1024, 64*1024, 64*1024*1024)),
		},
		Worker: WorkerConfig{
			DiscoveryEnabled:         l.boolean("DISCOVERY_ENABLED", true),
			DiscoveryInterval:        l.duration("DISCOVERY_INTERVAL", 6*time.Hour, time.Minute, 7*24*time.Hour),
			DiscoveryCreatedLookback: l.duration("DISCOVERY_CREATED_LOOKBACK", 30*24*time.Hour, time.Hour, 365*24*time.Hour),
			DiscoveryPushedLookback:  l.duration("DISCOVERY_PUSHED_LOOKBACK", 7*24*time.Hour, time.Hour, 365*24*time.Hour),
			ScoringInterval:          l.duration("SCORING_INTERVAL", 15*time.Minute, time.Minute, 24*time.Hour),
			JobPollInterval:          l.duration("JOB_POLL_INTERVAL", 2*time.Second, 100*time.Millisecond, 5*time.Minute),
			JobBatchSize:             l.intRange("JOB_BATCH_SIZE", 10, 1, 200),
			StaleJobTimeout:          l.duration("STALE_JOB_TIMEOUT", 15*time.Minute, time.Minute, 24*time.Hour),
			ShutdownTimeout:          l.duration("WORKER_SHUTDOWN_TIMEOUT", 30*time.Second, time.Second, 10*time.Minute),
		},
		Tracking: TrackingConfig{
			HotLimit:               l.intRange("HOT_REPOSITORY_LIMIT", 500, 0, 100000),
			HotPollInterval:        l.duration("HOT_POLL_INTERVAL", time.Hour, time.Minute, 24*time.Hour),
			WarmLimit:              l.intRange("WARM_REPOSITORY_LIMIT", 2500, 0, 1000000),
			WarmPollInterval:       l.duration("WARM_POLL_INTERVAL", 4*time.Hour, time.Minute, 7*24*time.Hour),
			ColdPollInterval:       l.duration("COLD_POLL_INTERVAL", 12*time.Hour, time.Minute, 30*24*time.Hour),
			SnapshotBucketInterval: l.duration("SNAPSHOT_BUCKET_INTERVAL", time.Hour, time.Minute, 24*time.Hour),
		},
		Limits: LimitsConfig{
			PublicTrackingEnabled:     l.boolean("PUBLIC_TRACKING_ENABLED", true),
			PublicTrackingPerHour:     l.intRange("PUBLIC_TRACKING_RATE_LIMIT_PER_HOUR", 10, 0, 100000),
			APIReadRateLimitPerMinute: l.intRange("API_READ_RATE_LIMIT_PER_MINUTE", 120, 0, 1000000),
		},
		Metrics: MetricsConfig{
			Enabled: l.boolean("METRICS_ENABLED", true),
		},
	}

	l.crossValidate(cfg)

	if err := l.err(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Redacted returns a copy safe to log: the GitHub token is replaced with a
// fixed marker and the database URL's password is stripped.
func (c *Config) Redacted() map[string]any {
	return map[string]any{
		"appEnv":            string(c.App.Environment),
		"logLevel":          c.App.LogLevel,
		"demoMode":          c.App.DemoMode,
		"databaseURL":       RedactDatabaseURL(c.Database.URL),
		"apiAddress":        c.API.Address(),
		"apiPublicURL":      c.API.PublicURL,
		"webPublicURL":      c.API.WebPublicURL,
		"corsOrigins":       c.API.AllowedOrigins,
		"trustedProxies":    len(c.API.TrustedProxyCIDRs),
		"githubTokenSet":    c.GitHub.HasToken(),
		"githubBaseURL":     c.GitHub.BaseURL,
		"githubAPIVersion":  c.GitHub.APIVersion,
		"discoveryEnabled":  c.Worker.DiscoveryEnabled,
		"scoringInterval":   c.Worker.ScoringInterval.String(),
		"metricsEnabled":    c.Metrics.Enabled,
		"publicTracking":    c.Limits.PublicTrackingEnabled,
		"snapshotBucket":    c.Tracking.SnapshotBucketInterval.String(),
		"rateBudgetPercent": c.GitHub.RateBudgetPercent,
	}
}

// RedactDatabaseURL removes the password from a PostgreSQL connection string.
// A URL that cannot be parsed is reported as an opaque marker rather than
// echoed back, because an unparseable value may still contain a credential.
func RedactDatabaseURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		if err != nil {
			return "(unparseable database url)"
		}
		return raw
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		u.User = url.UserPassword(u.User.Username(), "xxxxx")
	}
	return u.String()
}

// --- loader -----------------------------------------------------------------

type loader struct {
	getenv   Getenv
	problems []string
}

func (l *loader) add(key, detail string) {
	l.problems = append(l.problems, fmt.Sprintf("%s: %s", key, detail))
}

func (l *loader) err() error {
	if len(l.problems) == 0 {
		return nil
	}
	sort.Strings(l.problems)
	return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(l.problems, "\n  - "))
}

func (l *loader) raw(key string) (string, bool) {
	v := strings.TrimSpace(l.getenv(key))
	return v, v != ""
}

func (l *loader) str(key, def string) string {
	if v, ok := l.raw(key); ok {
		return v
	}
	return def
}

func (l *loader) boolean(key string, def bool) bool {
	v, ok := l.raw(key)
	if !ok {
		return def
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		l.add(key, fmt.Sprintf("%q is not a boolean (use true or false)", v))
		return def
	}
	return parsed
}

func (l *loader) intRange(key string, def, min, max int) int {
	v, ok := l.raw(key)
	if !ok {
		return def
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		l.add(key, fmt.Sprintf("%q is not an integer", v))
		return def
	}
	if parsed < min || parsed > max {
		l.add(key, fmt.Sprintf("%d is out of range (expected %d..%d)", parsed, min, max))
		return def
	}
	return parsed
}

func (l *loader) duration(key string, def, min, max time.Duration) time.Duration {
	v, ok := l.raw(key)
	if !ok {
		return def
	}
	parsed, err := time.ParseDuration(v)
	if err != nil {
		l.add(key, fmt.Sprintf("%q is not a duration (examples: 30s, 15m, 6h)", v))
		return def
	}
	if parsed < min || parsed > max {
		l.add(key, fmt.Sprintf("%s is out of range (expected %s..%s)", parsed, min, max))
		return def
	}
	return parsed
}

func (l *loader) enum(key, def string, allowed ...string) string {
	v, ok := l.raw(key)
	if !ok {
		return def
	}
	lower := strings.ToLower(v)
	for _, a := range allowed {
		if lower == a {
			return lower
		}
	}
	l.add(key, fmt.Sprintf("%q is not one of: %s", v, strings.Join(allowed, ", ")))
	return def
}

func (l *loader) databaseURL(key, def string) string {
	v, ok := l.raw(key)
	if !ok {
		return def
	}
	u, err := url.Parse(v)
	if err != nil {
		// Do not echo the value: it may contain a password.
		l.add(key, "is not a valid URL")
		return def
	}
	switch u.Scheme {
	case "postgres", "postgresql":
	default:
		l.add(key, fmt.Sprintf("scheme %q is not supported (use postgres:// or postgresql://)", u.Scheme))
		return def
	}
	if u.Host == "" {
		l.add(key, "is missing a host")
		return def
	}
	if strings.Trim(u.Path, "/") == "" {
		l.add(key, "is missing a database name")
		return def
	}
	return v
}

// publicURL validates an absolute http(s) base URL and strips any trailing
// slash so that path joining is unambiguous everywhere else.
func (l *loader) publicURL(key, def string) string {
	v, ok := l.raw(key)
	if !ok {
		return strings.TrimRight(def, "/")
	}
	u, err := url.Parse(v)
	if err != nil {
		l.add(key, fmt.Sprintf("%q is not a valid URL", v))
		return strings.TrimRight(def, "/")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		l.add(key, fmt.Sprintf("%q must use http:// or https://", v))
		return strings.TrimRight(def, "/")
	}
	if u.Host == "" {
		l.add(key, fmt.Sprintf("%q is missing a host", v))
		return strings.TrimRight(def, "/")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		l.add(key, fmt.Sprintf("%q must not contain a query string or fragment", v))
		return strings.TrimRight(def, "/")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return strings.TrimRight(u.String(), "/")
}

// origins parses a comma-separated CORS allowlist. Only exact scheme://host[:port]
// entries are accepted; "*" is deliberately not supported because the API is
// read-mostly but still rate limited per client.
func (l *loader) origins(key, def string) []string {
	v, ok := l.raw(key)
	if !ok {
		v = def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == "*" {
			l.add(key, `"*" is not supported; list explicit origins`)
			continue
		}
		u, err := url.Parse(p)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			l.add(key, fmt.Sprintf("%q is not a valid origin (expected scheme://host[:port])", p))
			continue
		}
		out = append(out, strings.ToLower(u.Scheme+"://"+u.Host))
	}
	return out
}

func (l *loader) cidrs(key string) []*net.IPNet {
	v, ok := l.raw(key)
	if !ok {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]*net.IPNet, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		_, network, err := net.ParseCIDR(p)
		if err != nil {
			l.add(key, fmt.Sprintf("%q is not a valid CIDR (example: 10.0.0.0/8)", p))
			continue
		}
		out = append(out, network)
	}
	return out
}

// crossValidate checks invariants that span more than one field.
func (l *loader) crossValidate(c *Config) {
	if !c.App.DemoMode && !c.GitHub.HasToken() {
		l.add("GITHUB_TOKEN", "is required when DEMO_MODE=false (live mode cannot collect data without a token)")
	}
	if c.Database.MinConnections > c.Database.MaxConnections {
		l.add("DATABASE_MIN_CONNECTIONS", fmt.Sprintf("%d exceeds DATABASE_MAX_CONNECTIONS (%d)", c.Database.MinConnections, c.Database.MaxConnections))
	}
	if c.Tracking.HotPollInterval > c.Tracking.WarmPollInterval {
		l.add("HOT_POLL_INTERVAL", "must not be longer than WARM_POLL_INTERVAL (hot repositories are polled most often)")
	}
	if c.Tracking.WarmPollInterval > c.Tracking.ColdPollInterval {
		l.add("WARM_POLL_INTERVAL", "must not be longer than COLD_POLL_INTERVAL")
	}
	if c.App.Environment.IsProduction() {
		if strings.HasPrefix(c.API.PublicURL, "http://") && !isLoopbackURL(c.API.PublicURL) {
			l.add("API_PUBLIC_URL", "must use https:// in production")
		}
		if strings.HasPrefix(c.API.WebPublicURL, "http://") && !isLoopbackURL(c.API.WebPublicURL) {
			l.add("WEB_PUBLIC_URL", "must use https:// in production")
		}
		if c.App.DemoMode {
			l.add("DEMO_MODE", "must be false in production (demo data would be served to real users)")
		}
	}
}

func isLoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ErrNoConfig is returned by helpers that require configuration that is absent.
var ErrNoConfig = errors.New("configuration not loaded")
