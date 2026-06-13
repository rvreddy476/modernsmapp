package database

import "embed"

//go:embed setup.sql
var SetupSQL string

// Migrations holds every .sql file under database/migrations/, embedded into
// the binary at build time. The migration runner discovers them via fs.ReadDir
// at "migrations" and applies any not yet recorded in `schema_migrations`.
//
//go:embed migrations/*.sql
var Migrations embed.FS
