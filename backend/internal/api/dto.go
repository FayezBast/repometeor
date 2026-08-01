package api

import (
	"time"

	"github.com/repometeor/repometeor/backend/internal/repository"
	"github.com/repometeor/repometeor/backend/internal/scoring"
)

// The DTOs below are the API's public contract. They are deliberately separate
// from the storage models so that a database column can be renamed without
// breaking clients, and so that nothing internal leaks by accident.

// RepositoryDTO is a repository as the API presents it.
type RepositoryDTO struct {
	Owner       string     `json:"owner"`
	Name        string     `json:"name"`
	FullName    string     `json:"fullName"`
	Description string     `json:"description"`
	URL         string     `json:"url"`
	HomepageURL *string    `json:"homepageUrl"`
	AvatarURL   *string    `json:"avatarUrl"`
	Language    *string    `json:"language"`
	Topics      []string   `json:"topics"`
	License     *string    `json:"license"`
	Stars       int        `json:"stars"`
	Forks       int        `json:"forks"`
	OpenIssues  int        `json:"openIssues"`
	IsArchived  bool       `json:"isArchived"`
	IsFork      bool       `json:"isFork"`
	CreatedAt   time.Time  `json:"createdAt"`
	PushedAt    *time.Time `json:"pushedAt"`
}

// MomentumDTO is a momentum score as the API presents it.
type MomentumDTO struct {
	Window          string    `json:"window"`
	Score           float64   `json:"score"`
	Rank            *int      `json:"rank"`
	StarDelta       int       `json:"starDelta"`
	StarVelocity    float64   `json:"starVelocity"`
	RelativeGrowth  float64   `json:"relativeGrowth"`
	Acceleration    float64   `json:"acceleration"`
	ForkDelta       int       `json:"forkDelta"`
	ForkVelocity    float64   `json:"forkVelocity"`
	ActivityScore   float64   `json:"activityScore"`
	FreshnessScore  float64   `json:"freshnessScore"`
	Confidence      float64   `json:"confidence"`
	ConfidenceLabel string    `json:"confidenceLabel"`
	Reasons         []string  `json:"reasons"`
	Flags           []string  `json:"flags"`
	Collections     []string  `json:"collections"`
	FormulaVersion  string    `json:"formulaVersion"`
	ScoredAt        time.Time `json:"scoredAt"`
}

// TrendingItemDTO pairs a repository with its momentum.
type TrendingItemDTO struct {
	Repository RepositoryDTO `json:"repository"`
	Momentum   MomentumDTO   `json:"momentum"`
	// Sparkline holds up to 24 recent star counts, oldest first, so a card can
	// render an inline chart without a second request.
	Sparkline []int `json:"sparkline"`
}

// MetaDTO is the metadata block every collection response carries.
type MetaDTO struct {
	Window         string    `json:"window"`
	Collection     string    `json:"collection"`
	GeneratedAt    time.Time `json:"generatedAt"`
	DemoMode       bool      `json:"demoMode"`
	ResultCount    int       `json:"resultCount"`
	FormulaVersion string    `json:"formulaVersion"`
}

// LinksDTO carries pagination links.
type LinksDTO struct {
	NextCursor *string `json:"nextCursor"`
}

// TrendingResponse is the /v1/trending payload.
type TrendingResponse struct {
	Data  []TrendingItemDTO `json:"data"`
	Meta  MetaDTO           `json:"meta"`
	Links LinksDTO          `json:"links"`
}

// RepositoryDetailResponse is the /v1/repositories/{owner}/{repo} payload.
type RepositoryDetailResponse struct {
	Repository RepositoryDTO          `json:"repository"`
	Momentum   map[string]MomentumDTO `json:"momentum"`
	Tracking   TrackingDTO            `json:"tracking"`
	Links      RepositoryLinksDTO     `json:"links"`
	Meta       DetailMetaDTO          `json:"meta"`
}

