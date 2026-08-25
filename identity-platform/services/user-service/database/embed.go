package database

import _ "embed"

// SetupSQL is the schema user-service owns.
//
// Embedded rather than read from disk so the binary and the DDL it expects
// cannot be deployed at different versions — the failure mode that left
// usr.inbox_events uncreated in every environment.
//
//go:embed setup.sql
var SetupSQL string
