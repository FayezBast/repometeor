// Package demo builds RepoMeteor's deterministic demo dataset.
//
// Everything here is fictional. Owners, repository names and descriptions are
// invented, and the numbers come from a seeded generator rather than from
// GitHub. Snapshots written from this package carry source='demo_seed' so that
// no demo row can ever be mistaken for a real observation, and the web UI shows
// a permanent "Demo data" indicator whenever the API reports demo mode.
//
// The dataset is a product surface in its own right: it has to demonstrate
// every ranking behaviour — small-and-fast beating large-and-slow, hidden gems,
// new entrants, low confidence, anomaly flags — so that a first run of
// `docker compose up` shows what RepoMeteor actually does.
package demo

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"
)

// Pattern describes how a repository's star count evolves.
type Pattern string

const (
	// PatternLinear grows at a steady rate.
	PatternLinear Pattern = "linear"
	// PatternAccelerating grows faster as the window progresses.
	PatternAccelerating Pattern = "accelerating"
	// PatternDecelerating grows quickly then tapers off.
	PatternDecelerating Pattern = "decelerating"
	// PatternFlat does not move.
	PatternFlat Pattern = "flat"
	// PatternDeclining loses stars.
	PatternDeclining Pattern = "declining"
	// PatternSpike jumps sharply in a single interval.
	PatternSpike Pattern = "spike"
)

// Coverage describes how complete a repository's snapshot history is.
type Coverage string

const (
	// CoverageFull has an unbroken hourly series across the whole period.
	CoverageFull Coverage = "full"
	// CoverageGap is missing a contiguous stretch of hours.
	CoverageGap Coverage = "gap"
	// CoverageShort only has the most recent few hours: a new entrant.
	CoverageShort Coverage = "short"
	// CoverageStale stopped being collected some time ago.
	CoverageStale Coverage = "stale"
)

// Repository is one fictional repository in the dataset.
type Repository struct {
	GitHubID      int64
	Owner         string
	Name          string
	Description   string
	Language      string
	Topics        []string
	LicenseSPDX   string
	HomepageURL   string
	DefaultBranch string

	// StartStars is the star count at the beginning of the generated period.
	StartStars int
	// EndStars is the star count now.
	EndStars int
	// StartForks and EndForks bracket fork growth.
	StartForks int
	EndForks   int
	OpenIssues int

	Pattern  Pattern
	Coverage Coverage

	IsFork     bool
	IsArchived bool
	IsDisabled bool
	IsTemplate bool

	// AgeDays is how long ago the repository was created.
	AgeDays int
	// PushedHoursAgo is how recently the default branch was updated.
	PushedHoursAgo int
	// ReleaseDaysAgo is how long ago a release was published; negative means
	// the repository has never published one.
	ReleaseDaysAgo int

	// Note explains what this entry demonstrates. It is documentation for
	// contributors, not user-facing copy.
	Note string
}

// FullName renders "owner/name".
func (r Repository) FullName() string { return r.Owner + "/" + r.Name }

// HTMLURL renders the fictional GitHub URL.
func (r Repository) HTMLURL() string { return "https://github.com/" + r.FullName() }

// AvatarURL is deliberately empty: the demo dataset references no external
// images, so the UI exercises its initials fallback and the page has no
// third-party requests.
func (r Repository) AvatarURL() string { return "" }

// Snapshot is one generated observation.
type Snapshot struct {
	At     time.Time
	Stars  int
	Forks  int
	Issues int
}

// Period is how much history the generator produces. Eight days is just over
// the 7d window, so every ranking window has a genuine baseline.
const Period = 8 * 24 * time.Hour

// SnapshotInterval is the spacing of generated snapshots.
const SnapshotInterval = time.Hour

