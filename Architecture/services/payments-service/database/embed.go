package database

import "embed"

//go:embed setup.sql
var SetupSQL string

//go:embed migrations/*.sql
var Migrations embed.FS

// Gated holds migrations that must NOT run at service boot.
//
// A2. `999_provider_reference_uniqueness.sql` can REFUSE existing data and
// takes a SHARE lock on payment_intents, blocking every write for its
// duration. Either behaviour would break a live mixed-version fleet
// mid-rollout, which is precisely what the boot set may never do — so it is
// embedded for tooling and operator use but is never applied by the migration
// runner.
//
// The file itself documents its lock behaviour, rollout order and rollback.
//
//go:embed gated/*.sql
var Gated embed.FS
