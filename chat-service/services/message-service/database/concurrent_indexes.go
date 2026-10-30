package database

// ConcurrentIndex is one index built with CREATE INDEX CONCURRENTLY outside
// any transaction (audit L9, 30 Oct 2026). See
// migrations/020_concurrent_indexes.sql for why these cannot live in
// setup.sql and how postgres.BuildConcurrentIndexes applies them.
type ConcurrentIndex struct {
	// Name is the index name in schema chat; it is how an INVALID leftover
	// of an interrupted build is found and dropped.
	Name string
	// SQL is the single CREATE INDEX CONCURRENTLY IF NOT EXISTS statement,
	// exactly as it appears in migrations/020_concurrent_indexes.sql.
	SQL string
}

// ConcurrentIndexes lists every online-built index, in build order. Add new
// ones to the end and to a migration file (a unit test keeps them in step).
var ConcurrentIndexes = []ConcurrentIndex{
	{
		Name: "idx_scheduled_msg_sender_pending",
		SQL:  `CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_scheduled_msg_sender_pending ON chat.scheduled_messages (sender_id) WHERE status = 'pending'`,
	},
	{
		Name: "idx_delivery_intents_uncompacted",
		SQL:  `CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_delivery_intents_uncompacted ON chat.message_delivery_intents (completed_at) WHERE completed_at IS NOT NULL AND member_ids <> '{}'::uuid[]`,
	},
}
