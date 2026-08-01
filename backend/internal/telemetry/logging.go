// Package telemetry provides structured logging and Prometheus metrics for
// RepoMeteor services.
//
// Logging uses log/slog with a redacting handler: any attribute whose key looks
// like a credential is replaced before it reaches an output stream. That makes
// accidental secret logging a design-level impossibility rather than a review
// checklist item.
package telemetry

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"strings"
)

// Redacted is the placeholder substituted for sensitive attribute values.
const Redacted = "[REDACTED]"

// contextKey is the private key type for values carried on a context.
type contextKey int

const (
	requestIDKey contextKey = iota
	jobIDKey
)

// sensitiveKeys are attribute names whose values are always redacted. Matching
// is case-insensitive and substring-based, so "githubToken", "GITHUB_TOKEN" and
// "x-api-key" are all covered.
var sensitiveKeys = []string{
	"authorization",
	"token",
	"secret",
	"password",
	"passwd",
	"apikey",
	"api_key",
	"api-key",
	"credential",
	"cookie",
	"set-cookie",
	"private_key",
	"access_key",
}

// urlKeys are attribute names whose values are URLs that may embed credentials.
var urlKeys = []string{
	"database_url",
	"databaseurl",
	"dsn",
	"connection_string",
	"connectionstring",
}

// NewLogger builds the standard RepoMeteor logger: JSON output, the configured
// minimum level, and a fixed "service" attribute on every record.
func NewLogger(w io.Writer, level string, service string) *slog.Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: ParseLevel(level),
	})
	return slog.New(&redactingHandler{inner: handler}).With(slog.String("service", service))
}

// ParseLevel maps a configuration string to an slog level. Unknown values fall
// back to info rather than failing, because configuration validation has
// already rejected genuinely invalid values.
func ParseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// redactingHandler wraps another handler and scrubs sensitive attributes.
type redactingHandler struct {
	inner slog.Handler
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	cloned := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)

	// Correlation identifiers travel on the context so that call sites do not
	// have to thread them through every function signature.
	if id := RequestIDFrom(ctx); id != "" {
		cloned.AddAttrs(slog.String("requestId", id))
	}
	if id := JobIDFrom(ctx); id != "" {
		cloned.AddAttrs(slog.String("jobId", id))
	}

	record.Attrs(func(a slog.Attr) bool {
		cloned.AddAttrs(redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, cloned)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	safe := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		safe = append(safe, redactAttr(a))
	}
	return &redactingHandler{inner: h.inner.WithAttrs(safe)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{inner: h.inner.WithGroup(name)}
}

// redactAttr rewrites a single attribute, recursing into groups.
func redactAttr(a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		safe := make([]any, 0, len(group))
		for _, member := range group {
			safe = append(safe, redactAttr(member))
		}
		return slog.Group(a.Key, safe...)
	}

	key := strings.ToLower(a.Key)
	for _, sensitive := range sensitiveKeys {
		if strings.Contains(key, sensitive) {
			return slog.String(a.Key, Redacted)
		}
	}
	for _, urlKey := range urlKeys {
		if strings.Contains(key, urlKey) {
			return slog.String(a.Key, RedactURL(a.Value.String()))
		}
	}
	// A value that is itself a URL with embedded credentials is redacted
	// regardless of its key.
	if a.Value.Kind() == slog.KindString {
		if s := a.Value.String(); looksLikeCredentialedURL(s) {
			return slog.String(a.Key, RedactURL(s))
		}
	}
	return a
}

func looksLikeCredentialedURL(s string) bool {
	if !strings.Contains(s, "://") || !strings.Contains(s, "@") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.User == nil {
		return false
	}
	_, hasPassword := u.User.Password()
	return hasPassword
}

// RedactURL replaces the password component of a URL. Unparseable input is
// reported opaquely rather than echoed, since it may still hold a secret.
func RedactURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "[UNPARSEABLE URL]"
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
		}
	}
	return u.String()
}

// WithRequestID returns a context carrying an HTTP request correlation ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFrom returns the request ID on ctx, or "".
func RequestIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// WithJobID returns a context carrying a worker job correlation ID.
func WithJobID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, jobIDKey, id)
}

// JobIDFrom returns the job ID on ctx, or "".
func JobIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(jobIDKey).(string)
	return id
}

// Discard returns a logger that writes nowhere. Useful in tests.
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}