// Repositories returns the fixed demo catalogue.
//
// The list is a literal rather than generated, because each entry exists to
// demonstrate a specific behaviour and that intent should be readable.
func Repositories() []Repository {
	repos := []Repository{
		// --- Strong risers, small to mid size ------------------------------
		{
			Owner: "orbit-labs", Name: "signal-kit",
			Description: "Typed event streams for distributed Go services",
			Language:    "Go", Topics: []string{"developer-tools", "distributed-systems", "events"},
			LicenseSPDX: "Apache-2.0", HomepageURL: "https://example.invalid/signal-kit",
			StartStars: 928, EndStars: 1240, StartForks: 69, EndForks: 81, OpenIssues: 12,
			Pattern: PatternAccelerating, Coverage: CoverageFull,
			AgeDays: 210, PushedHoursAgo: 4, ReleaseDaysAgo: 3,
			Note: "Headline riser: strong accelerating growth with recent activity.",
		},
		{
			Owner: "northstar-dev", Name: "terminal-ai",
			Description: "A local-first terminal assistant with pluggable model backends",
			Language:    "Rust", Topics: []string{"cli", "developer-tools", "local-first"},
			LicenseSPDX: "MIT",
			StartStars:  536, EndStars: 680, StartForks: 30, EndForks: 44, OpenIssues: 21,
			Pattern: PatternAccelerating, Coverage: CoverageFull,
			AgeDays: 95, PushedHoursAgo: 2, ReleaseDaysAgo: 6,
			Note: "Second-place riser; younger project so freshness contributes.",
		},
		{
			Owner: "comet-tools", Name: "local-indexer",
			Description: "Incremental code search index that runs entirely on your machine",
			Language:    "Go", Topics: []string{"search", "developer-tools", "local-first"},
			LicenseSPDX: "MPL-2.0",
			StartStars:  137, EndStars: 213, StartForks: 8, EndForks: 17, OpenIssues: 6,
			Pattern: PatternLinear, Coverage: CoverageFull,
			AgeDays: 48, PushedHoursAgo: 9, ReleaseDaysAgo: 11,
			Note: "Hidden gem: small base, high relative growth.",
		},
		{
			Owner: "driftwood", Name: "schema-drift",
			Description: "Detects breaking changes between database migrations in CI",
			Language:    "Python", Topics: []string{"database", "ci", "developer-tools"},
			LicenseSPDX: "Apache-2.0",
			StartStars:  61, EndStars: 148, StartForks: 4, EndForks: 12, OpenIssues: 9,
			Pattern: PatternAccelerating, Coverage: CoverageFull,
			AgeDays: 33, PushedHoursAgo: 1, ReleaseDaysAgo: 2,
			Note: "Hidden gem with the strongest relative growth in the set.",
		},
		{
			Owner: "lanternfish", Name: "tracewire",
			Description: "Zero-config distributed tracing for small teams",
			Language:    "TypeScript", Topics: []string{"observability", "tracing", "developer-tools"},
			LicenseSPDX: "MIT",
			StartStars:  289, EndStars: 402, StartForks: 19, EndForks: 28, OpenIssues: 14,
			Pattern: PatternLinear, Coverage: CoverageFull,
			AgeDays: 140, PushedHoursAgo: 6, ReleaseDaysAgo: 9,
			Note: "Steady mid-size riser.",
		},

		// --- Large, established --------------------------------------------
		{
			Owner: "meridian-systems", Name: "quarry",
			Description: "Columnar analytics engine with a SQL front end",
			Language:    "C++", Topics: []string{"database", "analytics", "sql"},
			LicenseSPDX: "Apache-2.0",
			StartStars:  51820, EndStars: 52410, StartForks: 4102, EndForks: 4160, OpenIssues: 812,
			Pattern: PatternLinear, Coverage: CoverageFull,
			AgeDays: 2100, PushedHoursAgo: 3, ReleaseDaysAgo: 8,
			Note: "Established mover: very large but still gaining meaningfully.",
		},
		{
			Owner: "harborline", Name: "vessel",
			Description: "Container build tooling that produces reproducible images",
			Language:    "Go", Topics: []string{"containers", "build-tools", "reproducible-builds"},
			LicenseSPDX: "Apache-2.0",
			StartStars:  18740, EndStars: 18960, StartForks: 1502, EndForks: 1519, OpenIssues: 340,
			Pattern: PatternLinear, Coverage: CoverageFull,
			AgeDays: 1450, PushedHoursAgo: 11, ReleaseDaysAgo: 20,
			Note: "Established mover with moderate velocity.",
		},
		{
			Owner: "stonebridge", Name: "atlas-ui",
			Description: "Accessible React component library with a strict design system",
			Language:    "TypeScript", Topics: []string{"react", "design-system", "accessibility"},
			LicenseSPDX: "MIT",
			StartStars:  24310, EndStars: 24355, StartForks: 1890, EndForks: 1893, OpenIssues: 275,
			Pattern: PatternDecelerating, Coverage: CoverageFull,
			AgeDays: 1720, PushedHoursAgo: 30, ReleaseDaysAgo: 45,
			Note: "Large and slow: must rank below small fast movers.",
		},
		{
			Owner: "ironwood-labs", Name: "sentinel-lint",
			Description: "Static analysis for infrastructure-as-code templates",
			Language:    "Go", Topics: []string{"static-analysis", "infrastructure", "security"},
			LicenseSPDX: "Apache-2.0",
			StartStars:  11240, EndStars: 11390, StartForks: 720, EndForks: 731, OpenIssues: 158,
			Pattern: PatternLinear, Coverage: CoverageFull,
			AgeDays: 980, PushedHoursAgo: 7, ReleaseDaysAgo: 14,
			Note: "Just above the established-movers threshold.",
		},

		// --- Decelerating and flat -----------------------------------------
		{
			Owner: "quiet-harbor", Name: "flatline-db",
			Description: "Embedded key-value store with append-only storage",
			Language:    "Rust", Topics: []string{"database", "embedded", "storage"},
			LicenseSPDX: "MIT",
			StartStars:  844, EndStars: 846, StartForks: 51, EndForks: 51, OpenIssues: 33,
			Pattern: PatternFlat, Coverage: CoverageFull,
			AgeDays: 620, PushedHoursAgo: 200, ReleaseDaysAgo: 120,
			Note: "Essentially static: exercises the bottom of the ranking.",
		},
		{
			Owner: "paperlantern", Name: "slowburn",
			Description: "Background job runner with at-least-once delivery semantics",
			Language:    "Ruby", Topics: []string{"jobs", "queue", "backend"},
			LicenseSPDX: "MIT",
			StartStars:  1902, EndStars: 1888, StartForks: 140, EndForks: 139, OpenIssues: 61,
			Pattern: PatternDeclining, Coverage: CoverageFull,
			AgeDays: 1100, PushedHoursAgo: 400, ReleaseDaysAgo: 210,
			Note: "Negative star delta: must not produce a negative velocity.",
		},
		{
			Owner: "duskwing", Name: "fading-charts",
			Description: "Charting primitives for dashboards, now in maintenance mode",
			Language:    "JavaScript", Topics: []string{"charts", "visualization", "frontend"},
			LicenseSPDX: "BSD-3-Clause",
			StartStars:  3410, EndStars: 3402, StartForks: 260, EndForks: 259, OpenIssues: 95,
			Pattern: PatternDeclining, Coverage: CoverageFull,
			AgeDays: 1600, PushedHoursAgo: 900, ReleaseDaysAgo: 400,
			Note: "Second declining example, larger base.",
		},

		// --- Data-quality edge cases ---------------------------------------
		{
			Owner: "brightfield", Name: "sudden-spark",
			Description: "Prompt templating library with a small runtime",
			Language:    "Python", Topics: []string{"templating", "developer-tools"},
			LicenseSPDX: "MIT",
			StartStars:  180, EndStars: 520, StartForks: 12, EndForks: 12, OpenIssues: 3,
			Pattern: PatternSpike, Coverage: CoverageFull,
			AgeDays: 300, PushedHoursAgo: 2400, ReleaseDaysAgo: -1,
			Note: "Unusual growth pattern: large star gain, zero fork growth, stale repository.",
		},
		{
			Owner: "halfmoon", Name: "patchy-collector",
			Description: "Metrics collector for edge deployments with intermittent connectivity",
			Language:    "Go", Topics: []string{"observability", "metrics", "edge"},
			LicenseSPDX: "Apache-2.0",
			StartStars:  455, EndStars: 588, StartForks: 30, EndForks: 39, OpenIssues: 18,
			Pattern: PatternLinear, Coverage: CoverageGap,
			AgeDays: 260, PushedHoursAgo: 5, ReleaseDaysAgo: 12,
			Note: "Snapshot gap: exercises the snapshot_gap flag and reduced confidence.",
		},
		{
			Owner: "firstlight", Name: "brand-new-runner",
			Description: "Minimal task runner configured entirely in TOML",
			Language:    "Rust", Topics: []string{"cli", "build-tools", "developer-tools"},
			LicenseSPDX: "MIT",
			StartStars:  38, EndStars: 96, StartForks: 2, EndForks: 6, OpenIssues: 2,
			Pattern: PatternAccelerating, Coverage: CoverageShort,
			AgeDays: 9, PushedHoursAgo: 1, ReleaseDaysAgo: 1,
			Note: "New entrant: only a few hours of history, low confidence, not in the main leaderboard.",
		},
		{
			Owner: "twilight-works", Name: "abandoned-cache",
			Description: "In-memory cache with TTL support; no longer maintained",
			Language:    "Java", Topics: []string{"caching", "performance"},
			LicenseSPDX: "Apache-2.0",
			StartStars:  2210, EndStars: 2215, StartForks: 190, EndForks: 190, OpenIssues: 140,
			Pattern: PatternFlat, Coverage: CoverageStale,
			AgeDays: 2400, PushedHoursAgo: 4000, ReleaseDaysAgo: 900,
			Note: "Stale collection: newest snapshot is old, so the repository drops out of rankings.",
		},

		// --- Structurally excluded ------------------------------------------
		{
			Owner: "museum-piece", Name: "archived-toolkit",
			Description: "Historical build toolkit, archived by its maintainers",
			Language:    "Shell", Topics: []string{"build-tools", "legacy"},
			LicenseSPDX: "MIT",
			StartStars:  6100, EndStars: 6480, StartForks: 410, EndForks: 418, OpenIssues: 0,
			Pattern: PatternLinear, Coverage: CoverageFull,
			IsArchived: true,
			AgeDays:    2800, PushedHoursAgo: 8000, ReleaseDaysAgo: 1200,
			Note: "Archived: must never appear in rankings despite real growth.",
		},
		{
			Owner: "copycat-dev", Name: "vessel",
			Description: "A fork of harborline/vessel with local patches",
			Language:    "Go", Topics: []string{"containers", "build-tools"},
			LicenseSPDX: "Apache-2.0",
			StartStars:  1240, EndStars: 1490, StartForks: 60, EndForks: 66, OpenIssues: 4,
			Pattern: PatternLinear, Coverage: CoverageFull,
			IsFork:  true,
			AgeDays: 400, PushedHoursAgo: 20, ReleaseDaysAgo: -1,
			Note: "Fork: excluded from rankings.",
		},
		{
			Owner: "shuttered", Name: "disabled-service",
			Description: "Service scaffolding project that has been disabled",
			Language:    "PHP", Topics: []string{"backend", "scaffolding"},
			LicenseSPDX: "MIT",
			StartStars:  740, EndStars: 812, StartForks: 44, EndForks: 47, OpenIssues: 12,
			Pattern: PatternLinear, Coverage: CoverageFull,
			IsDisabled: true,
			AgeDays:    900, PushedHoursAgo: 600, ReleaseDaysAgo: -1,
			Note: "Disabled: excluded from rankings.",
		},
		{
			Owner: "sparse-docs", Name: "nodesc",
			Description: "",
			Language:    "Kotlin", Topics: []string{"android"},
			LicenseSPDX: "",
			StartStars:  310, EndStars: 402, StartForks: 20, EndForks: 24, OpenIssues: 7,
			Pattern: PatternLinear, Coverage: CoverageFull,
			AgeDays: 500, PushedHoursAgo: 15, ReleaseDaysAgo: -1,
			Note: "No description: excluded so that unexplained entries never reach the leaderboard.",
		},
		{
			Owner: "tinystart", Name: "two-stars",
			Description: "An experiment in minimal HTTP routing",
			Language:    "Go", Topics: []string{"http", "experiments"},
			LicenseSPDX: "MIT",
			StartStars:  2, EndStars: 4, StartForks: 0, EndForks: 0, OpenIssues: 0,
			Pattern: PatternLinear, Coverage: CoverageFull,
			AgeDays: 20, PushedHoursAgo: 40, ReleaseDaysAgo: -1,
			Note: "Below the five-star floor: excluded.",
		},
	}

	// The remaining entries fill out the catalogue so that percentile
	// normalisation has a realistic distribution to work against, and so that
	// language and topic filters return meaningful counts.
	repos = append(repos, generatedFillers()...)
	return repos
}

