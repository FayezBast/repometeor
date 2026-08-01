package api

import (
	"github.com/gin-gonic/gin"

	"github.com/FayezBast/repometeor/backend/internal/scoring"
)

// methodologyLastUpdated is the date the formula last changed. It is bumped
// together with scoring.FormulaVersion.
const methodologyLastUpdated = "2026-08-01"

// handleMethodology publishes the complete scoring method.
//
// This endpoint is the product's transparency commitment expressed as an API:
// every weight, threshold and rule that produces a ranking is machine-readable
// here, so a reader can recompute any score from the feature data returned
// alongside it. Nothing about the ranking is hidden behind this response.
func (s *Server) handleMethodology(c *gin.Context) {
	weights := scoring.DefaultWeights
	opts := scoring.DefaultOptions()

	windows := make([]WindowDTO, 0, len(scoring.Windows))
	for _, w := range scoring.Windows {
		windows = append(windows, WindowDTO{
			Name:               w.Name,
			Duration:           w.Duration.String(),
			BaselineTolerance:  w.BaselineTolerance.String(),
			MinObservedHistory: w.MinObservedHistory.String(),
			StaleAfter:         w.StaleAfter.String(),
		})
	}

	response := MethodologyResponse{
		FormulaVersion: scoring.FormulaVersion,
		Description: "RepoMeteor ranks repositories by momentum measured from its own " +
			"periodic snapshots of aggregate public counters. Raw feature values are " +
			"converted to percentile ranks within the current cohort, combined with fixed " +
			"weights, and then scaled by a confidence factor that reflects how completely " +
			"the window was observed.",

		Weights: map[string]float64{
			"starVelocity":   weights.StarVelocity,
			"relativeGrowth": weights.RelativeGrowth,
			"acceleration":   weights.Acceleration,
			"forkVelocity":   weights.ForkVelocity,
			"activity":       weights.Activity,
			"freshness":      weights.Freshness,
		},

		Windows: windows,

		Confidence: ConfidenceDTO{
			Description: "Confidence measures data quality, never project quality. A low " +
				"confidence means RepoMeteor has not observed the repository for long " +
				"enough to be certain, not that the project is weak.",
			Weights: map[string]float64{
				"coverage": 0.45,
				"density":  0.25,
				"midpoint": 0.15,
				"recency":  0.15,
			},
			Labels: map[string]string{
				"high":   "at least 0.85",
				"medium": "0.60 to 0.849",
				"low":    "below 0.60",
			},
			Penalties: map[string]float64{
				"recentlyDiscovered": 0.85,
				"largeSnapshotGap":   0.85,
				"provisional":        0.55,
			},
		},

		Eligibility: EligibilityDTO{
			MinStars:             opts.MinStars,
			MinDescriptionLength: opts.MinDescriptionLength,
			ExcludedStates:       []string{"fork", "archived", "disabled", "missing", "private"},
			Rules: []string{
				"Forks, archived, disabled and missing repositories are never ranked.",
				"A repository needs at least 5 stars and a meaningful description.",
				"The newest snapshot must be recent enough for the requested window.",
				"A repository needs a baseline snapshot near the window start and enough " +
					"observed history to be compared fairly; otherwise it appears only " +
					"under New Entrants with a low confidence label.",
				"A short observation is never extrapolated to the full window length.",
			},
		},

		Collections: []CollectionDTO{
			{
				Name:          scoring.CollectionRising,
				DefaultWindow: scoring.Window6h.Name,
				Rule:          "Highest 6-hour momentum scores among fully eligible repositories.",
			},
			{
				Name:          scoring.CollectionDaily,
				DefaultWindow: scoring.Window24h.Name,
				Rule:          "Highest 24-hour momentum scores among fully eligible repositories.",
			},
			{
				Name:          scoring.CollectionWeekly,
				DefaultWindow: scoring.Window7d.Name,
				Rule:          "Highest 7-day momentum scores among fully eligible repositories.",
			},
			{
				Name:          scoring.CollectionHiddenGems,
				DefaultWindow: scoring.Window24h.Name,
				Rule: "Between 10 and 1,000 total stars, positive star growth, and at least " +
					"medium confidence.",
			},
			{
				Name:          scoring.CollectionNewEntrants,
				DefaultWindow: scoring.Window24h.Name,
				Rule: "Recently discovered repositories with measured movement but not yet " +
					"enough observed history for a standard ranking. Always low confidence.",
			},
			{
				Name:          scoring.CollectionEstablished,
				DefaultWindow: scoring.Window24h.Name,
				Rule: "More than 10,000 total stars, star velocity in the top 40% of the " +
					"cohort, and at least medium confidence.",
			},
		},

		AnomalyFlags: []FlagDTO{
			{
				Code: scoring.FlagUnusualStarForkRatio,
				Description: "Substantial star growth with no fork growth on a quiet repository. " +
					"An unusual data pattern, not an allegation about the project.",
			},
			{
				Code:        scoring.FlagStaleRepositorySpike,
				Description: "Stars rising sharply on a repository that has not been pushed to in months.",
			},
			{
				Code:        scoring.FlagInsufficientHistory,
				Description: "RepoMeteor has not observed the repository for long enough to rank it normally.",
			},
			{
				Code:        scoring.FlagLargeSingleInterval,
				Description: "Most of the window's growth landed in a single interval: a step rather than a trend.",
			},
			{
				Code:        scoring.FlagSnapshotGap,
				Description: "A stretch of the window is missing from RepoMeteor's collection.",
			},
			{
				Code:        scoring.FlagRecentlyDiscovered,
				Description: "Tracking began inside the requested window.",
			},
		},

		Snapshots: SnapshotPolicyDTO{
			Description: "RepoMeteor periodically records each tracked repository's aggregate " +
				"star, fork and issue counts. All history comes from these snapshots; " +
				"RepoMeteor has no access to counts from before it began tracking a repository.",
			Interval: s.cfg.Tracking.SnapshotBucketInterval.String(),
			Source:   "GitHub REST API repository resource (aggregate counters only)",
			PrivacyNote: "RepoMeteor does not call GitHub's stargazers endpoints and does not " +
				"collect, store or process the identity of any individual who starred, " +
				"watched or forked a repository.",
		},

		Formula: "score = 100 × (0.32·p(starVelocity) + 0.23·p(relativeGrowth) + " +
			"0.17·p(acceleration) + 0.10·p(forkVelocity) + 0.13·p(activity) + " +
			"0.05·p(freshness)) × (0.75 + 0.25·confidence), clamped to [0, 100], " +
			"where p(x) is the percentile rank of x within the current cohort.",

		LastUpdated: methodologyLastUpdated,
		DemoMode:    s.cfg.App.DemoMode,
	}

	s.respondWithETag(c, response)
}
