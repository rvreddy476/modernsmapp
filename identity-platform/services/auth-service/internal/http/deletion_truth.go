package http

// Module 3 M3-P0-7 / SR-7 — telling the truth about account deletion.
//
// WHAT THE PLATFORM CLAIMED
//
// `DELETE /v1/auth/account` answered:
//
//	"Account scheduled for deletion in 30 days"
//
// and the mobile confirmation dialog said:
//
//	"This action is permanent and cannot be undone. All your data will be
//	 deleted."
//
// WHAT ACTUALLY HAPPENED
//
// The row was marked `account_status = 'pending_deletion'` with
// `scheduled_purge_date = NOW() + INTERVAL '30 days'`, a
// `user.deletion_requested` outbox event was written, and sessions were
// revoked. That is all.
//
// Nothing in this repository ever reads `pending_deletion`. There is no purge
// worker, no scheduled job, and no consumer of `user.deletion_requested` that
// erases anything. `grep -rn pending_deletion --include=*.go` returns exactly
// one hit: the UPDATE that sets it.
//
// So the data was never deleted, and the user was told it would be. Posts,
// profile, graph edges and messages all remained. For a platform operating
// under the DPDP Act — which gives a data principal the right to erasure —
// that is not a missing feature, it is an untrue statement about a legal right.
//
// WHAT SR-7 DOES
//
// Deletion is DISABLED and the messaging is corrected. The endpoint keeps
// doing the parts that genuinely work — revoking every session and
// deactivating the account so it stops being reachable — and says exactly
// that, plus how to obtain real erasure.
//
// The alternative, leaving the claim in place until a purge pipeline exists,
// was rejected: a user who believes their data is gone behaves differently
// from one who knows it is retained, and they cannot un-make that decision.

// LB-6: THE ENDPOINT NOW MUTATES NOTHING.
//
// SR-7 corrected the messaging but kept the mutation: the account was still
// marked `pending_deletion` and a `user.deletion_requested` outbox event was
// still emitted. That event enters the SAME incomplete cross-service workflow
// the degradation was supposed to avoid — some consumers may act on it and
// erase their slice while others do not, producing partial irreversible
// erasure with the rest of the data intact. That is worse than either
// finishing the pipeline or not starting it.
//
// So self-service deletion is DISABLED until Module 7's orchestrator exists.
// The endpoint answers a stable, honest 503 and changes no row.
//
// A user who wants their account gone is not left without a path: the response
// names the support channel, which is a real process a human runs.

// DeletionUnavailableCode is the stable error code clients branch on.
const DeletionUnavailableCode = "DELETION_UNAVAILABLE"

// DeletionUnavailableMessage describes the actual state of the world.
const DeletionUnavailableMessage = "Self-service account deletion is not available yet. " +
	"Nothing has been changed on your account — you are still signed in. " +
	"To request deletion of your account and erasure of your data, contact " +
	"privacy@cleestudio.com and we will process it manually."

// DeletionUnavailableDetails is returned as structured data so a client renders
// the truth rather than a summary it invented.
type DeletionUnavailableDetails struct {
	// Every field is false. They are reported EXPLICITLY rather than omitted:
	// an absent field reads as "not applicable", where false reads as "this
	// did not happen", which is the fact the user needs.
	AccountChanged  bool   `json:"account_changed"`
	SessionsRevoked bool   `json:"sessions_revoked"`
	DataErased      bool   `json:"data_erased"`
	RequestVia      string `json:"request_via"`
}

// CurrentDeletionDetails is what the endpoint truthfully does today: nothing.
func CurrentDeletionDetails() DeletionUnavailableDetails {
	return DeletionUnavailableDetails{
		AccountChanged:  false,
		SessionsRevoked: false,
		DataErased:      false,
		RequestVia:      "privacy@cleestudio.com",
	}
}
