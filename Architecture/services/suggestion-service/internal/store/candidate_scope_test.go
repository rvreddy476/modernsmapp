package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// SG-M1: no statement may delete suggestion candidates by candidate alone.
// One user's "block" used to run DELETE ... WHERE candidate_id = $1 and erase
// that person from every viewer's suggestions (with a full-table scan: every
// index leads with viewer_id). Every delete must name the viewer.
func TestCandidateDeletesAreScopedToAViewer(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			sql := strings.Join(strings.Fields(lit.Value), " ")
			if !strings.Contains(sql, "DELETE FROM suggestion_candidates") {
				return true
			}
			checked++
			if !strings.Contains(sql, "viewer_id = $") {
				t.Errorf("%s: a candidate delete does not name the viewer: %s", name, sql)
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("found no candidate deletes; this guard would pass while checking nothing")
	}
}

func TestBlockRate(t *testing.T) {
	for _, tc := range []struct {
		blocks, followers int
		want              float32
	}{
		{0, 0, 0}, {5, 0, 0}, {0, 10, 0}, {10, 30, 0.25},
	} {
		if got := BlockRate(tc.blocks, tc.followers); got != tc.want {
			t.Errorf("BlockRate(%d, %d) = %v, want %v", tc.blocks, tc.followers, got, tc.want)
		}
	}
}
