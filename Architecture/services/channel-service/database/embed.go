package database

import _ "embed"

//go:embed setup.sql
var SetupSQL string

// BackfillReactionCountSQL is the one-off repair in
// migrations/001_backfill_reaction_count.sql, which recomputes
// channel_updates.reaction_count from update_reactions.
//
// NOTHING APPLIES IT AUTOMATICALLY. channel-service has no migration
// runner — BootstrapSchema applies SetupSQL and nothing else — and this
// must stay that way: the script is run by hand, one database at a time.
// It is embedded so the integration tests exercise the exact text an
// operator runs, rather than a copy that can drift from it.
//
//go:embed migrations/001_backfill_reaction_count.sql
var BackfillReactionCountSQL string
