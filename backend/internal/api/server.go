package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/repometeor/repometeor/backend/internal/clock"
	"github.com/repometeor/repometeor/backend/internal/config"
	"github.com/repometeor/repometeor/backend/internal/database"
	"github.com/repometeor/repometeor/backend/internal/service"
	"github.com/repometeor/repometeor/backend/internal/telemetry"
)

// Version is the API build version, set at link time in release builds.
var Version = "0.1.0"

// Cache policies per endpoint class. They are short enough that a scoring run
// is visible quickly, and long enough that a busy leaderboard is served from
// cache rather than recomputed for every visitor.
const (
	cacheTrending    = 60 * time.Second
	cacheDetail      = 60 * time.Second
	cacheHistory     = 5 * time.Minute
	cacheFacets      = 5 * time.Minute
	cacheMethodology = time.Hour
)

// Server is the RepoMeteor HTTP API.
type Server struct {
	cfg        *config.Config
	service    *service.Service
	db         *database.DB
	log        *slog.Logger
	metrics    *telemetry.Metrics
	clock      clock.Clock
	router     *gin.Engine
	http       *http.Server
	ipResolver *ClientIPResolver
}

// Options configures a Server.
type Options struct {
	Config  *config.Config
	Service *service.Service
	DB      *database.DB
	Log     *slog.Logger
	Metrics *telemetry.Metrics
	Clock   clock.Clock
}

// New builds the API server and its routes.
func New(opts Options) *Server {
	if opts.Config.App.Environment.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.TestMode)
	}

	s := &Server{
		cfg:        opts.Config,
		service:    opts.Service,
		db:         opts.DB,
		log:        opts.Log,
		metrics:    opts.Metrics,
		clock:      opts.Clock,
		ipResolver: resolveTrustedProxies(opts.Config.API),
	}

	s.router = s.buildRouter()
	s.http = &http.Server{
		Addr:              opts.Config.API.Address(),
		Handler:           s.router,
		ReadHeaderTimeout: opts.Config.API.ReadHeaderTimeout,
		ReadTimeout:       opts.Config.API.ReadTimeout,
		WriteTimeout:      opts.Config.API.WriteTimeout,
		IdleTimeout:       opts.Config.API.IdleTimeout,
		// Gin's own logger is replaced by the structured one, so the standard
		// library's error log is routed through slog too.
		ErrorLog: slog.NewLogLogger(opts.Log.Handler(), slog.LevelWarn),
	}

	return s
}

// Handler exposes the router, which is what the test suite drives.
func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) buildRouter() *gin.Engine {
	router := gin.New()

	// Gin's own proxy trust machinery is not used: RepoMeteor resolves client
	// addresses through its own explicitly configured allowlist.
	_ = router.SetTrustedProxies(nil)
	router.RedirectTrailingSlash = true
	router.HandleMethodNotAllowed = true

	router.Use(
		requestID(),
		recovery(s.log),
		requestLogger(s.log, s.metrics),
		securityHeaders(),
		cors(s.cfg.API.AllowedOrigins),
		bodyLimit(s.cfg.API.MaxRequestBytes),
	)

	router.NoRoute(func(c *gin.Context) {
		notFound(c, "No route matches this path. See "+s.cfg.API.PublicURL+"/v1/methodology for the API index.")
	})
	router.NoMethod(func(c *gin.Context) {
		writeProblem(c, Problem{
			Type:   ProblemUnsupported,
			Title:  "Method not allowed",
			Status: http.StatusMethodNotAllowed,
			Detail: "This route does not accept " + c.Request.Method + ".",
		})
	})

	// Health endpoints are deliberately outside the rate limiter: a probe must
	// never be throttled, and they expose nothing sensitive.
	get(router, "/healthz", s.handleHealth)
	get(router, "/readyz", s.handleReady)
	if s.cfg.Metrics.Enabled {
		get(router, "/metrics", gin.WrapH(promhttp.HandlerFor(
			s.metrics.Registry(), promhttp.HandlerOpts{},
		)))
	}

	readLimit := newReadLimiter(s.cfg.Limits.APIReadRateLimitPerMinute, time.Minute, s.clock)

	v1 := router.Group("/v1")
	v1.Use(rateLimitReads(readLimit, s.ipResolver))
	{
		get(v1, "/trending", cacheControl(cacheTrending), s.handleTrending)
		get(v1, "/search", cacheControl(cacheFacets), s.handleSearch)
		get(v1, "/languages", cacheControl(cacheFacets), s.handleLanguages)
		get(v1, "/topics", cacheControl(cacheFacets), s.handleTopics)
		get(v1, "/compare", cacheControl(cacheHistory), s.handleCompare)
		get(v1, "/methodology", cacheControl(cacheMethodology), s.handleMethodology)

		get(v1, "/repositories/:owner/:repository", cacheControl(cacheDetail), s.handleRepositoryDetail)
		get(v1, "/repositories/:owner/:repository/history", cacheControl(cacheHistory), s.handleHistory)

		get(v1, "/badges/:owner/:repository", s.handleBadge)
		get(v1, "/feeds/trending.xml", s.handleTrendingFeed)

		v1.POST("/repositories/track", requireJSON(), s.handleTrack)
	}

	return router
}

