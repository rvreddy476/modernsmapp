package postgres

import (
	"strings"
	"testing"
)

// Slice C / C-P0-4 — the reference inventory must stay honest.
//
// Plain unit tests over the declared classification and the composed predicate.
// The catalog-walking exhaustiveness check that needs a live database lives in
// reclaim_policy_integration_test.go.

func resolveAll(t *testing.T) []resolvedReference {
	t.Helper()
	out := make([]resolvedReference, 0, len(LiveMediaReferences))
	for _, ref := range LiveMediaReferences {
		out = append(out, resolvedReference{ref: ref, isUUID: true})
	}
	return out
}

// Nothing may be both a live claim and a derived by-product: the first blocks
// reclamation and the second is ignored by it, so a table in both lists means
// the policy contradicts itself and the outcome depends on evaluation order.
func TestLiveAndDerivedListsDoNotOverlap(t *testing.T) {
	derived := make(map[string]bool, len(DerivedMediaTables))
	for _, table := range DerivedMediaTables {
		derived[table] = true
	}
	for _, ref := range LiveMediaReferences {
		if derived[ref.Table] {
			t.Errorf("%s is listed as BOTH a live reference and a derived table; "+
				"one of the two classifications is wrong", ref.Table)
		}
	}
}

// The predicate that FINDS orphans and the predicate that CONFIRMS them are
// built from one list, so they cannot drift.
func TestLiveReferencePredicateCoversEveryResolvedReference(t *testing.T) {
	sql := liveReferenceSQL(resolveAll(t), "$1")

	for _, ref := range LiveMediaReferences {
		if !strings.Contains(sql, "FROM "+ref.Table+" ") {
			t.Errorf("predicate does not query %s; media held only by that table "+
				"would be reclaimed while still in use", ref.Table)
		}
		if !strings.Contains(sql, ref.Table+"."+ref.Column+" = $1") {
			t.Errorf("predicate does not match %s.%s", ref.Table, ref.Column)
		}
	}
}

// An EMPTY resolved set must block everything, not permit everything.
//
// `NOT (false)` is `true`, so a predicate that collapsed to `false` when no
// reference table resolved would make every asset a candidate. The safe
// direction when we know nothing is to reclaim nothing.
func TestEmptyResolutionBlocksAllReclamation(t *testing.T) {
	if got := liveReferenceSQL(nil, "$1"); got != "TRUE" {
		t.Fatalf("an empty reference set must yield TRUE (nothing reclaimable), got %q", got)
	}
}

// owner_media_slots is the one whose omission is SILENT.
//
// It references media_assets with ON DELETE CASCADE, so reclaiming an asset
// held only by a profile slot deletes a live avatar and raises no constraint
// error at all — the sweep reports success.
func TestOwnerMediaSlotsIsProtected(t *testing.T) {
	if !isLive("owner_media_slots", "media_asset_id") {
		t.Fatal("owner_media_slots is not in LiveMediaReferences: GC would delete " +
			"active avatars and the ON DELETE CASCADE would hide it")
	}
}

// media_clips was misclassified as derived and is now live — C-P0-4.
//
// It carries a live `post_id` and the ordered trim ranges that ARE the Flick
// edit/playback plan, so deleting the parent asset destroys a published edit.
func TestMediaClipsIsLiveNotDerived(t *testing.T) {
	if !isLive("media_clips", "media_asset_id") {
		t.Error("media_clips must block reclamation: it holds the canonical " +
			"Flick edit plan, not a derived by-product")
	}
	for _, table := range DerivedMediaTables {
		if table == "media_clips" {
			t.Error("media_clips must no longer be classified as derived")
		}
	}
}

