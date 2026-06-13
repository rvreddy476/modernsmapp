package database

import "embed"

//go:embed setup.sql
var SetupSQL string

//go:embed migrations/*.sql
var Migrations embed.FS
