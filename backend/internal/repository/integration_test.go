//go:build integration

// Package repository integration tests run against a real PostgreSQL instance.
//
// They are behind the `integration` build tag so that `go test ./...` stays
// fast and dependency-free. Run them with:
//
//	make test-integration
//
// or directly:
//
//	REPOMETEOR_TEST_DATABASE_URL=postgres://... go test -tags=integration ./...
package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FayezBast/repometeor/backend/internal/clock"
	"github.com/FayezBast/repometeor/backend/internal/config"
	"github.com/FayezBast/repometeor/backend/internal/database"
	"github.com/FayezBast/repometeor/backend/internal/reponame"
	"github.com/FayezBast/repometeor/backend/internal/telemetry"
)

var testRef = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

// testDatabaseURL resolves the integration database, skipping when unset so a
// developer without PostgreSQL still gets a green `make test`.
func testDatabaseURL(t *testing.T) string {
	t.Helper()

	url := os.Getenv("REPOMETEOR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("REPOMETEOR_TEST_DATABASE_URL is not set; skipping integration tests")
	}
	return url
}

// TestMain migrates the test database once before any test runs, so that tests
// which inspect the schema directly do not depend on another test having
// connected first.
func TestMain(m *testing.M) {
	url := os.Getenv("REPOMETEOR_TEST_DATABASE_URL")
	if url != "" {
		cfg := config.DatabaseConfig{URL: url, ConnectTimeout: 10 * time.Second}
		if _, err := database.MigrateUp(context.Background(), cfg, telemetry.Discard()); err != nil {
			fmt.Fprintf(os.Stderr, "migrate test database: %v\n", err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// newTestDB opens a pool against the migrated test database.
//
// Tests share one database and are isolated by truncating every table on setup.
// They therefore run sequentially, which is fast enough at this scale and keeps
// the harness simple.
func newTestDB(t *testing.T) *database.DB {
	t.Helper()

	url := testDatabaseURL(t)
	ctx := context.Background()
	log := telemetry.Discard()

	cfg := config.DatabaseConfig{
		URL:            url,
		MaxConnections: 5,
		MinConnections: 1,
		ConnectTimeout: 10 * time.Second,
		QueryTimeout:   10 * time.Second,
	}

	db, err := database.Connect(ctx, cfg, log)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(db.Close)

	truncateAll(t, db)
	return db
}

// truncateAll clears every table between tests.
func truncateAll(t *testing.T, db *database.DB) {
	t.Helper()

	_, err := db.Pool().Exec(context.Background(), `
        TRUNCATE repositories, repository_aliases, repository_snapshots,
                 momentum_scores, scoring_runs, crawl_jobs, discovery_runs,
                 github_request_cache, rate_limit_counters
        RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate test tables: %v", err)
	}
}

func sampleRepository(githubID int64, owner, name string) UpsertRepositoryInput {
	return UpsertRepositoryInput{
		GitHubID:        githubID,
		NodeID:          fmt.Sprintf("R_%d", githubID),
		OwnerLogin:      owner,
		Name:            name,
		FullName:        owner + "/" + name,
		Description:     "A fictional repository used in integration tests",
		HTMLURL:         "https://github.com/" + owner + "/" + name,
		DefaultBranch:   "main",
		PrimaryLanguage: "Go",
		Topics:          []string{"developer-tools", "testing"},
		LicenseSPDX:     "Apache-2.0",
		CreatedAt:       testRef.Add(-200 * 24 * time.Hour),
		PushedAt:        ptr(testRef.Add(-2 * time.Hour)),
	}
}

func ptr[T any](v T) *T { return &v }

// --- migrations --------------------------------------------------------------

func TestMigrationsApplyFromEmptyDatabase(t *testing.T) {
	url := testDatabaseURL(t)
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	version, err := database.Version(ctx, conn)
	if err != nil {
		t.Fatalf("read version: %v", err)
	}

	migrations, err := database.LoadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if version != len(migrations) {
		t.Fatalf("applied version %d, want %d", version, len(migrations))
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	url := testDatabaseURL(t)
	cfg := config.DatabaseConfig{URL: url, ConnectTimeout: 10 * time.Second}

	result, err := database.MigrateUp(context.Background(), cfg, telemetry.Discard())
	if err != nil {
		t.Fatalf("second migrate run: %v", err)
	}
	if len(result.Applied) != 0 {
		t.Fatalf("re-running migrations applied %v, want nothing", result.Applied)
	}
}

// --- repositories ------------------------------------------------------------

func TestRepositoryUpsertAndLookup(t *testing.T) {
	db := newTestDB(t)
	store := NewRepositories(db)
	ctx := context.Background()

	created, err := store.Upsert(ctx, sampleRepository(1001, "orbit-labs", "signal-kit"))
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if created.ID == 0 {
		t.Fatal("Upsert() returned no id")
	}
	if created.FullName != "orbit-labs/signal-kit" {
		t.Errorf("FullName = %q", created.FullName)
	}
	if len(created.Topics) != 2 {
		t.Errorf("Topics = %v, want 2 entries", created.Topics)
	}

	byName, err := store.GetByName(ctx, reponame.MustParse("orbit-labs/signal-kit"))
	if err != nil {
		t.Fatalf("GetByName() error = %v", err)
	}
	if byName.ID != created.ID {
		t.Errorf("GetByName id = %d, want %d", byName.ID, created.ID)
	}

	byID, err := store.GetByGitHubID(ctx, 1001)
	if err != nil {
		t.Fatalf("GetByGitHubID() error = %v", err)
	}
	if byID.ID != created.ID {
		t.Errorf("GetByGitHubID id = %d, want %d", byID.ID, created.ID)
	}
}

func TestRepositoryLookupIsCaseInsensitive(t *testing.T) {
	db := newTestDB(t)
	store := NewRepositories(db)
	ctx := context.Background()

	created, err := store.Upsert(ctx, sampleRepository(1002, "Orbit-Labs", "Signal-Kit"))
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	for _, variant := range []string{"orbit-labs/signal-kit", "ORBIT-LABS/SIGNAL-KIT", "Orbit-Labs/Signal-Kit"} {
		found, err := store.GetByName(ctx, reponame.MustParse(variant))
		if err != nil {
			t.Fatalf("GetByName(%q) error = %v", variant, err)
		}
		if found.ID != created.ID {
			t.Errorf("GetByName(%q) returned a different repository", variant)
		}
	}

	// GitHub's own capitalisation must be preserved for display.
	if created.OwnerLogin != "Orbit-Labs" || created.Name != "Signal-Kit" {
		t.Errorf("stored capitalisation = %s/%s, want Orbit-Labs/Signal-Kit", created.OwnerLogin, created.Name)
	}
}

func TestRepositoryUpsertIsIdempotent(t *testing.T) {
	db := newTestDB(t)
	store := NewRepositories(db)
	ctx := context.Background()

	first, err := store.Upsert(ctx, sampleRepository(1003, "orbit-labs", "signal-kit"))
	if err != nil {
		t.Fatalf("first Upsert() error = %v", err)
	}

	input := sampleRepository(1003, "orbit-labs", "signal-kit")
	input.Description = "An updated description"
	second, err := store.Upsert(ctx, input)
	if err != nil {
		t.Fatalf("second Upsert() error = %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("upsert created a second row: %d then %d", first.ID, second.ID)
	}
	if second.Description != "An updated description" {
		t.Errorf("Description = %q, want the updated value", second.Description)
	}

	count, err := store.Count(ctx, StateActive)
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("Count() = %d, want 1", count)
	}
}

func TestRepositoryRenamePreservesIdentityAndRecordsAlias(t *testing.T) {
	db := newTestDB(t)
	store := NewRepositories(db)
	ctx := context.Background()

	original, err := store.Upsert(ctx, sampleRepository(1004, "orbit-labs", "signal-kit"))
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	renamed, err := store.Upsert(ctx, sampleRepository(1004, "orbit-labs", "signal-kit-v2"))
	if err != nil {
		t.Fatalf("rename Upsert() error = %v", err)
	}

	if renamed.ID != original.ID {
		t.Fatalf("rename changed the internal id from %d to %d; history would be orphaned",
			original.ID, renamed.ID)
	}
	if renamed.Name != "signal-kit-v2" {
		t.Errorf("Name = %q, want signal-kit-v2", renamed.Name)
	}

	// The old name must still resolve, so existing RepoMeteor URLs keep working.
	viaAlias, err := store.ResolveAlias(ctx, reponame.MustParse("orbit-labs/signal-kit"))
	if err != nil {
		t.Fatalf("ResolveAlias() error = %v", err)
	}
	if viaAlias.ID != original.ID {
		t.Errorf("alias resolved to %d, want %d", viaAlias.ID, original.ID)
	}
}

func TestRepositoryNotFound(t *testing.T) {
	db := newTestDB(t)
	store := NewRepositories(db)

	_, err := store.GetByName(context.Background(), reponame.MustParse("nobody/nothing"))
	if !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("error = %v, want database.ErrNotFound", err)
	}
}

func TestRepositoryRejectsInvalidGitHubID(t *testing.T) {
	db := newTestDB(t)
	store := NewRepositories(db)

	input := sampleRepository(0, "orbit-labs", "signal-kit")
	if _, err := store.Upsert(context.Background(), input); err == nil {
		t.Fatal("expected an error for a zero github id")
	}

	input.GitHubID = -5
	if _, err := store.Upsert(context.Background(), input); err == nil {
		t.Fatal("expected an error for a negative github id")
	}
}

func TestDueForCollectionOrdersByTier(t *testing.T) {
	db := newTestDB(t)
	store := NewRepositories(db)
	ctx := context.Background()

	cold, _ := store.Upsert(ctx, sampleRepository(2001, "a", "cold"))
	warm, _ := store.Upsert(ctx, sampleRepository(2002, "b", "warm"))
	hot, _ := store.Upsert(ctx, sampleRepository(2003, "c", "hot"))

	past := testRef.Add(-time.Hour)
	if err := store.SetTrackingTier(ctx, cold.ID, TierCold, past); err != nil {
		t.Fatalf("SetTrackingTier: %v", err)
	}
	if err := store.SetTrackingTier(ctx, warm.ID, TierWarm, past); err != nil {
		t.Fatalf("SetTrackingTier: %v", err)
	}
	if err := store.SetTrackingTier(ctx, hot.ID, TierHot, past); err != nil {
		t.Fatalf("SetTrackingTier: %v", err)
	}

	due, err := store.DueForCollection(ctx, testRef, 10)
	if err != nil {
		t.Fatalf("DueForCollection() error = %v", err)
	}
	if len(due) != 3 {
		t.Fatalf("got %d due repositories, want 3", len(due))
	}
	want := []TrackingTier{TierHot, TierWarm, TierCold}
	for i, tier := range want {
		if due[i].TrackingTier != tier {
			t.Errorf("position %d is %s, want %s", i, due[i].TrackingTier, tier)
		}
	}
}

func TestDueForCollectionExcludesInactiveRepositories(t *testing.T) {
	db := newTestDB(t)
	store := NewRepositories(db)
	ctx := context.Background()

	active, _ := store.Upsert(ctx, sampleRepository(2101, "a", "active"))
	missing, _ := store.Upsert(ctx, sampleRepository(2102, "b", "missing"))

	past := testRef.Add(-time.Hour)
	_ = store.SetTrackingTier(ctx, active.ID, TierHot, past)
	_ = store.SetTrackingTier(ctx, missing.ID, TierHot, past)
	if err := store.SetTrackingState(ctx, missing.ID, StateMissing); err != nil {
		t.Fatalf("SetTrackingState: %v", err)
	}

	due, err := store.DueForCollection(ctx, testRef, 10)
	if err != nil {
		t.Fatalf("DueForCollection() error = %v", err)
	}
	if len(due) != 1 || due[0].ID != active.ID {
		t.Fatalf("got %d repositories, want only the active one", len(due))
	}
}

func TestMarkFailureIncrementsAndMarkCheckedResets(t *testing.T) {
	db := newTestDB(t)
	store := NewRepositories(db)
	ctx := context.Background()

	repo, _ := store.Upsert(ctx, sampleRepository(2201, "a", "flaky"))

	for want := 1; want <= 3; want++ {
		got, err := store.MarkFailure(ctx, repo.ID, testRef.Add(time.Hour), "network error")
		if err != nil {
			t.Fatalf("MarkFailure() error = %v", err)
		}
		if got != want {
			t.Fatalf("consecutive failures = %d, want %d", got, want)
		}
	}

	if err := store.MarkChecked(ctx, repo.ID, testRef, testRef.Add(time.Hour), `W/"etag"`, ""); err != nil {
		t.Fatalf("MarkChecked() error = %v", err)
	}

	reloaded, _ := store.GetByID(ctx, repo.ID)
	if reloaded.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want a successful check to reset it", reloaded.ConsecutiveFailures)
	}
	if reloaded.ETag != `W/"etag"` {
		t.Errorf("ETag = %q, want it stored", reloaded.ETag)
	}
}

func TestRepositorySearch(t *testing.T) {
	db := newTestDB(t)
	store := NewRepositories(db)
	ctx := context.Background()

	for id, name := range map[int64]string{
		3001: "orbit-labs/signal-kit",
		3002: "orbit-labs/signal-relay",
		3003: "other-org/unrelated",
	} {
		owner, repo, _ := strings.Cut(name, "/")
		if _, err := store.Upsert(ctx, sampleRepository(id, owner, repo)); err != nil {
			t.Fatalf("Upsert(%s): %v", name, err)
		}
	}

	results, err := store.Search(ctx, "signal", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2: %v", len(results), results)
	}
}

func TestRepositorySearchEscapesLikeMetacharacters(t *testing.T) {
	db := newTestDB(t)
	store := NewRepositories(db)
	ctx := context.Background()

	if _, err := store.Upsert(ctx, sampleRepository(3101, "orbit-labs", "signal-kit")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// A bare "%" must not act as a wildcard that returns everything.
	results, err := store.Search(ctx, "%", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("searching for %%%% returned %d results; LIKE metacharacters must be escaped", len(results))
	}
}

func TestLanguagesAndTopicFacets(t *testing.T) {
	db := newTestDB(t)
	store := NewRepositories(db)
	ctx := context.Background()

	goRepo := sampleRepository(4001, "a", "go-one")
	rustRepo := sampleRepository(4002, "b", "rust-one")
	rustRepo.PrimaryLanguage = "Rust"
	rustRepo.Topics = []string{"developer-tools"}

	if _, err := store.Upsert(ctx, goRepo); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := store.Upsert(ctx, rustRepo); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	languages, err := store.Languages(ctx)
	if err != nil {
		t.Fatalf("Languages() error = %v", err)
	}
	if len(languages) != 2 {
		t.Fatalf("got %d languages, want 2: %v", len(languages), languages)
	}

	topics, err := store.Topics(ctx, 10)
	if err != nil {
		t.Fatalf("Topics() error = %v", err)
	}
	byTopic := map[string]int{}
	for _, tc := range topics {
		byTopic[tc.Topic] = tc.Count
	}
	if byTopic["developer-tools"] != 2 {
		t.Errorf("developer-tools count = %d, want 2", byTopic["developer-tools"])
	}
	if byTopic["testing"] != 1 {
		t.Errorf("testing count = %d, want 1", byTopic["testing"])
	}
}

// --- snapshots ---------------------------------------------------------------

func TestSnapshotUpsertIsIdempotentPerBucket(t *testing.T) {
	db := newTestDB(t)
	repos := NewRepositories(db)
	snaps := NewSnapshots(db)
	ctx := context.Background()

	repo, _ := repos.Upsert(ctx, sampleRepository(5001, "a", "snapshots"))
	bucket := clock.Bucket(testRef, time.Hour)

	first, err := snaps.Upsert(ctx, Snapshot{
		RepositoryID: repo.ID, CapturedAt: testRef, BucketStart: bucket,
		Stars: 100, Forks: 10, OpenIssues: 5, Source: SourceGitHubAPI,
	})
	if err != nil {
		t.Fatalf("first Upsert() error = %v", err)
	}

	// A second poll inside the same bucket updates rather than duplicating.
	second, err := snaps.Upsert(ctx, Snapshot{
		RepositoryID: repo.ID, CapturedAt: testRef.Add(20 * time.Minute), BucketStart: bucket,
		Stars: 105, Forks: 11, OpenIssues: 5, Source: SourceGitHubAPI,
	})
	if err != nil {
		t.Fatalf("second Upsert() error = %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("the same bucket produced two rows: %d and %d", first.ID, second.ID)
	}
	if second.Stars != 105 {
		t.Errorf("Stars = %d, want the updated 105", second.Stars)
	}

	count, err := snaps.Count(ctx)
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("stored %d snapshots, want 1", count)
	}
}

func TestSnapshotRejectsNegativeCounts(t *testing.T) {
	db := newTestDB(t)
	repos := NewRepositories(db)
	snaps := NewSnapshots(db)
	ctx := context.Background()

	repo, _ := repos.Upsert(ctx, sampleRepository(5101, "a", "negative"))

	_, err := snaps.Upsert(ctx, Snapshot{
		RepositoryID: repo.ID, CapturedAt: testRef, BucketStart: clock.Bucket(testRef, time.Hour),
		Stars: -1, Forks: 0, OpenIssues: 0,
	})
	if err == nil {
		t.Fatal("expected an error for a negative star count")
	}
}

func TestSnapshotHistoryAndDownsampling(t *testing.T) {
	db := newTestDB(t)
	repos := NewRepositories(db)
	snaps := NewSnapshots(db)
	ctx := context.Background()

	repo, _ := repos.Upsert(ctx, sampleRepository(5201, "a", "history"))

	// 72 hourly snapshots.
	for i := range 72 {
		at := testRef.Add(-time.Duration(71-i) * time.Hour)
		if _, err := snaps.Upsert(ctx, Snapshot{
			RepositoryID: repo.ID, CapturedAt: at, BucketStart: clock.Bucket(at, time.Hour),
			Stars: 100 + i*3, Forks: 10 + i, OpenIssues: 4,
		}); err != nil {
			t.Fatalf("Upsert snapshot %d: %v", i, err)
		}
	}

	hourly, err := snaps.History(ctx, repo.ID, testRef.Add(-72*time.Hour), ResolutionHourly)
	if err != nil {
		t.Fatalf("History(hourly) error = %v", err)
	}
	if len(hourly) != 72 {
		t.Fatalf("hourly history returned %d points, want 72", len(hourly))
	}

	daily, err := snaps.History(ctx, repo.ID, testRef.Add(-72*time.Hour), ResolutionDaily)
	if err != nil {
		t.Fatalf("History(daily) error = %v", err)
	}
	if len(daily) > 5 || len(daily) < 3 {
		t.Fatalf("daily history returned %d points, want roughly one per day", len(daily))
	}

	// Deltas are relative to the previous returned point at that resolution.
	for i := 1; i < len(hourly); i++ {
		if hourly[i].StarDelta != 3 {
			t.Fatalf("hourly point %d delta = %d, want 3", i, hourly[i].StarDelta)
		}
	}
	if hourly[0].StarDelta != 0 {
		t.Errorf("the first point should have a zero delta, got %d", hourly[0].StarDelta)
	}
}

func TestSnapshotBatchLoadersAvoidNPlusOne(t *testing.T) {
	db := newTestDB(t)
	repos := NewRepositories(db)
	snaps := NewSnapshots(db)
	ctx := context.Background()

	var ids []int64
	for i := range 5 {
		repo, err := repos.Upsert(ctx, sampleRepository(int64(5300+i), "owner", fmt.Sprintf("repo-%d", i)))
		if err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		ids = append(ids, repo.ID)

		for j := range 4 {
			at := testRef.Add(-time.Duration(3-j) * time.Hour)
			if _, err := snaps.Upsert(ctx, Snapshot{
				RepositoryID: repo.ID, CapturedAt: at, BucketStart: clock.Bucket(at, time.Hour),
				Stars: 100 + i*10 + j, Forks: 5, OpenIssues: 1,
			}); err != nil {
				t.Fatalf("Upsert snapshot: %v", err)
			}
		}
	}

	history, err := snaps.SinceForMany(ctx, ids, testRef.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("SinceForMany() error = %v", err)
	}
	if len(history) != 5 {
		t.Fatalf("got history for %d repositories, want 5", len(history))
	}
	for id, series := range history {
		if len(series) != 4 {
			t.Errorf("repository %d has %d snapshots, want 4", id, len(series))
		}
		for i := 1; i < len(series); i++ {
			if !series[i].CapturedAt.After(series[i-1].CapturedAt) {
				t.Errorf("repository %d series is not ordered oldest first", id)
			}
		}
	}

	latest, err := snaps.LatestForMany(ctx, ids)
	if err != nil {
		t.Fatalf("LatestForMany() error = %v", err)
	}
	if len(latest) != 5 {
		t.Fatalf("got %d latest snapshots, want 5", len(latest))
	}

	sparklines, err := snaps.SparklinesForMany(ctx, ids, 24)
	if err != nil {
		t.Fatalf("SparklinesForMany() error = %v", err)
	}
	for id, points := range sparklines {
		if len(points) != 4 {
			t.Errorf("repository %d sparkline has %d points, want 4", id, len(points))
		}
	}
}

func TestEarliestCaptureReportsWhenTrackingBegan(t *testing.T) {
	db := newTestDB(t)
	repos := NewRepositories(db)
	snaps := NewSnapshots(db)
	ctx := context.Background()

	repo, _ := repos.Upsert(ctx, sampleRepository(5401, "a", "earliest"))

	if _, err := snaps.EarliestCapture(ctx, repo.ID); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound before any snapshot exists", err)
	}

	first := testRef.Add(-48 * time.Hour)
	for _, at := range []time.Time{first, testRef.Add(-24 * time.Hour), testRef} {
		if _, err := snaps.Upsert(ctx, Snapshot{
			RepositoryID: repo.ID, CapturedAt: at, BucketStart: clock.Bucket(at, time.Hour),
			Stars: 100, Forks: 5, OpenIssues: 1,
		}); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}

	earliest, err := snaps.EarliestCapture(ctx, repo.ID)
	if err != nil {
		t.Fatalf("EarliestCapture() error = %v", err)
	}
	if !earliest.Equal(first) {
		t.Fatalf("EarliestCapture() = %v, want %v", earliest, first)
	}
}

// --- job queue ---------------------------------------------------------------

func TestJobEnqueueAndClaim(t *testing.T) {
	db := newTestDB(t)
	jobs := NewJobs(db)
	ctx := context.Background()

	created, err := jobs.Enqueue(ctx, EnqueueInput{
		Kind: JobCollectRepository, Priority: 10, RunAfter: testRef.Add(-time.Minute),
		Payload: []byte(`{"owner":"a","name":"b"}`),
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if created.Status != JobQueued {
		t.Errorf("Status = %q, want queued", created.Status)
	}

	claimed, err := jobs.Claim(ctx, "worker-1", 10, testRef)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d jobs, want 1", len(claimed))
	}
	if claimed[0].Status != JobRunning {
		t.Errorf("Status = %q, want running", claimed[0].Status)
	}
	if claimed[0].LockedBy != "worker-1" {
		t.Errorf("LockedBy = %q, want worker-1", claimed[0].LockedBy)
	}
	if claimed[0].Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", claimed[0].Attempts)
	}
}

func TestJobClaimRespectsRunAfter(t *testing.T) {
	db := newTestDB(t)
	jobs := NewJobs(db)
	ctx := context.Background()

	if _, err := jobs.Enqueue(ctx, EnqueueInput{
		Kind: JobCollectRepository, RunAfter: testRef.Add(time.Hour),
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	claimed, err := jobs.Claim(ctx, "worker-1", 10, testRef)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed %d jobs, want 0 for a future run_after", len(claimed))
	}
}

func TestJobClaimOrdersByPriorityThenAge(t *testing.T) {
	db := newTestDB(t)
	jobs := NewJobs(db)
	ctx := context.Background()

	low, _ := jobs.Enqueue(ctx, EnqueueInput{Kind: JobCollectRepository, Priority: 1, RunAfter: testRef.Add(-2 * time.Hour)})
	high, _ := jobs.Enqueue(ctx, EnqueueInput{Kind: JobCollectRepository, Priority: 100, RunAfter: testRef.Add(-time.Minute)})

	claimed, err := jobs.Claim(ctx, "worker-1", 10, testRef)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d jobs, want 2", len(claimed))
	}
	if claimed[0].ID != high.ID {
		t.Errorf("first claimed job = %d, want the higher-priority %d", claimed[0].ID, high.ID)
	}
	if claimed[1].ID != low.ID {
		t.Errorf("second claimed job = %d, want %d", claimed[1].ID, low.ID)
	}
}

func TestConcurrentWorkersNeverClaimTheSameJob(t *testing.T) {
	db := newTestDB(t)
	jobs := NewJobs(db)
	ctx := context.Background()

	const jobCount = 40
	for i := range jobCount {
		if _, err := jobs.Enqueue(ctx, EnqueueInput{
			Kind: JobCollectRepository, RunAfter: testRef.Add(-time.Minute),
			Payload: []byte(fmt.Sprintf(`{"n":%d}`, i)),
		}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	const workers = 8
	var (
		mu      sync.Mutex
		claimed = map[int64]string{}
		wg      sync.WaitGroup
	)

	for w := range workers {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			id := fmt.Sprintf("worker-%d", worker)

			for range 10 {
				batch, err := jobs.Claim(ctx, id, 3, testRef)
				if err != nil {
					t.Errorf("%s Claim() error = %v", id, err)
					return
				}
				if len(batch) == 0 {
					return
				}

				mu.Lock()
				for _, job := range batch {
					if previous, dup := claimed[job.ID]; dup {
						t.Errorf("job %d was claimed by both %s and %s", job.ID, previous, id)
					}
					claimed[job.ID] = id
				}
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	if len(claimed) != jobCount {
		t.Fatalf("claimed %d distinct jobs, want %d", len(claimed), jobCount)
	}
}

func TestJobDeduplication(t *testing.T) {
	db := newTestDB(t)
	jobs := NewJobs(db)
	ctx := context.Background()

	first, err := jobs.Enqueue(ctx, EnqueueInput{
		Kind: JobCollectRepository, RunAfter: testRef, DedupeKey: "collect:a/b",
	})
	if err != nil {
		t.Fatalf("first Enqueue() error = %v", err)
	}

	second, err := jobs.Enqueue(ctx, EnqueueInput{
		Kind: JobCollectRepository, RunAfter: testRef, DedupeKey: "collect:a/b",
	})
	if !errors.Is(err, ErrDuplicateJob) {
		t.Fatalf("error = %v, want ErrDuplicateJob", err)
	}
	if second.ID != first.ID {
		t.Errorf("duplicate enqueue returned job %d, want the existing %d", second.ID, first.ID)
	}
}

func TestCompletedJobsDoNotBlockFutureDedupeKeys(t *testing.T) {
	db := newTestDB(t)
	jobs := NewJobs(db)
	ctx := context.Background()

	first, _ := jobs.Enqueue(ctx, EnqueueInput{
		Kind: JobScoreRepositories, RunAfter: testRef, DedupeKey: "score:slot-1",
	})
	if _, err := jobs.Claim(ctx, "worker-1", 1, testRef); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := jobs.Complete(ctx, first.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// The same key must be reusable once the previous run finished, otherwise a
	// recurring schedule would fire exactly once and then stop forever.
	second, err := jobs.Enqueue(ctx, EnqueueInput{
		Kind: JobScoreRepositories, RunAfter: testRef, DedupeKey: "score:slot-1",
	})
	if err != nil {
		t.Fatalf("re-enqueue after completion error = %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("expected a new job row")
	}
}

func TestJobRetryThenDeadLetter(t *testing.T) {
	db := newTestDB(t)
	jobs := NewJobs(db)
	ctx := context.Background()

	created, _ := jobs.Enqueue(ctx, EnqueueInput{
		Kind: JobCollectRepository, RunAfter: testRef.Add(-time.Minute), MaxAttempts: 3,
	})

	for attempt := 1; attempt <= 2; attempt++ {
		claimed, err := jobs.Claim(ctx, "worker-1", 1, testRef.Add(time.Duration(attempt)*time.Hour))
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if len(claimed) != 1 {
			t.Fatalf("attempt %d claimed %d jobs, want 1", attempt, len(claimed))
		}

		status, err := jobs.Fail(ctx, created.ID, errors.New("boom"), testRef)
		if err != nil {
			t.Fatalf("Fail: %v", err)
		}
		if status != JobQueued {
			t.Fatalf("attempt %d status = %q, want queued", attempt, status)
		}
	}

	// Third attempt exhausts max_attempts.
	if _, err := jobs.Claim(ctx, "worker-1", 1, testRef.Add(10*time.Hour)); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	status, err := jobs.Fail(ctx, created.ID, errors.New("boom"), testRef)
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if status != JobDead {
		t.Fatalf("final status = %q, want dead", status)
	}

	final, _ := jobs.Get(ctx, created.ID)
	if final.LastError == "" {
		t.Error("a dead job should record why it failed")
	}
	if final.LockedBy != "" {
		t.Error("a dead job should not remain locked")
	}
}

func TestFailSchedulesABackoff(t *testing.T) {
	db := newTestDB(t)
	jobs := NewJobs(db)
	ctx := context.Background()

	created, _ := jobs.Enqueue(ctx, EnqueueInput{
		Kind: JobCollectRepository, RunAfter: testRef.Add(-time.Minute), MaxAttempts: 5,
	})
	if _, err := jobs.Claim(ctx, "worker-1", 1, testRef); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := jobs.Fail(ctx, created.ID, errors.New("boom"), testRef); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	reloaded, _ := jobs.Get(ctx, created.ID)
	if !reloaded.RunAfter.After(testRef) {
		t.Fatalf("RunAfter = %v, want it scheduled after %v", reloaded.RunAfter, testRef)
	}

	// The job must not be immediately reclaimable.
	claimed, _ := jobs.Claim(ctx, "worker-1", 1, testRef)
	if len(claimed) != 0 {
		t.Fatal("a failed job should not be claimable before its backoff elapses")
	}
}

func TestRescheduleDoesNotConsumeAnAttempt(t *testing.T) {
	db := newTestDB(t)
	jobs := NewJobs(db)
	ctx := context.Background()

	created, _ := jobs.Enqueue(ctx, EnqueueInput{
		Kind: JobCollectRepository, RunAfter: testRef.Add(-time.Minute), MaxAttempts: 3,
	})
	if _, err := jobs.Claim(ctx, "worker-1", 1, testRef); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if err := jobs.Reschedule(ctx, created.ID, testRef.Add(time.Hour), "rate limit reached"); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	reloaded, _ := jobs.Get(ctx, created.ID)
	if reloaded.Status != JobQueued {
		t.Errorf("Status = %q, want queued", reloaded.Status)
	}
	if reloaded.Attempts != 0 {
		t.Errorf("Attempts = %d; a deferral must not consume an attempt", reloaded.Attempts)
	}
}

func TestStaleLockRecovery(t *testing.T) {
	db := newTestDB(t)
	jobs := NewJobs(db)
	ctx := context.Background()

	created, _ := jobs.Enqueue(ctx, EnqueueInput{
		Kind: JobCollectRepository, RunAfter: testRef.Add(-time.Minute), MaxAttempts: 5,
	})
	if _, err := jobs.Claim(ctx, "crashed-worker", 1, testRef); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	// Nothing is recovered while the lock is fresh.
	recovered, err := jobs.RecoverStale(ctx, testRef.Add(-time.Hour))
	if err != nil {
		t.Fatalf("RecoverStale: %v", err)
	}
	if recovered != 0 {
		t.Fatalf("recovered %d jobs, want 0 for a fresh lock", recovered)
	}

	// Once the lock is older than the cutoff, the job returns to the queue.
	recovered, err = jobs.RecoverStale(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("RecoverStale: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered %d jobs, want 1", recovered)
	}

	reloaded, _ := jobs.Get(ctx, created.ID)
	if reloaded.Status != JobQueued {
		t.Errorf("Status = %q, want queued", reloaded.Status)
	}
	if reloaded.Attempts != 1 {
		t.Errorf("Attempts = %d, want the attempt to be preserved so the job can still die", reloaded.Attempts)
	}
}

func TestStaleRecoveryRespectsMaxAttempts(t *testing.T) {
	db := newTestDB(t)
	jobs := NewJobs(db)
	ctx := context.Background()

	created, _ := jobs.Enqueue(ctx, EnqueueInput{
		Kind: JobCollectRepository, RunAfter: testRef.Add(-time.Minute), MaxAttempts: 1,
	})
	if _, err := jobs.Claim(ctx, "crashed-worker", 1, testRef); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if _, err := jobs.RecoverStale(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("RecoverStale: %v", err)
	}

	reloaded, _ := jobs.Get(ctx, created.ID)
	if reloaded.Status != JobDead {
		t.Fatalf("Status = %q, want dead; a job that kills its worker must not loop forever", reloaded.Status)
	}
}

func TestJobDepth(t *testing.T) {
	db := newTestDB(t)
	jobs := NewJobs(db)
	ctx := context.Background()

	for range 3 {
		if _, err := jobs.Enqueue(ctx, EnqueueInput{Kind: JobCollectRepository, RunAfter: testRef}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	if _, err := jobs.Claim(ctx, "worker-1", 1, testRef); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	depth, err := jobs.Depth(ctx)
	if err != nil {
		t.Fatalf("Depth() error = %v", err)
	}
	if depth[JobQueued] != 2 {
		t.Errorf("queued = %d, want 2", depth[JobQueued])
	}
	if depth[JobRunning] != 1 {
		t.Errorf("running = %d, want 1", depth[JobRunning])
	}
}

func TestRetryDelayGrowsAndStaysBounded(t *testing.T) {
	var previous time.Duration
	for attempt := 1; attempt <= 10; attempt++ {
		d := RetryDelay(attempt)
		if d < time.Second {
			t.Fatalf("attempt %d: delay = %v, want at least 1s", attempt, d)
		}
		if d > retryMaxDelay {
			t.Fatalf("attempt %d: delay = %v, want at most %v", attempt, d, retryMaxDelay)
		}
		if attempt <= 5 && attempt > 1 && d < previous/3 {
			t.Fatalf("attempt %d: delay %v collapsed relative to %v", attempt, d, previous)
		}
		previous = d
	}
}

// --- rate limiting -----------------------------------------------------------

func TestRateLimiterEnforcesAWindow(t *testing.T) {
	db := newTestDB(t)
	limits := NewRateLimits(db, []byte("test-key"))
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		allowed, remaining, _, err := limits.Allow(ctx, "track", "203.0.113.5", 3, time.Hour, testRef)
		if err != nil {
			t.Fatalf("Allow() error = %v", err)
		}
		if !allowed {
			t.Fatalf("request %d was refused, want it allowed", i)
		}
		if want := 3 - i; remaining != want {
			t.Errorf("request %d remaining = %d, want %d", i, remaining, want)
		}
	}

	allowed, remaining, resetAt, err := limits.Allow(ctx, "track", "203.0.113.5", 3, time.Hour, testRef)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if allowed {
		t.Error("the fourth request should be refused")
	}
	if remaining != 0 {
		t.Errorf("remaining = %d, want 0", remaining)
	}
	if !resetAt.After(testRef) {
		t.Errorf("resetAt = %v, want a future time", resetAt)
	}
}

func TestRateLimiterIsolatesClients(t *testing.T) {
	db := newTestDB(t)
	limits := NewRateLimits(db, []byte("test-key"))
	ctx := context.Background()

	for range 3 {
		if _, _, _, err := limits.Allow(ctx, "track", "203.0.113.5", 3, time.Hour, testRef); err != nil {
			t.Fatalf("Allow: %v", err)
		}
	}

	allowed, _, _, err := limits.Allow(ctx, "track", "198.51.100.7", 3, time.Hour, testRef)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !allowed {
		t.Fatal("a different client must have its own allowance")
	}
}

func TestRateLimiterResetsInANewWindow(t *testing.T) {
	db := newTestDB(t)
	limits := NewRateLimits(db, []byte("test-key"))
	ctx := context.Background()

	for range 3 {
		if _, _, _, err := limits.Allow(ctx, "track", "203.0.113.5", 3, time.Hour, testRef); err != nil {
			t.Fatalf("Allow: %v", err)
		}
	}

	next := testRef.Add(time.Hour)
	allowed, _, _, err := limits.Allow(ctx, "track", "203.0.113.5", 3, time.Hour, next)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !allowed {
		t.Fatal("the allowance should reset in a new window")
	}
}

func TestRateLimiterStoresNoRawAddresses(t *testing.T) {
	db := newTestDB(t)
	limits := NewRateLimits(db, []byte("test-key"))
	ctx := context.Background()

	const address = "203.0.113.5"
	if _, _, _, err := limits.Allow(ctx, "track", address, 10, time.Hour, testRef); err != nil {
		t.Fatalf("Allow: %v", err)
	}

	var keys []string
	rows, err := db.Pool().Query(ctx, `SELECT bucket_key FROM rate_limit_counters`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatalf("scan: %v", err)
		}
		keys = append(keys, key)
	}

	if len(keys) == 0 {
		t.Fatal("no counter row was written")
	}
	for _, key := range keys {
		if strings.Contains(key, address) {
			t.Fatalf("the stored key %q contains a raw IP address", key)
		}
	}
}

// --- github request cache ----------------------------------------------------

func TestRequestCacheRoundTrip(t *testing.T) {
	db := newTestDB(t)
	cache := NewCache(db)
	ctx := context.Background()

	entry := CacheEntry{
		RequestKey:        "repo:orbit-labs/signal-kit",
		ETag:              `W/"abc123"`,
		LastModified:      "Fri, 01 Aug 2026 08:00:00 GMT",
		LastStatus:        200,
		LastCheckedAt:     testRef,
		RateLimitResource: "core",
	}
	if err := cache.Put(ctx, entry); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	loaded, err := cache.Get(ctx, entry.RequestKey)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if loaded.ETag != entry.ETag || loaded.LastModified != entry.LastModified {
		t.Errorf("validators = %q/%q, want %q/%q",
			loaded.ETag, loaded.LastModified, entry.ETag, entry.LastModified)
	}

	// A 304 update must not erase the validators it was matched against.
	if err := cache.Put(ctx, CacheEntry{
		RequestKey: entry.RequestKey, LastStatus: 304, LastCheckedAt: testRef.Add(time.Hour),
	}); err != nil {
		t.Fatalf("Put(304) error = %v", err)
	}

	reloaded, _ := cache.Get(ctx, entry.RequestKey)
	if reloaded.ETag != entry.ETag {
		t.Errorf("ETag = %q after a 304, want it preserved", reloaded.ETag)
	}
	if reloaded.LastStatus != 304 {
		t.Errorf("LastStatus = %d, want 304", reloaded.LastStatus)
	}
}
