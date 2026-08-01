package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/repometeor/repometeor/backend/internal/reponame"
	"github.com/repometeor/repometeor/backend/internal/repository"
	"github.com/repometeor/repometeor/backend/internal/scoring"
	"github.com/repometeor/repometeor/backend/internal/service"
)

// Query parameter bounds. Every numeric input is clamped rather than trusted.
const (
	defaultLimit = 20
	maxLimit     = 100
	maxQueryLen  = 120
	maxTopicLen  = 50
	maxLangLen   = 40
)

// --- trending ----------------------------------------------------------------

func (s *Server) handleTrending(c *gin.Context) {
	query, problem := parseTrendingQuery(c)
	if problem != nil {
		badRequest(c, problem.detail, problem.fields...)
		return
	}

	result, err := s.service.Trending(c.Request.Context(), query)
	if err != nil {
		if errors.Is(err, service.ErrInvalidCursor) {
			badRequest(c, "The cursor is not valid. Request the first page without a cursor.",
				FieldError{Field: "cursor", Message: "malformed or out of range"})
			return
		}
		s.fail(c, "trending query failed", err)
		return
	}

	items := make([]TrendingItemDTO, 0, len(result.Items))
	for _, ranked := range result.Items {
		items = append(items, TrendingItemDTO{
			Repository: toRepositoryDTO(ranked.Repository, ranked.Snapshot),
			Momentum:   toMomentumDTO(ranked.Score),
			Sparkline:  nonNilInts(ranked.Sparkline),
		})
	}

	response := TrendingResponse{
		Data: items,
		Meta: MetaDTO{
			Window:         result.Window,
			Collection:     result.Collection,
			GeneratedAt:    result.GeneratedAt,
			DemoMode:       result.DemoMode,
			ResultCount:    result.ResultCount,
			FormulaVersion: result.FormulaVersion,
		},
		Links: LinksDTO{NextCursor: optionalString(result.NextCursor)},
	}

	s.respondWithETag(c, response)
}

type queryProblem struct {
	detail string
	fields []FieldError
}

// parseTrendingQuery validates every trending parameter.
//
// Unknown values are rejected rather than ignored: silently serving a different
// window than the one requested would make a shared URL mean two things.
func parseTrendingQuery(c *gin.Context) (service.TrendingQuery, *queryProblem) {
	var fields []FieldError
	query := service.TrendingQuery{Limit: defaultLimit}

	collection := strings.TrimSpace(c.Query("collection"))
	if collection != "" {
		if !contains(scoring.CollectionNames(), collection) {
			fields = append(fields, FieldError{
				Field:   "collection",
				Message: "must be one of: " + strings.Join(scoring.CollectionNames(), ", "),
			})
		} else {
			query.Collection = collection
		}
	}

	window := strings.TrimSpace(c.Query("window"))
	switch {
	case window == "" && query.Collection != "":
		// A collection implies its most meaningful window.
		query.Window = scoring.DefaultWindowForCollection(query.Collection)
	case window == "":
		query.Window = scoring.Window24h.Name
	default:
		if _, err := scoring.WindowByName(window); err != nil {
			fields = append(fields, FieldError{
				Field:   "window",
				Message: "must be one of: " + strings.Join(scoring.WindowNames(), ", "),
			})
		} else {
			query.Window = window
		}
	}

	if query.Collection == "" && query.Window != "" {
		query.Collection = scoring.PrimaryCollectionForWindow(query.Window)
	}

	if language := strings.TrimSpace(c.Query("language")); language != "" {
		if len(language) > maxLangLen || !isPrintableFilter(language) {
			fields = append(fields, FieldError{Field: "language", Message: "is not a valid language name"})
		} else {
			query.Language = language
		}
	}

	if topic := strings.TrimSpace(c.Query("topic")); topic != "" {
		if len(topic) > maxTopicLen || !isSlug(topic) {
			fields = append(fields, FieldError{
				Field:   "topic",
				Message: "may contain only lowercase letters, digits, hyphens, dots and underscores",
			})
		} else {
			query.Topic = strings.ToLower(topic)
		}
	}

	if raw := strings.TrimSpace(c.Query("minStars")); raw != "" {
		value, err := parseBoundedInt(raw, 0, 100_000_000)
		if err != nil {
			fields = append(fields, FieldError{Field: "minStars", Message: err.Error()})
		} else {
			query.MinStars = &value
		}
	}
	if raw := strings.TrimSpace(c.Query("maxStars")); raw != "" {
		value, err := parseBoundedInt(raw, 0, 100_000_000)
		if err != nil {
			fields = append(fields, FieldError{Field: "maxStars", Message: err.Error()})
		} else {
			query.MaxStars = &value
		}
	}
	if query.MinStars != nil && query.MaxStars != nil && *query.MinStars > *query.MaxStars {
		fields = append(fields, FieldError{Field: "minStars", Message: "must not exceed maxStars"})
	}

	if confidence := strings.TrimSpace(c.Query("confidence")); confidence != "" {
		if _, _, ok := scoring.ConfidenceBounds(confidence); !ok {
			fields = append(fields, FieldError{Field: "confidence", Message: "must be one of: high, medium, low"})
		} else {
			query.Confidence = confidence
		}
	}

	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		value, err := parseBoundedInt(raw, 1, maxLimit)
		if err != nil {
			fields = append(fields, FieldError{
				Field:   "limit",
				Message: fmt.Sprintf("must be an integer between 1 and %d", maxLimit),
			})
		} else {
			query.Limit = value
		}
	}

	if cursor := strings.TrimSpace(c.Query("cursor")); cursor != "" {
		if len(cursor) > 256 {
			fields = append(fields, FieldError{Field: "cursor", Message: "is too long"})
		} else {
			query.Cursor = cursor
		}
	}

	if len(fields) > 0 {
		return service.TrendingQuery{}, &queryProblem{
			detail: "One or more query parameters are invalid.",
			fields: fields,
		}
	}
	return query, nil
}

