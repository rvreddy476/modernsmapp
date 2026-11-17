//go:build integration

package postgres

// The admin review guards against real rows (adminreview.go).
//
//	COMMERCE_TEST_DSN=.../commerce_it_test go test -tags=integration ./internal/store/postgres/ -run AdminReview -v
//
// Each refused action must leave the row exactly as it was and write no
// review row, no moderation row and no role intent; and of two admins acting
// on one row at the same moment exactly one may win.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/atpost/shared/identityroles"
	"github.com/google/uuid"
)

// reviewSeller inserts a seller in `status` and returns its id and user id.
// A raw INSERT on purpose: several of the states below (approved, rejected,
// suspended) are exactly the ones no store call may reach from a draft.
func reviewSeller(t *testing.T, status string) (sellerID, userID uuid.UUID) {
	t.Helper()
	sellerID, userID = uuid.New(), uuid.New()
	storeStatus := "inactive"
	switch status {
	case "approved":
		storeStatus = "active"
	case "suspended":
		storeStatus = "suspended"
	}
	mustExec(t, `INSERT INTO sellers (id,user_id,store_name,slug,email,state,status,store_status)
	             VALUES ($1,$2,'Review Guard Store',$3,'review-guard@example.test','KA',$4,$5)`,
		sellerID, userID, "review-guard-"+sellerID.String()[:12], status, storeStatus)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = testPool.Exec(ctx, `DELETE FROM product_moderation_log WHERE product_id IN (SELECT id FROM products WHERE seller_id=$1)`, sellerID)
		_, _ = testPool.Exec(ctx, `DELETE FROM product_offers WHERE seller_id=$1`, sellerID)
		_, _ = testPool.Exec(ctx, `DELETE FROM products WHERE seller_id=$1`, sellerID)
		_, _ = testPool.Exec(ctx, `DELETE FROM seller_onboarding_reviews WHERE seller_id=$1`, sellerID)
		_, _ = testPool.Exec(ctx, `DELETE FROM identity_role_intents WHERE user_id=$1`, userID)
		_, _ = testPool.Exec(ctx, `DELETE FROM sellers WHERE id=$1`, sellerID)
	})
	return sellerID, userID
}

// reviewProduct inserts a product of sellerID with the given approval_status,
// and its offer (the whole-estate offer checker requires one).
func reviewProduct(t *testing.T, sellerID uuid.UUID, approvalStatus string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	status, published := "draft", "NULL"
	if approvalStatus == "approved" {
		status, published = "active", "NOW()"
	}
	mustExec(t, `INSERT INTO products (id,seller_id,title,slug,status,approval_status,return_policy_type,published_at)
	             VALUES ($1,$2,'Review Guard Product',$3,$4,$5,'7_days',`+published+`)`,
		id, sellerID, "review-guard-"+id.String()[:12], status, approvalStatus)
	seedOfferFor(t, id)
	return id
}

type sellerRow struct {
	Status, StoreStatus string
	Approved, Rejected  bool
	Reviews, Intents    int
}

func readSellerRow(t *testing.T, sellerID, userID uuid.UUID) sellerRow {
	t.Helper()
	ctx := context.Background()
	var r sellerRow
	if err := testPool.QueryRow(ctx, `
		SELECT status, store_status, approved_at IS NOT NULL, rejected_at IS NOT NULL
		  FROM sellers WHERE id=$1`, sellerID).Scan(&r.Status, &r.StoreStatus, &r.Approved, &r.Rejected); err != nil {
		t.Fatalf("read seller: %v", err)
	}
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM seller_onboarding_reviews WHERE seller_id=$1`, sellerID).Scan(&r.Reviews); err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM identity_role_intents WHERE user_id=$1`, userID).Scan(&r.Intents); err != nil {
		t.Fatalf("count intents: %v", err)
	}
	return r
}

type productRow struct {
	Approval, Status       string
	Published              bool
	OfferApproval, OfferSt string
	Log                    int
}

