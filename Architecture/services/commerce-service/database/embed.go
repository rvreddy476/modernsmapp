package database

import _ "embed"

//go:embed setup.sql
var SetupSQL string

//go:embed migrations/001_seller_onboarding.sql
var Migration001 string

// Migrations holds all migration SQL in order.
var Migrations = []string{
	Migration001,
}
