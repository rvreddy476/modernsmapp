package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

/*
A projection and its scanner must count the same.

Adding a column to groupPostV2Columns without adding it to scanGroupPostV2 —
or to scanGroupPostV2WithViewer, which reads the SAME projection plus three
viewer flags — compiles perfectly and fails at runtime on the first query,
with an arity error that names no column. Adding the anonymity columns did
exactly this to the viewer scanner and the compiler said nothing.

These count rather than inspect: a ratchet, cheap to keep true, that fails
the moment the two drift.
*/

func TestGroupPostProjectionAndScannersAgree(t *testing.T) {
	src := readFile(t, "group.go")

	plain := countColumns(t, src, "const groupPostV2Columns = ")
	qualified := countColumns(t, src, "const groupPostV2ColumnsP = ")
	if plain != qualified {
		t.Fatalf("groupPostV2Columns has %d columns and groupPostV2ColumnsP has %d — they are the same projection with a table alias and must match", plain, qualified)
	}

	viewerCols := countColumns(t, src, "const viewerEngagementColumns = ")

	scanPlain := countScanArgs(t, "scanGroupPostV2")
	if scanPlain != plain {
		t.Fatalf("groupPostV2Columns selects %d columns but scanGroupPostV2 scans %d — every query using this pair fails at runtime", plain, scanPlain)
	}

	scanViewer := countScanArgs(t, "scanGroupPostV2WithViewer")
	want := qualified + viewerCols
	if scanViewer != want {
		t.Fatalf("groupPostV2ColumnsP + viewerEngagementColumns select %d columns but scanGroupPostV2WithViewer scans %d — this is the scanner the feed uses, so the whole group feed 500s", want, scanViewer)
	}
}

// The anonymity columns specifically, since they are what this guard was
// written for and a missing one leaks the real author rather than erroring.
func TestAnonymityColumnsAreSelectedAndScanned(t *testing.T) {
	src := readFile(t, "group.go")
	for _, frag := range []string{"is_anonymous, anon_alias", "p.is_anonymous, p.anon_alias"} {
		if !strings.Contains(src, frag) {
			t.Errorf("projection is missing %q — a post read without them has IsAnonymous false and is emitted with its real author id", frag)
		}
	}
	for _, fn := range []string{"scanGroupPostV2", "scanGroupPostV2WithViewer"} {
		body := funcBody(t, "group.go", fn)
		if !strings.Contains(body, "&p.IsAnonymous") || !strings.Contains(body, "&p.AnonAlias") {
			t.Errorf("%s does not scan the anonymity columns", fn)
		}
	}
}

// The insert must write them, or an anonymous post is stored as a named one.
func TestInsertWritesTheAnonymityColumns(t *testing.T) {
	body := funcBody(t, "group.go", "CreateGroupPostV2")
	if !strings.Contains(body, "is_anonymous, anon_alias") {
		t.Fatal("the INSERT column list omits is_anonymous/anon_alias — the flag is accepted by the service and dropped by the store, and the post is saved under the author's real name")
	}
	if !strings.Contains(body, "p.IsAnonymous, p.AnonAlias") {
		t.Fatal("the INSERT does not pass the anonymity values as arguments")
	}
}

func countColumns(t *testing.T, src, decl string) int {
	t.Helper()
	i := strings.Index(src, decl)
	if i < 0 {
		t.Fatalf("declaration not found: %s", decl)
	}
	rest := src[i+len(decl):]
	// The literal runs to the closing backtick.
	if rest[0] != '`' {
		t.Fatalf("%s is not a raw string literal", decl)
	}
	end := strings.IndexByte(rest[1:], '`')
	if end < 0 {
		t.Fatalf("unterminated literal for %s", decl)
	}
	return len(strings.Split(rest[1:1+end], ","))
}

func countScanArgs(t *testing.T, fn string) int {
	t.Helper()
	body := funcBody(t, "group.go", fn)
	start := strings.Index(body, "row.Scan(")
	if start < 0 {
		t.Fatalf("%s does not call row.Scan", fn)
	}
	rest := body[start+len("row.Scan("):]
	depth := 1
	end := 0
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
	return strings.Count(rest[:end], "&p.")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func funcBody(t *testing.T, path, name string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	src := readFile(t, path)
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != name || fd.Body == nil {
			continue
		}
		return src[fset.Position(fd.Body.Pos()).Offset:fset.Position(fd.Body.End()).Offset]
	}
	t.Fatalf("%s: no function named %s", path, name)
	return ""
}