// fillerSpec is a compact description used to build the remainder of the set.
type fillerSpec struct {
	owner, name, description, language string
	topics                             []string
	licence                            string
	startStars, endStars               int
	startForks, endForks               int
	pattern                            Pattern
	ageDays, pushedHoursAgo            int
	releaseDaysAgo                     int
}

func generatedFillers() []Repository {
	specs := []fillerSpec{
		{"cinderpath", "flux-router", "File-based routing for server-rendered applications", "TypeScript",
			[]string{"web-framework", "frontend", "routing"}, "MIT", 3120, 3380, 210, 224, PatternLinear, 430, 5, 7},
		{"willowbank", "ledgerly", "Double-entry accounting primitives with strict typing", "TypeScript",
			[]string{"fintech", "accounting", "developer-tools"}, "MIT", 890, 1012, 55, 63, PatternAccelerating, 180, 3, 4},
		{"saltmarsh", "pipeflow", "Declarative data pipelines with local execution", "Python",
			[]string{"data-engineering", "etl", "developer-tools"}, "Apache-2.0", 4210, 4405, 380, 391, PatternLinear, 700, 12, 18},
		{"emberglass", "shardwright", "Consistent hashing and shard placement toolkit", "Go",
			[]string{"distributed-systems", "sharding"}, "MIT", 620, 742, 41, 49, PatternAccelerating, 150, 8, 5},
		{"tallgrass", "yamlpatch", "Structural patching for YAML documents", "Go",
			[]string{"yaml", "configuration", "cli"}, "Apache-2.0", 214, 268, 14, 19, PatternLinear, 88, 16, 25},
		{"riverstone", "quicklaunch", "Cold-start optimiser for serverless runtimes", "Rust",
			[]string{"serverless", "performance"}, "MIT", 1580, 1742, 96, 108, PatternAccelerating, 240, 2, 3},
		{"birchline", "typedsql", "Compile-time checked SQL for typed languages", "Rust",
			[]string{"database", "sql", "type-safety"}, "MIT", 2940, 3105, 180, 190, PatternLinear, 380, 6, 10},
		{"foxglove-io", "chartsmith", "Composable chart primitives for the web", "JavaScript",
			[]string{"charts", "visualization", "frontend"}, "MIT", 1120, 1198, 88, 92, PatternDecelerating, 560, 22, 40},
		{"pinegrove", "webhookd", "Reliable webhook delivery with replay and dead-lettering", "Go",
			[]string{"webhooks", "backend", "reliability"}, "Apache-2.0", 468, 561, 27, 35, PatternLinear, 120, 4, 6},
		{"aurorabyte", "vectorlite", "Small vector index with an embeddable file format", "C++",
			[]string{"search", "embeddings", "database"}, "Apache-2.0", 7220, 7590, 520, 541, PatternAccelerating, 320, 1, 2},
		{"greyharbor", "portmap", "Service discovery for single-host deployments", "Go",
			[]string{"networking", "service-discovery"}, "MIT", 342, 389, 22, 26, PatternLinear, 210, 18, 30},
		{"kestrelworks", "swiftforms", "Declarative form validation for SwiftUI", "Swift",
			[]string{"ios", "swiftui", "forms"}, "MIT", 980, 1104, 62, 71, PatternAccelerating, 260, 5, 8},
		{"mistvalley", "androidkit", "Modular Android build conventions", "Kotlin",
			[]string{"android", "build-tools", "gradle"}, "Apache-2.0", 1640, 1712, 130, 134, PatternLinear, 640, 14, 21},
		{"cobbleworks", "phpstan-rules", "Additional static analysis rules for PHP projects", "PHP",
			[]string{"static-analysis", "php", "quality"}, "MIT", 512, 548, 40, 42, PatternLinear, 480, 26, 35},
		{"amberfield", "railsprobe", "Runtime introspection for Ruby web applications", "Ruby",
			[]string{"observability", "ruby", "debugging"}, "MIT", 730, 803, 48, 54, PatternLinear, 350, 9, 13},
		{"heronbay", "jvmwatch", "Low-overhead JVM allocation profiler", "Java",
			[]string{"performance", "jvm", "profiling"}, "Apache-2.0", 2380, 2506, 175, 183, PatternAccelerating, 590, 7, 9},
		{"clearcut", "netshape", "Traffic shaping and fault injection for integration tests", "Go",
			[]string{"testing", "networking", "chaos-engineering"}, "Apache-2.0", 1290, 1401, 84, 92, PatternLinear, 300, 10, 15},
		{"opalgrove", "csvsurgeon", "Streaming CSV transformation with a tiny memory footprint", "Rust",
			[]string{"cli", "data-engineering", "csv"}, "MIT", 186, 241, 10, 15, PatternAccelerating, 60, 3, 2},
		{"lightkeeper", "authshim", "Adapters that unify session handling across frameworks", "TypeScript",
			[]string{"authentication", "backend", "developer-tools"}, "MIT", 1960, 2088, 118, 127, PatternLinear, 410, 11, 16},
		{"deepfern", "notebookdiff", "Meaningful diffs for computational notebooks", "Python",
			[]string{"notebooks", "diff", "developer-tools"}, "BSD-3-Clause", 640, 731, 39, 46, PatternAccelerating, 170, 6, 5},
		{"steelthread", "wasmpack-lite", "Minimal packaging for WebAssembly modules", "Rust",
			[]string{"webassembly", "build-tools"}, "MIT", 448, 512, 28, 33, PatternLinear, 195, 13, 19},
		{"marblearch", "gridlayout", "Constraint-based layout engine for canvas rendering", "TypeScript",
			[]string{"layout", "canvas", "frontend"}, "MIT", 296, 334, 18, 21, PatternDecelerating, 520, 28, 60},
		{"hollowpine", "shellcheckplus", "Extended shell script linting with project conventions", "Shell",
			[]string{"shell", "static-analysis", "cli"}, "GPL-3.0", 158, 197, 9, 13, PatternLinear, 105, 20, 33},
		{"canvasbrook", "sqlreplay", "Deterministic replay of production query workloads", "Go",
			[]string{"database", "testing", "performance"}, "Apache-2.0", 862, 968, 54, 62, PatternAccelerating, 230, 4, 7},
		{"lowtide", "featurekit", "Feature flags evaluated entirely on the client", "TypeScript",
			[]string{"feature-flags", "frontend", "developer-tools"}, "MIT", 1420, 1533, 91, 99, PatternLinear, 275, 8, 11},
		{"granitefall", "pgbouncer-metrics", "Prometheus exporter for connection pool statistics", "Go",
			[]string{"observability", "postgresql", "metrics"}, "Apache-2.0", 384, 425, 24, 27, PatternLinear, 460, 24, 44},
		{"tidewater", "asyncqueue", "Structured concurrency helpers for Python services", "Python",
			[]string{"concurrency", "asyncio", "backend"}, "MIT", 2140, 2298, 160, 171, PatternAccelerating, 330, 5, 6},
		{"pinnaclerow", "gradlecache", "Shared remote build cache with content addressing", "Java",
			[]string{"build-tools", "gradle", "caching"}, "Apache-2.0", 706, 774, 46, 51, PatternLinear, 380, 17, 27},
		{"echoford", "csharpmock", "Source-generated test doubles for C# projects", "C#",
			[]string{"testing", "dotnet", "code-generation"}, "MIT", 1080, 1176, 68, 75, PatternLinear, 290, 12, 14},
		{"velvetmoss", "blazorforms", "Form components with server-side validation for Blazor", "C#",
			[]string{"dotnet", "forms", "web-framework"}, "MIT", 522, 588, 33, 38, PatternAccelerating, 200, 6, 9},
		{"quartzline", "swiftlog", "Structured logging with zero-cost disabled levels", "Swift",
			[]string{"ios", "logging", "performance"}, "Apache-2.0", 396, 441, 25, 29, PatternLinear, 250, 19, 28},
		{"thistledown", "jsonschema-gen", "Generates JSON Schema from typed source definitions", "TypeScript",
			[]string{"json-schema", "code-generation", "developer-tools"}, "MIT", 1750, 1866, 105, 113, PatternLinear, 360, 9, 12},
		{"beaconhill", "ratelimiter", "Composable rate limiting middleware for HTTP services", "Go",
			[]string{"http", "rate-limiting", "backend"}, "MIT", 1310, 1428, 82, 91, PatternAccelerating, 215, 3, 4},
		{"silverbrook", "diffviewer", "Side-by-side diff rendering with syntax awareness", "JavaScript",
			[]string{"diff", "frontend", "developer-tools"}, "MIT", 940, 1006, 60, 65, PatternLinear, 470, 15, 23},
		{"nightjar-io", "cronparse", "Human-readable cron expression parsing and description", "Python",
			[]string{"cron", "scheduling", "cli"}, "MIT", 268, 312, 16, 20, PatternLinear, 130, 21, 31},
		{"amberline", "querybudget", "Query cost estimation and budgeting for GraphQL servers", "TypeScript",
			[]string{"graphql", "performance", "backend"}, "Apache-2.0", 590, 682, 36, 43, PatternAccelerating, 145, 5, 6},
	}

	out := make([]Repository, 0, len(specs))
	for _, s := range specs {
		out = append(out, Repository{
			Owner: s.owner, Name: s.name, Description: s.description,
			Language: s.language, Topics: s.topics, LicenseSPDX: s.licence,
			StartStars: s.startStars, EndStars: s.endStars,
			StartForks: s.startForks, EndForks: s.endForks,
			OpenIssues: (s.endStars / 40) + 3,
			Pattern:    s.pattern, Coverage: CoverageFull,
			AgeDays: s.ageDays, PushedHoursAgo: s.pushedHoursAgo, ReleaseDaysAgo: s.releaseDaysAgo,
			Note: "Catalogue filler: provides a realistic distribution for percentile normalisation.",
		})
	}
	return out
}

