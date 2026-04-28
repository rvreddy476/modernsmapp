package database

import _ "embed"

//go:embed setup.sql
var SetupSQL string

//go:embed migrations/001_seller_onboarding.sql
var Migration001 string

//go:embed migrations/002_invoices_shipments.sql
var Migration002 string

//go:embed migrations/003_returns_refunds.sql
var Migration003 string

//go:embed migrations/004_cod_remittances.sql
var Migration004 string

// Migrations holds all migration SQL in order.
var Migrations = []string{
	Migration001,
	Migration002,
	Migration003,
	Migration004,
}
