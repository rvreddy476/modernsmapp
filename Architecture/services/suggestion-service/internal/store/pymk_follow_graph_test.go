package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

/*
	People you may know runs on the FOLLOW graph (24 Oct 2026).

	Connect was retired: graph-service refuses new connection requests, so
	the connections table can only shrink. GetFriendsOfFriends,
	GetMutualFriendIDs and GetAllUsersWithFriends read it directly, which
	meant an account made after that day was never even enumerated for a
	batch, and nobody's friends-of-friends could grow. A friend is now a
	MUTUAL follow, graph-service's "friends" bond.

	There is no Postgres in unit tests, so these guards read the SQL
	literals inside each function (comments are not parsed, so prose cannot
	satisfy them). pymk_follow_graph_integration_test.go checks the same
	behaviour against a live scratch database under -tags integration.
*/

// flatSQL is queryLiterals with every whitespace run collapsed to one space,
// so a guard does not depend on indentation or line endings.
func flatSQL(t *testing.T, fnName string) string {
	t.Helper()
	return strings.Join(strings.Fields(queryLiterals(t, fnName)), " ")
}

func requireSQL(t *testing.T, fnName, sql, fragment, why string) {
	t.Helper()
	if !strings.Contains(sql, fragment) {
		t.Fatalf("%s: missing %q — %s", fnName, fragment, why)
	}
}

func TestPYMKReadersNoLongerReadConnections(t *testing.T) {
	for _, fn := range []string{"GetFriendsOfFriends", "GetMutualFriendIDs", "GetAllUsersWithFriends"} {
		sql := flatSQL(t, fn)
		if strings.Contains(sql, "connections") {
			t.Errorf("%s still reads the retired connections table", fn)
		}
		if !strings.Contains(sql, "FROM follows") {
			t.Errorf("%s does not read the follow graph", fn)
		}
	}
}

func TestFriendsOfFriendsBridgesAreMutualFollows(t *testing.T) {
	const fn = "GetFriendsOfFriends"
	sql := flatSQL(t, fn)
	requireSQL(t, fn, sql,
		"JOIN follows back ON back.follower_id = $1 AND back.followee_id = f.follower_id",
		"a viewer's friend must be followed back by the viewer (one-way followers are not friends)")
	requireSQL(t, fn, sql,
		"JOIN follows cb ON cb.follower_id = mf.friend_id AND cb.followee_id = c.follower_id",
		"a candidate must be followed back by the bridging friend (friend of a friend, not any follower)")
	requireSQL(t, fn, sql, "COUNT(DISTINCT fof.via_friend)",
		"the count is distinct friends in common")
}

func TestFriendsOfFriendsExcludesFollowedBlockedAndHidden(t *testing.T) {
	const fn = "GetFriendsOfFriends"
	sql := flatSQL(t, fn)
	requireSQL(t, fn, sql,
		"NOT EXISTS (SELECT 1 FROM follows vf WHERE vf.follower_id = $1 AND vf.followee_id = fof.candidate_id)",
		"someone the viewer already follows must not be suggested")
	requireSQL(t, fn, sql,
		"NOT EXISTS (SELECT 1 FROM blocks bv WHERE bv.blocker_id = $1 AND bv.blocked_id = fof.candidate_id)",
		"an account the viewer blocked must not be suggested")
	requireSQL(t, fn, sql,
		"NOT EXISTS (SELECT 1 FROM blocks bc WHERE bc.blocker_id = fof.candidate_id AND bc.blocked_id = $1)",
		"an account that blocked the viewer must not be suggested")
	requireSQL(t, fn, sql,
		"NOT EXISTS (SELECT 1 FROM hidden_users hc WHERE hc.user_id = fof.candidate_id)",
		"a hidden (deactivated / deletion-scheduled) candidate must not be suggested")
	requireSQL(t, fn, sql,
		"NOT EXISTS (SELECT 1 FROM hidden_users hf WHERE hf.user_id = f.follower_id)",
		"a hidden friend must not bridge or be counted")
	requireSQL(t, fn, sql, "WHERE c.follower_id <> $1",
		"the viewer must not be their own candidate")
	requireSQL(t, fn, sql, "<> ALL(COALESCE($2::uuid[], '{}'::uuid[]))",
		"excludeIDs must apply, and a nil slice (NULL) must not empty the result")
}

