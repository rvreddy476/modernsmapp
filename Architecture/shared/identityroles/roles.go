// Package identityroles is the ONE way a service tells identity that somebody
// has become — or stopped being — a seller, restaurant owner, delivery partner
// or rider partner.
//
// # WHY THIS PACKAGE EXISTS
//
// identity-auth-service owns roles for the whole ecosystem; it is the SSO. It
// grew four ecosystem roles and an internal API to grant them
// (POST/DELETE /v1/auth/internal/roles), but nothing called it, so the roles
// existed and nobody held them. Three services need to call it —
// commerce-service, food-service, rider-service — and three hand-rolled
// clients would drift on timeout, on retry, on what a 403 means, and worst of
// all on WHEN a role is granted. So there is one client, one durable queue,
// and one written-down answer to the semantic question, here.
//
// # THE SEMANTIC DECISION: THE ROLE MEANS "IS ON THIS JOURNEY"
//
// The question this package had to answer is whether `seller` means "has a
// seller record" or "is an APPROVED seller". It means the former, and the
// difference matters enough to justify the paragraphs below.
//
// A role is granted when the RECORD IS CREATED, while the applicant is still
// draft/pending. It is NOT gated on approval. The owning service's own
// verification_status / store_status / partner status keeps gating what the
// person may actually DO.
//
//	identity says WHO YOU ARE.  the service says WHAT STATE YOU ARE IN.
//
// The decisive argument is not aesthetic, it is that gating the role on
// approval locks people out of the flow that gets them approved. Every one of
// the three journeys has a step the applicant must reach BEFORE anyone
// approves them:
//
//	rider-service     POST /v1/rider/partners/me/documents   upload KYC docs,
//	                  which is what moves draft -> pending_verification.
//	                  A partner who cannot reach it can never be approved.
//	food-service      POST /v1/food/delivery/documents, and the restaurant
//	                  owner's menu/profile screens, all pre-APPROVED.
//	commerce-service  POST /v1/commerce/onboarding/submit — the seller fills in
//	                  nine onboarding steps as status='draft' and only then
//	                  submits. Approval happens after.
//
// If `seller` were granted only on approval, the seller area would be closed
// to exactly the people who need it most, and onboarding would deadlock.
//
// # WHEN THE ROLE IS REVOKED
//
// Revocation follows from the same rule: you leave the journey, not merely
// pause on it. The three services MUST agree on this table.
//
//	record created (draft / pending / PENDING_REVIEW)  ->  GRANT
//	approved                                           ->  GRANT (idempotent;
//	                                                       also the safety net
//	                                                       if the create-time
//	                                                       grant was lost)
//	rejected            (terminal denial)              ->  REVOKE
//	blocked / banned / closed / partner removed        ->  REVOKE
//	suspended / inactive / offline / temp-closed       ->  NO CHANGE
//
// Suspension is deliberately NOT a revocation. A suspended seller is still a
// seller — they must reach the seller area to see why they were suspended and
// to appeal — and commerce already expresses the suspension in
// sellers.status='suspended', which its own handlers check. Revoking the role
// too would be a second, weaker copy of that state living in another database,
// which is the exact disease this whole module is curing.
//
// Rejection IS a revocation, and that is a judgement worth stating: in
// commerce a rejected seller cannot resubmit (SubmitSellerApplication requires
// status='draft'), so 'rejected' is terminal, not a pause. Same for
// food's REJECTED and rider's rejected.
//
// # WHY EVERY CALL GOES THROUGH A DURABLE QUEUE
//
// A grant is a second network call inside an approval flow. If identity is
// down, the approval must not fail; but the grant must not be silently lost
// either, or you get a seller who cannot see the seller area and no record of
// why. So callers never call the HTTP client directly from a request path.
// They ENQUEUE an intent — in the same transaction as the domain write
// wherever the service has one — and a Worker delivers it. See outbox.go and
// worker.go for the durability argument and its honest failure modes.
package identityroles

// The four ecosystem roles. This list must equal
// identity-platform/services/auth-service/internal/roles.Ecosystem(); the
// grant endpoint rejects anything else with 403 ROLE_NOT_GRANTABLE, which this
// package surfaces as ErrNotGrantable — a permanent error that is never
// retried.
//
// They are duplicated rather than imported because auth-service is a separate
// Go module in a separate repository tree, with no dependency edge to
// Architecture/shared and no reason to grow one. TestEcosystemRoleList guards
// the copy by asserting the exact four strings.
const (
	RoleSeller          = "seller"
	RoleRestaurantOwner = "restaurant_owner"
	RoleDeliveryPartner = "delivery_partner"
	RoleRiderPartner    = "rider_partner"
)

// Ecosystem returns the four grantable roles in canonical order.
func Ecosystem() []string {
	return []string{RoleSeller, RoleRestaurantOwner, RoleDeliveryPartner, RoleRiderPartner}
}

// IsEcosystemRole reports whether role is one identity will accept from a
// service. Callers use it to fail fast locally rather than spending a network
// round trip to be told 403.
func IsEcosystemRole(role string) bool {
	switch role {
	case RoleSeller, RoleRestaurantOwner, RoleDeliveryPartner, RoleRiderPartner:
		return true
	}
	return false
}

// Op is the direction of an intent.
type Op string

const (
	OpGrant  Op = "grant"
	OpRevoke Op = "revoke"
)

// Intent is one durable "make identity agree with us" record: grant or revoke
// exactly one role for exactly one user.
type Intent struct {
	// ID is the queue row id. Zero for an intent that has not been enqueued.
	ID int64
	// Op is grant or revoke.
	Op Op
	// UserID is the identity account. NOT the service's own partner/seller id
	// — this is the single most common wiring mistake, because food's
	// :partnerId and rider's :id are both local primary keys, not user ids.
	UserID string
	// Role is one of the four ecosystem roles.
	Role string
	// Reason is free text recorded in identity's audit row.
	Reason string
	// Attempts is how many delivery attempts have already failed.
	Attempts int
}
