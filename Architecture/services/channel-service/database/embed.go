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

// RecountSubscriberCountSQL is the one-off repair in
// migrations/002_recount_subscriber_count.sql, which recomputes
// broadcast_channels.subscriber_count from channel_members. Run by hand,
// like 001; embedded so the integration test runs the exact text.
//
//go:embed migrations/002_recount_subscriber_count.sql
var RecountSubscriberCountSQL string
