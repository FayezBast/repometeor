// Package database owns PostgreSQL connectivity and schema migration.
//
// It deliberately exposes pgx types to callers rather than wrapping every query
// behind an interface: RepoMeteor uses no ORM, all SQL is written by hand with
// bind parameters, and hiding the driver would only add indirection.
package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FayezBast/repometeor/backend/internal/config"
)

// Pool is the subset of pgxpool.Pool that RepoMeteor uses. Accepting an
// interface lets repositories run against either a pool or a transaction.
type Pool interface {
	Querier
	Begin(ctx context.Context) (pgx.Tx, error)
	Ping(ctx context.Context) error
	Close()
}

// Querier is satisfied by *pgxpool.Pool, pgx.Tx and *pgx.Conn.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// DB wraps a connection pool with RepoMeteor's timeouts and logging.
type DB struct {
	pool         *pgxpool.Pool
	queryTimeout time.Duration
	log          *slog.Logger
}

// Connect opens and verifies a pool using the supplied configuration.
func Connect(ctx context.Context, cfg config.DatabaseConfig, log *slog.Logger) (*DB, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		// Never echo the URL: it may embed a password.
		return nil, errors.New("parse DATABASE_URL: invalid connection string")
	}

	poolCfg.MaxConns = cfg.MaxConnections
	poolCfg.MinConns = cfg.MinConnections
	poolCfg.MaxConnLifetime = time.Hour
	poolCfg.MaxConnIdleTime = 30 * time.Minute
	poolCfg.HealthCheckPeriod = time.Minute
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	// Always speak UTC to the database so that timestamptz round-trips are
	// unambiguous regardless of the server's configured zone.
	poolCfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	poolCfg.ConnConfig.RuntimeParams["application_name"] = "repometeor"

	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(connectCtx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	log.Info("connected to database",
		slog.String("host", poolCfg.ConnConfig.Host),
		slog.String("database", poolCfg.ConnConfig.Database),
		slog.Int("maxConnections", int(cfg.MaxConnections)),
	)

	return &DB{pool: pool, queryTimeout: cfg.QueryTimeout, log: log}, nil
}

// Pool returns the underlying pgx pool.
func (d *DB) Pool() *pgxpool.Pool { return d.pool }

// QueryTimeout is the default per-query deadline.
func (d *DB) QueryTimeout() time.Duration { return d.queryTimeout }

// Context derives a context bounded by the configured query timeout. Callers
// that already hold a shorter deadline keep theirs.
func (d *DB) Context(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d.queryTimeout)
}

// Ping verifies connectivity, used by the readiness probe.
func (d *DB) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return d.pool.Ping(ctx)
}

// Close releases every pooled connection.
func (d *DB) Close() {
	if d.pool != nil {
		d.pool.Close()
	}
}

// Stats reports pool utilisation for the readiness endpoint.
func (d *DB) Stats() map[string]any {
	s := d.pool.Stat()
	return map[string]any{
		"acquired":    s.AcquiredConns(),
		"idle":        s.IdleConns(),
		"total":       s.TotalConns(),
		"maxConns":    s.MaxConns(),
		"constructed": s.ConstructingConns(),
	}
}

// InTx runs fn inside a transaction, committing on success and rolling back on
// error or panic. Every multi-statement write in RepoMeteor goes through it.
func (d *DB) InTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			// context.WithoutCancel: the rollback must still run when the
			// caller's context is already cancelled.
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}

// MigrateUp applies pending migrations using a dedicated single connection,
// because advisory locks are session-scoped and a pooled connection could be
// handed to another caller mid-run.
func MigrateUp(ctx context.Context, cfg config.DatabaseConfig, log *slog.Logger) (MigrationResult, error) {
	conn, err := pgx.Connect(ctx, cfg.URL)
	if err != nil {
		return MigrationResult{}, fmt.Errorf("connect for migration: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	return Migrate(ctx, conn, log)
}

// RollbackLast reverts the most recent migration.
func RollbackLast(ctx context.Context, cfg config.DatabaseConfig, log *slog.Logger) (string, error) {
	conn, err := pgx.Connect(ctx, cfg.URL)
	if err != nil {
		return "", fmt.Errorf("connect for rollback: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	return Rollback(ctx, conn, log)
}

// SchemaVersion reports the applied schema version.
func SchemaVersion(ctx context.Context, cfg config.DatabaseConfig) (int, error) {
	conn, err := pgx.Connect(ctx, cfg.URL)
	if err != nil {
		return 0, fmt.Errorf("connect for version check: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	return Version(ctx, conn)
}

// IsUniqueViolation reports whether err is a PostgreSQL unique constraint
// violation (SQLSTATE 23505). Upserts use it to distinguish an expected race
// from a genuine failure.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// IsForeignKeyViolation reports whether err is SQLSTATE 23503.
func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// IsCheckViolation reports whether err is SQLSTATE 23514.
func IsCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

// ErrNotFound is returned by repository lookups when no row matches.
var ErrNotFound = errors.New("not found")

// NormaliseNotFound converts pgx.ErrNoRows into ErrNotFound so that service and
// HTTP layers can test for one sentinel.
func NormaliseNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
