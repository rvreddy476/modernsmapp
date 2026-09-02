package store

import "github.com/atpost/identity-shared/store/schemaguard"

// SchemaRequirements is everything auth-service reads or writes.
//
// Derived from the actual SQL in this package, not from a schema document —
// a document drifts, the queries do not. When a query starts touching a new
// table, add it here; the boot check is what turns "we forgot to run the
// pipeline" into a refusal to start rather than a 500 on the first user who
// hits that code path.
//
// Columns are listed only where absence would be *silently* wrong. A missing
// column the service writes to fails at the first request — late, but obvious.
// A missing column it reads can surface as a zero value that looks like real
// data, and those are the ones named here.
var SchemaRequirements = []schemaguard.Requirement{
	{
		Table: "auth.users",
		Columns: []string{
			"user_id", "email", "password_hash",
			// Read on every login. If email_verified vanished, the default
			// false would silently lock every account out of sign-in; if
			// account_status vanished, a suspended account would read as
			// active. Both are wrong in the dangerous direction.
			"email_verified", "account_status",
			// Account control (deactivate / delete / purge). Read on every
			// login and by the purge worker; a missing scheduled_purge_date
			// would make a pending-deletion account read as past its window,
			// and a missing purge_completed_at would let the worker re-purge.
			"deactivated_at", "scheduled_purge_date",
			"purge_requested_at", "purge_completed_at",
		},
	},
	// Purge coordination: the worker completes a purge only when this table
	// holds an ack from every required service. If it vanished, GetPurgeAcks
	// would fail loudly at the first tick — listed anyway because the INSERT
	// path (Kafka acks consumer) drops messages on error and would otherwise
	// silently discard every ack.
	{Table: "auth.account_purge_acks", Columns: []string{"user_id", "service", "acked_at"}},
	{Table: "auth.sessions"},
	{Table: "auth.otp_codes", Columns: []string{"otp_hash", "attempts", "expires_at"}},
	{Table: "auth.verification_transactions", Columns: []string{"token_hash", "consumed_at", "expires_at"}},
	{Table: "auth.registration_consents"},
	{Table: "auth.recovery_codes"},
	{Table: "auth.trusted_devices"},
	{Table: "auth.login_anomalies"},
	{Table: "auth.user_roles"},
	{Table: "auth.admin_audit"},
	{Table: "auth.webauthn_credentials"},
	{Table: "auth.outbox_events", Columns: []string{"published_at"}},

	// Added by this workstream. Their absence is exactly the failure mode the
	// guard exists for: registration enqueues into email_delivery_jobs inside
	// its transaction, so a missing table breaks every signup.
	{Table: "auth.email_delivery_jobs", Columns: []string{"sent_at", "next_attempt_at", "attempts"}},
	{Table: "auth.idempotency_keys", Columns: []string{"request_hash", "response_body", "expires_at"}},

	// Cross-schema reads. auth-service does not own these, which is why they
	// are worth asserting: a change in another service's pipeline can remove
	// them without anyone touching this codebase.
	{Table: "usr.users"},
	{Table: "usr.user_settings"},
	{Table: "profile.profiles", Columns: []string{"display_name", "first_name", "last_name", "gender"}},
}
