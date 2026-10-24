package service

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/atpost/suggestion-service/internal/store"
	"github.com/google/uuid"
)

/*
	Connect was retired on 24 Oct 2026; a friend is a MUTUAL follow, and a
	suggestion card offers only Follow (founder). So:

	  - every batch's exclude set holds the people the viewer already
	    follows. The friend batch did not: the location, profession, group,
	    school and company pools could hand back someone already followed;
	  - the read path drops followed people from stored candidates;
	  - the "friend_accept" interstitial (the pop-up after accepting a
	    connection request) is retired, as accepting one is.
*/

func TestCandidateExcludeSetHoldsViewerListsAndCooldowns(t *testing.T) {
	viewer, friend, followed, blocked, cooled := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	set := candidateExcludeSet(viewer,
		map[uuid.UUID]store.CooldownEntry{cooled: {CandidateID: cooled}},
		[]uuid.UUID{friend}, []uuid.UUID{followed}, []uuid.UUID{blocked})
	for name, id := range map[string]uuid.UUID{
		"viewer": viewer, "friend": friend, "followed": followed, "blocked": blocked, "cooldown": cooled,
	} {
		if !set[id] {
			t.Errorf("%s is not excluded", name)
		}
	}
	if len(set) != 5 {
		t.Fatalf("exclude set has %d ids, want 5", len(set))
	}
	if set := candidateExcludeSet(viewer, nil); len(set) != 1 || !set[viewer] {
		t.Fatalf("no lists: %v, want the viewer only", set)
	}
}

// followingFeedsExcludeSet reports whether fnName (in file) assigns the
// result of s.store.GetFollowingIDs to a variable and passes that variable
// to candidateExcludeSet — i.e. the viewer's follows are in the set it
// filters candidates with.
func followingFeedsExcludeSet(t *testing.T, file, fnName string) bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != fnName || fn.Body == nil {
			continue
		}
		var followingVar string
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok || len(as.Rhs) != 1 || len(as.Lhs) == 0 {
				return true
			}
			call, ok := as.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "GetFollowingIDs" {
				if id, ok := as.Lhs[0].(*ast.Ident); ok && id.Name != "_" {
					followingVar = id.Name
				}
			}
			return true
		})
		if followingVar == "" {
			return false
		}
		found := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "candidateExcludeSet" {
				return true
			}
			for _, a := range call.Args {
				if id, ok := a.(*ast.Ident); ok && id.Name == followingVar {
					found = true
				}
			}
			return true
		})
		return found
	}
	t.Fatalf("%s: no function named %s", file, fnName)
	return false
}

func TestEveryCandidatePathExcludesPeopleAlreadyFollowed(t *testing.T) {
	for _, c := range []struct{ file, fn string }{
		{"batch.go", "runFriendBatchForUser"},
		{"batch.go", "runFollowBatchForUser"},
		{"suggestion.go", "GetSuggestions"},
	} {
		if !followingFeedsExcludeSet(t, c.file, c.fn) {
			t.Errorf("%s does not put the viewer's follows (GetFollowingIDs) into candidateExcludeSet: "+
				"it can suggest someone the viewer already follows, and the card offers only Follow", c.fn)
		}
	}
}

func TestFriendAcceptInterstitialIsRetired(t *testing.T) {
	// No store: the retired trigger must not read anything. Before the
	// retirement this path read connections through GetMutualFriendIDs and
	// GetFriendIDs (a nil store panics here).
	s := &Service{}
	resp, err := s.GetInterstitialSuggestions(context.Background(), uuid.New(), "friend_accept", uuid.New(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 0 {
		t.Fatalf("friend_accept returned %d items, want none", len(resp.Items))
	}
	if InterstitialTriggerFollow != "follow" {
		t.Fatalf("InterstitialTriggerFollow = %q; clients send \"follow\"", InterstitialTriggerFollow)
	}
}
