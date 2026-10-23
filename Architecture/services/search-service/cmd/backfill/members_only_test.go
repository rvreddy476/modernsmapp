package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Members-only posts (posts.tier_required_id, post-service migration 016)
// are never searchable. The rebuild must skip them, or remove them when an
// earlier run or a lost event left them indexed — otherwise a routine
// reindex publishes every gated post's text.

func TestRowSearchEligible_MembersOnlyIsNeverEligible(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		visibility, review          string
		deleted, scheduled, members bool
		want                        bool
	}{
		{"public approved", "public", "approved", false, false, false, true},
		{"public approved members-only", "public", "approved", false, false, true, false},
		{"members-only and scheduled", "public", "approved", false, true, true, false},
		{"scheduled", "public", "approved", false, true, false, false},
		{"deleted", "public", "approved", true, false, false, false},
		{"pending", "public", "pending", false, false, false, false},
		{"followers", "followers", "approved", false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := rowSearchEligible(tc.visibility, tc.review, tc.deleted, tc.scheduled, tc.members)
			if got != tc.want {
				t.Fatalf("rowSearchEligible(%q, %q, deleted=%v, scheduled=%v, members=%v) = %v, want %v",
					tc.visibility, tc.review, tc.deleted, tc.scheduled, tc.members, got, tc.want)
			}
		})
	}
}

func TestPostsMembersOnlyExpr_ReadsTheGateColumn(t *testing.T) {
	if postsMembersOnlyExpr != "(p.tier_required_id IS NOT NULL)" {
		t.Fatalf("postsMembersOnlyExpr = %q; must be true exactly when posts.tier_required_id is set", postsMembersOnlyExpr)
	}
}

// Source guard: the posts SELECT carries the gate, the row scan reads it
// into the right variable (right after is_scheduled, in both lists), and
// the eligibility branch consults it.
func TestBackfillPosts_FiltersMembersOnlyRows(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	code := string(src)

	selectRe := regexp.MustCompile(`\(p\.publish_at IS NOT NULL\) AS is_scheduled,\s*` +
		"`" + regexp.QuoteMeta(` + postsMembersOnlyExpr + `) + "`" + ` AS is_members_only,`)
	if !selectRe.MatchString(code) {
		t.Fatal("the posts SELECT must select postsMembersOnlyExpr AS is_members_only right after is_scheduled")
	}
	scanRe := regexp.MustCompile(`&isDeleted, &isScheduled,\s*&isMembersOnly,`)
	if !scanRe.MatchString(code) {
		t.Fatal("the posts row scan must read is_members_only into isMembersOnly right after isScheduled")
	}
	if !strings.Contains(code, "if !rowSearchEligible(visibility, reviewStatus, isDeleted, isScheduled, isMembersOnly) {") {
		t.Fatal("backfillPosts must decide eligibility through rowSearchEligible with the members-only flag")
	}
	if strings.Contains(code, "isScheduled || !events.SearchEligible(") {
		t.Fatal("backfillPosts inlines the old eligibility rule, which ignores the members-only gate")
	}
}
