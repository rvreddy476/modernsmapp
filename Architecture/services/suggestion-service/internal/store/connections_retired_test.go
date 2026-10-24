package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

/*
	The rest of the suggestion readers move to the FOLLOW graph (25 Oct 2026).

	4375beb37 moved friends-of-friends, mutual-friend ids and the batch user
	list to mutual follows. Six more readers still read the retired
	connections / connection_requests tables, so for any account made after
	Connect was retired: the exclude list and read-path filter had no
	friends, triadic closure and "friends follow them" never fired, the
	"your friends blocked this person" safety signal was always 0, and
	GetMutualFollowNonFriends suggested the viewer's own (mutual-follow)
	friends. A friend is a MUTUAL follow, the definition 4375beb37 uses.

	Like pymk_follow_graph_test.go these read the SQL literals in each
	function (comments are not parsed); connections_retired_integration_test.go
	checks the behaviour against a live scratch database.
*/

func TestNoStoreReaderReadsTheRetiredConnectionTables(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "postgres.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			sql := strings.Join(strings.Fields(lit.Value), " ")
			for _, table := range []string{"FROM connections", "JOIN connections", "connection_requests"} {
				if strings.Contains(sql, table) {
					t.Errorf("%s still reads %q — connections were retired on 24 Oct 2026", fn.Name.Name, table)
				}
			}
			return true
		})
	}
}

func TestRetiredReadersAreGone(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "postgres.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		switch fn.Name.Name {
		case "GetPendingRequestIDs":
			t.Error("GetPendingRequestIDs still exists: connection requests were retired and can no longer be accepted")
		case "GetMutualFollowNonFriends":
			t.Error("GetMutualFollowNonFriends still exists: a friend IS a mutual follow, so it can only suggest the viewer's friends")
		}
	}
}

func TestFriendIDsAreMutualFollows(t *testing.T) {
	const fn = "GetFriendIDs"
	sql := flatSQL(t, fn)
	requireSQL(t, fn, sql, "FROM follows f", "friends come from the follow graph")
	requireSQL(t, fn, sql,
		"JOIN follows back ON back.follower_id = f.followee_id AND back.followee_id = $1",
		"a friend must follow the user back (a one-way followee is not a friend)")
	requireSQL(t, fn, sql, "WHERE f.follower_id = $1",
		"the walk starts from the user's own followees")
	requireSQL(t, fn, sql, "f.followee_id <> $1", "the user is not their own friend")
	if strings.Contains(sql, "LIMIT") {
		t.Fatalf("%s is an exclusion list and must not be capped: a friend past the cap would be suggested", fn)
	}
}

// myFriendsCTE is the bounded friend read 4375beb37 introduced, with the
// viewer at $1 and FriendScanLimit at the given placeholder.
func myFriendsCTE(limitParam string) []string {
	return []string{
		"SELECT follower_id FROM follows WHERE followee_id = $1 ORDER BY created_at DESC LIMIT " + limitParam,
		"JOIN follows back ON back.follower_id = $1 AND back.followee_id = f.follower_id",
		"WHERE f.follower_id <> $1 AND NOT EXISTS (SELECT 1 FROM hidden_users hf WHERE hf.user_id = f.follower_id)",
	}
}

func requireMyFriends(t *testing.T, fn, sql, limitParam string) {
	t.Helper()
	for _, frag := range myFriendsCTE(limitParam) {
		requireSQL(t, fn, sql, frag,
			"the viewer's friends are mutual follows from a capped scan of their newest followers, hidden ones skipped")
	}
}

func requireCandidateExclusions(t *testing.T, fn, sql, col string) {
	t.Helper()
	requireSQL(t, fn, sql,
		"NOT EXISTS (SELECT 1 FROM follows vf WHERE vf.follower_id = $1 AND vf.followee_id = "+col+")",
		"someone the viewer already follows must not be suggested (a card offers only Follow)")
	requireSQL(t, fn, sql,
		"NOT EXISTS (SELECT 1 FROM hidden_users hc WHERE hc.user_id = "+col+")",
		"a hidden (deactivated / deletion-scheduled) candidate must not be suggested")
	requireSQL(t, fn, sql,
		"NOT EXISTS (SELECT 1 FROM blocks bv WHERE bv.blocker_id = $1 AND bv.blocked_id = "+col+")",
		"an account the viewer blocked must not be suggested")
	requireSQL(t, fn, sql,
		"NOT EXISTS (SELECT 1 FROM blocks bc WHERE bc.blocker_id = "+col+" AND bc.blocked_id = $1)",
		"an account that blocked the viewer must not be suggested")
}

