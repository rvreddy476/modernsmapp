package store

import (
	"strings"
	"testing"
)

/*
groupColumns is one projection; every scanner that reads it must take
exactly as many destinations. scanGroup is the canonical reader.
DiscoverGroupsForUser scans the same columns by hand, then four scoring
fields — it fell one destination short when allow_anonymous_posts joined
the projection, and GET /v1/groups/discover answered 500 for everyone:
"number of field descriptions must equal number of destinations, got 37
and 36".
*/

const discoveryExtraColumns = 4 // friend_count, category_match, location_match, score

// groupColumnCount counts the projection's top-level commas: groupColumns
// carries `COALESCE(g.handle, '') AS handle`, whose inner comma is not a
// column boundary (the plain splitter in projection_scanner_test.go would
// count it and be one too high).
func groupColumnCount(t *testing.T) int {
	t.Helper()
	src := readFile(t, "group.go")
	const decl = "const groupColumns = `"
	i := strings.Index(src, decl)
	if i < 0 {
		t.Fatal("groupColumns declaration not found")
	}
	rest := src[i+len(decl):]
	end := strings.IndexByte(rest, '`')
	if end < 0 {
		t.Fatal("unterminated groupColumns literal")
	}
	depth, n := 0, 1
	for _, r := range rest[:end] {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				n++
			}
		}
	}
	return n
}

func TestGroupColumnsAndScanGroupAgree(t *testing.T) {
	cols := groupColumnCount(t)
	scanned := countScanDests(t, funcBody(t, "group.go", "scanGroup"), "row.Scan(", "&g.")
	if cols != scanned {
		t.Fatalf("groupColumns selects %d columns but scanGroup scans %d — every group read fails at runtime", cols, scanned)
	}
}

func TestDiscoverGroupsScansGroupColumnsPlusScoring(t *testing.T) {
	cols := groupColumnCount(t)
	body := funcBody(t, "group_posts.go", "DiscoverGroupsForUser")
	if !strings.Contains(body, "groupColumns") {
		t.Fatal("DiscoverGroupsForUser no longer selects groupColumns; update this guard with its projection")
	}
	scanned := countScanDests(t, body, "rows.Scan(", "&d.")
	want := cols + discoveryExtraColumns
	if scanned != want {
		t.Fatalf("DiscoverGroupsForUser selects groupColumns (%d) + %d scoring fields = %d columns but scans %d — GET /v1/groups/discover 500s for every viewer", cols, discoveryExtraColumns, want, scanned)
	}
	if !strings.Contains(body, "&d.AllowAnonymousPosts") {
		t.Fatal("DiscoverGroupsForUser does not scan AllowAnonymousPosts — the column groupColumns ends with")
	}
}

// countScanDests counts `prefix` destinations inside the first `call(` in body.
func countScanDests(t *testing.T, body, call, prefix string) int {
	t.Helper()
	start := strings.Index(body, call)
	if start < 0 {
		t.Fatalf("body does not contain %s", call)
	}
	rest := body[start+len(call):]
	depth := 1
	end := -1
	for i, r := range rest {
		if r == '(' {
			depth++
		} else if r == ')' {
			depth--
			if depth == 0 {
				end = i
				break
			}
		}
	}
	if end < 0 {
		t.Fatalf("unbalanced parentheses after %s", call)
	}
	return strings.Count(rest[:end], prefix)
}