// Dataset is the fully generated demo data.
type Dataset struct {
	// ReferenceTime is the "now" the dataset was generated against. Every
	// timestamp is derived from it, so a fixed reference time makes the whole
	// dataset reproducible.
	ReferenceTime time.Time
	Repositories  []Repository
	// Snapshots is keyed by repository index into Repositories.
	Snapshots [][]Snapshot
}

// Build generates the complete dataset against a reference time.
//
// The generator is seeded per repository from its index, so the same reference
// time always produces byte-identical output — including on a different machine
// and a different Go version, because math/rand/v2's PCG source is specified.
func Build(referenceTime time.Time) Dataset {
	referenceTime = referenceTime.UTC().Truncate(SnapshotInterval)

	repos := Repositories()
	dataset := Dataset{
		ReferenceTime: referenceTime,
		Repositories:  make([]Repository, 0, len(repos)),
		Snapshots:     make([][]Snapshot, 0, len(repos)),
	}

	for i, repo := range repos {
		// Stable, unique fictional GitHub IDs.
		repo.GitHubID = int64(9_000_000 + i*17)
		if repo.DefaultBranch == "" {
			repo.DefaultBranch = "main"
		}

		dataset.Repositories = append(dataset.Repositories, repo)
		dataset.Snapshots = append(dataset.Snapshots, generateSnapshots(referenceTime, repo, i))
	}
	return dataset
}

