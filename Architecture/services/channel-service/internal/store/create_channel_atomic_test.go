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
A channel and who owns it are one fact, so they must be written once.

CreateChannel used to insert only broadcast_channels; the service then called
AddMember for the owner and, on failure, logged a warning and returned success.
That produced channels with subscriber_count 1 — "owner counts" — and nobody in
channel_members. One channel on the development stack is in that state. Such a
channel still resolves its owner from broadcast_channels.owner_id, so it is not
unusable, but every membership read disagrees with the record: the /admins list
has no owner, and the channel's own Overview could not name who runs it.

There is no database in this package's tests, so the guard is structural: it
holds the shape of the function rather than its behaviour. That is weaker than
an integration test and stronger than nothing — it makes splitting the two
writes apart again, or swallowing the second one's error, a test failure.
*/
func TestCreateChannelWritesOwnerMembershipInSameTransaction(t *testing.T) {
	fn := funcBody(t, "channel.go", "CreateChannel")

	if !strings.Contains(fn, "s.db.Begin(ctx)") {
		t.Fatal("CreateChannel must open a transaction — the channel row and the owner's membership row have to commit together or not at all")
	}
	if !strings.Contains(fn, "tx.Commit(ctx)") {
		t.Fatal("CreateChannel must commit its transaction")
	}
	if !strings.Contains(fn, "defer tx.Rollback(ctx)") {
		t.Fatal("CreateChannel must defer a rollback, or a failure between the two inserts leaves the transaction open")
	}
	if !strings.Contains(fn, "INSERT INTO channel_members") {
		t.Fatal("CreateChannel must insert the owner's channel_members row; doing it in a later call is how a channel ends up owned by nobody")
	}
	if !strings.Contains(fn, "'owner'") {
		t.Fatal("the membership row CreateChannel writes must carry the owner role")
	}

	// Both writes must go through the transaction. A stray s.db.Exec or
	// s.db.QueryRow inside this function would commit outside it, which is the
	// exact bug being closed.
	if strings.Contains(fn, "s.db.QueryRow(ctx") || strings.Contains(fn, "s.db.Exec(ctx") {
		t.Fatal("CreateChannel writes through s.db instead of tx — that write commits independently of the transaction")
	}
}

// The service must no longer add the owner separately. Two writers for one
// fact is how the states above diverge, and the old call site logged its error
// rather than returning it, so the caller was told the channel was fine.
func TestServiceDoesNotAddOwnerMemberSeparately(t *testing.T) {
	fn := funcBodyIn(t, "../service/channel.go", "CreateChannel")

	if strings.Contains(fn, "s.store.AddMember(ctx") {
		t.Fatal("service.CreateChannel calls AddMember — the owner's row belongs in store.CreateChannel's transaction, and a swallowed failure here is what left a channel with no owner row")
	}
	if strings.Contains(fn, `Role:      "owner"`) {
		t.Fatal("service.CreateChannel still builds an owner membership row; store.CreateChannel owns that write")
	}
}

func funcBody(t *testing.T, file, name string) string {
	t.Helper()
	return funcBodyIn(t, file, name)
}

// funcBodyIn returns the source text of the named top-level function or method.
func funcBodyIn(t *testing.T, path, name string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	src, err := readFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != name || fd.Body == nil {
			continue
		}
		start := fset.Position(fd.Body.Pos()).Offset
		end := fset.Position(fd.Body.End()).Offset
		if start < 0 || end > len(src) {
			t.Fatalf("%s.%s: offsets out of range", path, name)
		}
		return src[start:end]
	}
	t.Fatalf("%s: no function named %s — it was renamed or removed, and this guard no longer protects anything", path, name)
	return ""
}

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}