// The non-FK references are the class the old foreign-key walk could not see.
//
// Every one of these is a plain UUID column with no constraint, so nothing in
// the database announces it as a media reference.
func TestNonForeignKeyReferencesAreProtected(t *testing.T) {
	for _, tc := range []struct{ table, column string }{
		{"users", "avatar_media_id"},
		{"users", "cover_media_id"},
		{"channels", "avatar_media_id"},
		{"channels", "banner_media_id"},
		{"posts", "cover_media_id"},
		{"stories", "media_id"},
		{"portfolio_items", "media_id"},
		{"video_metadata", "media_asset_id"},
		{"reel_drafts", "media_id"},
		{"reel_drafts", "cover_media_id"},
		{"audio_tracks", "media_id"},
	} {
		if !isLive(tc.table, tc.column) {
			t.Errorf("%s.%s is a live media reference with NO foreign key; "+
				"omitting it means GC deletes media that surface still shows",
				tc.table, tc.column)
		}
	}
}

// A soft-deleted draft must NOT pin its media forever.
func TestDraftReferenceExcludesDeletedDrafts(t *testing.T) {
	for _, ref := range LiveMediaReferences {
		if ref.Table != "post_draft_media" {
			continue
		}
		if !strings.Contains(ref.Predicate, "status <> 'deleted'") {
			t.Errorf("post_draft_media predicate must exclude deleted drafts, got %q",
				ref.Predicate)
		}
		return
	}
	t.Fatal("post_draft_media is not in LiveMediaReferences")
}

// A UUID[] column must be matched with `= ANY(...)`, not `=`.
//
// Equality against an array is a type error, which would fail the whole sweep
// closed — safe, but it would also mean the array was never really protected.
func TestArrayReferencesUseAnyMatching(t *testing.T) {
	refs := []resolvedReference{{
		ref:    MediaReference{Table: "some_table", Column: "media_ids", Array: true},
		isUUID: true,
	}}
	sql := liveReferenceSQL(refs, "$1")

	if !strings.Contains(sql, "$1 = ANY(some_table.media_ids)") {
		t.Errorf("array columns must use ANY() matching, got %q", sql)
	}
}

// A non-UUID column holding a media id needs a cast.
func TestTextColumnsAreCast(t *testing.T) {
	refs := []resolvedReference{{
		ref:    MediaReference{Table: "business_pages", Column: "avatar_media_id"},
		isUUID: false,
	}}
	sql := liveReferenceSQL(refs, "$1")

	if !strings.Contains(sql, "$1::text") {
		t.Errorf("text columns must be compared as text, got %q", sql)
	}
}

// Every entry states what breaks without it. That is the entry cost that keeps
// the list reviewable: an unexplained table cannot be audited by the next
// person deciding whether GC is safe.
func TestEveryLiveReferenceExplainsItself(t *testing.T) {
	for _, ref := range LiveMediaReferences {
		if strings.TrimSpace(ref.Why) == "" {
			t.Errorf("%s.%s has no Why", ref.Table, ref.Column)
		}
	}
}

// The composer lease is still a wire contract with Android.
//
// It no longer scopes confirmed reclamation — confirmed deletion is off
// (C-CLB-1) — but `init` still records it, so the data a future safe sweeper
// needs accumulates from launch rather than starting empty.
func TestComposerLeaseConstant(t *testing.T) {
	if UploadPurposeComposer != "composer" {
		t.Fatalf("the lease value is a wire contract with Android, got %q",
			UploadPurposeComposer)
	}
}

// Only `pending_upload` is reclaimable — Slice C, C-CLB-1.
//
// The value is compared against `processing_status`, whose CHECK constraint
// names it exactly. A typo here would silently make the sweep match nothing
// and audit-H9 growth would return with every test still green.
func TestOnlyPendingUploadIsReclaimable(t *testing.T) {
	if ProcessingStatusPendingUpload != "pending_upload" {
		t.Fatalf("must equal the processing_status CHECK value, got %q",
			ProcessingStatusPendingUpload)
	}
}

func isLive(table, column string) bool {
	for _, ref := range LiveMediaReferences {
		if ref.Table == table && ref.Column == column {
			return true
		}
	}
	return false
}
