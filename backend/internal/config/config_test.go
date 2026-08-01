package config

import (
	"net"
	"strings"
	"testing"
	"time"
)

func baseEnv() map[string]string {
	return map[string]string{
		"DEMO_MODE":    "true",
		"DATABASE_URL": "postgres://u:p@db:5432/repometeor?sslmode=disable",
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFromMap(baseEnv())
	if err != nil {
		t.Fatalf("LoadFromMap() error = %v", err)
	}

	if cfg.App.Environment != EnvDevelopment {
		t.Errorf("Environment = %q, want development", cfg.App.Environment)
	}
	if !cfg.App.DemoMode {
		t.Error("DemoMode = false, want true")
	}
	if cfg.API.Port != 8080 {
		t.Errorf("API.Port = %d, want 8080", cfg.API.Port)
	}
	if cfg.API.Address() != "0.0.0.0:8080" {
		t.Errorf("API.Address() = %q, want 0.0.0.0:8080", cfg.API.Address())
	}
	if cfg.GitHub.RateBudgetPercent != 60 {
		t.Errorf("RateBudgetPercent = %d, want 60", cfg.GitHub.RateBudgetPercent)
	}
	if cfg.Tracking.SnapshotBucketInterval != time.Hour {
		t.Errorf("SnapshotBucketInterval = %v, want 1h", cfg.Tracking.SnapshotBucketInterval)
	}
	if got, want := cfg.Worker.ScoringInterval, 15*time.Minute; got != want {
		t.Errorf("ScoringInterval = %v, want %v", got, want)
	}
}

func TestLiveModeRequiresToken(t *testing.T) {
	env := baseEnv()
	env["DEMO_MODE"] = "false"

	_, err := LoadFromMap(env)
	if err == nil {
		t.Fatal("expected an error when DEMO_MODE=false without GITHUB_TOKEN")
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Fatalf("error = %v, want it to mention GITHUB_TOKEN", err)
	}

	env["GITHUB_TOKEN"] = "ghp_example"
	cfg, err := LoadFromMap(env)
	if err != nil {
		t.Fatalf("LoadFromMap() with token error = %v", err)
	}
	if !cfg.GitHub.HasToken() {
		t.Error("HasToken() = false, want true")
	}
}

func TestInvalidDurationIsRejected(t *testing.T) {
	env := baseEnv()
	env["SCORING_INTERVAL"] = "fifteen minutes"

	_, err := LoadFromMap(env)
	if err == nil {
		t.Fatal("expected an error for an unparseable duration")
	}
	if !strings.Contains(err.Error(), "SCORING_INTERVAL") {
		t.Fatalf("error = %v, want it to mention SCORING_INTERVAL", err)
	}
}

func TestDurationOutOfRangeIsRejected(t *testing.T) {
	env := baseEnv()
	env["GITHUB_REQUEST_TIMEOUT"] = "10ms"

	_, err := LoadFromMap(env)
	if err == nil || !strings.Contains(err.Error(), "GITHUB_REQUEST_TIMEOUT") {
		t.Fatalf("error = %v, want a GITHUB_REQUEST_TIMEOUT range error", err)
	}
}

func TestPercentageOutOfRangeIsRejected(t *testing.T) {
	for _, v := range []string{"0", "101", "-5"} {
		env := baseEnv()
		env["GITHUB_RATE_BUDGET_PERCENT"] = v

		_, err := LoadFromMap(env)
		if err == nil || !strings.Contains(err.Error(), "GITHUB_RATE_BUDGET_PERCENT") {
			t.Fatalf("value %q: error = %v, want a range error", v, err)
		}
	}
}

func TestInvalidPublicURLIsRejected(t *testing.T) {
	cases := map[string]string{
		"missing scheme": "localhost:8080",
		"ftp scheme":     "ftp://example.com",
		"with query":     "https://example.com?x=1",
		"no host":        "https://",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			env := baseEnv()
			env["API_PUBLIC_URL"] = value

			_, err := LoadFromMap(env)
			if err == nil || !strings.Contains(err.Error(), "API_PUBLIC_URL") {
				t.Fatalf("error = %v, want an API_PUBLIC_URL error", err)
			}
		})
	}
}

