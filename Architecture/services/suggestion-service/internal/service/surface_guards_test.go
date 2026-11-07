package service

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/google/uuid"
)

// SG-M2: the suggestion surfaces return through filterEgress, never through
// filterBlocked alone (which checks blocks only, so a deactivated account
// or a hidden card would slip through).
func TestSurfacesReturnThroughTheEgressFilter(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "suggestion.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	egress := 0
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "filterBlocked":
			t.Errorf("suggestion.go returns through filterBlocked; use filterEgress")
		case "filterEgress":
			egress++
		}
		return true
	})
	// cache hit, stored candidates, interstitial, popular fallback
	if egress < 4 {
		t.Fatalf("filterEgress is called %d times in suggestion.go, want every surface (4)", egress)
	}
}

// SG-L2: one impression report writes at most maxImpressionItems rows. The
// ids here are unparseable, so the loop never reaches the (absent) store.
func TestLogImpressionsIsCapped(t *testing.T) {
	req := &ImpressionRequest{}
	for i := 0; i < maxImpressionItems+25; i++ {
		req.Items = append(req.Items, struct {
			CandidateUserID string  `json:"candidate_user_id"`
			Rank            int16   `json:"rank"`
			Score           float32 `json:"score"`
		}{CandidateUserID: "not-a-uuid"})
	}
	if err := (&Service{}).LogImpressions(context.Background(), uuid.New(), req); err != nil {
		t.Fatal(err)
	}
	if len(req.Items) != maxImpressionItems {
		t.Fatalf("an impression report kept %d items, want at most %d", len(req.Items), maxImpressionItems)
	}
}