// --- repository detail -------------------------------------------------------

func (s *Server) handleRepositoryDetail(c *gin.Context) {
	name, ok := s.repositoryNameParam(c)
	if !ok {
		return
	}

	detail, err := s.service.RepositoryDetail(c.Request.Context(), name)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			notFound(c, fmt.Sprintf("RepoMeteor is not tracking %s. Submit it at %s/submit.",
				name, s.cfg.API.WebPublicURL))
			return
		}
		s.fail(c, "repository detail failed", err)
		return
	}

	momentum := make(map[string]MomentumDTO, len(detail.Scores))
	for window, score := range detail.Scores {
		momentum[window] = toMomentumDTO(score)
	}

	response := RepositoryDetailResponse{
		Repository: toRepositoryDTO(detail.Repository, detail.Latest),
		Momentum:   momentum,
		Tracking: TrackingDTO{
			State:         string(detail.Repository.TrackingState),
			Tier:          string(detail.Repository.TrackingTier),
			TrackingSince: detail.TrackingSince,
			FirstSeenAt:   detail.Repository.FirstSeenAt,
			LastCheckedAt: detail.Repository.LastCheckedAt,
		},
		Links: s.repositoryLinks(detail.Repository.RepoName()),
		Meta: DetailMetaDTO{
			DemoMode:       detail.DemoMode,
			GeneratedAt:    s.clock.Now(),
			FormulaVersion: detail.FormulaVersion,
		},
	}
	if detail.Redirected {
		canonical := detail.CanonicalName
		response.Meta.CanonicalFullName = &canonical
	}

	s.respondWithETag(c, response)
}

// repositoryLinks builds every derived URL from configuration, so a deployment
// on a different domain produces correct snippets without code changes.
func (s *Server) repositoryLinks(name reponame.Name) RepositoryLinksDTO {
	apiBase := s.cfg.API.PublicURL
	webBase := s.cfg.API.WebPublicURL
	path := name.Owner + "/" + name.Repository

	badge := fmt.Sprintf("%s/v1/badges/%s.svg?window=24h", apiBase, path)
	web := fmt.Sprintf("%s/r/%s", webBase, path)

	return RepositoryLinksDTO{
		Self:          fmt.Sprintf("%s/v1/repositories/%s", apiBase, path),
		Web:           web,
		GitHub:        "https://github.com/" + path,
		Badge:         badge,
		History:       fmt.Sprintf("%s/v1/repositories/%s/history", apiBase, path),
		Feed:          fmt.Sprintf("%s/v1/feeds/trending.xml", apiBase),
		BadgeMarkdown: fmt.Sprintf("[![RepoMeteor momentum](%s)](%s)", badge, web),
	}
}

// --- history -----------------------------------------------------------------

