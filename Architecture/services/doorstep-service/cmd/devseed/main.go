// Command devseed applies the doorstep migrations and seeds the Hyderabad
// pilot catalogue (internal/devseed) on a DEVELOPMENT database.
//
//	ENV=dev POSTGRES_DSN=postgres://.../app?sslmode=disable go run ./cmd/devseed
//
// It refuses unless ENV (or APP_ENV / ENVIRONMENT / DEPLOY_ENV) says local,
// dev or development and nothing says otherwise. The seed is idempotent
// (deterministic ids, ON CONFLICT DO NOTHING) and never overwrites admin edits.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/atpost/doorstep-service/database"
	"github.com/atpost/doorstep-service/internal/devseed"
	"github.com/atpost/doorstep-service/internal/runtimeenv"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if !runtimeenv.IsDevelopment(os.Getenv) {
		slog.Error("refusing to seed", "error", devseed.ErrNotDevelopment)
		os.Exit(1)
	}
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		slog.Error("POSTGRES_DSN is required")
		os.Exit(1)
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		slog.Error("parse dsn", "error", err)
		os.Exit(1)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = "public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		slog.Error("connect", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := database.BootstrapSchema(ctx, pool); err != nil {
		slog.Error("migrate", "error", err)
		os.Exit(1)
	}
	if err := devseed.Run(ctx, pool, os.Getenv); err != nil {
		slog.Error("seed", "error", err)
		os.Exit(1)
	}
	cats, svcs, opts, addons, rates := devseed.Counts()
	slog.Info("Hyderabad seed applied", "categories", cats, "services", svcs, "options", opts, "addons", addons, "rate_cards", rates)
}
