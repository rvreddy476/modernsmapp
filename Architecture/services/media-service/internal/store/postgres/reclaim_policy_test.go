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
		want := ref.Table + "." + ref.Column + " = $1"
		if ref.Array {
			want = "$1 = ANY(" + ref.Table + "." + ref.Column + ")"
		}
		if !strings.Contains(sql, want) {
			t.Errorf("predicate does not match %s.%s (want %q)", ref.Table, ref.Column, want)
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

// The eighteen columns the boot sweep refused on (2026-09-27), exactly as the
// live catalog reported them. Until each was classified, orphaned media was
// never reclaimed at all.
var refusedAtBoot2026_09_27 = map[string]string{
	"answer_media.media_id":                 "uuid",
	"broadcast_channels.avatar_media_id":    "uuid",
	"broadcast_channels.banner_media_id":    "uuid",
	"channel_updates.media_ids":             "_uuid",
	"communities.avatar_media_id":           "uuid",
	"communities.banner_media_id":           "uuid",
	"dating_photos.media_id":                "uuid",
	"dating_selfie_attempts.video_media_id": "uuid",
	"dating_verifications.selfie_media_id":  "uuid",
	"fundraisers.cover_media_id":            "uuid",
	"group_events.cover_media_id":           "uuid",
	"group_resources.media_id":              "uuid",
	"groups.avatar_media_id":                "uuid",
	"groups.cover_media_id":                 "uuid",
	"invoices.pdf_media_id":                 "uuid",
	"live_streams.cover_media_id":           "uuid",
	"post_purge_media.media_id":             "uuid",
	"question_media.media_id":               "uuid",
}

func catalogOf(cols map[string]string) map[string]catalogColumn {
	out := make(map[string]catalogColumn, len(cols))
	for key, udt := range cols {
		out[key] = catalogColumn{table: strings.SplitN(key, ".", 2)[0], udt: udt}
	}
	return out
}

// Resolution over the catalog the boot log reported must now succeed, and
// every one of those columns must come back as a PROTECTING reference — a
// resolution that merely stopped refusing (say, by marking a table derived)
// would pass the first check and still let the sweep delete live media.
func TestBootRefusedColumnsAreClassifiedAsLiveClaims(t *testing.T) {
	refs, err := resolveAgainstCatalog(catalogOf(refusedAtBoot2026_09_27))
	if err != nil {
		t.Fatalf("the columns the boot sweep refused on still refuse: %v", err)
	}
	resolved := map[string]resolvedReference{}
	for _, r := range refs {
		resolved[r.ref.Table+"."+r.ref.Column] = r
	}
	for key := range refusedAtBoot2026_09_27 {
		if _, ok := resolved[key]; !ok {
			t.Errorf("%s resolved without protecting anything; it must be a live claim", key)
		}
	}

	sql := liveReferenceSQL(refs, "$1")
	if !strings.Contains(sql, "$1 = ANY(channel_updates.media_ids)") {
		t.Errorf("channel_updates.media_ids is UUID[] and must be matched with ANY(), got:\n%s", sql)
	}
	if strings.Contains(sql, "channel_updates.media_ids = $1") {
		t.Error("channel_updates.media_ids is compared with = against an array: a type error that fails every sweep")
	}
}

// Dating selfie and verification media are sensitive: they must never be
// reclaimed while their row exists, so they are unconditional claims — no
// Predicate that could let a row stop counting.
func TestDatingEvidenceMediaAreUnconditionalClaims(t *testing.T) {
	for _, tc := range []struct{ table, column string }{
		{"dating_photos", "media_id"},
		{"dating_selfie_attempts", "video_media_id"},
		{"dating_verifications", "selfie_media_id"},
	} {
		ref, ok := liveRef(tc.table, tc.column)
		if !ok {
			t.Errorf("%s.%s must be a live claim: reclaiming it silently destroys dating verification media",
				tc.table, tc.column)
			continue
		}
		if ref.Predicate != "" {
			t.Errorf("%s.%s must hold while the row exists, got predicate %q", tc.table, tc.column, ref.Predicate)
		}
	}
}

// post_purge_media is the post purge's work queue: an asset in it is being
// deleted by the purge, and the sweep must not take it out from under it.
func TestPostPurgeQueueBlocksReclamation(t *testing.T) {
	if !isLive("post_purge_media", "media_id") {
		t.Fatal("post_purge_media must block reclamation: the purge owns the deletion of a queued asset")
	}
	for _, table := range DerivedMediaTables {
		if table == "post_purge_media" {
			t.Fatal("post_purge_media must not be derived; it belongs to post-service, not to the asset")
		}
	}
}

// A declared shape that disagrees with the catalog must refuse. Declaring a
// UUID[] column scalar composes `col = $1`, a type error; declaring a scalar
// an array composes `$1 = ANY(col)`, also an error. Either way the reference
// would not really protect, so resolution stops.
func TestArrayDeclarationMustMatchTheCatalog(t *testing.T) {
	// channel_updates.media_ids is declared Array; a catalog saying uuid
	// (scalar) is a disagreement.
	if _, err := resolveAgainstCatalog(catalogOf(map[string]string{"channel_updates.media_ids": "uuid"})); err == nil {
		t.Error("a declared array column that the catalog says is scalar must refuse")
	}
	// users.avatar_media_id is declared scalar; a catalog saying _uuid is a
	// disagreement.
	if _, err := resolveAgainstCatalog(catalogOf(map[string]string{"users.avatar_media_id": "_uuid"})); err == nil {
		t.Error("a declared scalar column that the catalog says is an array must refuse")
	}
	// And the agreeing shapes resolve.
	if _, err := resolveAgainstCatalog(catalogOf(map[string]string{
		"channel_updates.media_ids": "_uuid", "users.avatar_media_id": "uuid",
	})); err != nil {
		t.Errorf("agreeing shapes must resolve: %v", err)
	}
}

// Still refuses on an unknown column — the guard the new entries must not
// have weakened.
func TestUnknownColumnStillRefuses(t *testing.T) {
	cols := map[string]string{"slice_c_guard_probe.media_id": "uuid"}
	for k, v := range refusedAtBoot2026_09_27 {
		cols[k] = v
	}
	_, err := resolveAgainstCatalog(catalogOf(cols))
	if _, ok := err.(ErrUnclassifiedMediaReference); !ok {
		t.Fatalf("an unclassified column must refuse with ErrUnclassifiedMediaReference, got %v", err)
	}
}

// Every live entry that is an array column is declared as one: a `_ids`
// column name is the schema's convention for UUID[].
func TestIdsColumnsAreDeclaredArrays(t *testing.T) {
	for _, ref := range LiveMediaReferences {
		if strings.HasSuffix(ref.Column, "_ids") && !ref.Array {
			t.Errorf("%s.%s looks like a UUID[] column but is not declared Array", ref.Table, ref.Column)
		}
	}
}

// No table/column may be listed twice: a duplicate is harmless in SQL but
// means two reviewers classified the same claim, possibly with different
// predicates.
func TestNoDuplicateLiveReferences(t *testing.T) {
	seen := map[string]bool{}
	for _, ref := range LiveMediaReferences {
		key := ref.Table + "." + ref.Column
		if seen[key] {
			t.Errorf("%s is listed twice in LiveMediaReferences", key)
		}
		seen[key] = true
	}
}

func liveRef(table, column string) (MediaReference, bool) {
	for _, ref := range LiveMediaReferences {
		if ref.Table == table && ref.Column == column {
			return ref, true
		}
	}
	return MediaReference{}, false
}