func (s *Server) handleHistory(c *gin.Context) {
	name, ok := s.repositoryNameParam(c)
	if !ok {
		return
	}

	rangeDuration, resolution, problem := parseHistoryParams(c)
	if problem != nil {
		badRequest(c, problem.detail, problem.fields...)
		return
	}

	window := strings.TrimSpace(c.Query("window"))
	if window != "" {
		if _, err := scoring.WindowByName(window); err != nil {
			badRequest(c, "The window is not valid.",
				FieldError{Field: "window", Message: "must be one of: " + strings.Join(scoring.WindowNames(), ", ")})
			return
		}
	}

	result, err := s.service.History(c.Request.Context(), name, service.HistoryQuery{
		Range: rangeDuration, Resolution: resolution, Window: window,
	})
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			notFound(c, fmt.Sprintf("RepoMeteor is not tracking %s.", name))
			return
		}
		s.fail(c, "history query failed", err)
		return
	}

	points := toHistoryPointDTOs(result.Points)
	s.respondWithETag(c, HistoryResponse{
		Data: points,
		Meta: HistoryMetaDTO{
			Repository:    result.Repository.FullName,
			Range:         formatRange(rangeDuration),
			Resolution:    string(result.Resolution),
			From:          result.From,
			To:            result.To,
			TrackingSince: result.TrackingSince,
			PointCount:    len(points),
			DemoMode:      result.DemoMode,
		},
	})
}

// historyRanges is the allowlist of ranges. An arbitrary duration would let a
// caller request an unbounded scan.
var historyRanges = map[string]time.Duration{
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
	"90d": 90 * 24 * time.Hour,
}

func parseHistoryParams(c *gin.Context) (time.Duration, repository.Resolution, *queryProblem) {
	rangeKey := strings.TrimSpace(c.Query("range"))
	if rangeKey == "" {
		rangeKey = "7d"
	}
	rangeDuration, ok := historyRanges[rangeKey]
	if !ok {
		return 0, "", &queryProblem{
			detail: "The range is not valid.",
			fields: []FieldError{{Field: "range", Message: "must be one of: 24h, 7d, 30d, 90d"}},
		}
	}

	resolutionKey := strings.TrimSpace(c.Query("resolution"))
	if resolutionKey == "" {
		// Choose a resolution that keeps the payload bounded for the range.
		if rangeDuration > 14*24*time.Hour {
			return rangeDuration, repository.ResolutionDaily, nil
		}
		return rangeDuration, repository.ResolutionHourly, nil
	}

	switch repository.Resolution(resolutionKey) {
	case repository.ResolutionRaw:
		return rangeDuration, repository.ResolutionRaw, nil
	case repository.ResolutionHourly:
		return rangeDuration, repository.ResolutionHourly, nil
	case repository.ResolutionDaily:
		return rangeDuration, repository.ResolutionDaily, nil
	default:
		return 0, "", &queryProblem{
			detail: "The resolution is not valid.",
			fields: []FieldError{{Field: "resolution", Message: "must be one of: raw, 1h, 1d"}},
		}
	}
}

func formatRange(d time.Duration) string {
	for key, value := range historyRanges {
		if value == d {
			return key
		}
	}
	return d.String()
}

// --- compare -----------------------------------------------------------------