func TestMutualFriendIDsAreMutualWithBothAndNotHidden(t *testing.T) {
	const fn = "GetMutualFriendIDs"
	sql := flatSQL(t, fn)
	requireSQL(t, fn, sql,
		"EXISTS (SELECT 1 FROM follows vb WHERE vb.follower_id = $1 AND vb.followee_id = a.follower_id)",
		"the shown friend must be followed back by the viewer")
	requireSQL(t, fn, sql,
		"EXISTS (SELECT 1 FROM follows fc WHERE fc.follower_id = a.follower_id AND fc.followee_id = $2)",
		"the shown friend must follow the candidate")
	requireSQL(t, fn, sql,
		"EXISTS (SELECT 1 FROM follows cf WHERE cf.follower_id = $2 AND cf.followee_id = a.follower_id)",
		"the candidate must follow the shown friend back")
	requireSQL(t, fn, sql,
		"NOT EXISTS (SELECT 1 FROM hidden_users h WHERE h.user_id = a.follower_id)",
		"a hidden account must never be shown as a friend in common")
	requireSQL(t, fn, sql, "NOT IN ($1, $2)",
		"neither the viewer nor the candidate is their own friend in common")
}

func TestBatchEnumeratesMutualFollowersAndSkipsHidden(t *testing.T) {
	const fn = "GetAllUsersWithFriends"
	sql := flatSQL(t, fn)
	requireSQL(t, fn, sql,
		"JOIN follows b ON b.follower_id = a.followee_id AND b.followee_id = a.follower_id",
		"a user with a friend is one with a mutual follow")
	requireSQL(t, fn, sql,
		"NOT EXISTS (SELECT 1 FROM hidden_users h WHERE h.user_id = a.follower_id)",
		"hidden accounts get no suggestions computed")
}

// queryArgs returns the source text of the arguments of the first
// s.appDB.Query call in fnName, so a guard can pin which bounds are passed.
func queryArgs(t *testing.T, fnName string) []string {
	t.Helper()
	fset := token.NewFileSet()
	src, err := parser.ParseFile(fset, "postgres.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range src.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != fnName || fn.Body == nil {
			continue
		}
		var out []string
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || out != nil {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Query" {
				return true
			}
			out = []string{}
			for _, a := range call.Args {
				if id, ok := a.(*ast.Ident); ok {
					out = append(out, id.Name)
				} else {
					out = append(out, "?")
				}
			}
			return false
		})
		if out == nil {
			t.Fatalf("%s: no Query call found", fnName)
		}
		return out
	}
	t.Fatalf("postgres.go: no function named %s", fnName)
	return nil
}

func TestPYMKScansAreBounded(t *testing.T) {
	if FriendScanLimit <= 0 || FriendScanLimit > 1000 {
		t.Fatalf("FriendScanLimit = %d; graph-service caps the same scan at 1,000", FriendScanLimit)
	}
	if FriendOfFriendScanLimit <= 0 || FriendOfFriendScanLimit > 1000 {
		t.Fatalf("FriendOfFriendScanLimit = %d; the second hop must be capped", FriendOfFriendScanLimit)
	}

	const fof = "GetFriendsOfFriends"
	sql := flatSQL(t, fof)
	requireSQL(t, fof, sql,
		"SELECT follower_id FROM follows WHERE followee_id = $1 ORDER BY created_at DESC LIMIT $3",
		"the viewer's friends must be read from a capped, index-ordered scan")
	requireSQL(t, fof, sql,
		"SELECT follower_id FROM follows WHERE followee_id = mf.friend_id ORDER BY created_at DESC LIMIT $4",
		"each friend's second hop must be capped")
	args := queryArgs(t, fof)
	if len(args) != 6 || args[4] != "FriendScanLimit" || args[5] != "FriendOfFriendScanLimit" {
		t.Fatalf("%s Query args %v: $3 must be FriendScanLimit and $4 FriendOfFriendScanLimit", fof, args)
	}

	const mf = "GetMutualFriendIDs"
	sql = flatSQL(t, mf)
	requireSQL(t, mf, sql,
		"SELECT follower_id, created_at FROM follows WHERE followee_id = $1 ORDER BY created_at DESC LIMIT $4",
		"the viewer's friends must be read from a capped scan")
	args = queryArgs(t, mf)
	if len(args) != 6 || args[5] != "FriendScanLimit" {
		t.Fatalf("%s Query args %v: $4 must be FriendScanLimit", mf, args)
	}
}