// TrackingDTO describes RepoMeteor's own relationship with a repository.
//
// TrackingSince is the honest statement of where history begins: it is when
// RepoMeteor first captured a snapshot, not when the repository was created.
type TrackingDTO struct {
	State         string     `json:"state"`
	Tier          string     `json:"tier"`
	TrackingSince *time.Time `json:"trackingSince"`
	FirstSeenAt   time.Time  `json:"firstSeenAt"`
	LastCheckedAt *time.Time `json:"lastCheckedAt"`
}

// RepositoryLinksDTO carries the derived URLs for a repository.
type RepositoryLinksDTO struct {
	Self    string `json:"self"`
	Web     string `json:"web"`
	GitHub  string `json:"github"`
	Badge   string `json:"badge"`
	History string `json:"history"`
	Feed    string `json:"feed"`
	// BadgeMarkdown is a ready-to-paste README snippet.
	BadgeMarkdown string `json:"badgeMarkdown"`
}

// DetailMetaDTO is the metadata block on a detail response.
type DetailMetaDTO struct {
	DemoMode       bool      `json:"demoMode"`
	GeneratedAt    time.Time `json:"generatedAt"`
	FormulaVersion string    `json:"formulaVersion"`
	// CanonicalFullName is set when the request used a former name.
	CanonicalFullName *string `json:"canonicalFullName,omitempty"`
}

// HistoryPointDTO is one point on a history chart.
type HistoryPointDTO struct {
	At        time.Time `json:"at"`
	Stars     int       `json:"stars"`
	Forks     int       `json:"forks"`
	StarDelta int       `json:"starDelta"`
	ForkDelta int       `json:"forkDelta"`
	Score     *float64  `json:"score,omitempty"`
}

// HistoryResponse is the history payload.
type HistoryResponse struct {
	Data []HistoryPointDTO `json:"data"`
	Meta HistoryMetaDTO    `json:"meta"`
}

// HistoryMetaDTO describes the requested and available range.
type HistoryMetaDTO struct {
	Repository    string     `json:"repository"`
	Range         string     `json:"range"`
	Resolution    string     `json:"resolution"`
	From          time.Time  `json:"from"`
	To            time.Time  `json:"to"`
	TrackingSince *time.Time `json:"trackingSince"`
	PointCount    int        `json:"pointCount"`
	DemoMode      bool       `json:"demoMode"`
}

// CompareEntryDTO is one repository in a comparison.
type CompareEntryDTO struct {
	Repository    RepositoryDTO     `json:"repository"`
	Points        []HistoryPointDTO `json:"points"`
	Momentum      *MomentumDTO      `json:"momentum"`
	Available     bool              `json:"available"`
	Note          *string           `json:"note"`
	TrackingSince *time.Time        `json:"trackingSince"`
	// Summary holds normalised comparison metrics for the whole range.
	Summary CompareSummaryDTO `json:"summary"`
}

// CompareSummaryDTO is the aligned comparison metric set.
type CompareSummaryDTO struct {
	StartStars     int     `json:"startStars"`
	EndStars       int     `json:"endStars"`
	AbsoluteGrowth int     `json:"absoluteGrowth"`
	RelativeGrowth float64 `json:"relativeGrowth"`
	ForkGrowth     int     `json:"forkGrowth"`
}

// CompareResponse is the compare payload.
type CompareResponse struct {
	Data []CompareEntryDTO `json:"data"`
	Meta CompareMetaDTO    `json:"meta"`
}

// CompareMetaDTO describes the shared comparison range.
type CompareMetaDTO struct {
	Window     string    `json:"window"`
	Range      string    `json:"range"`
	Resolution string    `json:"resolution"`
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	DemoMode   bool      `json:"demoMode"`
}

// SearchResponse is the search payload.
type SearchResponse struct {
	Data []RepositoryDTO `json:"data"`
	Meta SearchMetaDTO   `json:"meta"`
}

// SearchMetaDTO describes a search.
type SearchMetaDTO struct {
	Query       string `json:"query"`
	ResultCount int    `json:"resultCount"`
	DemoMode    bool   `json:"demoMode"`
}

