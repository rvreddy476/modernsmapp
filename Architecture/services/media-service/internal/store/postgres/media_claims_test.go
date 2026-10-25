package postgres

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The owner-delete decision: any holder refuses, with the holders attached.
func TestOwnerDeleteRefusedWhileAnyClaimExists(t *testing.T) {
	if err := refuseClaimedDelete(nil); err != nil {
		t.Fatalf("no claims must allow the delete, got %v", err)
	}
	holders := []ClaimHolder{{Claimant: "channel-service", Kind: "channel_update", Count: 1}}
	err := refuseClaimedDelete(holders)
	if !errors.Is(err, ErrMediaInUse) {
		t.Fatalf("one claim must refuse the delete with ErrMediaInUse, got %v", err)
	}
	var inUse *MediaInUseError
	if !errors.As(err, &inUse) || len(inUse.Holders) != 1 || inUse.Holders[0] != holders[0] {
		t.Fatalf("the refusal must carry its holders for the owner, got %#v", err)
	}
}

// A claim and a physical delete must take conflicting row locks, or the
// reference check inside the delete cannot see a concurrent claim (C-CLB-1).
// FOR UPDATE conflicts with every row lock; KEY SHARE is the weakest one that
// still conflicts with it. NOWAIT / SKIP LOCKED would turn "wait for the
// other side" into "proceed as if it were not there".
func TestClaimAndDeleteLocksConflict(t *testing.T) {
	if !strings.HasSuffix(lockMediaForDeleteSQL, "FOR UPDATE") {
		t.Errorf("the owner delete must lock the media row FOR UPDATE: %q", lockMediaForDeleteSQL)
	}
	if !strings.HasSuffix(lockMediaForClaimSQL, "FOR KEY SHARE") &&
		!strings.HasSuffix(lockMediaForClaimSQL, "FOR SHARE") {
		t.Errorf("a claim must lock the media row FOR KEY SHARE (or stronger): %q", lockMediaForClaimSQL)
	}
	for _, q := range []string{lockMediaForDeleteSQL, lockMediaForClaimSQL} {
		if !strings.Contains(q, "FROM media_assets WHERE id = $1") {
			t.Errorf("lock must be on the asset row itself: %q", q)
		}
		for _, bad := range []string{"NOWAIT", "SKIP LOCKED"} {
			if strings.Contains(q, bad) {
				t.Errorf("%q must wait for the other side, not %s", q, bad)
			}
		}
	}
}

// DeleteMedia must lock, then read claims and refuse, all before any row is
// deleted. Read from the source because the store needs PostgreSQL to run;
// the behaviour itself is proven in CI by TestOwnerDeleteAndClaimRaceNeverDangle.
func TestDeleteMediaChecksClaimsUnderTheLockBeforeDeleting(t *testing.T) {
	body := storeMethodBody(t, "media.go", "DeleteMedia")
	order := []string{"lockMediaForDeleteSQL", "mediaClaimHoldersTx(ctx, tx, id)", "refuseClaimedDelete(holders)", "DELETE FROM"}
	at := -1
	for _, step := range order {
		i := strings.Index(body, step)
		if i < 0 {
			t.Fatalf("DeleteMedia no longer contains %q", step)
		}
		if i < at {
			t.Fatalf("DeleteMedia runs %q out of order; want %v", step, order)
		}
		at = i
	}
}

// The referrer purge (post purge) must also refuse a claimed asset: the post
// was its last POST, not necessarily its last user.
func TestReferrerPurgeConsultsClaims(t *testing.T) {
	id := uuid.New()
	for _, c := range referrerPurgeChecks(id, uuid.New()) {
		if c.table != "media_claims" {
			continue
		}
		if !strings.Contains(c.sql, "FROM media_claims WHERE media_id = $1") || len(c.args) != 1 || c.args[0] != id {
			t.Fatalf("media_claims check is malformed: %q %v", c.sql, c.args)
		}
		return
	}
	t.Fatal("DeleteAssetForReferrer does not check media_claims; a post purge would delete media a channel update still shows")
}

// An account erasure removes the user's claims before the assets, or the
// RESTRICT key would make the purge fail forever.
func TestPurgeUserDropsClaimsBeforeAssets(t *testing.T) {
	claims, assets := -1, -1
	for i, st := range purgeUserSteps() {
		if st.table == "media_claims" && strings.HasPrefix(strings.TrimSpace(st.sql), "DELETE FROM media_claims") {
			claims = i
		}
		if st.table == "media_assets" && strings.HasPrefix(strings.TrimSpace(st.sql), "DELETE FROM media_assets") {
			assets = i
		}
	}
	if claims < 0 {
		t.Fatal("PurgeUser does not delete media_claims rows; the RESTRICT foreign key would refuse every erasure of claimed media")
	}
	if assets < 0 || claims > assets {
		t.Fatalf("media_claims (step %d) must go before media_assets (step %d)", claims, assets)
	}
}

// The claim table is a live reference for the sweeper too.
func TestMediaClaimsBlockReclamation(t *testing.T) {
	for _, ref := range LiveMediaReferences {
		if ref.Table == "media_claims" && ref.Column == "media_id" && !ref.Array && ref.Predicate == "" {
			return
		}
	}
	t.Fatal("media_claims.media_id is not an unconditional live reference")
}

func TestUniqueSortedIDs(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	got := uniqueSortedIDs([]uuid.UUID{b, a, b, a})
	if len(got) != 2 || got[0].String() > got[1].String() {
		t.Fatalf("got %v", got)
	}
}

// storeMethodBody prints a *MediaAssetStore method's body from file.
func storeMethodBody(t *testing.T, file, name string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Name.Name != name || fn.Body == nil {
			continue
		}
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, fset, fn.Body); err != nil {
			t.Fatalf("print %s: %v", name, err)
		}
		return buf.String()
	}
	t.Fatalf("no method %s in %s", name, file)
	return ""
}
