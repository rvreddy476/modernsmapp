package postgres

// The allowlist, and the state machine that decides whether an edit is
// permitted at all. Neither needs a database — both are the shape of the
// contract rather than the behaviour of a query.

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The columns a seller must never be able to name.
//
// `UpdateProduct` interpolated the caller's map KEYS into the statement as
// column names, so every one of these was one un-audited request body away
// from being written. The replacement is a struct, and this asserts the
// struct's assignment builder cannot emit any of them.
func TestTheProductPatchCannotWriteTheColumnsThatDecideOwnershipOrApproval(t *testing.T) {
	forbidden := []string{
		"seller_id", "slug", "approval_status", "rejection_reason",
		"moderation_flags", "status", "visibility", "published_at",
		"avg_rating", "review_count", "order_count", "view_count",
		"wishlist_count", "is_featured", "attributes_doc",
		"source_image_url", "gtin", "created_at",
	}

	// A patch with EVERY client-settable field populated, so the assertion is
	// about what the builder can emit at its widest rather than about a
	// sparse example.
	id := uuid.New()
	s, n, f := "x", 1, 1.0
	kw := []string{"k"}
	p := ProductPatch{
		CategoryID: &id, BrandID: &id, TaxClassID: &id,
		Title: &s, ShortTitle: &s, Description: &s, ShortDescription: &s,
		BrandName: &s, ManufacturerName: &s, ProductType: &s, Condition: &s,
		PrimaryImageMediaID: &id, VideoMediaID: &id,
		WeightGrams: &n, LengthCm: &f, WidthCm: &f, HeightCm: &f,
		CountryOfOrigin: &s, WarrantyInfo: &s,
		ReturnPolicyType: &s, ReturnPolicyDays: &n, HSNCode: &s,
		SearchKeywords: &kw, MetaTitle: &s, MetaDescription: &s,
	}

	sets, args := p.assignments()
	if len(sets) != len(args) {
		t.Fatalf("every assignment must carry exactly one bound argument; got %d sets and %d args",
			len(sets), len(args))
	}
	joined := strings.Join(sets, " ")
	for _, col := range forbidden {
		if strings.Contains(joined, col+"=") {
			t.Fatalf("the patch emitted %q, which is not seller-editable:\n%s", col, joined)
		}
	}

	// And the positive half: an empty patch writes nothing at all, so a
	// request that names no field cannot bump updated_at on every product a
	// loop happens to touch.
	if empty := (ProductPatch{}); empty.TouchesAnyColumn() {
		t.Fatal("an empty patch reported that it would write something")
	}
}

// approval_status IS written by this struct, in exactly one case: the
// revalidation bounce, which is set by the service after the caller
// acknowledged it and never from a request body.
func TestOnlyTheRevalidationFlagCanTouchApprovalStatus(t *testing.T) {
	title := "New title"

	plain, _ := ProductPatch{Title: &title}.assignments()
	if strings.Contains(strings.Join(plain, " "), "approval_status") {
		t.Fatalf("a plain title patch touched approval_status: %v", plain)
	}

	bounced, args := ProductPatch{Title: &title, Revalidate: true}.assignments()
	joined := strings.Join(bounced, " ")
	for _, want := range []string{
		"approval_status='submitted'", "status='draft'", "published_at=NULL",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("a revalidating patch must set %s; got %v", want, bounced)
		}
	}
	// The three are literals, not parameters — they are the fixed consequence
	// of the rule rather than values anybody chose.
	if len(args) != 1 {
		t.Fatalf("the revalidation assignments must not consume bound arguments; got %d args", len(args))
	}
}

// The state machine, spelled out. Each arm is a decision with a reason
// attached; a change to any of them should have to change this table.
func TestProductEditabilityPerReviewState(t *testing.T) {
	for _, tc := range []struct {
		status            string
		editable          bool
		needsRevalidation bool
		why               string
	}{
		{"draft", true, false, "the state a create leaves behind"},
		{"pending", true, false, "the schema DEFAULT, carried by every pre-'draft' row"},
		{"rejected", true, false, "fix and resubmit is the only useful response"},
		{"changes_requested", true, false, "the reviewer asked for exactly this"},
		{"flagged", true, false, "editing is how a flag is cleared"},
		{"approved", true, true, "editable, but the approval was for the old content"},
		{"submitted", false, false, "a reviewer is reading it right now"},
		{"under_review", false, false, "likewise"},
		{"hidden", false, false, "an operator took it down; editing routes around them"},
		{"archived", false, false, "retired, and referenced by order history"},
		{"live", false, false, "the retired spelling migration 022 converts; not a state to edit from"},
		{"", false, false, "an unknown state is refused rather than assumed safe"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			editable, needsRevalidation := ProductEditability(tc.status)
			if editable != tc.editable {
				t.Fatalf("editable=%v, want %v (%s)", editable, tc.editable, tc.why)
			}
			if needsRevalidation != tc.needsRevalidation {
				t.Fatalf("needsRevalidation=%v, want %v (%s)",
					needsRevalidation, tc.needsRevalidation, tc.why)
			}
		})
	}
}

// An empty string clears a nullable text column rather than storing "".
//
// Storing the empty string leaves `warranty_info <> NULL` true, so every
// "does this listing have a warranty note?" read answers yes and renders an
// empty paragraph.
func TestClearingATextFieldStoresNullRatherThanAnEmptyString(t *testing.T) {
	blank := "   "
	_, args := ProductPatch{WarrantyInfo: &blank}.assignments()
	if len(args) != 1 {
		t.Fatalf("expected one assignment, got %d", len(args))
	}
	if args[0] != nil {
		t.Fatalf("a blank warranty note must become SQL NULL, got %#v", args[0])
	}
}