func (s *Server) handleCompare(c *gin.Context) {
	raw := strings.TrimSpace(c.Query("repositories"))
	if raw == "" {
		badRequest(c, "The repositories parameter is required.",
			FieldError{Field: "repositories", Message: "supply between 2 and 4 comma-separated repositories"})
		return
	}
	if len(raw) > 600 {
		badRequest(c, "The repositories parameter is too long.",
			FieldError{Field: "repositories", Message: "is too long"})
		return
	}

	names, err := reponame.ParseList(raw, 2, 4)
	if err != nil {
		badRequest(c, "The repositories parameter is not valid.",
			FieldError{Field: "repositories", Message: err.Error()})
		return
	}

	window := strings.TrimSpace(c.Query("window"))
	if window == "" {
		window = scoring.Window24h.Name
	}
	if _, err := scoring.WindowByName(window); err != nil {
		badRequest(c, "The window is not valid.",
			FieldError{Field: "window", Message: "must be one of: " + strings.Join(scoring.WindowNames(), ", ")})
		return
	}

	rangeKey := strings.TrimSpace(c.Query("range"))
	if rangeKey == "" {
		rangeKey = "7d"
	}
	rangeDuration, ok := historyRanges[rangeKey]
	if !ok {
		badRequest(c, "The range is not valid.",
			FieldError{Field: "range", Message: "must be one of: 24h, 7d, 30d, 90d"})
		return
	}

	result, err := s.service.Compare(c.Request.Context(), names, window, rangeDuration)
	if err != nil {
		s.fail(c, "compare failed", err)
		return
	}

	entries := make([]CompareEntryDTO, 0, len(result.Entries))
	for _, entry := range result.Entries {
		dto := CompareEntryDTO{
			Repository:    toRepositoryDTO(entry.Repository, latestFromPoints(entry.Points)),
			Points:        toHistoryPointDTOs(entry.Points),
			Available:     entry.Available,
			Note:          optionalString(entry.Unavailable),
			TrackingSince: entry.TrackingSince,
			Summary:       summariseHistory(entry.Points),
		}
		if entry.Score != nil {
			momentum := toMomentumDTO(*entry.Score)
			dto.Momentum = &momentum
		}
		entries = append(entries, dto)
	}

	s.respondWithETag(c, CompareResponse{
		Data: entries,
		Meta: CompareMetaDTO{
			Window:     result.Window,
			Range:      rangeKey,
			Resolution: string(result.Resolution),
			From:       result.From,
			To:         result.To,
			DemoMode:   result.DemoMode,
		},
	})
}

// latestFromPoints derives current counts from the last history point, so a
// comparison entry shows totals without another query.
func latestFromPoints(points []repository.HistoryPoint) *repository.Snapshot {
	if len(points) == 0 {
		return nil
	}
	last := points[len(points)-1]
	return &repository.Snapshot{Stars: last.Stars, Forks: last.Forks, CapturedAt: last.At}
}

// --- search ------------------------------------------------------------------

func (s *Server) handleSearch(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		badRequest(c, "The q parameter is required.",
			FieldError{Field: "q", Message: "supply a repository name or URL"})
		return
	}
	if len(query) > maxQueryLen {
		badRequest(c, "The query is too long.",
			FieldError{Field: "q", Message: fmt.Sprintf("must be at most %d characters", maxQueryLen)})
		return
	}
	if !isPrintableFilter(query) {
		badRequest(c, "The query contains characters that are not allowed.",
			FieldError{Field: "q", Message: "may not contain control characters"})
		return
	}

	limit := defaultLimit
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		value, err := parseBoundedInt(raw, 1, maxLimit)
		if err != nil {
			badRequest(c, "The limit is not valid.",
				FieldError{Field: "limit", Message: err.Error()})
			return
		}
		limit = value
	}

	repos, err := s.service.Search(c.Request.Context(), query, limit)
	if err != nil {
		s.fail(c, "search failed", err)
		return
	}

	data := make([]RepositoryDTO, 0, len(repos))
	for _, repo := range repos {
		data = append(data, toRepositoryDTO(repo, nil))
	}

	response := SearchResponse{Data: data}
	response.Meta.Query = query
	response.Meta.ResultCount = len(data)
	response.Meta.DemoMode = s.cfg.App.DemoMode

	c.JSON(http.StatusOK, response)
}

// --- facets ------------------------------------------------------------------

func (s *Server) handleLanguages(c *gin.Context) {
	languages, err := s.service.Languages(c.Request.Context())
	if err != nil {
		s.fail(c, "language facets failed", err)
		return
	}

	data := make([]FacetDTO, 0, len(languages))
	for _, l := range languages {
		data = append(data, FacetDTO{Value: l.Language, Count: l.Count})
	}
	s.respondFacets(c, data)
}

func (s *Server) handleTopics(c *gin.Context) {
	limit := 100
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		value, err := parseBoundedInt(raw, 1, 500)
		if err != nil {
			badRequest(c, "The limit is not valid.", FieldError{Field: "limit", Message: err.Error()})
			return
		}
		limit = value
	}

	topics, err := s.service.Topics(c.Request.Context(), limit)
	if err != nil {
		s.fail(c, "topic facets failed", err)
		return
	}

	data := make([]FacetDTO, 0, len(topics))
	for _, t := range topics {
		data = append(data, FacetDTO{Value: t.Topic, Count: t.Count})
	}
	s.respondFacets(c, data)
}

func (s *Server) respondFacets(c *gin.Context, data []FacetDTO) {
	response := FacetsResponse{Data: data}
	response.Meta.ResultCount = len(data)
	response.Meta.DemoMode = s.cfg.App.DemoMode
	s.respondWithETag(c, response)
}

