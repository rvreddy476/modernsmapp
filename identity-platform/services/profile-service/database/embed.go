package database

import _ "embed"

// SetupSQL is the schema profile-service owns.
//
// Embedded rather than read from disk so the binary and the DDL it expects
// cannot be deployed at different versions — the failure mode that left six of
// this service's tables uncreated in every environment.
//
//go:embed setup.sql
var SetupSQL string
