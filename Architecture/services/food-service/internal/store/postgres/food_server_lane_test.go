package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/shared/partnerwork"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Food server lane (9 Nov 2026): the pure halves of the new rules. The SQL
// halves are in food_server_lane_integration_test.go (CI only).

func TestParsePartnerOrderStatuses(t *testing.T) {
	got, err := ParsePartnerOrderStatuses([]string{"confirmed, PREPARING", "PREPARING", "", "ready_for_pickup"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "CONFIRMED" || got[1] != "PREPARING" || got[2] != "READY_FOR_PICKUP" {
		t.Fatalf("statuses = %v", got)
	}
	if got, err := ParsePartnerOrderStatuses(nil); err != nil || got != nil {
		t.Fatalf("no filter: %v %v", got, err)
	}
	for _, bad := range []string{"SHIPPED", "CONFIRMED,NOPE", "confirmed;drop"} {
		if _, err := ParsePartnerOrderStatuses([]string{bad}); !errors.Is(err, ErrInvalidOrderStatusFilter) {
			t.Fatalf("%q accepted: %v", bad, err)
		}
	}
}

func TestOrderCursorRoundTripAndRefusal(t *testing.T) {
	c := OrderCursor{CreatedAt: time.Date(2026, 11, 9, 10, 0, 0, 123456000, time.UTC), ID: uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0015")}
	back, err := DecodeOrderCursor(EncodeOrderCursor(c))
	if err != nil || !back.CreatedAt.Equal(c.CreatedAt) || back.ID != c.ID {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	for _, bad := range []string{"", "!!", EncodeOrderCursor(c)[:10], "1762682400000000.not-a-uuid", "-5.0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0015"} {
		if _, err := DecodeOrderCursor(bad); !errors.Is(err, ErrInvalidOrderCursor) {
			t.Fatalf("cursor %q accepted: %v", bad, err)
		}
	}
}

func TestPartnerOrdersPageLimit(t *testing.T) {
	for in, want := range map[int]int{0: 100, -1: 100, 1: 1, 50: 50, 100: 100, 101: 100} {
		if got := partnerOrdersPageLimit(in); got != want {
			t.Fatalf("limit %d: %d, want %d", in, got, want)
		}
	}
}

func TestPageWithCursor(t *testing.T) {
	at := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	orders := []Order{{ID: uuid.New()}, {ID: uuid.New()}, {ID: uuid.New()}}
	created := []time.Time{at, at.Add(-time.Second), at.Add(-2 * time.Second)}

	page, next := pageWithCursor(orders, created, 2)
	if len(page) != 2 || next == nil || next.ID != orders[1].ID || !next.CreatedAt.Equal(created[1]) {
		t.Fatalf("look-ahead page: %d orders, next %+v", len(page), next)
	}
	// Exactly a page, or fewer: the last page names no cursor.
	if page, next := pageWithCursor(orders[:2], created[:2], 2); len(page) != 2 || next != nil {
		t.Fatalf("full last page: %d orders, next %+v", len(page), next)
	}
	if page, next := pageWithCursor(nil, nil, 2); len(page) != 0 || next != nil {
		t.Fatalf("empty: %v %v", page, next)
	}
}

func TestReplayedEntityID(t *testing.T) {
	id := uuid.New()
	if got, err := replayedEntityID(map[string]any{"id": id.String()}); err != nil || got != id {
		t.Fatalf("stored id: %s %v", got, err)
	}
	for _, body := range []map[string]any{nil, {}, {"order_id": id.String()}, {"id": "nope"}, {"id": 7}} {
		if _, err := replayedEntityID(body); !errors.Is(err, ErrIdempotencyPayloadMismatch) {
			t.Fatalf("body %v replayed: %v", body, err)
		}
	}
}

// onJobTx answers the partner lookup in refuseOfflineOnJobTx; every other
// pgx.Tx method is unused (nil embedded interface).
type onJobTx struct {
	pgx.Tx
	onJob   bool
	missing bool
	reads   int
}

type onJobRow struct{ tx *onJobTx }

func (r onJobRow) Scan(dest ...any) error {
	if r.tx.missing {
		return pgx.ErrNoRows
	}
	*(dest[0].(*bool)) = r.tx.onJob
	return nil
}

func (t *onJobTx) QueryRow(context.Context, string, ...any) pgx.Row {
	t.reads++
	return onJobRow{t}
}

func TestRefuseOfflineOnJobTx(t *testing.T) {
	ctx, user := context.Background(), uuid.New()

	t.Setenv(partnerwork.EnabledVariable, "false")
	if err := refuseOfflineOnJobTx(ctx, &onJobTx{onJob: true}, user, false); !errors.Is(err, ErrRiderOnActiveJob) {
		t.Fatalf("offline on a job: %v, want ErrRiderOnActiveJob", err)
	}
	if err := refuseOfflineOnJobTx(ctx, &onJobTx{onJob: false}, user, false); err != nil {
		t.Fatalf("offline with no job refused: %v", err)
	}
	if err := refuseOfflineOnJobTx(ctx, &onJobTx{missing: true}, user, false); err != nil {
		t.Fatalf("a non-partner is answered by the update that follows, not here: %v", err)
	}
	online := &onJobTx{onJob: true}
	if err := refuseOfflineOnJobTx(ctx, online, user, true); err != nil || online.reads != 0 {
		t.Fatalf("going online: err=%v reads=%d", err, online.reads)
	}

	// partnerwork on refuses in partner_work.domain_duty; this rule stands aside.
	t.Setenv(partnerwork.EnabledVariable, "true")
	on := &onJobTx{onJob: true}
	if err := refuseOfflineOnJobTx(ctx, on, user, false); err != nil || on.reads != 0 {
		t.Fatalf("partnerwork on: err=%v reads=%d", err, on.reads)
	}
}