// FacetDTO is one filter value with its count.
type FacetDTO struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// FacetsResponse lists available filter values.
type FacetsResponse struct {
	Data []FacetDTO `json:"data"`
	Meta struct {
		ResultCount int  `json:"resultCount"`
		DemoMode    bool `json:"demoMode"`
	} `json:"meta"`
}

// TrackResponse is the tracking submission payload.
type TrackResponse struct {
	Status     string  `json:"status"`
	Repository string  `json:"repository"`
	Message    string  `json:"message"`
	WebURL     *string `json:"webUrl"`
}

// MethodologyResponse publishes the full scoring method.
//
// It exists so that the ranking is auditable from the API alone: the weights,
// the eligibility rules and the confidence thresholds are all machine-readable,
// not just prose on a page.
type MethodologyResponse struct {
	FormulaVersion string             `json:"formulaVersion"`
	Description    string             `json:"description"`
	Weights        map[string]float64 `json:"weights"`
	Windows        []WindowDTO        `json:"windows"`
	Confidence     ConfidenceDTO      `json:"confidence"`
	Eligibility    EligibilityDTO     `json:"eligibility"`
	Collections    []CollectionDTO    `json:"collections"`
	AnomalyFlags   []FlagDTO          `json:"anomalyFlags"`
	Snapshots      SnapshotPolicyDTO  `json:"snapshots"`
	Formula        string             `json:"formula"`
	LastUpdated    string             `json:"lastUpdated"`
	DemoMode       bool               `json:"demoMode"`
}

// WindowDTO documents one ranking window.
type WindowDTO struct {
	Name               string `json:"name"`
	Duration           string `json:"duration"`
	BaselineTolerance  string `json:"baselineTolerance"`
	MinObservedHistory string `json:"minObservedHistory"`
	StaleAfter         string `json:"staleAfter"`
}

// ConfidenceDTO documents the confidence model.
type ConfidenceDTO struct {
	Description string             `json:"description"`
	Weights     map[string]float64 `json:"weights"`
	Labels      map[string]string  `json:"labels"`
	Penalties   map[string]float64 `json:"penalties"`
}

// EligibilityDTO documents the eligibility rules.
type EligibilityDTO struct {
	MinStars             int      `json:"minStars"`
	MinDescriptionLength int      `json:"minDescriptionLength"`
	ExcludedStates       []string `json:"excludedStates"`
	Rules                []string `json:"rules"`
}

// CollectionDTO documents a collection's membership rule.
type CollectionDTO struct {
	Name          string `json:"name"`
	DefaultWindow string `json:"defaultWindow"`
	Rule          string `json:"rule"`
}

// FlagDTO documents an anomaly flag.
type FlagDTO struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}

// SnapshotPolicyDTO documents how history is collected.
type SnapshotPolicyDTO struct {
	Description string `json:"description"`
	Interval    string `json:"interval"`
	Source      string `json:"source"`
	// PrivacyNote is part of the contract, not decoration: the absence of
	// stargazer data is a product commitment.
	PrivacyNote string `json:"privacyNote"`
}

// HealthResponse is the /healthz payload.
type HealthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
	Version string `json:"version"`
}

// ReadyResponse is the /readyz payload.
type ReadyResponse struct {
	Status   string            `json:"status"`
	Checks   map[string]string `json:"checks"`
	DemoMode bool              `json:"demoMode"`
}

// --- mapping -----------------------------------------------------------------

func toRepositoryDTO(r repository.Repository, latest *repository.Snapshot) RepositoryDTO {
	dto := RepositoryDTO{
		Owner:       r.OwnerLogin,
		Name:        r.Name,
		FullName:    r.FullName,
		Description: r.Description,
		URL:         r.HTMLURL,
		Topics:      nonNilStrings(r.Topics),
		IsArchived:  r.IsArchived,
		IsFork:      r.IsFork,
		CreatedAt:   r.CreatedAt,
		PushedAt:    r.PushedAt,
	}

	dto.HomepageURL = optionalString(r.HomepageURL)
	dto.AvatarURL = optionalString(r.AvatarURL)
	dto.Language = optionalString(r.PrimaryLanguage)
	dto.License = optionalString(r.LicenseSPDX)

	if latest != nil {
		dto.Stars = latest.Stars
		dto.Forks = latest.Forks
		dto.OpenIssues = latest.OpenIssues
	}
	return dto
}

