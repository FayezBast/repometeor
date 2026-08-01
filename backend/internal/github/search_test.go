package github

import (
	"strings"
	"testing"
	"time"
)

func TestBuildSearchQueryComponents(t *testing.T) {
	created := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	pushed := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		input SearchInput
		want  []string
	}{
		{
			name:  "language only",
			input: SearchInput{Language: "Go"},
			want:  []string{`language:"Go"`, "is:public"},
		},
		{
			name:  "created after",
			input: SearchInput{CreatedAfter: &created},
			want:  []string{"created:>=2026-07-01"},
		},
		{
			name:  "created range",
			input: SearchInput{CreatedAfter: &created, CreatedBefore: &before},
			want:  []string{"created:2026-07-01..2026-07-31"},
		},
		{
			name:  "pushed after",
			input: SearchInput{PushedAfter: &pushed},
			want:  []string{"pushed:>=2026-07-25"},
		},
		{
			name:  "star range",
			input: SearchInput{MinStars: 5, MaxStars: 20},
			want:  []string{"stars:5..20"},
		},
		{
			name:  "minimum stars only",
			input: SearchInput{MinStars: 20},
			want:  []string{"stars:>=20"},
		},
		{
			name:  "maximum stars only",
			input: SearchInput{MaxStars: 100},
			want:  []string{"stars:<=100"},
		},
		{
			name:  "exclusions",
			input: SearchInput{ExcludeForks: true, ExcludeArchived: true},
			want:  []string{"fork:false", "archived:false"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildSearchQuery(tc.input)
			for _, fragment := range tc.want {
				if !strings.Contains(got, fragment) {
					t.Errorf("query %q is missing %q", got, fragment)
				}
			}
		})
	}
}

func TestBuildSearchQueryAlwaysRestrictsToPublic(t *testing.T) {
	if got := BuildSearchQuery(SearchInput{}); !strings.Contains(got, "is:public") {
		t.Fatalf("query %q must always restrict to public repositories", got)
	}
}

func TestSanitiseLanguageRejectsInjection(t *testing.T) {
	// A corrupted configuration value must never reach GitHub's query grammar.
	unsafe := []string{
		`Go" OR stars:>1 "`,
		"Go\nstars:>1",
		"Go;rm -rf",
		"lang:Go",
		strings.Repeat("a", 100),
		"",
		"  ",
	}
	for _, language := range unsafe {
		if got := sanitiseLanguage(language); got != "" {
			t.Errorf("sanitiseLanguage(%q) = %q, want it rejected", language, got)
		}
	}
}

func TestSanitiseLanguageAcceptsRealNames(t *testing.T) {
	for _, language := range DiscoveryLanguages {
		if got := sanitiseLanguage(language); got != language {
			t.Errorf("sanitiseLanguage(%q) = %q, want it accepted unchanged", language, got)
		}
	}
	// Names with the characters real languages actually use.
	for _, language := range []string{"C++", "C#", "Objective-C", "Jupyter Notebook"} {
		if got := sanitiseLanguage(language); got != language {
			t.Errorf("sanitiseLanguage(%q) = %q, want it accepted", language, got)
		}
	}
}

func TestUnsafeLanguageIsOmittedFromTheQuery(t *testing.T) {
	got := BuildSearchQuery(SearchInput{Language: `Go" OR stars:>1 "`, MinStars: 5})
	if strings.Contains(got, "OR") {
		t.Fatalf("query %q leaked injected syntax", got)
	}
	if strings.Contains(got, "language:") {
		t.Fatalf("query %q should omit the language qualifier entirely when unsafe", got)
	}
}

func TestPlanDiscoveryCoversEveryLanguageWithBothStrategies(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	plans := PlanDiscovery(now, 30*24*time.Hour, 7*24*time.Hour)

	if len(plans) != len(DiscoveryLanguages)*2 {
		t.Fatalf("got %d plans, want %d", len(plans), len(DiscoveryLanguages)*2)
	}

	strategies := map[string]map[string]bool{}
	for _, plan := range plans {
		if strategies[plan.Input.Language] == nil {
			strategies[plan.Input.Language] = map[string]bool{}
		}
		strategies[plan.Input.Language][plan.Strategy] = true

		if !plan.Input.ExcludeForks {
			t.Errorf("%s/%s does not exclude forks", plan.Input.Language, plan.Strategy)
		}
		if !plan.Input.ExcludeArchived {
			t.Errorf("%s/%s does not exclude archived repositories", plan.Input.Language, plan.Strategy)
		}
	}

	for _, language := range DiscoveryLanguages {
		if !strategies[language][StrategyRecentlyCreated] {
			t.Errorf("%s is missing the recently-created strategy", language)
		}
		if !strategies[language][StrategyRecentlyActive] {
			t.Errorf("%s is missing the recently-active strategy", language)
		}
	}
}

func TestPlanDiscoveryUsesTheSpecifiedStarFloors(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	plans := PlanDiscovery(now, 30*24*time.Hour, 7*24*time.Hour)

	for _, plan := range plans {
		switch plan.Strategy {
		case StrategyRecentlyCreated:
			if plan.Input.MinStars != 5 {
				t.Errorf("recently-created MinStars = %d, want 5", plan.Input.MinStars)
			}
			if plan.Input.CreatedAfter == nil {
				t.Error("recently-created has no CreatedAfter bound")
			}
		case StrategyRecentlyActive:
			if plan.Input.MinStars != 20 {
				t.Errorf("recently-active MinStars = %d, want 20", plan.Input.MinStars)
			}
			if plan.Input.PushedAfter == nil {
				t.Error("recently-active has no PushedAfter bound")
			}
		}
	}
}

func TestPartitionByStarsProducesDisjointBands(t *testing.T) {
	base := SearchInput{Language: "Go", ExcludeForks: true}
	partitions := PartitionByStars(base, DefaultStarBands)

	if len(partitions) != len(DefaultStarBands) {
		t.Fatalf("got %d partitions, want %d", len(partitions), len(DefaultStarBands))
	}

	for i, p := range partitions {
		if p.Language != "Go" || !p.ExcludeForks {
			t.Errorf("partition %d lost its base criteria: %+v", i, p)
		}
		if p.Page != 1 {
			t.Errorf("partition %d starts at page %d, want 1", i, p.Page)
		}
		if i > 0 {
			previous := partitions[i-1]
			if previous.MaxStars != 0 && p.MinStars <= previous.MaxStars {
				t.Errorf("partitions %d and %d overlap: ...%d and %d...", i-1, i, previous.MaxStars, p.MinStars)
			}
		}
	}

	// The final band must be open-ended so nothing above it is missed.
	if last := partitions[len(partitions)-1]; last.MaxStars != 0 {
		t.Errorf("final band MaxStars = %d, want it open-ended", last.MaxStars)
	}
}

func TestNoDiscoveryStrategyScrapesHTML(t *testing.T) {
	// Every discovery path must go through the search API. This guards the
	// product commitment not to scrape GitHub Trending.
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	for _, plan := range PlanDiscovery(now, 30*24*time.Hour, 7*24*time.Hour) {
		query := BuildSearchQuery(plan.Input)
		if strings.Contains(query, "http") || strings.Contains(query, "trending") {
			t.Fatalf("discovery query %q references a web page rather than search qualifiers", query)
		}
	}
}