// generateSnapshots produces the hourly series for one repository.
func generateSnapshots(now time.Time, repo Repository, index int) []Snapshot {
	//nolint:gosec // deterministic generator; not cryptography
	rng := rand.New(rand.NewPCG(uint64(index)+1, 0x5EED))

	points := int(Period / SnapshotInterval)
	start := now.Add(-Period)

	// Coverage decides which of the hourly slots actually produce a snapshot.
	include := coverageFilter(repo.Coverage, points, now, start)

	snapshots := make([]Snapshot, 0, points+1)
	for i := 0; i <= points; i++ {
		at := start.Add(time.Duration(i) * SnapshotInterval)
		if !include(i, at) {
			continue
		}

		progress := float64(i) / float64(points)
		shaped := shape(repo.Pattern, progress)

		stars := interpolate(repo.StartStars, repo.EndStars, shaped)
		forks := interpolate(repo.StartForks, repo.EndForks, shaped)

		// A small amount of deterministic noise stops every series looking
		// mechanically smooth, without ever pushing a count below zero or past
		// the declared endpoints.
		if i > 0 && i < points {
			stars += int(math.Round(rng.NormFloat64() * 1.5))
			stars = clampInt(stars, minInt(repo.StartStars, repo.EndStars), maxInt(repo.StartStars, repo.EndStars))
		}

		snapshots = append(snapshots, Snapshot{
			At:     at,
			Stars:  maxInt(stars, 0),
			Forks:  maxInt(forks, 0),
			Issues: maxInt(repo.OpenIssues+int(math.Round(rng.NormFloat64())), 0),
		})
	}
	return snapshots
}

