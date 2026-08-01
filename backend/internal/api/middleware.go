package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/repometeor/repometeor/backend/internal/clock"
	"github.com/repometeor/repometeor/backend/internal/config"
	"github.com/repometeor/repometeor/backend/internal/telemetry"
)

// requestIDKey is the gin context key for the request correlation ID.
const requestIDKey = "repometeor_request_id"

// RequestIDHeader is the header the API reads and echoes.
const RequestIDHeader = "X-Request-Id"

// RequestIDFrom returns the current request's correlation ID.
func RequestIDFrom(c *gin.Context) string {
	if value, ok := c.Get(requestIDKey); ok {
		if id, ok := value.(string); ok {
			return id
		}
	}
	return ""
}

// requestID assigns a correlation ID to every request.
//
// A client-supplied X-Request-Id is honoured so that a trace can span services,
// but only after being sanitised: it is echoed into a response header and into
// logs, so an unvalidated value would allow header and log injection.
func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := sanitiseRequestID(c.GetHeader(RequestIDHeader))
		if id == "" {
			id = newRequestID()
		}

		c.Set(requestIDKey, id)
		c.Header(RequestIDHeader, id)
		c.Request = c.Request.WithContext(telemetry.WithRequestID(c.Request.Context(), id))

		c.Next()
	}
}

// sanitiseRequestID accepts only short, printable, unambiguous identifiers.
func sanitiseRequestID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 64 {
		return ""
	}
	for _, r := range raw {
		isAllowed := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_'
		if !isAllowed {
			return ""
		}
	}
	return raw
}

func newRequestID() string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// A correlation ID is not a security boundary; a time-based fallback is
		// adequate and must never fail the request.
		return "req-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf[:])
}

// requestLogger logs one structured line per request.
func requestLogger(log *slog.Logger, metrics *telemetry.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		metrics.IncInFlight()
		defer metrics.DecInFlight()

		c.Next()

		duration := time.Since(start)
		status := c.Writer.Status()

		// FullPath is the route template ("/v1/repositories/:owner/:repository"),
		// which keeps metric cardinality bounded. An unmatched request has an
		// empty template, so it is bucketed rather than echoed back.
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		metrics.ObserveHTTP(route, c.Request.Method, status, duration)

		level := slog.LevelInfo
		switch {
		case status >= 500:
			level = slog.LevelError
		case status >= 400:
			level = slog.LevelWarn
		}

		log.Log(c.Request.Context(), level, "http request",
			slog.String("method", c.Request.Method),
			slog.String("route", route),
			slog.Int("status", status),
			slog.Int64("durationMs", duration.Milliseconds()),
			slog.Int("bytes", c.Writer.Size()),
		)
	}
}

// recovery converts a panic into a 500 without leaking a stack trace.
func recovery(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				// The stack goes to the server log, never to the client.
				log.ErrorContext(c.Request.Context(), "handler panicked",
					slog.String("route", c.FullPath()),
					slog.Any("panic", recovered),
				)
				if !c.Writer.Written() {
					internalError(c)
				} else {
					c.Abort()
				}
			}
		}()
		c.Next()
	}
}

// securityHeaders sets conservative response headers.
//
// The API serves JSON, SVG and XML to programmatic clients and to <img> tags,
// so the policy is tuned for that: nothing here is a document that should ever
// execute script or be framed.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "cross-origin")
		// A restrictive CSP for the API's own responses. Badge SVGs are
		// self-contained and reference no external resources.
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		c.Next()
	}
}

// cors implements an exact-origin allowlist.
//
// Wildcards are deliberately unsupported: the API is rate limited per client,
// and an open policy would let any page spend a visitor's allowance.
func cors(allowed []string) gin.HandlerFunc {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, origin := range allowed {
		allowedSet[strings.ToLower(origin)] = struct{}{}
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			if _, ok := allowedSet[strings.ToLower(origin)]; ok {
				h := c.Writer.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Content-Type, "+RequestIDHeader)
				h.Set("Access-Control-Expose-Headers", RequestIDHeader+", ETag, Retry-After")
				h.Set("Access-Control-Max-Age", "600")
				// Responses differ by Origin, so caches must not share them.
				h.Add("Vary", "Origin")
			}
		}

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// bodyLimit caps request bodies.
func bodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

// requireJSON rejects write requests that do not declare a JSON body.
func requireJSON() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost && c.Request.Method != http.MethodPut {
			c.Next()
			return
		}

		contentType := c.GetHeader("Content-Type")
		if mediaType, _, err := mime(contentType); err != nil || mediaType != "application/json" {
			writeProblem(c, Problem{
				Type:   ProblemUnsupported,
				Title:  "Unsupported media type",
				Status: http.StatusUnsupportedMediaType,
				Detail: "Request bodies must be application/json.",
			})
			return
		}
		c.Next()
	}
}

// mime splits a Content-Type header into its media type and parameters.
func mime(value string) (string, string, error) {
	if value == "" {
		return "", "", fmt.Errorf("missing content type")
	}
	mediaType, params, _ := strings.Cut(value, ";")
	return strings.ToLower(strings.TrimSpace(mediaType)), strings.TrimSpace(params), nil
}

// ClientIPResolver determines the client address used for rate limiting.
//
// Forwarded headers are only trusted when the immediate peer is inside a
// configured trusted-proxy CIDR. Without that check any client could set
// X-Forwarded-For and obtain an unlimited number of fresh rate-limit buckets.
type ClientIPResolver struct {
	trusted []*net.IPNet
}