func TestPublicURLTrailingSlashIsStripped(t *testing.T) {
	env := baseEnv()
	env["API_PUBLIC_URL"] = "https://api.example.com/"

	cfg, err := LoadFromMap(env)
	if err != nil {
		t.Fatalf("LoadFromMap() error = %v", err)
	}
	if cfg.API.PublicURL != "https://api.example.com" {
		t.Fatalf("PublicURL = %q, want https://api.example.com", cfg.API.PublicURL)
	}
}

func TestDatabaseURLValidation(t *testing.T) {
	cases := map[string]string{
		"wrong scheme":     "mysql://u:p@db:3306/repometeor",
		"missing host":     "postgres:///repometeor",
		"missing database": "postgres://u:p@db:5432/",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			env := baseEnv()
			env["DATABASE_URL"] = value

			_, err := LoadFromMap(env)
			if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
				t.Fatalf("error = %v, want a DATABASE_URL error", err)
			}
		})
	}
}

func TestDatabaseURLErrorDoesNotLeakValue(t *testing.T) {
	env := baseEnv()
	env["DATABASE_URL"] = "postgres://user:sup3rsecret@db:5432/x\x7f"

	_, err := LoadFromMap(env)
	if err == nil {
		t.Fatal("expected an error for an unparseable DATABASE_URL")
	}
	if strings.Contains(err.Error(), "sup3rsecret") {
		t.Fatalf("error leaked the password: %v", err)
	}
}

func TestOriginsParsing(t *testing.T) {
	env := baseEnv()
	env["CORS_ALLOWED_ORIGINS"] = "http://localhost:3000, https://Example.COM ,"

	cfg, err := LoadFromMap(env)
	if err != nil {
		t.Fatalf("LoadFromMap() error = %v", err)
	}
	want := []string{"http://localhost:3000", "https://example.com"}
	if len(cfg.API.AllowedOrigins) != len(want) {
		t.Fatalf("AllowedOrigins = %v, want %v", cfg.API.AllowedOrigins, want)
	}
	for i := range want {
		if cfg.API.AllowedOrigins[i] != want[i] {
			t.Fatalf("AllowedOrigins[%d] = %q, want %q", i, cfg.API.AllowedOrigins[i], want[i])
		}
	}
}

func TestWildcardOriginIsRejected(t *testing.T) {
	env := baseEnv()
	env["CORS_ALLOWED_ORIGINS"] = "*"

	_, err := LoadFromMap(env)
	if err == nil || !strings.Contains(err.Error(), "CORS_ALLOWED_ORIGINS") {
		t.Fatalf("error = %v, want a CORS_ALLOWED_ORIGINS error", err)
	}
}

func TestTrustedProxyCIDRs(t *testing.T) {
	env := baseEnv()
	env["TRUSTED_PROXY_CIDRS"] = "10.0.0.0/8, 192.168.1.0/24"

	cfg, err := LoadFromMap(env)
	if err != nil {
		t.Fatalf("LoadFromMap() error = %v", err)
	}
	if len(cfg.API.TrustedProxyCIDRs) != 2 {
		t.Fatalf("got %d CIDRs, want 2", len(cfg.API.TrustedProxyCIDRs))
	}
	if !cfg.API.TrustedProxyCIDRs[0].Contains(net.ParseIP("10.1.2.3")) {
		t.Error("10.0.0.0/8 should contain 10.1.2.3")
	}

	env["TRUSTED_PROXY_CIDRS"] = "not-a-cidr"
	if _, err := LoadFromMap(env); err == nil {
		t.Fatal("expected an error for an invalid CIDR")
	}
}

func TestProductionRejectsInsecureAndDemoSettings(t *testing.T) {
	env := baseEnv()
	env["APP_ENV"] = "production"
	env["GITHUB_TOKEN"] = "ghp_example"
	env["API_PUBLIC_URL"] = "http://api.example.com"
	env["WEB_PUBLIC_URL"] = "http://example.com"

	_, err := LoadFromMap(env)
	if err == nil {
		t.Fatal("expected production validation errors")
	}
	for _, want := range []string{"API_PUBLIC_URL", "WEB_PUBLIC_URL", "DEMO_MODE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %s", err, want)
		}
	}
}