// --- tracking ----------------------------------------------------------------

type trackRequest struct {
	Repository string `json:"repository"`
}

func (s *Server) handleTrack(c *gin.Context) {
	var body trackRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		badRequest(c, "The request body could not be read.",
			FieldError{Field: "repository", Message: "supply a JSON object with a repository field"})
		return
	}

	name, err := reponame.Parse(body.Repository)
	if err != nil {
		badRequest(c, "The repository reference is not valid.",
			FieldError{Field: "repository", Message: err.Error()})
		return
	}

	// The rate limiter runs after validation so that malformed submissions do
	// not consume a legitimate caller's allowance.
	client := s.ipResolver.Resolve(c.Request)
	allowed, remaining, resetAt, err := s.service.AllowTracking(c.Request.Context(), client, s.clock.Now())
	if err != nil {
		s.fail(c, "tracking rate limit check failed", err)
		return
	}

	c.Header("X-RateLimit-Limit", strconv.Itoa(s.cfg.Limits.PublicTrackingPerHour))
	c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))
	c.Header("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))

	if !allowed {
		rateLimited(c, fmt.Sprintf(
			"Submissions are limited to %d per hour. Your allowance resets shortly.",
			s.cfg.Limits.PublicTrackingPerHour), retryAfterSeconds(s.clock.Now(), resetAt))
		return
	}

	result, err := s.service.Track(c.Request.Context(), name)
	switch {
	case err == nil:
	case errors.Is(err, service.ErrTrackingDisabled):
		writeProblem(c, Problem{
			Type:   ProblemUnsupported,
			Title:  "Tracking submissions are disabled",
			Status: http.StatusForbidden,
			Detail: "This RepoMeteor instance does not accept public tracking submissions.",
		})
		return
	case errors.Is(err, service.ErrDemoMode):
		writeProblem(c, Problem{
			Type:   ProblemUnsupported,
			Title:  "Demo mode",
			Status: http.StatusForbidden,
			Detail: "This instance is running on demo data and cannot track real repositories.",
		})
		return
	default:
		s.fail(c, "tracking submission failed", err)
		return
	}

	webURL := fmt.Sprintf("%s/r/%s/%s", s.cfg.API.WebPublicURL, name.Owner, name.Repository)

	switch result.Outcome {
	case service.TrackAlreadyTracked:
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, TrackResponse{
			Status:     "already_tracked",
			Repository: result.Repository.FullName,
			Message:    "RepoMeteor already tracks this repository.",
			WebURL:     &webURL,
		})
	default:
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusAccepted, TrackResponse{
			Status:     "queued",
			Repository: name.String(),
			Message: "Queued for collection. Tracking starts now — RepoMeteor cannot " +
				"reconstruct history from before this moment, so rankings will appear " +
				"once enough snapshots exist.",
			WebURL: &webURL,
		})
	}
}

// --- helpers -----------------------------------------------------------------

// repositoryNameParam extracts and validates the owner/repository path pair.
func (s *Server) repositoryNameParam(c *gin.Context) (reponame.Name, bool) {
	owner := c.Param("owner")
	repo := strings.TrimSuffix(c.Param("repository"), ".svg")

	name, err := reponame.Parse(owner + "/" + repo)
	if err != nil {
		badRequest(c, "The repository path is not valid.",
			FieldError{Field: "repository", Message: err.Error()})
		return reponame.Name{}, false
	}
	return name, true
}

// fail logs the real cause and returns an opaque 500.
func (s *Server) fail(c *gin.Context, message string, err error) {
	s.log.ErrorContext(c.Request.Context(), message,
		slogString("error", err.Error()),
		slogString("route", c.FullPath()),
	)
	internalError(c)
}

func parseBoundedInt(raw string, low, high int) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("must be an integer")
	}
	if value < low || value > high {
		return 0, fmt.Errorf("must be between %d and %d", low, high)
	}
	return value, nil
}

// isSlug matches the topic character set GitHub itself permits.
func isSlug(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		isAllowed := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '.' || r == '_'
		if !isAllowed {
			return false
		}
	}
	return true
}

// isPrintableFilter rejects control characters, which would otherwise reach
// logs and response headers.
func isPrintableFilter(v string) bool {
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

func nonNilInts(in []int) []int {
	if in == nil {
		return []int{}
	}
	return in
}