// NewClientIPResolver constructs a resolver.
func NewClientIPResolver(trusted []*net.IPNet) *ClientIPResolver {
	return &ClientIPResolver{trusted: trusted}
}

// Resolve returns the client address for the request.
func (r *ClientIPResolver) Resolve(req *http.Request) string {
	peer := peerAddress(req)

	if len(r.trusted) == 0 || !r.isTrusted(peer) {
		return peer
	}

	// Take the right-most address that is not itself a trusted proxy: that is
	// the first hop the trusted infrastructure actually observed.
	if forwarded := req.Header.Get("X-Forwarded-For"); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			candidate := strings.TrimSpace(parts[i])
			if net.ParseIP(candidate) == nil {
				continue
			}
			if r.isTrusted(candidate) {
				continue
			}
			return candidate
		}
	}
	if real := strings.TrimSpace(req.Header.Get("X-Real-Ip")); real != "" && net.ParseIP(real) != nil {
		return real
	}
	return peer
}

func (r *ClientIPResolver) isTrusted(address string) bool {
	ip := net.ParseIP(address)
	if ip == nil {
		return false
	}
	for _, network := range r.trusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func peerAddress(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

// readLimiter is an in-process fixed-window limiter for read endpoints.
//
// Read traffic is limited per instance rather than in the database: a read is
// cheap, the limit exists to stop one client saturating the service, and a
// database round-trip per read would defeat the purpose.
type readLimiter struct {
	mu      sync.Mutex
	windows map[string]*readWindow
	limit   int
	window  time.Duration
	clock   clock.Clock
	// lastSweep bounds map growth without a background goroutine.
	lastSweep time.Time
}

type readWindow struct {
	start time.Time
	hits  int
}

func newReadLimiter(limit int, window time.Duration, c clock.Clock) *readLimiter {
	return &readLimiter{
		windows: make(map[string]*readWindow),
		limit:   limit,
		window:  window,
		clock:   c,
	}
}

// allow reports whether a client may make another read request.
func (l *readLimiter) allow(client string) (bool, int, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock.Now()
	start := now.Truncate(l.window)

	l.sweep(now)

	entry, ok := l.windows[client]
	if !ok || !entry.start.Equal(start) {
		entry = &readWindow{start: start}
		l.windows[client] = entry
	}
	entry.hits++

	remaining := l.limit - entry.hits
	if remaining < 0 {
		remaining = 0
	}
	return entry.hits <= l.limit, remaining, start.Add(l.window)
}

// sweep discards windows that have elapsed. It runs at most once per window so
// that a burst of distinct clients cannot make every request pay for a scan.
func (l *readLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < l.window {
		return
	}
	l.lastSweep = now

	cutoff := now.Add(-2 * l.window)
	for key, entry := range l.windows {
		if entry.start.Before(cutoff) {
			delete(l.windows, key)
		}
	}
}

// rateLimitReads applies the read limiter and publishes standard headers.
func rateLimitReads(limiter *readLimiter, resolver *ClientIPResolver) gin.HandlerFunc {
	if limiter == nil || limiter.limit <= 0 {
		// Limiting disabled by configuration.
		return func(c *gin.Context) { c.Next() }
	}

	return func(c *gin.Context) {
		client := resolver.Resolve(c.Request)
		allowed, remaining, resetAt := limiter.allow(client)

		h := c.Writer.Header()
		h.Set("X-RateLimit-Limit", strconv.Itoa(limiter.limit))
		h.Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		h.Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))

		if !allowed {
			rateLimited(c, fmt.Sprintf(
				"This endpoint allows %d requests per minute. The allowance resets shortly.", limiter.limit),
				retryAfterSeconds(limiter.clock.Now(), resetAt))
			return
		}
		c.Next()
	}
}

// cacheControl sets a public cache policy for successful GET responses.
func cacheControl(maxAge time.Duration) gin.HandlerFunc {
	value := fmt.Sprintf("public, max-age=%d, stale-while-revalidate=%d",
		int(maxAge.Seconds()), int(maxAge.Seconds())*2)

	return func(c *gin.Context) {
		c.Next()
		// Only successful responses are cacheable; problems set no-store
		// themselves and must not be overwritten here.
		if c.Writer.Status() < 400 && c.Writer.Header().Get("Cache-Control") == "" {
			c.Writer.Header().Set("Cache-Control", value)
		}
	}
}

// itoa is a tiny helper so problem.go does not need strconv.
func itoa(v int) string { return strconv.Itoa(v) }

// mustMarshal encodes a value that is known to be serialisable.
func mustMarshal(v any) []byte {
	encoded, err := json.Marshal(v)
	if err != nil {
		// Problem details are plain structs; a failure would be a programming
		// error. Fall back to a minimal valid body rather than panicking inside
		// an error path.
		return []byte(`{"type":"/problems/internal-error","title":"Internal server error","status":500}`)
	}
	return encoded
}

// resolveTrustedProxies converts configuration into a resolver.
func resolveTrustedProxies(cfg config.APIConfig) *ClientIPResolver {
	return NewClientIPResolver(cfg.TrustedProxyCIDRs)
}

// retryAfterSeconds renders a Retry-After value from the service clock.
//
// It is always at least one second: a 429 whose Retry-After rounds to zero —
// or, worse, is omitted because the value was non-positive — leaves a client
// with no guidance and invites an immediate retry. Using the injectable clock
// rather than time.Until keeps the header consistent with the limiter that
// produced resetAt.
func retryAfterSeconds(now, resetAt time.Time) int {
	seconds := int(resetAt.Sub(now).Seconds()) + 1
	if seconds < 1 {
		return 1
	}
	return seconds
}