func TestTriadicClosureRunsOnMutualFollows(t *testing.T) {
	const fn = "GetTriadicClosureCandidates"
	sql := flatSQL(t, fn)
	requireMyFriends(t, fn, sql, "$4")
	requireSQL(t, fn, sql,
		"SELECT follower_id FROM follows WHERE followee_id = mf.friend_id ORDER BY created_at DESC LIMIT $5",
		"each friend's own friends must come from a capped scan")
	requireSQL(t, fn, sql,
		"JOIN follows cb ON cb.follower_id = mf.friend_id AND cb.followee_id = c.follower_id",
		"a friend's friend must be followed back by that friend")
	requireSQL(t, fn, sql, "JOIN my_friends mf2 ON mf2.friend_id = ff.x",
		"B and C must both be the viewer's friends")
	requireSQL(t, fn, sql, "SELECT DISTINCT LEAST(ff.b, ff.x) AS b, GREATEST(ff.b, ff.x) AS c",
		"B↔C is one triad whichever side's scan saw it")
	requireSQL(t, fn, sql, "JOIN friend_friends dc ON dc.b = t.c AND dc.x = db.x",
		"D must be a friend of both B and C")
	requireSQL(t, fn, sql, "WHERE c.follower_id <> $1", "the viewer is not their own candidate")
	requireSQL(t, fn, sql, "<> ALL(COALESCE($2::uuid[], '{}'::uuid[]))",
		"excludeIDs must apply, and a nil slice (NULL) must not empty the result")
	requireCandidateExclusions(t, fn, sql, "cand.d_id")

	args := callArgs(t, fn, "Query")
	if len(args) != 7 || args[5] != "FriendScanLimit" || args[6] != "FriendOfFriendScanLimit" {
		t.Fatalf("%s Query args %v: $4 must be FriendScanLimit and $5 FriendOfFriendScanLimit", fn, args)
	}
}

func TestSocialProofFriendsAreMutualFollows(t *testing.T) {
	const fn = "GetSocialProofCandidates"
	sql := flatSQL(t, fn)
	requireMyFriends(t, fn, sql, "$3")
	requireSQL(t, fn, sql,
		"SELECT followee_id FROM follows WHERE follower_id = mf.friend_id ORDER BY created_at DESC LIMIT $4",
		"each friend contributes only a capped scan of their newest follows")
	requireSQL(t, fn, sql, "COUNT(DISTINCT fl.via_friend)", "the count is distinct friends who follow them")
	requireSQL(t, fn, sql, "WHERE ff.followee_id <> $1", "the viewer is not their own candidate")
	requireCandidateExclusions(t, fn, sql, "fl.candidate_id")

	args := callArgs(t, fn, "Query")
	if len(args) != 6 || args[4] != "FriendScanLimit" || args[5] != "FriendOfFriendScanLimit" {
		t.Fatalf("%s Query args %v: $3 must be FriendScanLimit and $4 FriendOfFriendScanLimit", fn, args)
	}
}

func TestBlockedByFriendsCountsMutualFollowFriends(t *testing.T) {
	for _, c := range []struct{ fn, method, where string }{
		{"GetBlockCountByFriends", "QueryRow", "WHERE b.blocked_id = $2"},
		{"GetBlockCountByFriendsBatch", "Query", "WHERE b.blocked_id = ANY($2)"},
	} {
		sql := flatSQL(t, c.fn)
		requireMyFriends(t, c.fn, sql, "$3")
		requireSQL(t, c.fn, sql, "JOIN my_friends mf ON mf.friend_id = b.blocker_id",
			"only blocks made by the viewer's friends count")
		requireSQL(t, c.fn, sql, c.where, "the count is of blocks against the candidate")
		args := callArgs(t, c.fn, c.method)
		if len(args) != 5 || args[4] != "FriendScanLimit" {
			t.Fatalf("%s %s args %v: $3 must be FriendScanLimit", c.fn, c.method, args)
		}
	}
}

// callArgs is queryArgs for any method name (QueryRow as well as Query): the
// source of each argument of the first s.appDB.<method> call in fnName,
// identifiers by name and anything else as "?".
func callArgs(t *testing.T, fnName, method string) []string {
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
			if !ok || sel.Sel.Name != method {
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
			t.Fatalf("%s: no %s call found", fnName, method)
		}
		return out
	}
	t.Fatalf("postgres.go: no function named %s", fnName)
	return nil
}
