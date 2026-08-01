package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func decode(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("no log output")
	}
	// Take the last record if several were written.
	if idx := strings.LastIndex(line, "\n"); idx >= 0 {
		line = line[idx+1:]
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(line), &out); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, line)
	}
	return out
}

func TestLoggerRedactsSensitiveAttributes(t *testing.T) {
	sensitive := map[string]string{
		"token":         "ghp_realtoken",
		"github_token":  "ghp_realtoken",
		"githubToken":   "ghp_realtoken",
		"Authorization": "Bearer ghp_realtoken",
		"apiKey":        "sk-secret",
		"password":      "hunter2",
		"Cookie":        "session=abc",
	}

	for key, value := range sensitive {
		t.Run(key, func(t *testing.T) {
			var buf bytes.Buffer
			log := NewLogger(&buf, "info", "test")
			log.Info("attempt", slog.String(key, value))

			out := decode(t, &buf)
			if out[key] != Redacted {
				t.Fatalf("%s = %v, want %s", key, out[key], Redacted)
			}
			if strings.Contains(buf.String(), value) {
				t.Fatalf("log leaked the value %q: %s", value, buf.String())
			}
		})
	}
}

func TestLoggerRedactsDatabaseURLPassword(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, "info", "test")
	log.Info("connecting", slog.String("database_url", "postgres://user:dbsecret@host:5432/db"))

	if strings.Contains(buf.String(), "dbsecret") {
		t.Fatalf("log leaked the database password: %s", buf.String())
	}
	out := decode(t, &buf)
	if got, want := out["database_url"], "postgres://user:xxxxx@host:5432/db"; got != want {
		t.Fatalf("database_url = %v, want %v", got, want)
	}
}

func TestLoggerRedactsCredentialedURLUnderAnyKey(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, "info", "test")
	log.Info("target", slog.String("upstream", "https://user:leaked@internal.example/x"))

	if strings.Contains(buf.String(), "leaked") {
		t.Fatalf("log leaked credentials in a URL value: %s", buf.String())
	}
}

func TestLoggerLeavesOrdinaryURLsIntact(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, "info", "test")
	log.Info("fetch", slog.String("url", "https://api.github.com/repos/orbit-labs/signal-kit"))

	out := decode(t, &buf)
	if got, want := out["url"], "https://api.github.com/repos/orbit-labs/signal-kit"; got != want {
		t.Fatalf("url = %v, want %v", got, want)
	}
}

func TestLoggerRedactsInsideGroups(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, "info", "test")
	log.Info("request", slog.Group("headers", slog.String("authorization", "Bearer ghp_x"), slog.String("accept", "application/json")))

	if strings.Contains(buf.String(), "ghp_x") {
		t.Fatalf("log leaked a grouped secret: %s", buf.String())
	}
	out := decode(t, &buf)
	headers, ok := out["headers"].(map[string]any)
	if !ok {
		t.Fatalf("headers group missing: %v", out)
	}
	if headers["authorization"] != Redacted {
		t.Fatalf("authorization = %v, want %s", headers["authorization"], Redacted)
	}
	if headers["accept"] != "application/json" {
		t.Fatalf("accept = %v, want application/json", headers["accept"])
	}
}

func TestLoggerRedactsAttrsAttachedWithWith(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, "info", "test").With(slog.String("token", "ghp_persisted"))
	log.Info("hello")

	if strings.Contains(buf.String(), "ghp_persisted") {
		t.Fatalf("log leaked a With() secret: %s", buf.String())
	}
}

func TestLoggerAddsServiceAttribute(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, "info", "repometeor-api")
	log.Info("started")

	if got := decode(t, &buf)["service"]; got != "repometeor-api" {
		t.Fatalf("service = %v, want repometeor-api", got)
	}
}

func TestLoggerIncludesCorrelationIDsFromContext(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, "info", "test")

	ctx := WithRequestID(context.Background(), "req-123")
	ctx = WithJobID(ctx, "job-456")
	log.InfoContext(ctx, "handled")

	out := decode(t, &buf)
	if out["requestId"] != "req-123" {
		t.Errorf("requestId = %v, want req-123", out["requestId"])
	}
	if out["jobId"] != "job-456" {
		t.Errorf("jobId = %v, want job-456", out["jobId"])
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, "warn", "test")

	log.Debug("debug message")
	log.Info("info message")
	if buf.Len() != 0 {
		t.Fatalf("expected no output below warn, got: %s", buf.String())
	}

	log.Warn("warn message")
	if buf.Len() == 0 {
		t.Fatal("expected warn output")
	}
}

func TestParseLevel(t *testing.T) {
	tests := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		"info":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"":        slog.LevelInfo,
		"unknown": slog.LevelInfo,
	}
	for in, want := range tests {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestRedactURL(t *testing.T) {
	tests := map[string]string{
		"postgres://u:p@h:5432/db": "postgres://u:xxxxx@h:5432/db",
		"postgres://u@h:5432/db":   "postgres://u@h:5432/db",
		"https://example.com/x":    "https://example.com/x",
		"":                         "",
	}
	for in, want := range tests {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
	if got := RedactURL("://bad url with spaces\x7f"); !strings.Contains(got, "UNPARSEABLE") {
		t.Errorf("RedactURL(bad) = %q, want an opaque marker", got)
	}
}

func TestContextHelpersOnEmptyContext(t *testing.T) {
	if got := RequestIDFrom(context.Background()); got != "" {
		t.Errorf("RequestIDFrom(empty) = %q, want empty", got)
	}
	//nolint:staticcheck // deliberately exercising the nil-context guard
	if got := JobIDFrom(nil); got != "" {
		t.Errorf("JobIDFrom(nil) = %q, want empty", got)
	}
}
