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

	// EVERY column in userSettingsColumns, not a sample.
	//
	// The package doc says to list columns sparingly, and this is the case it
	// carves out for: GetSettings and UpdateSettings both name the whole list
	// in one SELECT/RETURNING, so a single missing column is not a degraded
	// read — it is a hard 42703 on every settings request the service serves.
	// graph-service then cannot fetch the target's privacy, falls back to
	// strict defaults, and direct messaging silently stops working for
	// everyone while the service reports itself healthy.
	//
	// That is exactly what happened: auth-service's setup.sql created only the
	// legacy three columns, the guard checked only two of them, and the
	// service passed its boot check and failed its first real request. The
	// columns are now created there and asserted in full here, so the two
	// cannot drift apart again without the boot failing loudly.
	//
	// Keep this list byte-identical to userSettingsColumns in users.go.
	{Table: "usr.user_settings", Columns: []string{
		"user_id", "account_visibility", "allow_messages_from", "allow_comments_from",
		"who_can_message", "who_can_send_connection_request", "who_can_call", "who_can_add_to_groups",
		"who_can_see_online_status", "who_can_see_read_receipts", "who_can_see_last_seen",
		"who_can_see_profile_photo",
		"allow_phone_discovery", "allow_contact_sync_match", "discoverable_by_phone_to_contacts",
		"strict_privacy_mode", "block_unknown_calls", "auto_filter_abusive_content", "under_18_mode",
		"tc_close_friends_posts", "tc_location_pings", "tc_after_hours_posts", "tc_audio_room_invite",
		"chat_availability", "send_typing_indicators", "show_message_preview",
		"privacy_version", "created_at", "updated_at",
	}},

	// Consumer idempotency. Its absence would not fail a request — it would
	// make redelivered Kafka events reapply, silently.
	{Table: "usr.inbox_events", Columns: []string{"consumer_name", "event_id"}},
}
