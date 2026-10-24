package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

/*
	"Follows you" (founder, 25 Oct 2026): GetFollowersNotFollowedBack reads
	the viewer's newest followers whom the viewer has not followed back.
	Like pymk_follow_graph_test.go these guards read the SQL literal (comments
	are not parsed); follows_you_integration_test.go checks the behaviour on
	a live scratch database.
*/

func TestFollowersNotFollowedBackIsBoundedAndNewestFirst(t *testing.T) {
	const fn = "GetFollowersNotFollowedBack"
	if FollowerScanLimit <= 0 || FollowerScanLimit > 1000 {
		t.Fatalf("FollowerScanLimit = %d; the follows-you scan is capped at 1,000", FollowerScanLimit)
	}
	sql := flatSQL(t, fn)
	requireSQL(t, fn, sql,
		"SELECT follower_id, created_at FROM follows WHERE followee_id = $1 ORDER BY created_at DESC LIMIT $3",
		"the viewer's followers must come from a capped, newest-first index scan")
	requireSQL(t, fn, sql, "ORDER BY f.created_at DESC",
		"the newest follow comes first (it scores highest)")
	args := queryArgs(t, fn)
	if len(args) != 5 || args[4] != "FollowerScanLimit" {
		t.Fatalf("%s Query args %v: $3 must be FollowerScanLimit", fn, args)
	}
}

func TestFollowersNotFollowedBackExcludes(t *testing.T) {
	const fn = "GetFollowersNotFollowedBack"
	sql := flatSQL(t, fn)
	for fragment, why := range map[string]string{
		"NOT EXISTS (SELECT 1 FROM follows vf WHERE vf.follower_id = $1 AND vf.followee_id = f.follower_id)": "someone the viewer follows back (a friend) is not a follows-you suggestion",
		"NOT EXISTS (SELECT 1 FROM hidden_users h WHERE h.user_id = f.follower_id)":                          "a hidden (deactivated / deletion-scheduled) follower must not be suggested",
		"NOT EXISTS (SELECT 1 FROM blocks bv WHERE bv.blocker_id = $1 AND bv.blocked_id = f.follower_id)":    "a follower the viewer blocked must not be suggested",
		"NOT EXISTS (SELECT 1 FROM blocks bc WHERE bc.blocker_id = f.follower_id AND bc.blocked_id = $1)":    "a follower who blocked the viewer must not be suggested",
		"f.follower_id <> $1":                        "the viewer must not be their own candidate",
		"<> ALL(COALESCE($2::uuid[], '{}'::uuid[]))": "excludeIDs (cooldowns, the batch exclude set) must apply, and a nil slice must not empty the result",
	} {
		requireSQL(t, fn, sql, fragment, why)
	}
}

func TestPairSignalsNoLongerTouchMutualFollow(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "scylla.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.Contains(lit.Value, "mutual_follow") {
			t.Errorf("scylla.go still reads or writes mutual_follow (always false for a candidate): %s", lit.Value)
		}
		return true
	})
}
