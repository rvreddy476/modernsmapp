// Audit 2026-10-30 M2: a live location share to a match ends at unmatch and
// block. Integration tests skip without TEST_PG_DSN (CI only, "_test" DB).
package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

func m2ShareState(t *testing.T, st *store.Store, shareID uuid.UUID) (stopped, pointCleared bool) {
	t.Helper()
	return d8QueryInt(t, st, `SELECT COUNT(*)::int FROM dating_location_shares WHERE id = $1 AND stopped_at IS NOT NULL`, shareID) == 1,
		d8QueryInt(t, st, `SELECT COUNT(*)::int FROM dating_location_shares
            WHERE id = $1 AND location_sealed IS NULL AND latitude IS NULL AND longitude IS NULL`, shareID) == 1
}

func TestAuditM2_UnmatchEndsMatchShareBothWays(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)
	mid := d3Match(t, svc, a, b)

	fromA, err := svc.ShareLocation(ctx, a, LocationShareRequest{RecipientID: b, DurationMinutes: 60,
		Latitude: floatPtr(12.971599), Longitude: floatPtr(77.594566)})
	if err != nil {
		t.Fatalf("share a->b: %v", err)
	}
	fromB, err := svc.ShareLocation(ctx, b, LocationShareRequest{RecipientID: a, DurationMinutes: 60,
		Latitude: floatPtr(12.9), Longitude: floatPtr(77.5)})
	if err != nil {
		t.Fatalf("share b->a: %v", err)
	}
	if _, err := svc.GetSharedLocation(ctx, b, fromA.ShareID); err != nil {
		t.Fatalf("read before unmatch: %v", err)
	}

	if err := svc.CloseMatch(ctx, mid, a); err != nil {
		t.Fatalf("unmatch: %v", err)
	}
	for _, c := range []struct {
		reader uuid.UUID
		share  uuid.UUID
	}{{b, fromA.ShareID}, {a, fromB.ShareID}} {
		if _, err := svc.GetSharedLocation(ctx, c.reader, c.share); !errors.Is(err, store.ErrLocationShareNotFound) {
			t.Fatalf("read after unmatch: err=%v, want not found", err)
		}
		if stopped, cleared := m2ShareState(t, st, c.share); !stopped || !cleared {
			t.Fatalf("share %s after unmatch: stopped=%v pointCleared=%v; want both (same transaction as the close)", c.share, stopped, cleared)
		}
	}
	incoming, err := st.ListActiveSharesForRecipient(ctx, b)
	if err != nil || len(incoming) != 0 {
		t.Fatalf("incoming shares after unmatch = %d err=%v, want none", len(incoming), err)
	}
}

func TestAuditM2_BlockEndsMatchShare(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)
	d3Match(t, svc, a, b)
	share, err := svc.ShareLocation(ctx, a, LocationShareRequest{RecipientID: b, DurationMinutes: 60,
		Latitude: floatPtr(12.97), Longitude: floatPtr(77.59)})
	if err != nil {
		t.Fatalf("share: %v", err)
	}
	if err := svc.Block(ctx, b, a); err != nil {
		t.Fatalf("block: %v", err)
	}
	if stopped, cleared := m2ShareState(t, st, share.ShareID); !stopped || !cleared {
		t.Fatalf("after block: stopped=%v pointCleared=%v; want both", stopped, cleared)
	}
	// Unblocking restores nothing: no open match, so the share stays unreadable.
	if _, err := svc.Unblock(ctx, b, a); err != nil {
		t.Fatalf("unblock: %v", err)
	}
	if _, err := svc.GetSharedLocation(ctx, b, share.ShareID); !errors.Is(err, store.ErrLocationShareNotFound) {
		t.Fatalf("read after unblock: err=%v, want not found", err)
	}
}

// The read-side check alone (a row the trigger never saw, e.g. a match
// closed before this release): a live, unexpired, unstopped match share is
// unreadable once the pair has no open match. A trusted-contact share is not.
func TestAuditM2_ReadRequiresOpenMatchForMatchShares(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)
	mid := d3Match(t, svc, a, b)
	share, err := svc.ShareLocation(ctx, a, LocationShareRequest{RecipientID: b, DurationMinutes: 60,
		Latitude: floatPtr(12.97), Longitude: floatPtr(77.59)})
	if err != nil {
		t.Fatalf("share: %v", err)
	}
	// Close the match behind the trigger's back, in one transaction so no
	// other session ever sees the trigger disabled.
	tx, err := st.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`ALTER TABLE dating_matches DISABLE TRIGGER dating_matches_stop_location_shares`,
		`UPDATE dating_matches SET status = 'expired' WHERE id = '` + mid.String() + `'`,
		`ALTER TABLE dating_matches ENABLE TRIGGER dating_matches_stop_location_shares`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if stopped, _ := m2ShareState(t, st, share.ShareID); stopped {
		t.Fatalf("setup: the share was stopped; the trigger was meant to be off")
	}
	if _, err := svc.GetSharedLocation(ctx, b, share.ShareID); !errors.Is(err, store.ErrLocationShareNotFound) {
		t.Fatalf("read with no open match: err=%v, want not found", err)
	}
	if out, err := st.ListActiveSharesByUser(ctx, a); err != nil || len(out) != 0 {
		t.Fatalf("outgoing list = %d err=%v, want none", len(out), err)
	}

	// Trusted-contact shares do not depend on a match.
	c := uuid.New()
	seedActiveProfile(t, st, c)
	d8Exec(t, st, `INSERT INTO dating_trusted_contacts (user_id, contact_id) VALUES ($1, $2)`, a, c)
	tc, err := svc.ShareLocation(ctx, a, LocationShareRequest{RecipientID: c, DurationMinutes: 30,
		Latitude: floatPtr(12.97), Longitude: floatPtr(77.59)})
	if err != nil || tc.RecipientKind != store.ShareRecipientTrustedContact {
		t.Fatalf("trusted share = %+v err=%v", tc, err)
	}
	if _, err := svc.GetSharedLocation(ctx, c, tc.ShareID); err != nil {
		t.Fatalf("trusted contact read: %v", err)
	}
}
