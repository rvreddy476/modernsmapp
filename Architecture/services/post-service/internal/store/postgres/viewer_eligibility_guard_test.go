package postgres

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// P-4 source-scan guard (Copyright Match plan, sections 4 and 6.2).
//
// Every viewer read must judge posts.effective_review_status through the
// helpers in viewer_eligibility.go. A raw `review_status = 'approved'` in
// a query would let a post under an active copyright hold through
// (review_status is still 'approved' while the hold is expressed in a
// restriction row), so this test fails on any such literal in a
// production file of this package. A raw `effective_review_status =
// 'approved'` outside viewer_eligibility.go fails too: the definition
// lives in ONE place.
//
// Writers and admin views compare review_status with OTHER literals
// ('pending', 'flagged', 'needs_changes') and are not matched. A write
// that sets review_status = 'approved' via a parameter is not matched
// either; only the literal predicate is.

var (
	rawApprovedPredicate       = regexp.MustCompile(`review_status\s*=\s*'approved'`)
	rawEffectiveApproved       = regexp.MustCompile(`effective_review_status\s*=\s*'approved'`)
	viewerEligibilityHelperSrc = "viewer_eligibility.go"
)

func TestNoViewerReadHardcodesTheBaseApprovedStatus(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	helperUses := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue // prose about the predicate is not a query
			}
			if strings.Contains(line, "viewerApprovedSQL") || strings.Contains(line, "viewerApproved(") || strings.Contains(line, "ViewerEligibleSQL(") {
				if file != viewerEligibilityHelperSrc {
					helperUses++
				}
			}
			// The regexp also matches the helper's own literal via the
			// "effective_" prefix; the helper file is the one allowed place.
			if rawEffectiveApproved.MatchString(line) {
				if file != viewerEligibilityHelperSrc {
					t.Errorf("%s:%d spells the effective predicate by hand; use viewerApprovedSQL / viewerApproved / ViewerEligibleSQL\n  line: %s", file, i+1, strings.TrimSpace(line))
				}
				continue
			}
			if rawApprovedPredicate.MatchString(line) {
				t.Errorf("%s:%d reads the BASE review_status = 'approved': a post under an active restriction would pass.\n"+
					"  Use viewerApprovedSQL / viewerApproved(alias) / ViewerEligibleSQL(alias) (viewer_eligibility.go).\n  line: %s",
					file, i+1, strings.TrimSpace(line))
			}
		}
	}
	if helperUses == 0 {
		t.Fatal("guard found no use of the viewer eligibility helper — it has stopped guarding anything")
	}
	t.Logf("%d viewer predicates go through the helper", helperUses)
}

// The revalidation and eligibility reads must carry the restriction count
// / effective status, or a hold placed after a body was cached (or after
// the index was built) would not bind.
func TestRevalidationAndEligibilityReadsCarryTheEffectiveStatus(t *testing.T) {
	for file, needles := range map[string][]string{
		"post_access_state.go":  {"active_restriction_count"},
		"search_eligibility.go": {"effective_review_status, active_restriction_count", "ReviewStatus:     effective", "effective_review_status, review_status, (active_restriction_count > 0)"},
		"posts.go":              {"mention_usernames, active_restriction_count`", "&p.ActiveRestrictionCount"},
	} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, needle := range needles {
			if !strings.Contains(string(src), needle) {
				t.Errorf("%s no longer contains %q", file, needle)
			}
		}
	}
}
