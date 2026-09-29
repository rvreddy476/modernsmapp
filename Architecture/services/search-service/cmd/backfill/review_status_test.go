package main

import (
	"os"
	"strings"
	"testing"

	"github.com/atpost/shared/events"
)

// The bug this guards: the posts rebuild read posts.review_status, the
// BASE moderation status, which post-service migration 056 leaves at
// 'approved' while a case-specific restriction holds the post. A reindex
// therefore put a held video back into the public index. The rebuild must
// read the stored generated column effective_review_status ('restricted'
// while a hold is active) whenever the database has it.

func TestReviewStatusExpr_ReadsEffectiveColumnWhenPresent(t *testing.T) {
	got := reviewStatusExpr(true)
	if !strings.Contains(got, "p."+postsEffectiveReviewStatusColumn) {
		t.Fatalf("reviewStatusExpr(true) = %q; must read p.effective_review_status", got)
	}
	// "p.review_status" is a prefix of nothing else in the expression, so
	// a plain substring check catches a fall-through to the base column.
	if strings.Contains(got, "p."+postsBaseReviewStatusColumn) {
		t.Fatalf("reviewStatusExpr(true) = %q; reads the base column, which is 'approved' under an active restriction", got)
	}
	if !strings.HasPrefix(got, "COALESCE(") || !strings.HasSuffix(got, ", '')") {
		t.Fatalf("reviewStatusExpr(true) = %q; a NULL status must scan as '' so the allowlist rejects it", got)
	}
}

func TestReviewStatusExpr_FallsBackToBaseColumnOnPre056Database(t *testing.T) {
	got := reviewStatusExpr(false)
	if !strings.Contains(got, "p."+postsBaseReviewStatusColumn) {
		t.Fatalf("reviewStatusExpr(false) = %q; a database without 056 only has review_status", got)
	}
	if strings.Contains(got, postsEffectiveReviewStatusColumn) {
		t.Fatalf("reviewStatusExpr(false) = %q; referencing an absent column is a parse error, not a fallback", got)
	}
	if !strings.HasPrefix(got, "COALESCE(") || !strings.HasSuffix(got, ", '')") {
		t.Fatalf("reviewStatusExpr(false) = %q; a NULL status must scan as '' so the allowlist rejects it", got)
	}
}

// The value the effective column yields under a hold must be ineligible
// through the exact predicate backfillPosts applies. This is what turns
// "read the right column" into "a held post is removed, not re-indexed".
func TestRestrictedIsIneligibleForTheRebuild(t *testing.T) {
	for _, status := range []string{"restricted", "Restricted", " restricted "} {
		if events.SearchEligible("public", status, false) {
			t.Fatalf("SearchEligible(public, %q) = true; a held post would be re-indexed by a rebuild", status)
		}
	}
	if !events.SearchEligible("public", "approved", false) {
		t.Fatal("precondition: public+approved is eligible")
	}
}

// Source guard: the posts SELECT must obtain its status through
// reviewStatusExpr, never by inlining the base column again. The literal
// "p.review_status" does not appear in main.go once the column name flows
// through the constant, so its reappearance is exactly the regression.
func TestBackfillPosts_DoesNotInlineBaseReviewStatus(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	code := string(src)
	if strings.Contains(code, "p.review_status") {
		t.Fatal("main.go references p.review_status directly; the posts rebuild must read the status through reviewStatusExpr " +
			"(effective_review_status when migration 056 is applied) or a reindex re-exposes restricted posts")
	}
	if !strings.Contains(code, "effectiveReviewStatusExpr(ctx, pool)") {
		t.Fatal("backfillPosts no longer detects effective_review_status via effectiveReviewStatusExpr")
	}
}
