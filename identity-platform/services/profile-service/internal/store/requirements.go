package store

import "github.com/atpost/identity-shared/store/schemaguard"

// SchemaRequirements is everything profile-service reads or writes.
//
// Derived from the actual SQL in this package, not from a schema document — a
// document drifts, the queries do not.
//
// This list is the reason the guard exists. Before it, six of these tables did
// not exist in ANY environment: profile-service had no schema file, and the
// boot-time migration runner it relied on was pointed at a disabled directory.
// The service reported healthy and answered 500 on every request that touched
// them, because nothing checked. A missing table is now a refusal to start.
//
// profile.follows, profile.blocks and profile.friendships are deliberately
// absent. Those are the shadow social-graph tables retired by
// internal/http/retired_graph_routes.go — graph-service owns the canonical
// graph, and requiring the shadow tables here would pressure someone to
// recreate them and reopen the block-enforcement bypass.
var SchemaRequirements = []schemaguard.Requirement{
	// Owned by auth-service's setup.sql, required by nearly every query here.
	// Worth asserting precisely because this service does not create it: a
	// change in another service's schema can remove it without anyone touching
	// this codebase.
	{
		Table: "profile.profiles",
		Columns: []string{
			"user_id", "username", "display_name",
			// Read on the profile surface. If these vanished the API would
			// serve empty strings that look like a user who filled nothing in,
			// rather than failing — wrong in the direction nobody notices.
			"first_name", "last_name", "gender",
		},
	},

	{Table: "profile.user_about", Columns: []string{"section", "item_id", "data", "visibility"}},
	{Table: "profile.user_links", Columns: []string{"platform", "url", "sort_order"}},
	{Table: "profile.profile_links", Columns: []string{"profile_id", "sort_order", "is_pinned", "visibility"}},
	{Table: "profile.handle_history", Columns: []string{"old_username", "new_username", "cooldown_until"}},
	{Table: "profile.module_profiles", Columns: []string{"module", "use_global_identity", "links", "defaults"}},
	{Table: "profile.profile_stats", Columns: []string{"follower_count", "following_count", "post_count", "is_creator"}},

	// Consumer idempotency. Its absence would not fail a request — it would
	// make redelivered Kafka events reapply, so a counter drifts upward and
	// nothing ever reports an error.
	{Table: "profile.inbox_events", Columns: []string{"consumer_name", "event_id"}},

	// Account lifecycle (auth-service 30-day deletion flow): the hide/unhide
	// marker consulted by every profile read gate. Its absence would not fail
	// a request either — it would make a deactivated account's profile
	// readable by everyone, silently.
	{Table: "profile.hidden_profiles", Columns: []string{"user_id", "reason", "hidden_at"}},
}