func toMomentumDTO(s repository.MomentumScore) MomentumDTO {
	// The label is derived from the same rounded value that is published, not
	// from the raw one. Otherwise a confidence of 0.8497 would be rendered as
	// "0.85" next to the label "medium", which reads as a contradiction.
	confidence := round3(s.Confidence)

	return MomentumDTO{
		Window:          s.Window,
		Score:           round1(s.Score),
		Rank:            s.Rank,
		StarDelta:       s.StarDelta,
		StarVelocity:    round3(s.StarVelocity),
		RelativeGrowth:  round4(s.RelativeGrowth),
		Acceleration:    round4(s.Acceleration),
		ForkDelta:       s.ForkDelta,
		ForkVelocity:    round3(s.ForkVelocity),
		ActivityScore:   round4(s.ActivityScore),
		FreshnessScore:  round4(s.FreshnessScore),
		Confidence:      confidence,
		ConfidenceLabel: scoring.ConfidenceLabel(confidence),
		Reasons:         reasonTexts(s),
		Flags:           nonNilStrings(s.AnomalyFlags),
		Collections:     nonNilStrings(s.Collections),
		FormulaVersion:  s.FormulaVersion,
		ScoredAt:        s.ScoredAt,
	}
}

// reasonTexts recovers the rendered explanation sentences from feature_data.
//
// The sentences are generated once at scoring time and stored, so the API never
// re-derives them: what a user reads is exactly what was computed for that run.
func reasonTexts(s repository.MomentumScore) []string {
	raw, ok := s.FeatureData["reasons"]
	if !ok {
		return []string{}
	}

	entries, ok := raw.([]any)
	if !ok {
		return []string{}
	}

	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		object, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := object["text"].(string); ok && text != "" {
			out = append(out, text)
		}
	}
	return out
}

func toHistoryPointDTOs(points []repository.HistoryPoint) []HistoryPointDTO {
	out := make([]HistoryPointDTO, 0, len(points))
	for _, p := range points {
		out = append(out, HistoryPointDTO{
			At:        p.At,
			Stars:     p.Stars,
			Forks:     p.Forks,
			StarDelta: p.StarDelta,
			ForkDelta: p.ForkDelta,
			Score:     p.Score,
		})
	}
	return out
}

func summariseHistory(points []repository.HistoryPoint) CompareSummaryDTO {
	if len(points) == 0 {
		return CompareSummaryDTO{}
	}

	first, last := points[0], points[len(points)-1]
	summary := CompareSummaryDTO{
		StartStars:     first.Stars,
		EndStars:       last.Stars,
		AbsoluteGrowth: last.Stars - first.Stars,
		ForkGrowth:     last.Forks - first.Forks,
	}
	// The same floor the scoring engine uses, so the comparison page and the
	// leaderboard describe growth the same way.
	base := first.Stars
	if base < 20 {
		base = 20
	}
	summary.RelativeGrowth = round4(float64(summary.AbsoluteGrowth) / float64(base))
	return summary
}

func optionalString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func round1(v float64) float64 { return roundTo(v, 1) }
func round3(v float64) float64 { return roundTo(v, 3) }
func round4(v float64) float64 { return roundTo(v, 4) }

func roundTo(v float64, places int) float64 {
	factor := 1.0
	for range places {
		factor *= 10
	}
	return float64(int64(v*factor+copySign(0.5, v))) / factor
}

func copySign(magnitude, sign float64) float64 {
	if sign < 0 {
		return -magnitude
	}
	return magnitude
}
