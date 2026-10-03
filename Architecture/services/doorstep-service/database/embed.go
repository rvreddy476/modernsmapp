// Package database embeds the doorstep-service migrations and applies them on
// startup through shared/store/migrationrunner (the rider-service pattern):
// versioned files, each in its own transaction, recorded once per database
// under the service name "doorstep-service".
package database

import (
	"context"
	"embed"
	"fmt"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ServiceName is the schema_migrations.service value for this service.
const ServiceName = "doorstep-service"

//go:embed migrations/*.sql
var Migrations embed.FS

// BootstrapSchema applies every migration not yet recorded for this service.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if err := migrationrunner.Run(ctx, db, ServiceName, Migrations, "migrations"); err != nil {
		return fmt.Errorf("apply doorstep migrations: %w", err)
	}
	return nil
}