func TestProductionAllowsLoopbackHTTP(t *testing.T) {
	env := baseEnv()
	env["APP_ENV"] = "production"
	env["DEMO_MODE"] = "false"
	env["GITHUB_TOKEN"] = "ghp_example"
	env["API_PUBLIC_URL"] = "http://localhost:8080"
	env["WEB_PUBLIC_URL"] = "http://127.0.0.1:3000"

	if _, err := LoadFromMap(env); err != nil {
		t.Fatalf("LoadFromMap() error = %v, want loopback http to be allowed", err)
	}
}

func TestPollIntervalOrderingIsEnforced(t *testing.T) {
	env := baseEnv()
	env["HOT_POLL_INTERVAL"] = "8h"
	env["WARM_POLL_INTERVAL"] = "4h"

	_, err := LoadFromMap(env)
	if err == nil || !strings.Contains(err.Error(), "HOT_POLL_INTERVAL") {
		t.Fatalf("error = %v, want a HOT_POLL_INTERVAL ordering error", err)
	}
}

func TestConnectionBoundsOrdering(t *testing.T) {
	env := baseEnv()
	env["DATABASE_MIN_CONNECTIONS"] = "20"
	env["DATABASE_MAX_CONNECTIONS"] = "10"

	_, err := LoadFromMap(env)
	if err == nil || !strings.Contains(err.Error(), "DATABASE_MIN_CONNECTIONS") {
		t.Fatalf("error = %v, want a DATABASE_MIN_CONNECTIONS error", err)
	}
}

func TestAllProblemsAreReportedTogether(t *testing.T) {
	env := baseEnv()
	env["API_PORT"] = "0"
	env["LOG_LEVEL"] = "verbose"
	env["METRICS_ENABLED"] = "maybe"

	_, err := LoadFromMap(env)
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"API_PORT", "LOG_LEVEL", "METRICS_ENABLED"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %s", err, want)
		}
	}
}

func TestRedactDatabaseURL(t *testing.T) {
	tests := map[string]struct{ in, want string }{
		"with password": {
			in:   "postgres://user:secret@host:5432/db?sslmode=disable",
			want: "postgres://user:xxxxx@host:5432/db?sslmode=disable",
		},
		"without password": {
			in:   "postgres://user@host:5432/db",
			want: "postgres://user@host:5432/db",
		},
		"no user info": {
			in:   "postgres://host:5432/db",
			want: "postgres://host:5432/db",
		},
		"empty": {in: "", want: ""},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := RedactDatabaseURL(tc.in); got != tc.want {
				t.Fatalf("RedactDatabaseURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedactedOmitsSecrets(t *testing.T) {
	env := baseEnv()
	env["DEMO_MODE"] = "false"
	env["GITHUB_TOKEN"] = "ghp_supersecrettoken"
	env["DATABASE_URL"] = "postgres://user:dbsecret@host:5432/db"

	cfg, err := LoadFromMap(env)
	if err != nil {
		t.Fatalf("LoadFromMap() error = %v", err)
	}

	redacted := cfg.Redacted()
	rendered := strings.ToLower(strings.TrimSpace(joinValues(redacted)))
	for _, secret := range []string{"ghp_supersecrettoken", "dbsecret"} {
		if strings.Contains(rendered, secret) {
			t.Fatalf("Redacted() leaked %q: %v", secret, redacted)
		}
	}
	if redacted["githubTokenSet"] != true {
		t.Error("githubTokenSet should be true")
	}
}

func joinValues(m map[string]any) string {
	var b strings.Builder
	for k, v := range m {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(strings.TrimSpace(stringify(v)))
		b.WriteString(" ")
	}
	return b.String()
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []string:
		return strings.Join(t, ",")
	default:
		return ""
	}
}
