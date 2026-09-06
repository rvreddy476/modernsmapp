//go:build integration

package service

// Filling a compliance gap must not take the listing down.
//
// Step 13 shipped the sweeper on decision 8: making a field required later
// must NEVER delist a listing that was compliant when it was approved. It
// flags a gap; the seller fixes it on their next edit.
//
// Step 14 found that the mechanism defeated itself. Filling the field is an
// attribute edit, `substantiveFields` counts any attribute write as
// substantive, and a substantive edit to an approved listing is
// `RevalidationRequiredError` — apply it and go back to review. So the ONE
// action the dashboard asked for was the one action that took the listing
// off sale, and the rational response was to ignore the dashboard.
//
// These tests are both sides of the carve-out that resolves it. The
// permissive side is one test; the refusing side is four, because the way
// this goes wrong is not that the door fails to open, it is that it opens
// wider than it was supposed to.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/service/... \
//	  -run GapCarveOut -v -count=1

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/commerce-service/internal/store/postgres"
)

// flaggedFixture is an APPROVED, live listing with exactly one open
// compliance gap on it — the state decision 8 describes.
//
// Built the long way round (create → submit → approve → tighten the rule →
// sweep) rather than by inserting a gap row, because the carve-out reads the
// gap table and a hand-written row would prove only that the code can read
// its own fixture.
type flaggedFixture struct {
	*gateFixture
	product *postgres.Product
}

