package github

import (
	"fmt"
	"strings"
	"time"
)

// DiscoveryLanguages are the languages RepoMeteor partitions discovery across.
//
// GitHub's search API returns at most 1000 results for any single query, so a
// naive "all recent repositories" search would silently miss most of them.
// Partitioning by language — and, where needed, by creation day and star band —
// keeps each individual query comfortably inside that ceiling.
var DiscoveryLanguages = []string{
	"TypeScript",
	"Python",
	"Go",
	"Rust",
	"JavaScript",
	"Java",
	"C++",
	"C#",
	"Kotlin",
	"Swift",
	"Ruby",
	"PHP",
	"Shell",
}

// Discovery strategy names, recorded on every discovery run.
const (
	StrategyRecentlyCreated = "recently_created"
	StrategyRecentlyActive  = "recently_active"
	StrategyUserSubmitted   = "user_submitted"
)

// BuildSearchQuery renders a GitHub search query from structured criteria.
//
// The input is a struct rather than a string, so no user-supplied value can
// reach GitHub's query grammar. Language names come from the fixed
// DiscoveryLanguages list and are quoted; every other component is a number or
// a formatted date.
func BuildSearchQuery(in SearchInput) string {
	var parts []string

	if language := sanitiseLanguage(in.Language); language != "" {
		parts = append(parts, fmt.Sprintf("language:%q", language))
	}

	switch {
	case in.CreatedAfter != nil && in.CreatedBefore != nil:
		parts = append(parts, fmt.Sprintf("created:%s..%s",
			formatSearchDate(*in.CreatedAfter), formatSearchDate(*in.CreatedBefore)))
	case in.CreatedAfter != nil:
		parts = append(parts, "created:>="+formatSearchDate(*in.CreatedAfter))
	case in.CreatedBefore != nil:
		parts = append(parts, "created:<="+formatSearchDate(*in.CreatedBefore))
	}

	if in.PushedAfter != nil {
		parts = append(parts, "pushed:>="+formatSearchDate(*in.PushedAfter))
	}

	switch {
	case in.MinStars > 0 && in.MaxStars > 0:
		parts = append(parts, fmt.Sprintf("stars:%d..%d", in.MinStars, in.MaxStars))
	case in.MinStars > 0:
		parts = append(parts, fmt.Sprintf("stars:>=%d", in.MinStars))
	case in.MaxStars > 0:
		parts = append(parts, fmt.Sprintf("stars:<=%d", in.MaxStars))
	}

	if in.ExcludeForks {
		parts = append(parts, "fork:false")
	}
	if in.ExcludeArchived {
		parts = append(parts, "archived:false")
	}

	// Public repositories only; RepoMeteor never indexes anything else.
	parts = append(parts, "is:public")

	return strings.Join(parts, " ")
}

// sanitiseLanguage accepts only characters that appear in real GitHub language
// names, so a corrupted configuration value cannot inject query syntax.
func sanitiseLanguage(language string) string {
	language = strings.TrimSpace(language)
	if language == "" || len(language) > 40 {
		return ""
	}
	for _, r := range language {
		isAllowed := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '+' || r == '#' || r == ' ' || r == '-'
		if !isAllowed {
			return ""
		}
	}
	return language
}

// formatSearchDate renders a UTC date in the form GitHub search expects.
func formatSearchDate(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// DiscoveryPlan is one partitioned search to execute.
type DiscoveryPlan struct {
	Strategy string
	Input    SearchInput
}

// PlanDiscovery builds the full set of searches for one discovery run.
//
// Two strategies run per language:
//
//   - recently created: new repositories with at least five stars, which is
//     where genuinely early signals live;
//   - recently active: established repositories pushed recently with at least
//     twenty stars, which catches projects re-entering momentum.
//
// Each is bounded by a lookback window so the result count stays inside
// GitHub's 1000-result ceiling.
func PlanDiscovery(now time.Time, createdLookback, pushedLookback time.Duration) []DiscoveryPlan {
	createdAfter := now.Add(-createdLookback)
	pushedAfter := now.Add(-pushedLookback)

	plans := make([]DiscoveryPlan, 0, len(DiscoveryLanguages)*2)
	for _, language := range DiscoveryLanguages {
		plans = append(plans, DiscoveryPlan{
			Strategy: StrategyRecentlyCreated,
			Input: SearchInput{
				Language:        language,
				CreatedAfter:    &createdAfter,
				MinStars:        5,
				ExcludeForks:    true,
				ExcludeArchived: true,
				Sort:            "stars",
				Order:           "desc",
				PerPage:         100,
				Page:            1,
			},
		})
		plans = append(plans, DiscoveryPlan{
			Strategy: StrategyRecentlyActive,
			Input: SearchInput{
				Language:        language,
				PushedAfter:     &pushedAfter,
				MinStars:        20,
				ExcludeForks:    true,
				ExcludeArchived: true,
				Sort:            "updated",
				Order:           "desc",
				PerPage:         100,
				Page:            1,
			},
		})
	}
	return plans
}

// PartitionByStars splits a search into star bands.
//
// When a query reports more than 1000 total results, no amount of paging will
// reach the remainder. Splitting the star range into disjoint bands turns one
// truncated query into several complete ones.
func PartitionByStars(base SearchInput, bands [][2]int) []SearchInput {
	out := make([]SearchInput, 0, len(bands))
	for _, band := range bands {
		partition := base
		partition.MinStars = band[0]
		partition.MaxStars = band[1]
		partition.Page = 1
		out = append(out, partition)
	}
	return out
}

// DefaultStarBands partitions the star axis into ranges that each stay well
// inside GitHub's result ceiling. The final band is open-ended.
var DefaultStarBands = [][2]int{
	{5, 20},
	{21, 50},
	{51, 150},
	{151, 500},
	{501, 2000},
	{2001, 0},
}
