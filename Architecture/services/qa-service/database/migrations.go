package database

import (
	"embed"
)

// Numbered migrations (Q&A backend audit, 30 Oct 2026: the outbox, edit
// history, async community stats, consumer dead letters).
//
// setup.sql stays the boot schema script (applied only when it changed,
// postgres.BootstrapSchema); files under migrations/ run once per database
// through shared/store/migrationrunner, recorded in schema_migrations under
// MigrationService, each in its own transaction and under qa-service's
// advisory lock (RunExclusive), after setup.sql.

// MigrationService is the schema_migrations.service value for qa-service.
const MigrationService = "qa-service"

//go:embed migrations/*.sql
var Migrations embed.FS
