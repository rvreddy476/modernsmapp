package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

/*
A retry must answer with the group the first attempt made.

CreateGroup validated everything before it reached the store's idempotency
check, and the store's check is the last thing it does. So a replay of a
request that had already succeeded hit the handle-availability check, found
the group the FIRST attempt created, and returned:

	handle 'x' is already taken

— a conflict with itself. That is the one answer a retry must never get, and
pressing "Create group" again could never clear it. Reproduced against the
running stack: two identical requests with one Idempotency-Key gave 200 then
409.

The replay lookup now runs first, before the rate limit and before any
validation. This guard holds that ORDER, because the ordering is the whole
bug — every individual check is correct on its own and wrong on a replay.

Structural rather than behavioural: this package's tests have no database, and
the thing worth protecting is which statement comes first.
*/
func TestCreateGroupAnswersReplayBeforeValidating(t *testing.T) {
	body := funcSource(t, "group.go", "CreateGroup")

	lookup := strings.Index(body, "FindGroupByIdempotencyKey")
	if lookup < 0 {
		t.Fatal("CreateGroup no longer looks up a previous request by idempotency key — a retry after a lost response will conflict with the group it already created")
	}

	// Everything a replay must not be subjected to. Each of these is correct
	// for a first attempt and wrong for a repeat.
	for _, after := range []struct{ marker, why string }{
		{"rl:group_create:", "the rate limit counts one intent twice if a replay reaches it"},
		{"ValidateGroupName(req.Name)", "a replay re-validates a name that was already accepted"},
		{"CheckHandleAvailability", "a replay finds its OWN group and reports the handle as taken — this is the reported bug"},
	} {
		at := strings.Index(body, after.marker)
		if at < 0 {
			t.Fatalf("CreateGroup no longer contains %q, so this guard no longer protects the ordering around it", after.marker)
		}
		if at < lookup {
			t.Fatalf("%s runs BEFORE the idempotency lookup: %s", after.marker, after.why)
		}
	}
}

// funcSource returns the source text of the named top-level function's body.
func funcSource(t *testing.T, path, name string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(raw)
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