// routeRegistrar is satisfied by both *gin.Engine and *gin.RouterGroup.
type routeRegistrar interface {
	GET(path string, handlers ...gin.HandlerFunc) gin.IRoutes
	HEAD(path string, handlers ...gin.HandlerFunc) gin.IRoutes
}

// get registers a read route for both GET and HEAD.
//
// Gin does not derive HEAD from GET. Badges in particular are fetched by image
// proxies that issue HEAD to revalidate, and monitoring tools commonly probe
// with HEAD, so a GET-only route would answer both with 405. Go's http.Server
// discards the body of a HEAD response automatically, so the same handler is
// correct for both methods.
func get(r routeRegistrar, path string, handlers ...gin.HandlerFunc) {
	r.GET(path, handlers...)
	r.HEAD(path, handlers...)
}

// Start begins serving. It returns once the listener stops.
func (s *Server) Start() error {
	s.log.Info("api listening",
		slog.String("address", s.cfg.API.Address()),
		slog.String("publicUrl", s.cfg.API.PublicURL),
		slog.Bool("demoMode", s.cfg.App.DemoMode),
		slog.Bool("metrics", s.cfg.Metrics.Enabled),
	)

	if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown stops accepting connections and waits for in-flight requests.
func (s *Server) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.API.ShutdownTimeout)
	defer cancel()

	s.log.Info("api shutting down", slog.String("timeout", s.cfg.API.ShutdownTimeout.String()))
	return s.http.Shutdown(ctx)
}

// --- health ------------------------------------------------------------------

// handleHealth reports process liveness.
//
// It deliberately does not touch the database. A liveness probe answers "should
// this process be restarted?", and a database outage is not fixed by restarting
// the API — that is what /readyz is for.
func (s *Server) handleHealth(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, HealthResponse{
		Status:  "ok",
		Service: "repometeor-api",
		Version: Version,
	})
}

// handleReady reports whether the service can serve traffic.
func (s *Server) handleReady(c *gin.Context) {
	c.Header("Cache-Control", "no-store")

	checks := map[string]string{}
	ready := true

	if s.db == nil {
		checks["database"] = "not configured"
		ready = false
	} else if err := s.db.Ping(c.Request.Context()); err != nil {
		// The error text stays in the log; the probe response says only that
		// the dependency is unavailable.
		s.log.WarnContext(c.Request.Context(), "readiness check failed",
			slogString("dependency", "database"), slogString("error", err.Error()))
		checks["database"] = "unavailable"
		ready = false
	} else {
		checks["database"] = "ok"
	}

	if !ready {
		writeProblem(c, Problem{
			Type:   ProblemNotReady,
			Title:  "Service not ready",
			Status: http.StatusServiceUnavailable,
			Detail: "A required dependency is unavailable.",
		})
		return
	}

	c.JSON(http.StatusOK, ReadyResponse{
		Status:   "ready",
		Checks:   checks,
		DemoMode: s.cfg.App.DemoMode,
	})
}

// --- conditional responses ---------------------------------------------------

// respondWithETag sends JSON with a strong-enough validator for revalidation.
//
// Ranking responses change only when a scoring run completes, so most requests
// during a run's lifetime can be answered with a 304 and no body at all.
func (s *Server) respondWithETag(c *gin.Context, payload any) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		s.fail(c, "response encoding failed", err)
		return
	}

	sum := sha256.Sum256(encoded)
	etag := `W/"` + hex.EncodeToString(sum[:12]) + `"`
	c.Header("ETag", etag)

	if match := c.GetHeader("If-None-Match"); match != "" && etagMatches(match, etag) {
		c.Status(http.StatusNotModified)
		return
	}

	c.Data(http.StatusOK, "application/json; charset=utf-8", encoded)
}

// etagMatches implements the If-None-Match comparison, including the "*" form
// and comma-separated lists.
func etagMatches(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "*" {
		return true
	}
	for _, candidate := range strings.Split(header, ",") {
		if strings.TrimSpace(candidate) == etag {
			return true
		}
	}
	return false
}

// slogString is a tiny alias so handlers read cleanly.
func slogString(key, value string) slog.Attr { return slog.String(key, value) }

// ensure fmt stays referenced for handlers that build messages.
var _ = fmt.Sprintf
