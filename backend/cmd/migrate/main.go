// Command migrate applies, inspects or rolls back RepoMeteor's database schema.
//
//	migrate            apply every pending migration
//	migrate up         same as above
//	migrate status     print the current schema version
//	migrate down       revert the most recent migration (one step)
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/FayezBast/repometeor/backend/internal/config"
	"github.com/FayezBast/repometeor/backend/internal/database"
	"github.com/FayezBast/repometeor/backend/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := telemetry.NewLogger(os.Stdout, cfg.App.LogLevel, "repometeor-migrate")

	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	switch command {
	case "up":
		result, err := database.MigrateUp(ctx, cfg.Database, log)
		if err != nil {
			return err
		}
		if len(result.Applied) == 0 {
			fmt.Printf("schema is up to date at version %04d\n", result.Current)
			return nil
		}
		for _, id := range result.Applied {
			fmt.Printf("applied %s\n", id)
		}
		fmt.Printf("schema is now at version %04d\n", result.Current)
		return nil

	case "status":
		version, err := database.SchemaVersion(ctx, cfg.Database)
		if err != nil {
			return err
		}
		migrations, err := database.LoadMigrations()
		if err != nil {
			return err
		}
		fmt.Printf("applied version: %04d\n", version)
		fmt.Printf("latest version:  %04d\n", len(migrations))
		if version < len(migrations) {
			fmt.Printf("pending:         %d migration(s)\n", len(migrations)-version)
		}
		return nil

	case "down":
		id, err := database.RollbackLast(ctx, cfg.Database, log)
		if err != nil {
			return err
		}
		fmt.Printf("rolled back %s\n", id)
		return nil

	default:
		return errors.New("unknown command; expected one of: up, status, down")
	}
}