// coverageFilter returns a predicate deciding whether slot i is collected.
func coverageFilter(coverage Coverage, points int, now, start time.Time) func(int, time.Time) bool {
	switch coverage {
	case CoverageGap:
		// Drop a contiguous 14-hour stretch inside the last two days, which is
		// long enough to trip the snapshot_gap flag on the 24h window.
		gapStart := points - 40
		gapEnd := points - 26
		return func(i int, _ time.Time) bool { return i < gapStart || i > gapEnd }

	case CoverageShort:
		// Only the most recent five hours exist: a genuinely new entrant.
		cutoff := now.Add(-5 * time.Hour)
		return func(_ int, at time.Time) bool { return !at.Before(cutoff) }

	case CoverageStale:
		// Collection stopped twelve hours ago. That is deliberately chosen to
		// sit inside the 24h window's lead-in but beyond its eight-hour
		// staleness allowance, so this repository demonstrates the stale_data
		// exclusion rather than simply having no snapshots to look at.
		cutoff := now.Add(-12 * time.Hour)
		return func(_ int, at time.Time) bool { return at.Before(cutoff) }

	default:
		_ = start
		return func(int, time.Time) bool { return true }
	}
}

// shape maps linear progress through the period onto a growth curve in [0, 1].
func shape(pattern Pattern, progress float64) float64 {
	switch pattern {
	case PatternAccelerating:
		// Most of the gain lands late in the period.
		return math.Pow(progress, 2.4)
	case PatternDecelerating:
		// Most of the gain lands early.
		return 1 - math.Pow(1-progress, 2.4)
	case PatternFlat:
		return progress
	case PatternDeclining:
		return progress
	case PatternSpike:
		// Flat until 80% of the period, then a single sharp step.
		if progress < 0.8 {
			return progress * 0.05
		}
		return 0.04 + (progress-0.8)/0.2*0.96
	default:
		return progress
	}
}

