package database

import "embed"

//go:embed setup.sql
var SetupSQL string

//go:embed migrations/*.sql
var Migrations embed.FS

// Gated holds migrations that must NOT run at service boot.
//
// Commerce P0 A7 / review §5.1. Migrations 007–012 add every new constraint
// NOT VALID, because a replica still running the previous image would be
// rejected by them mid-rollout: the old writer clamps stock with
// GREATEST(0,…), stores an address pointer instead of a snapshot, and leaves
// tax at zero. Validating those constraints, and tightening the money
// columns to NOT NULL, is only safe once every old pod is drained and the
// old-vs-new money comparison has been empty for a full business day.
//
// Booting a pod is precisely the moment when that is NOT yet true. So these
// files are excluded from the set the service applies on startup and are run
// deliberately, once, by:
//
//	commerce-migrate -gated
//
// The gated file asserts its own preconditions and refuses with a specific
// message naming the one that failed, so running it early fails loudly
// rather than half-applying.
//
//go:embed gated/*.sql
var Gated embed.FS
