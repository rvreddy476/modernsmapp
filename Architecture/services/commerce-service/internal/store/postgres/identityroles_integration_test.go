//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/atpost/shared/identityroles"
	"github.com/google/uuid"
)

// The seller-lifecycle role intents.
//
// These test the seam that matters: a seller row and its identity role intent
// land in ONE transaction in commerce's own database. Nothing here talks to
// identity — delivery is the worker's job and is covered in
// shared/identityroles.
//
//	COMMERCE_TEST_DSN=postgres://postgres:postgres@127.0.0.1:5432/commerce_it_test?sslmode=disable \
//	  go test -tags=integration ./internal/store/postgres/ -run IdentityRole -v
//
// refuseTheLiveDatabase in TestMain stops this pointing at commerce_db.

func rolesStore(t *testing.T) *Store {
	t.Helper()
	return New(testPool).WithRoleIntents(identityroles.NewOutbox("", "commerce-service"))
}

func roleIntentsFor(t *testing.T, userID uuid.UUID) []string {
	t.Helper()
	rows, err := testPool.Query(context.Background(),
		`SELECT op FROM identity_role_intents WHERE user_id = $1 ORDER BY id`, userID)
	if err != nil {
		t.Fatalf("read intents: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var op string
		if err := rows.Scan(&op); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, op)
	}
	return out
}

// newDraftSeller runs the real StartSellerOnboarding so the test exercises the
// production path, not a hand-rolled INSERT.
func newDraftSeller(t *testing.T, s *Store) (sellerID, userID uuid.UUID) {
	t.Helper()
	userID = uuid.New()
	sel := &Seller{
		UserID:     userID,
		SellerType: "individual", BusinessType: "individual",
		StoreName: "Role Test Store",
		Slug:      "role-test-" + userID.String()[:8],
		Email:     "role-test-" + userID.String()[:8] + "@example.test",
	}
	if err := s.StartSellerOnboarding(context.Background(), sel); err != nil {
		t.Fatalf("StartSellerOnboarding: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = testPool.Exec(ctx, `DELETE FROM seller_onboarding_reviews WHERE seller_id=$1`, sel.ID)
		_, _ = testPool.Exec(ctx, `DELETE FROM sellers WHERE id=$1`, sel.ID)
		_, _ = testPool.Exec(ctx, `DELETE FROM identity_role_intents WHERE user_id=$1`, userID)
	})
	return sel.ID, userID
}

func TestIdentityRole_GrantedAtOnboardingStartNotAtApproval(t *testing.T) {
	s := rolesStore(t)
	_, userID := newDraftSeller(t, s)

	got := roleIntentsFor(t, userID)
	if len(got) != 1 || got[0] != "grant" {
		t.Fatalf("intents = %v, want one grant. The role is granted at status='draft' "+
			"because the seller must reach the seller area to finish the nine onboarding "+
			"steps and submit — gating it on approval would lock them out of the flow "+
			"that gets them approved", got)
	}
}

func TestIdentityRole_ApprovalReGrants(t *testing.T) {
	s := rolesStore(t)
	sellerID, userID := newDraftSeller(t, s)

	if err := s.ApproveSellerByAdmin(context.Background(), sellerID, uuid.New(), "looks good"); err != nil {
		t.Fatalf("ApproveSellerByAdmin: %v", err)
	}
	got := roleIntentsFor(t, userID)
	if len(got) != 2 || got[1] != "grant" {
		t.Fatalf("intents = %v, want a second grant. Approval re-grants deliberately: "+
			"identity's grant is idempotent and this is the repair for a create-time "+
			"intent that dead-lettered during an outage", got)
	}
}

func TestIdentityRole_RejectionRevokes(t *testing.T) {
	s := rolesStore(t)
	sellerID, userID := newDraftSeller(t, s)

	if err := s.RejectSellerByAdmin(context.Background(), sellerID, uuid.New(), "fake docs", ""); err != nil {
		t.Fatalf("RejectSellerByAdmin: %v", err)
	}
	got := roleIntentsFor(t, userID)
	if len(got) != 2 || got[1] != "revoke" {
		t.Fatalf("intents = %v, want a revoke. Rejection is terminal in commerce — "+
			"SubmitSellerApplication only accepts status='draft', so a rejected seller "+
			"cannot resubmit", got)
	}
}

func TestIdentityRole_SuspensionDoesNotRevoke(t *testing.T) {
	s := rolesStore(t)
	sellerID, userID := newDraftSeller(t, s)

	if err := s.SuspendSellerByAdmin(context.Background(), sellerID, uuid.New(), "policy", ""); err != nil {
		t.Fatalf("SuspendSellerByAdmin: %v", err)
	}
	got := roleIntentsFor(t, userID)
	if len(got) != 1 {
		t.Fatalf("intents = %v, want only the create-time grant. A suspended seller is "+
			"still a seller — they must reach the seller area to read why and to appeal, "+
			"and sellers.status already carries the suspension", got)
	}
}

func TestIdentityRole_RequestChangesDoesNotRevoke(t *testing.T) {
	s := rolesStore(t)
	sellerID, userID := newDraftSeller(t, s)

	if err := s.RequestSellerChanges(context.Background(), sellerID, uuid.New(), "fix the GST", ""); err != nil {
		t.Fatalf("RequestSellerChanges: %v", err)
	}
	got := roleIntentsFor(t, userID)
	if len(got) != 1 {
		t.Fatalf("intents = %v, want only the create-time grant. 'changes_required' is the "+
			"reversible outcome — that applicant still has to reach the seller area to "+
			"make the changes", got)
	}
}

func TestIdentityRole_UnattachedQueueIsANoOp(t *testing.T) {
	// The degrade path: identity unconfigured, every seller write still works.
	s := New(testPool)
	userID := uuid.New()
	sel := &Seller{
		UserID: userID, SellerType: "individual", BusinessType: "individual",
		StoreName: "No Identity",
		Slug:      "no-identity-" + userID.String()[:8], Email: "ni@example.test",
	}
	if err := s.StartSellerOnboarding(context.Background(), sel); err != nil {
		t.Fatalf("StartSellerOnboarding with no role queue must still succeed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM sellers WHERE id=$1`, sel.ID)
	})
	if got := roleIntentsFor(t, userID); len(got) != 0 {
		t.Fatalf("intents = %v, want none", got)
	}
}

func TestIdentityRole_ApproveUnknownSellerIsNotFound(t *testing.T) {
	// Behaviour change worth pinning: the approve/reject writes moved from
	// Exec to QueryRow ... RETURNING user_id, so an unknown sellerId is now
	// pgx.ErrNoRows (which writeCommerceError renders as 404) instead of a
	// silent 204.
	s := rolesStore(t)
	if err := s.ApproveSellerByAdmin(context.Background(), uuid.New(), uuid.New(), ""); err == nil {
		t.Fatal("approving an unknown seller must be an error, not a silent success")
	}
}
