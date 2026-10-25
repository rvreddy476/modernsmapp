package postgres

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// C-CLB-1 for array references — the guard that replaced
// TestNoLiveReferenceIsAnArrayColumn.
//
// The scalar race proof (TestConfirmedAssetSurvivesTheNonFKAvatarRace) covers
// a writer that takes no lock on the media row. An array column is the same
// kind of writer with one more handicap: it cannot carry a foreign key at
// all. So every Array entry in LiveMediaReferences must name a race proof
// here, and the named proof must exist and actually be one: it has to write
// that table's column, run the reclaim, and assert the C-CLB-1 refusal.
//
// The proofs themselves need PostgreSQL and run in CI only
// (composer_reclaim_integration_test.go); this check reads their source, so
// a missing, renamed or hollowed-out proof fails `go test` with no database.
var arrayReferenceRaceProofs = map[string]string{
	"channel_updates.media_ids": "TestConfirmedAssetSurvivesTheChannelUpdatesArrayRace",
}

func TestNoArrayReferenceWithoutARaceProof(t *testing.T) {
	bodies := testFuncBodies(t)

	arrays := map[string]bool{}
	for _, ref := range LiveMediaReferences {
		if !ref.Array {
			continue
		}
		key := ref.Table + "." + ref.Column
		arrays[key] = true

		name, ok := arrayReferenceRaceProofs[key]
		if !ok {
			t.Errorf("%s is an array reference with no C-CLB-1 race proof. Its writer "+
				"cannot take a foreign key and does not lock the media row; write a proof "+
				"in the shape of TestConfirmedAssetSurvivesTheChannelUpdatesArrayRace and "+
				"register it in arrayReferenceRaceProofs", key)
			continue
		}
		body, ok := bodies[name]
		if !ok {
			t.Errorf("%s names race proof %s, which is not a test in this package", key, name)
			continue
		}
		for _, must := range []struct{ text, why string }{
			{ref.Table, "write the referencing table"},
			{ref.Column, "write the array column itself"},
			{"DeleteOrphanMediaAtomic", "run the reclaim the writer races"},
			{"ErrMediaConfirmed", "assert the C-CLB-1 refusal"},
			{"dangling", "check that no committed element names a deleted row"},
		} {
			if !strings.Contains(body, must.text) {
				t.Errorf("race proof %s for %s must %s (no %q in its body)",
					name, key, must.why, must.text)
			}
		}
	}
	for key := range arrayReferenceRaceProofs {
		if !arrays[key] {
			t.Errorf("arrayReferenceRaceProofs lists %s, which is not an array entry in "+
				"LiveMediaReferences; a stale registration hides nothing but proves nothing", key)
		}
	}
}

// The status gate the array proof rests on, without a database.
//
// Every state the processing_status CHECK allows except pending_upload must be
// refused; pending_upload must pass (the audit-H9 sweep still runs).
func TestRefuseConfirmedReclaimCoversEveryFinishedState(t *testing.T) {
	for _, status := range []string{"uploaded", "processing", "ready", "failed", "rejected", ""} {
		if err := refuseConfirmedReclaim(status); !errors.Is(err, ErrMediaConfirmed) {
			t.Errorf("status %q: want ErrMediaConfirmed, got %v. A finished upload can be "+
				"claimed by writers that never lock the media row (channel_updates.media_ids, "+
				"users.avatar_media_id)", status, err)
		}
	}
	if err := refuseConfirmedReclaim(ProcessingStatusPendingUpload); err != nil {
		t.Errorf("pending_upload must stay reclaimable, got %v", err)
	}
}

// testFuncBodies returns the printed body of every Test function in this
// package's _test.go files, build tags ignored (the proofs are integration-
// tagged; go/parser reads them regardless).
func testFuncBodies(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("list test files: %v", err)
	}
	fset := token.NewFileSet()
	out := map[string]string{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			var buf bytes.Buffer
			if err := printer.Fprint(&buf, fset, fn.Body); err != nil {
				t.Fatalf("print %s: %v", fn.Name.Name, err)
			}
			out[fn.Name.Name] = buf.String()
		}
	}
	return out
}
