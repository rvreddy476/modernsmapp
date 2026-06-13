package database

import "embed"

//go:embed setup.sql
var SetupSQL string

// Migrations holds every .sql file under database/migrations/, embedded at
// build time. The shared migrationrunner discovers them via fs.ReadDir at
// "migrations" and applies any not yet recorded in `schema_migrations`.
// setup.sql (equivalent to 001_initial.sql) is applied separately first.
//
//go:embed migrations/*.sql
var Migrations embed.FS
