package postgres

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// CS-LB-3G — the projection guard.
//
// # WHY A GUARD AND NOT A COUNT
//
// The specification originally said "there are eight order-bearing projections".
// A hardcoded eight rots the moment someone adds a query, and the failure is
// silent: a new unordered read renders a carousel in arbitrary order and every
// existing test still passes. This test does not count. It finds every
// production query that touches `post_media` and requires each one to either
// carry an approved order clause or appear on a named exemption list.
//
// # THE EXEMPTIONS
//
// `PostIDsByMediaID` and its batch sibling walk the join the other way: they
// answer "which posts use this asset", and their result order is post ids, not
// carousel order. Ordering them by `pm.position` would be meaningless. They are
// listed by the function that owns them, not by line number, so ordinary edits
// above them do not silently re-arm or disarm the guard.
var exemptFromOrdering = map[string]string{
	"PostIDsByMediaID":   "reverse lookup: returns post ids, not carousel order",
	"PostIDsByMediaIDs":  "reverse lookup: returns post ids, not carousel order",
	"OrphanedDraftMedia": "NOT EXISTS existence check: selects no post_media column and returns none",
	"PurgeUser":          "DELETE, not a read: erases post_media rows for a purged user, returns nothing to order",
	"PurgePost":          "purge: unreferenced-media existence check and DELETE of a purged post's post_media rows, returns media ids not carousel order",
}

var (
	funcDecl       = regexp.MustCompile(`^func (?:\([^)]*\) )?([A-Za-z0-9_]+)\(`)
	touchesMedia   = regexp.MustCompile(`postMediaSource|FROM post_media|from post_media`)
	hasOrderClause = regexp.MustCompile(`postMediaOrder|postMediaBatchOrder|ORDER BY`)
)

func TestEveryPostMediaQueryIsOrderedOrExempt(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		lines := strings.Split(string(src), "\n")

		currentFunc := ""
		for i, line := range lines {
			if m := funcDecl.FindStringSubmatch(line); m != nil {
				currentFunc = m[1]
			}
			if !touchesMedia.MatchString(line) {
				continue
			}
			// The constant declarations themselves are not queries.
			if strings.Contains(line, "postMediaSource  =") ||
				strings.Contains(line, "postMediaColumns =") {
				continue
			}
			checked++

			if reason, exempt := exemptFromOrdering[currentFunc]; exempt {
				t.Logf("%s:%d %s exempt (%s)", file, i+1, currentFunc, reason)
				continue
			}

			// The order clause may be on this line or the two that follow,
			// because these queries are multi-line raw strings.
			window := strings.Join(lines[i:min(i+3, len(lines))], "\n")
			if !hasOrderClause.MatchString(window) {
				t.Errorf(
					"%s:%d in %s touches post_media with no order clause and no exemption.\n"+
						"  A carousel read without ORDER BY renders pages in arbitrary order.\n"+
						"  Either use postMediaOrder/postMediaBatchOrder, or add the function to\n"+
						"  exemptFromOrdering with a reason.\n  line: %s",
					file, i+1, currentFunc, strings.TrimSpace(line))
			}
		}
	}

	if checked == 0 {
		t.Fatal("guard found no post_media queries at all — it has stopped guarding anything")
	}
	t.Logf("checked %d post_media query sites", checked)
}

// The INSERT must record an ordinal. Without this, the create path could lose
// its `position` column in a refactor and every read would silently fall into
// the phase-A all-absent branch, which looks correct until two posts disagree.
func TestCreateInsertRecordsAnOrdinal(t *testing.T) {
	src, err := os.ReadFile("posts.go")
	if err != nil {
		t.Fatalf("read posts.go: %v", err)
	}
	if !strings.Contains(string(src), "INSERT INTO post_media (post_id, media_id, kind, position)") {
		t.Error("the create transaction must insert `position`; found no such INSERT")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
