package database

import "embed"

//go:embed setup.sql
var SetupSQL string

// Migrations holds every .sql file under database/migrations/, applied after
// setup.sql by the shared migrationrunner (tracked in schema_migrations).
//
//go:embed migrations/*.sql
var Migrations embed.FS
