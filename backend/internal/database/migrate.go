package database

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/repometeor/repometeor/backend/migrations"
)

// migrationLockID is the PostgreSQL advisory lock key held for the duration of
// a migration run. Concurrent deployments therefore serialise rather than
// racing to apply the same file.
const migrationLockID int64 = 7264771077 // "RPMTR" as digits

// Migration is a single versioned schema change.
type Migration struct {
	Version int
	Name    string
	UpSQL   string
	DownSQL string
}

// ID renders the canonical "0001_name" identifier.
func (m Migration) ID() string { return fmt.Sprintf("%04d_%s", m.Version, m.Name) }

// MigrationResult reports what a run did.
type MigrationResult struct {
	Applied []string
	Current int
}

// LoadMigrations parses the embedded SQL files into ordered migrations.
func LoadMigrations() ([]Migration, error) { return loadMigrations(migrations.FS, ".") }

func loadMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}

	byVersion := make(map[int]*Migration)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}

		version, base, direction, err := parseMigrationFilename(name)
		if err != nil {
			return nil, err
		}

		body, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}

		m, ok := byVersion[version]
		if !ok {
			m = &Migration{Version: version, Name: base}
			byVersion[version] = m
		}
		if m.Name != base {
			return nil, fmt.Errorf("migration %04d has conflicting names %q and %q", version, m.Name, base)
		}
		switch direction {
		case "up":
			m.UpSQL = string(body)
		case "down":
			m.DownSQL = string(body)
		}
	}

	out := make([]Migration, 0, len(byVersion))
	for _, m := range byVersion {
		if strings.TrimSpace(m.UpSQL) == "" {
			return nil, fmt.Errorf("migration %s has no up file", m.ID())
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })

	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migration versions must be contiguous starting at 1; found %d at position %d", m.Version, i+1)
		}
	}
	return out, nil
}

// parseMigrationFilename splits "0001_repositories.up.sql".
func parseMigrationFilename(name string) (version int, base, direction string, err error) {
	trimmed := strings.TrimSuffix(name, ".sql")

	dot := strings.LastIndex(trimmed, ".")
	if dot < 0 {
		return 0, "", "", fmt.Errorf("migration %q must be named <version>_<name>.<up|down>.sql", name)
	}
	direction = trimmed[dot+1:]
	if direction != "up" && direction != "down" {
		return 0, "", "", fmt.Errorf("migration %q has unknown direction %q", name, direction)
	}
	trimmed = trimmed[:dot]

	underscore := strings.Index(trimmed, "_")
	if underscore <= 0 {
		return 0, "", "", fmt.Errorf("migration %q must be named <version>_<name>.<up|down>.sql", name)
	}
	version, err = strconv.Atoi(trimmed[:underscore])
	if err != nil {
		return 0, "", "", fmt.Errorf("migration %q has a non-numeric version: %w", name, err)
	}
	if version < 1 {
		return 0, "", "", fmt.Errorf("migration %q version must be >= 1", name)
	}
	return version, trimmed[underscore+1:], direction, nil
}

// Migrate applies every pending migration in order.
//
// The whole run is serialised by a session-level advisory lock. Each individual
// migration executes inside its own transaction together with its
// schema_migrations bookkeeping row, so a failure leaves the database at a
// known version rather than half-applied.
func Migrate(ctx context.Context, conn *pgx.Conn, log *slog.Logger) (MigrationResult, error) {
	migrations, err := LoadMigrations()
	if err != nil {
		return MigrationResult{}, err
	}
	return migrateWith(ctx, conn, log, migrations)
}

func migrateWith(ctx context.Context, conn *pgx.Conn, log *slog.Logger, migrations []Migration) (MigrationResult, error) {
	var result MigrationResult

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return result, fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		// Best effort: the lock is also released when the session ends.
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, migrationLockID); err != nil {
			log.Warn("failed to release migration advisory lock", slog.String("error", err.Error()))
		}
	}()

	if err := ensureMigrationsTable(ctx, conn); err != nil {
		return result, err
	}

	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return result, err
	}

	for _, m := range migrations {
		if _, done := applied[m.Version]; done {
			result.Current = m.Version
			continue
		}

		start := time.Now()
		if err := applyOne(ctx, conn, m); err != nil {
			return result, fmt.Errorf("apply migration %s: %w", m.ID(), err)
		}
		result.Applied = append(result.Applied, m.ID())
		result.Current = m.Version

		log.Info("applied migration",
			slog.String("migration", m.ID()),
			slog.Int64("durationMs", time.Since(start).Milliseconds()),
		)
	}

	if len(result.Applied) == 0 {
		log.Info("database schema is up to date", slog.Int("version", result.Current))
	}
	return result, nil
}

func applyOne(ctx context.Context, conn *pgx.Conn, m Migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, m.UpSQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES ($1, $2, NOW())`,
		m.Version, m.Name,
	); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return tx.Commit(ctx)
}

// Rollback reverts the single most recently applied migration.
//
// It is deliberately one step at a time and refuses to run when the migration
// has no down file, because an unbounded automated rollback across several
// destructive migrations is more dangerous than a manual restore.
func Rollback(ctx context.Context, conn *pgx.Conn, log *slog.Logger) (string, error) {
	migrations, err := LoadMigrations()
	if err != nil {
		return "", err
	}

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return "", fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, migrationLockID)
	}()

	if err := ensureMigrationsTable(ctx, conn); err != nil {
		return "", err
	}

	var version int
	err = conn.QueryRow(ctx, `SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errors.New("no migrations have been applied")
	}
	if err != nil {
		return "", fmt.Errorf("read latest migration: %w", err)
	}

	var target *Migration
	for i := range migrations {
		if migrations[i].Version == version {
			target = &migrations[i]
			break
		}
	}
	if target == nil {
		return "", fmt.Errorf("migration %04d is recorded as applied but its files are missing", version)
	}
	if strings.TrimSpace(target.DownSQL) == "" {
		return "", fmt.Errorf("migration %s has no down file; roll back by restoring a backup", target.ID())
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, target.DownSQL); err != nil {
		return "", fmt.Errorf("apply down migration %s: %w", target.ID(), err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, version); err != nil {
		return "", fmt.Errorf("remove migration record: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}

	log.Warn("rolled back migration", slog.String("migration", target.ID()))
	return target.ID(), nil
}

// Version reports the highest applied migration version, or 0.
func Version(ctx context.Context, conn *pgx.Conn) (int, error) {
	if err := ensureMigrationsTable(ctx, conn); err != nil {
		return 0, err
	}
	var version int
	err := conn.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func ensureMigrationsTable(ctx context.Context, conn *pgx.Conn) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER     PRIMARY KEY,
    name       TEXT        NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`
	if _, err := conn.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

func appliedVersions(ctx context.Context, conn *pgx.Conn) (map[int]struct{}, error) {
	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]struct{})
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = struct{}{}
	}
	return applied, rows.Err()
}