func readProductRow(t *testing.T, productID uuid.UUID) productRow {
	t.Helper()
	ctx := context.Background()
	var r productRow
	if err := testPool.QueryRow(ctx, `
		SELECT p.approval_status, p.status, p.published_at IS NOT NULL, o.approval_status, o.status
		  FROM products p JOIN product_offers o ON o.product_id = p.id AND o.seller_id = p.seller_id
		 WHERE p.id=$1`, productID).Scan(&r.Approval, &r.Status, &r.Published, &r.OfferApproval, &r.OfferSt); err != nil {
		t.Fatalf("read product: %v", err)
	}
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM product_moderation_log WHERE product_id=$1`, productID).Scan(&r.Log); err != nil {
		t.Fatalf("count moderation log: %v", err)
	}
	return r
}

func reviewStore() *Store {
	return New(testPool).WithRoleIntents(identityroles.NewOutbox("", "commerce-service"))
}

// ─── Sellers ────────────────────────────────────────────────────────────

func TestAdminReview_ApproveRefusesEverySellerNotInTheQueue(t *testing.T) {
	ctx := context.Background()
	s := reviewStore()
	for _, status := range []string{"draft", "approved", "rejected", "suspended", "disabled"} {
		t.Run(status, func(t *testing.T) {
			sellerID, userID := reviewSeller(t, status)
			before := readSellerRow(t, sellerID, userID)

			err := s.ApproveSellerByAdmin(ctx, sellerID, uuid.New(), "should not work")
			var te *SellerTransitionError
			if !errors.As(err, &te) {
				t.Fatalf("approving a %s seller: %v, want a *SellerTransitionError", status, err)
			}
			if te.Status != status || te.Action != SellerActionApprove {
				t.Errorf("refusal carries %+v, want action approve and status %s", te, status)
			}
			if after := readSellerRow(t, sellerID, userID); after != before {
				t.Fatalf("a refused approval wrote: before %+v, after %+v", before, after)
			}
		})
	}
}

func TestAdminReview_ApproveMovesASubmittedSellerOnce(t *testing.T) {
	ctx := context.Background()
	s := reviewStore()
	for _, status := range []string{"submitted", "under_review", "changes_required"} {
		t.Run(status, func(t *testing.T) {
			sellerID, userID := reviewSeller(t, status)
			if err := s.ApproveSellerByAdmin(ctx, sellerID, uuid.New(), "ok"); err != nil {
				t.Fatalf("approving a %s seller: %v", status, err)
			}
			got := readSellerRow(t, sellerID, userID)
			want := sellerRow{Status: "approved", StoreStatus: "active", Approved: true, Reviews: 1, Intents: 1}
			if got != want {
				t.Fatalf("after approval: %+v, want %+v", got, want)
			}
			// And a second approval is refused and writes nothing more.
			err := s.ApproveSellerByAdmin(ctx, sellerID, uuid.New(), "again")
			if !errors.Is(err, ErrSellerTransitionNotAllowed) {
				t.Fatalf("approving an approved seller: %v, want ErrSellerTransitionNotAllowed", err)
			}
			if again := readSellerRow(t, sellerID, userID); again != want {
				t.Fatalf("the refused second approval wrote: %+v, want %+v", again, want)
			}
		})
	}
}

func TestAdminReview_UnknownSellerIsNotFoundForEveryAction(t *testing.T) {
	ctx := context.Background()
	s := reviewStore()
	id, actor := uuid.New(), uuid.New()
	for name, err := range map[string]error{
		"approve":         s.ApproveSellerByAdmin(ctx, id, actor, ""),
		"reject":          s.RejectSellerByAdmin(ctx, id, actor, "r", ""),
		"request_changes": s.RequestSellerChanges(ctx, id, actor, "c", ""),
		"suspend":         s.SuspendSellerByAdmin(ctx, id, actor, "r", ""),
		"unsuspend":       s.UnsuspendSellerByAdmin(ctx, id, actor, ""),
	} {
		if !errors.Is(err, ErrSellerNotFound) {
			t.Errorf("%s of an unknown seller: %v, want ErrSellerNotFound", name, err)
		}
	}
}

// Every other action, from every state it may not move from.
func TestAdminReview_OtherSellerActionsRefuseTheWrongState(t *testing.T) {
	ctx := context.Background()
	s := reviewStore()
	actions := map[string]func(uuid.UUID) error{
		SellerActionReject: func(id uuid.UUID) error { return s.RejectSellerByAdmin(ctx, id, uuid.New(), "reason", "") },
		SellerActionRequestChanges: func(id uuid.UUID) error {
			return s.RequestSellerChanges(ctx, id, uuid.New(), "changes", "")
		},
		SellerActionSuspend:   func(id uuid.UUID) error { return s.SuspendSellerByAdmin(ctx, id, uuid.New(), "reason", "") },
		SellerActionUnsuspend: func(id uuid.UUID) error { return s.UnsuspendSellerByAdmin(ctx, id, uuid.New(), "") },
	}
	for action, run := range actions {
		for _, status := range allSellerStatuses {
			if contains(SellerActionAllowedFrom(action), status) {
				continue
			}
			t.Run(action+"/"+status, func(t *testing.T) {
				sellerID, userID := reviewSeller(t, status)
				before := readSellerRow(t, sellerID, userID)
				err := run(sellerID)
				var te *SellerTransitionError
				if !errors.As(err, &te) || te.Status != status || te.Action != action {
					t.Fatalf("%s of a %s seller: %v, want a *SellerTransitionError naming both", action, status, err)
				}
				if after := readSellerRow(t, sellerID, userID); after != before {
					t.Fatalf("a refused %s wrote: before %+v, after %+v", action, before, after)
				}
			})
		}
	}
}

// The two side doors to `approved` that the unguarded writes left open.
func TestAdminReview_NoSideDoorApprovesADraft(t *testing.T) {
	ctx := context.Background()
	s := reviewStore()
	admin := uuid.New()

	// request changes, then approve out of the queue.
	sellerID, userID := reviewSeller(t, "draft")
	if err := s.RequestSellerChanges(ctx, sellerID, admin, "anything", ""); !errors.Is(err, ErrSellerTransitionNotAllowed) {
		t.Fatalf("request-changes on a draft: %v, want a refusal", err)
	}
	if err := s.ApproveSellerByAdmin(ctx, sellerID, admin, ""); !errors.Is(err, ErrSellerTransitionNotAllowed) {
		t.Fatalf("approve after the refused request-changes: %v, want a refusal", err)
	}
	// suspend, then lift the suspension.
	if err := s.SuspendSellerByAdmin(ctx, sellerID, admin, "anything", ""); !errors.Is(err, ErrSellerTransitionNotAllowed) {
		t.Fatalf("suspend on a draft: %v, want a refusal", err)
	}
	if err := s.UnsuspendSellerByAdmin(ctx, sellerID, admin, ""); !errors.Is(err, ErrSellerTransitionNotAllowed) {
		t.Fatalf("unsuspend on a draft: %v, want a refusal", err)
	}
	got := readSellerRow(t, sellerID, userID)
	if want := (sellerRow{Status: "draft", StoreStatus: "inactive"}); got != want {
		t.Fatalf("the draft moved: %+v, want %+v", got, want)
	}
}

func TestAdminReview_TwoConcurrentSellerApprovalsOneWins(t *testing.T) {
	ctx := context.Background()
	s := reviewStore()
	const admins = 8
	for round := 0; round < 5; round++ {
		sellerID, userID := reviewSeller(t, "submitted")
		start := make(chan struct{})
		errs := make([]error, admins)
		var wg sync.WaitGroup
		for i := 0; i < admins; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				errs[i] = s.ApproveSellerByAdmin(ctx, sellerID, uuid.New(), "concurrent")
			}(i)
		}
		close(start)
		wg.Wait()

		won := 0
		for i, err := range errs {
			switch {
			case err == nil:
				won++
			case errors.Is(err, ErrSellerTransitionNotAllowed):
			default:
				t.Fatalf("round %d admin %d: unexpected error %v", round, i, err)
			}
		}
		if won != 1 {
			t.Fatalf("round %d: %d of %d concurrent approvals succeeded, want exactly 1", round, won, admins)
		}
		got := readSellerRow(t, sellerID, userID)
		if want := (sellerRow{Status: "approved", StoreStatus: "active", Approved: true, Reviews: 1, Intents: 1}); got != want {
			t.Fatalf("round %d: after the race %+v, want %+v (one review row, one role grant)", round, got, want)
		}
	}
}

// An approve racing a reject: one decision stands and the other is refused;
// the loser never overwrites the winner.
func TestAdminReview_ApproveRacingRejectOneDecisionStands(t *testing.T) {
	ctx := context.Background()
	s := reviewStore()
	for round := 0; round < 10; round++ {
		sellerID, userID := reviewSeller(t, "submitted")
		start := make(chan struct{})
		var approveErr, rejectErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			approveErr = s.ApproveSellerByAdmin(ctx, sellerID, uuid.New(), "race")
		}()
		go func() {
			defer wg.Done()
			<-start
			rejectErr = s.RejectSellerByAdmin(ctx, sellerID, uuid.New(), "race", "")
		}()
		close(start)
		wg.Wait()

		got := readSellerRow(t, sellerID, userID)
		switch {
		case approveErr == nil && errors.Is(rejectErr, ErrSellerTransitionNotAllowed):
			if want := (sellerRow{Status: "approved", StoreStatus: "active", Approved: true, Reviews: 1, Intents: 1}); got != want {
				t.Fatalf("round %d: approve won, row is %+v, want %+v", round, got, want)
			}
		case rejectErr == nil && errors.Is(approveErr, ErrSellerTransitionNotAllowed):
			if want := (sellerRow{Status: "rejected", StoreStatus: "inactive", Rejected: true, Reviews: 1, Intents: 1}); got != want {
				t.Fatalf("round %d: reject won, row is %+v, want %+v", round, got, want)
			}
		default:
			t.Fatalf("round %d: approve=%v reject=%v, want exactly one success and one refusal (row %+v)",
				round, approveErr, rejectErr, got)
		}
	}
}

// ─── Products ───────────────────────────────────────────────────────────

func TestAdminReview_ApproveRefusesAProductNotInTheQueue(t *testing.T) {
	ctx := context.Background()
	s := New(testPool)
	sellerID, _ := reviewSeller(t, "approved")
	for _, approval := range []string{"draft", "approved", "rejected", "changes_requested", "hidden", "archived"} {
		t.Run(approval, func(t *testing.T) {
			productID := reviewProduct(t, sellerID, approval)
			before := readProductRow(t, productID)

			err := s.ApproveProductByAdmin(ctx, productID, uuid.New(), "should not work")
			var te *ProductTransitionError
			if !errors.As(err, &te) || te.ApprovalStatus != approval {
				t.Fatalf("approving a %s product: %v, want a *ProductTransitionError", approval, err)
			}
			if after := readProductRow(t, productID); after != before {
				t.Fatalf("a refused approval wrote: before %+v, after %+v", before, after)
			}
			// The automatic approval is the same write and the same guard.
			if err := s.AutoApproveProduct(ctx, productID); !errors.Is(err, ErrProductTransitionNotAllowed) {
				t.Fatalf("auto-approving a %s product: %v, want a refusal", approval, err)
			}
			if after := readProductRow(t, productID); after != before {
				t.Fatalf("a refused auto-approval wrote: before %+v, after %+v", before, after)
			}
		})
	}
}

func TestAdminReview_ApproveRefusesAProductOfAnUnapprovedSeller(t *testing.T) {
	ctx := context.Background()
	s := New(testPool)
	// A submitted listing whose shop is no longer (or was never) approved:
	// suspended or sent back while the listing waited, or a row written
	// before the submit gate asked.
	for _, sellerStatus := range []string{"draft", "submitted", "changes_required", "rejected", "suspended"} {
		t.Run(sellerStatus, func(t *testing.T) {
			sellerID, _ := reviewSeller(t, sellerStatus)
			productID := reviewProduct(t, sellerID, "submitted")
			before := readProductRow(t, productID)

			err := s.ApproveProductByAdmin(ctx, productID, uuid.New(), "should not work")
			var se *ProductSellerNotApprovedError
			if !errors.As(err, &se) || se.SellerStatus != sellerStatus {
				t.Fatalf("approving a product of a %s seller: %v, want a *ProductSellerNotApprovedError", sellerStatus, err)
			}
			if after := readProductRow(t, productID); after != before {
				t.Fatalf("a refused approval wrote: before %+v, after %+v", before, after)
			}
		})
	}
}

func TestAdminReview_ApproveMovesASubmittedProductOfAnApprovedSellerOnce(t *testing.T) {
	ctx := context.Background()
	s := New(testPool)
	sellerID, _ := reviewSeller(t, "approved")
	productID := reviewProduct(t, sellerID, "submitted")

	if err := s.ApproveProductByAdmin(ctx, productID, uuid.New(), "ok"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	want := productRow{Approval: "approved", Status: "active", Published: true,
		OfferApproval: "approved", OfferSt: "active", Log: 1}
	if got := readProductRow(t, productID); got != want {
		t.Fatalf("after approval: %+v, want %+v", got, want)
	}
	if err := s.ApproveProductByAdmin(ctx, productID, uuid.New(), "again"); !errors.Is(err, ErrProductTransitionNotAllowed) {
		t.Fatalf("approving an approved product: %v, want a refusal", err)
	}
	if got := readProductRow(t, productID); got != want {
		t.Fatalf("the refused second approval wrote: %+v, want %+v", got, want)
	}
}

func TestAdminReview_UnknownProductIsNotFoundForEveryAction(t *testing.T) {
	ctx := context.Background()
	s := New(testPool)
	id, actor := uuid.New(), uuid.New()
	for name, err := range map[string]error{
		"approve":         s.ApproveProductByAdmin(ctx, id, actor, ""),
		"auto_approve":    s.AutoApproveProduct(ctx, id),
		"reject":          s.RejectProductByAdmin(ctx, id, actor, "r"),
		"request_changes": s.RequestProductChangesByAdmin(ctx, id, actor, "m"),
	} {
		if !errors.Is(err, ErrProductNotFound) {
			t.Errorf("%s of an unknown product: %v, want ErrProductNotFound", name, err)
		}
	}
}

func TestAdminReview_RejectAndRequestChangesRefuseAProductNeverSubmitted(t *testing.T) {
	ctx := context.Background()
	s := New(testPool)
	sellerID, _ := reviewSeller(t, "approved")
	actions := map[string]func(uuid.UUID) error{
		ProductActionReject:         func(id uuid.UUID) error { return s.RejectProductByAdmin(ctx, id, uuid.New(), "reason") },
		ProductActionRequestChanges: func(id uuid.UUID) error { return s.RequestProductChangesByAdmin(ctx, id, uuid.New(), "message") },
	}
	for action, run := range actions {
		for _, approval := range []string{"draft", "rejected", "changes_requested", "hidden", "archived"} {
			t.Run(action+"/"+approval, func(t *testing.T) {
				productID := reviewProduct(t, sellerID, approval)
				before := readProductRow(t, productID)
				if err := run(productID); !errors.Is(err, ErrProductTransitionNotAllowed) {
					t.Fatalf("%s of a %s product: %v, want a refusal", action, approval, err)
				}
				if after := readProductRow(t, productID); after != before {
					t.Fatalf("a refused %s wrote: before %+v, after %+v", action, before, after)
				}
			})
		}
		// The takedown still works, whatever the shop's state.
		t.Run(action+"/takedown of a live listing of a suspended shop", func(t *testing.T) {
			suspended, _ := reviewSeller(t, "suspended")
			productID := reviewProduct(t, suspended, "approved")
			if err := run(productID); err != nil {
				t.Fatalf("%s of a live listing: %v", action, err)
			}
			if got := readProductRow(t, productID); got.Approval == "approved" || got.OfferApproval == "approved" || got.Log != 1 {
				t.Fatalf("after %s the listing is %+v", action, got)
			}
		})
	}
}

func TestAdminReview_TwoConcurrentProductApprovalsOneWins(t *testing.T) {
	ctx := context.Background()
	s := New(testPool)
	sellerID, _ := reviewSeller(t, "approved")
	const moderators = 8
	for round := 0; round < 5; round++ {
		productID := reviewProduct(t, sellerID, "submitted")
		start := make(chan struct{})
		errs := make([]error, moderators)
		var wg sync.WaitGroup
		for i := 0; i < moderators; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				errs[i] = s.ApproveProductByAdmin(ctx, productID, uuid.New(), "concurrent")
			}(i)
		}
		close(start)
		wg.Wait()

		won := 0
		for i, err := range errs {
			switch {
			case err == nil:
				won++
			case errors.Is(err, ErrProductTransitionNotAllowed):
			default:
				t.Fatalf("round %d moderator %d: unexpected error %v", round, i, err)
			}
		}
		if won != 1 {
			t.Fatalf("round %d: %d of %d concurrent approvals succeeded, want exactly 1", round, won, moderators)
		}
		want := productRow{Approval: "approved", Status: "active", Published: true,
			OfferApproval: "approved", OfferSt: "active", Log: 1}
		if got := readProductRow(t, productID); got != want {
			t.Fatalf("round %d: after the race %+v, want %+v (one moderation row)", round, got, want)
		}
	}
}

// ─── onboarding/start ───────────────────────────────────────────────────

// The row StartSellerOnboarding hands back carries what was stored. It used
// to carry an empty status while the status read said `draft`.
func TestAdminReview_StartOnboardingReturnsTheStoredStatus(t *testing.T) {
	s := reviewStore()
	userID := uuid.New()
	sel := &Seller{
		UserID: userID, SellerType: "individual", BusinessType: "individual",
		StoreName: "Start Status Store",
		Slug:      "start-status-" + userID.String()[:12],
		Email:     "start-status@example.test",
	}
	if err := s.StartSellerOnboarding(context.Background(), sel); err != nil {
		t.Fatalf("StartSellerOnboarding: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM identity_role_intents WHERE user_id=$1`, userID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM sellers WHERE id=$1`, sel.ID)
	})
	if sel.Status != "draft" || sel.OnboardingStep != 1 || sel.SellScope != SellScopeAllCategories {
		t.Fatalf("start returned status=%q step=%d sell_scope=%q, want draft / 1 / all_categories",
			sel.Status, sel.OnboardingStep, sel.SellScope)
	}
	stored, err := s.GetSellerOnboardingStatus(context.Background(), userID)
	if err != nil {
		t.Fatalf("status read: %v", err)
	}
	if stored.Status != sel.Status || stored.OnboardingStep != sel.OnboardingStep || stored.SellScope != sel.SellScope {
		t.Fatalf("start said %q/%d/%q, the status read says %q/%d/%q",
			sel.Status, sel.OnboardingStep, sel.SellScope, stored.Status, stored.OnboardingStep, stored.SellScope)
	}
}
