package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDeliveryReviewNotificationsCommitWithDecision(t *testing.T) {
	st, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, partner := seedDeliveryPartner(t, st)
	admin, media := uuid.New(), uuid.New()
	doc, err := st.AddDeliveryPartnerDocument(ctx, user, DeliveryDocumentRecord{DocumentType: "PASSPORT", MediaID: &media, Status: DocumentStatusPending})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.AdminDecideDeliveryPartnerDocument(ctx, admin, partner, doc.ID, "REJECTED", "private review notes"); err != nil {
		t.Fatal(err)
	}
	if err = st.AdminSetDeliveryPartnerStatus(ctx, admin, partner, "SUSPENDED", "private account notes"); err != nil {
		t.Fatal(err)
	}
	if err = st.AdminApproveDeliveryPartner(ctx, admin, partner, false, "private rejection notes"); err != nil {
		t.Fatal(err)
	}
	rows, err := st.db.Query(ctx, `SELECT event_type, payload::text FROM food.outbox_events WHERE partition_key = $1 ORDER BY id`, "food:delivery:"+partner.String())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var typ, raw string
		if err := rows.Scan(&typ, &raw); err != nil {
			t.Fatal(err)
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		if event["partner_user_id"] != user.String() || event["partner_id"] != partner.String() {
			t.Fatalf("wrong identity recipient: %s", raw)
		}
		if strings.Contains(raw, "private") || strings.Contains(raw, media.String()) {
			t.Fatal("review payload contains private content")
		}
		if typ == "food.delivery_partner.document_reviewed" && (event["document_id"] != doc.ID.String() || event["status"] != "REJECTED") {
			t.Fatalf("wrong document decision: %s", raw)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("notifications=%d, want 3", count)
	}
}

func TestDeliveryReviewOutboxRollsBackAndDoesNotAnnounceFailedApproval(t *testing.T) {
	st, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	_, partner := seedDeliveryPartnerWithStatus(t, st, "PENDING_REVIEW", false)
	tx, err := st.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = enqueueDeliveryReviewTx(ctx, tx, partner, uuid.Nil, "APPROVED"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = st.AdminApproveDeliveryPartner(ctx, uuid.New(), partner, true, ""); err == nil {
		t.Fatal("unverified fixture unexpectedly approved")
	}
	var count int
	if err = st.db.QueryRow(ctx, `SELECT count(*) FROM food.outbox_events WHERE partition_key = $1`, "food:delivery:"+partner.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("uncommitted review emitted a notification")
	}
}
