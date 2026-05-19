package database

import _ "embed"

//go:embed setup.sql
var SetupSQL string

//go:embed migrations/002_case_workflow.sql
var Migration002 string

//go:embed migrations/003_indexes.sql
var Migration003 string

//go:embed migrations/004_trust_extras.sql
var Migration004 string

//go:embed migrations/005_report_categories.sql
var Migration005 string

//go:embed migrations/006_user_trust_state.sql
var Migration006 string

// Migrations holds all incremental migration SQL in order. setup.sql (which is
// equivalent to 001_initial.sql) is applied separately by BootstrapSchema.
// Every migration uses IF NOT EXISTS / IF EXISTS guards so re-runs are safe.
var Migrations = []string{
	Migration002,
	Migration003,
	Migration004,
	Migration005,
	Migration006,
}