func interpolate(from, to int, t float64) int {
	return from + int(math.Round(float64(to-from)*clamp01(t)))
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampInt(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Summary describes the dataset's coverage, used by tests and the seed command
// to prove the demo data exercises what it claims to.
type Summary struct {
	Repositories int
	Snapshots    int
	Languages    []string
	Topics       []string
	MinStars     int
	MaxStars     int
	Patterns     map[Pattern]int
	Coverages    map[Coverage]int
	Archived     int
	Forks        int
	Disabled     int
	NoRelease    int
}

// Summarise computes the dataset summary.
func Summarise(d Dataset) Summary {
	s := Summary{
		Repositories: len(d.Repositories),
		MinStars:     math.MaxInt32,
		Patterns:     map[Pattern]int{},
		Coverages:    map[Coverage]int{},
	}

	languages := map[string]struct{}{}
	topics := map[string]struct{}{}

	for i, repo := range d.Repositories {
		s.Snapshots += len(d.Snapshots[i])
		s.Patterns[repo.Pattern]++
		s.Coverages[repo.Coverage]++

		if repo.Language != "" {
			languages[repo.Language] = struct{}{}
		}
		for _, topic := range repo.Topics {
			topics[topic] = struct{}{}
		}

		s.MinStars = minInt(s.MinStars, repo.EndStars)
		s.MaxStars = maxInt(s.MaxStars, repo.EndStars)

		if repo.IsArchived {
			s.Archived++
		}
		if repo.IsFork {
			s.Forks++
		}
		if repo.IsDisabled {
			s.Disabled++
		}
		if repo.ReleaseDaysAgo < 0 {
			s.NoRelease++
		}
	}

	s.Languages = sortedKeys(languages)
	s.Topics = sortedKeys(topics)
	return s
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	// Insertion sort: the sets are small and this avoids importing sort here.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// String renders a human-readable summary for the seed command's output.
func (s Summary) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d repositories, %d snapshots\n", s.Repositories, s.Snapshots)
	fmt.Fprintf(&b, "  languages: %d (%s)\n", len(s.Languages), strings.Join(s.Languages, ", "))
	fmt.Fprintf(&b, "  topics:    %d\n", len(s.Topics))
	fmt.Fprintf(&b, "  stars:     %d to %d\n", s.MinStars, s.MaxStars)
	fmt.Fprintf(&b, "  excluded:  %d archived, %d forks, %d disabled\n", s.Archived, s.Forks, s.Disabled)
	return b.String()
}
