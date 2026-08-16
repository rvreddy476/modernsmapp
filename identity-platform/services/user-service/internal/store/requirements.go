package store

import "github.com/atpost/identity-shared/store/schemaguard"

// SchemaRequirements is everything user-service reads or writes.
//
// Derived from the actual SQL in this package, not from a schema document — a
// document drifts, the queries do not.
//
// usr.inbox_events is the one this catches today: it was defined only in the
// old shared identity migrations directory and never applied anywhere, because
// the boot-time migration runner was pointed at a disabled directory. Its DDL
// now lives in this service's own database/setup.sql, and that directory has
// been deleted — it was a divergent history that disagreed with the schema the
// services actually create.
var SchemaRequirements = []schemaguard.Requirement{
	// Owned by auth-service's setup.sql. Asserted here because this service
	// does not create them and cannot serve a request without them.
	//
	// Note the key columns differ: usr.users is keyed `id` while
	// usr.user_settings is keyed `user_id`. Spelling either one wrong here
	// would refuse a boot that should have succeeded, so both are taken from
	// the live catalog rather than assumed to match.
	{Table: "usr.users", Columns: []string{"id", "status"}},
	{Table: "usr.user_settings", Columns: []string{"user_id", "account_visibility"}},

	// Consumer idempotency. Its absence would not fail a request — it would
	// make redelivered Kafka events reapply, silently.
	{Table: "usr.inbox_events", Columns: []string{"consumer_name", "event_id"}},
}