func newFlaggedFixture(t *testing.T) *flaggedFixture {
	t.Helper()
	ctx := context.Background()
	f := newGateFixture(t)

	p := f.complete() // author + pages answered; isbn optional and blank
	f.approve(p)

	// The founder makes ISBN required, after the listing was approved.
	if _, err := svcTestPool.Exec(ctx,
		`UPDATE category_attributes SET is_required = TRUE
		  WHERE category_id=$1 AND definition_id=$2`, f.categoryID, f.isbnDef); err != nil {
		t.Fatalf("tighten the rule: %v", err)
	}
	if _, err := f.svc.SweepComplianceGaps(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	open, err := f.store.OpenGapsForProduct(ctx, p.ID)
	if err != nil {
		t.Fatalf("read gaps: %v", err)
	}
	if len(open) != 1 || open[0].Code != f.isbnCode {
		t.Fatalf("the fixture does not have exactly one open gap on %q: %+v", f.isbnCode, open)
	}
	if got := f.approvalStatus(p.ID); got != "approved" {
		t.Fatalf("the fixture listing is %q, not approved, so it cannot show the carve-out", got)
	}
	return &flaggedFixture{gateFixture: f, product: p}
}

// live reports the listing's lifecycle as a buyer would experience it.
func (f *flaggedFixture) live() (status, approval string, sellable bool) {
	f.t.Helper()
	ctx := context.Background()
	if err := svcTestPool.QueryRow(ctx,
		`SELECT status, approval_status FROM products WHERE id=$1`, f.product.ID,
	).Scan(&status, &approval); err != nil {
		f.t.Fatalf("read lifecycle: %v", err)
	}
	_, ok, err := f.store.ProductSaleEligibility(ctx, f.variantID(f.product.ID))
	if err != nil {
		f.t.Fatalf("sale eligibility: %v", err)
	}
	return status, approval, ok
}

// ─── The permissive side ────────────────────────────────────────────────

// TestGapCarveOutFillingTheGapKeepsTheApproval is decision 8 actually
// working.
//
// Note what is NOT passed: `AckRevalidation`. The seller does not have to
// acknowledge a cost, because under the carve-out there is no cost — the edit
// is not substantive. A carve-out that still required the acknowledgement
// would still be telling the seller their listing is about to go down.
func TestGapCarveOutFillingTheGapKeepsTheApproval(t *testing.T) {
	ctx := context.Background()
	f := newFlaggedFixture(t)

	res, err := f.svc.UpdateProduct(ctx, UpdateProductInput{
		ActorUserID: f.userID, ProductID: f.product.ID,
		Attributes: []AttributeValueInput{{Code: f.isbnCode, Value: "9780143039655"}},
	})
	if err != nil {
		var reval *RevalidationRequiredError
		if errors.As(err, &reval) {
			t.Fatalf("filling an open compliance gap was refused as a substantive edit "+
				"(%v).\nThis is the contradiction the carve-out exists to remove: the "+
				"sweeper's only remedy would take the listing down, which is the thing "+
				"the sweeper exists to prevent.", reval.Fields)
		}
		t.Fatalf("fixing the gap: %v", err)
	}
	if res.Revalidated {
		t.Error("the result says the listing went back for review; the carve-out did not apply")
	}

	// ── And it is still on sale ──────────────────────────────
	status, approval, sellable := f.live()
	if status != "active" || approval != "approved" {
		t.Fatalf("the listing came down to fill in a field it was TOLD to fill in: "+
			"status=%q approval_status=%q", status, approval)
	}
	if !sellable {
		t.Fatal("the listing is no longer purchasable after its seller did what the " +
			"dashboard asked")
	}

	// ── And the gap is closed ────────────────────────────────
	still, err := f.store.OpenGapsForProduct(ctx, f.product.ID)
	if err != nil {
		t.Fatalf("re-read gaps: %v", err)
	}
	if len(still) != 0 {
		t.Fatalf("the gap survived the edit that filled it: %+v", still)
	}
}

// ─── The refusing side ──────────────────────────────────────────────────

// requireStillGoesToReview asserts that this edit is refused with
// RevalidationRequiredError — i.e. the carve-out did NOT open — and that the
// listing is untouched by the attempt.
func (f *flaggedFixture) requireStillGoesToReview(t *testing.T, what string, in UpdateProductInput) {
	t.Helper()
	in.ActorUserID, in.ProductID = f.userID, f.product.ID
	_, err := f.svc.UpdateProduct(context.Background(), in)

	var reval *RevalidationRequiredError
	if !errors.As(err, &reval) {
		t.Fatalf("%s: expected RevalidationRequiredError, got %v.\n"+
			"The compliance carve-out is wider than it was specified to be: it must open "+
			"ONLY for a request whose sole substantive change is filling attributes that "+
			"are already open gaps on this product.", what, err)
	}
	if status, approval, _ := f.live(); status != "active" || approval != "approved" {
		t.Errorf("%s: the refusal still moved the listing (status=%q approval=%q); a "+
			"refusal must write nothing", what, status, approval)
	}
}

// TestGapCarveOutAnUnflaggedAttributeStillGoesToReview is condition (2).
//
// `author` is a required field this listing already answers correctly, so it
// is NOT an open gap. Rewriting it is an ordinary substantive edit and costs
// the approval, exactly as before — otherwise "fill in the missing field"
// would have become "rewrite any attribute you like".
func TestGapCarveOutAnUnflaggedAttributeStillGoesToReview(t *testing.T) {
	f := newFlaggedFixture(t)
	f.requireStillGoesToReview(t, "rewriting an attribute that is not an open gap",
		UpdateProductInput{
			Attributes: []AttributeValueInput{{Code: f.authorCode, Value: "Someone Else"}},
		})
}

// TestGapCarveOutSmugglingAnUnflaggedAttributeAlongsideTheGapStillGoesToReview
// is the sharp edge of condition (2).
//
// One value the sweeper asked for, one it did not, in a single request. The
// rule is EVERY definition named, not merely one of them — a carve-out that
// checked "is any of these a gap" would let a seller rewrite anything simply
// by attaching a legitimate fix to it.
func TestGapCarveOutSmugglingAnUnflaggedAttributeAlongsideTheGapStillGoesToReview(t *testing.T) {
	f := newFlaggedFixture(t)
	f.requireStillGoesToReview(t, "a gap fill carrying an unflagged attribute with it",
		UpdateProductInput{
			Attributes: []AttributeValueInput{
				{Code: f.isbnCode, Value: "9780143039655"},
				{Code: f.authorCode, Value: "Someone Else"},
			},
		})
}

// TestGapCarveOutATitleChangeAlongsideTheGapStillGoesToReview is condition
// (1).
//
// This is the moderation bypass the revalidation rule exists to stop: get
// bland copy approved, then rewrite it. Attaching a genuine gap fix to the
// rewrite must not buy it a pass. The whole request goes to review — there is
// no partial application in which the attribute lands and the title waits.
func TestGapCarveOutATitleChangeAlongsideTheGapStillGoesToReview(t *testing.T) {
	f := newFlaggedFixture(t)
	title := "Buy Now — Cheapest On The Internet"
	f.requireStillGoesToReview(t, "a gap fill carrying a title rewrite with it",
		UpdateProductInput{
			Fields:     postgres.ProductPatch{Title: &title},
			Attributes: []AttributeValueInput{{Code: f.isbnCode, Value: "9780143039655"}},
		})
}

// TestGapCarveOutTheSellerCannotOpenTheDoorThemselves is condition (3), and
// it is the one that decides whether this is safe at all.
//
// The attack: manufacture a gap by blanking a required field, let the sweep
// flag it, then use the carve-out to write anything into it. The first move
// is where it fails — blanking `author` is an attribute edit on a definition
// that is not YET an open gap, so it is refused by condition (2) before any
// gap can be created. The door cannot be opened from the inside; only an
// operator making a field required opens it.
func TestGapCarveOutTheSellerCannotOpenTheDoorThemselves(t *testing.T) {
	ctx := context.Background()
	f := newFlaggedFixture(t)

	// Step one of the attack, on its own terms. Either the value validation
	// refuses a blank required field outright, or the edit is substantive and
	// costs the approval. Both are fine; what must NOT happen is the edit
	// applying in place, because that is a gap the seller created at no cost.
	_, err := f.svc.UpdateProduct(ctx, UpdateProductInput{
		ActorUserID: f.userID, ProductID: f.product.ID,
		Attributes: []AttributeValueInput{{Code: f.authorCode, Value: ""}},
	})
	if err == nil {
		t.Fatal("a seller blanked a required attribute on an approved listing and the edit " +
			"applied in place. That is a compliance gap they manufactured for free, and the " +
			"next edit through the carve-out could write anything into it.")
	}

	// And nothing moved.
	if status, approval, _ := f.live(); status != "active" || approval != "approved" {
		t.Errorf("the refused attack still moved the listing: status=%q approval=%q",
			status, approval)
	}
	open, err := f.store.OpenGapsForProduct(ctx, f.product.ID)
	if err != nil {
		t.Fatalf("read gaps: %v", err)
	}
	for _, g := range open {
		if g.DefinitionID != nil && *g.DefinitionID == f.authorDef {
			t.Fatal("the attack opened a gap on `author` after all")
		}
	}
}
