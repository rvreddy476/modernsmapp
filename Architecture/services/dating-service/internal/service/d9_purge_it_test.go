// Lane D9 purge completeness: the purged user's deck caches and every deck
// showing them are dropped, raw payment payloads naming them are redacted,
// and each open match is announced in the exact shape chat-service's dating
// consumer decodes. D8 evidence retention is covered by d8_safety_it_test.go.
// Skipped without TEST_PG_DSN (and REDIS_ADDR for the cache test).
package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// d9QueryRow scans one row in its own committed transaction.
func d9QueryRow(t *testing.T, st *store.Store, sql string, dest any, args ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := st.BeginTx(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.QueryRow(ctx, sql, args...).Scan(dest); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("query %q: %v", sql, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestD9_PurgeDropsDeckCaches(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	if svc.rdb == nil {
		t.Skip("REDIS_ADDR not set; skipping deck cache purge test")
	}
	ctx := context.Background()
	purged, viewer, other := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{purged, viewer, other} {
		seedActiveProfile(t, st, id)
	}
	// viewer's deck shows the purged user; the purged user's deck shows other.
	svc.writePulseCache(ctx, viewer, &PulseResponse{Data: []PulseCard{{CandidateID: purged}}})
	svc.writePulseCache(ctx, purged, &PulseResponse{Data: []PulseCard{{CandidateID: other}}})
	if err := svc.rdb.Set(ctx, boostTokenKey(purged), "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := svc.rdb.Set(ctx, boostRateLimitKey(purged), "1", 0).Err(); err != nil {
		t.Fatal(err)
	}

	if err := svc.PurgeProfile(ctx, purged); err != nil {
		t.Fatalf("purge: %v", err)
	}
	for name, key := range map[string]string{
		"viewer's deck showing the purged user": svc.cacheKey(viewer),
		"purged user's own deck":                svc.cacheKey(purged),
		"reverse index of the purged user":      deckMembershipKey(purged),
		"boost token":                           boostTokenKey(purged),
		"boost rate limit":                      boostRateLimitKey(purged),
	} {
		if n, err := svc.rdb.Exists(ctx, key).Result(); err != nil || n != 0 {
			t.Fatalf("%s still cached after purge (exists=%d err=%v)", name, n, err)
		}
	}
	if member, err := svc.rdb.SIsMember(ctx, deckMembershipKey(other), purged.String()).Result(); err != nil || member {
		t.Fatalf("purged user still listed as a viewer of another candidate (member=%v err=%v)", member, err)
	}
}

func TestD9_PurgeRedactsRawPaymentPayloads(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	// The Razorpay tables are read-only in code since lane P2; this fixture
	// writes a legacy plan row directly so old intents and events can exist.
	d8Exec(t, st, `INSERT INTO dating_premium_plans (id, plan_type, name, price_inr_paise, duration_days)
        VALUES ('monthly_399', 'subscription', 'Pulse Premium Monthly', 39900, 30) ON CONFLICT (id) DO NOTHING`)
	purged, bystander := uuid.New(), uuid.New()
	seedActiveProfile(t, st, purged)
	var planID string
	d9QueryRow(t, st, `SELECT id FROM dating_premium_plans ORDER BY id LIMIT 1`, &planID)
	order := "order_" + uuid.NewString()[:12]
	var intentID uuid.UUID
	d9QueryRow(t, st, `INSERT INTO dating_payment_intents (user_id, plan_id, amount_inr_paise, razorpay_order_id)
        VALUES ($1, $2, 9900, $3) RETURNING id`, &intentID, purged, planID, order)
	linkedEvent, unlinkedEvent, otherEvent := "evt_"+uuid.NewString(), "evt_"+uuid.NewString(), "evt_"+uuid.NewString()
	d8Exec(t, st, `INSERT INTO dating_payment_events (payment_intent_id, razorpay_event_id, event_type, payload)
        VALUES ($1, $2, 'payment.captured', jsonb_build_object('email', 'payer@example.com', 'contact', '+919900000000'))`, intentID, linkedEvent)
	d8Exec(t, st, `INSERT INTO dating_payment_events (razorpay_event_id, event_type, payload)
        VALUES ($1, 'order.paid', jsonb_build_object('order_id', $2::text, 'notes', jsonb_build_object('user_id', $3::text)))`, unlinkedEvent, order, purged.String())
	d8Exec(t, st, `INSERT INTO dating_payment_events (razorpay_event_id, event_type, payload)
        VALUES ($1, 'order.paid', jsonb_build_object('notes', jsonb_build_object('user_id', $2::text)))`, otherEvent, bystander.String())

	if err := svc.PurgeProfile(ctx, purged); err != nil {
		t.Fatalf("purge: %v", err)
	}
	for _, ev := range []string{linkedEvent, unlinkedEvent} {
		if n := d8QueryInt(t, st, `SELECT COUNT(*)::int FROM dating_payment_events
            WHERE razorpay_event_id = $1 AND payload = '{"redacted": true, "reason": "account_purged"}'::jsonb`, ev); n != 1 {
			t.Fatalf("raw payload of %s not redacted after purge", ev)
		}
	}
	if n := d8QueryInt(t, st, `SELECT COUNT(*)::int FROM dating_payment_events
        WHERE razorpay_event_id = $1 AND payload ? 'notes'`, otherEvent); n != 1 {
		t.Fatalf("another user's payment payload was redacted")
	}
}

func TestD9_PurgeAnnouncesClosedMatchInChatShape(t *testing.T) {
	svc, st, rec := newD3Svc(t)
	ctx := context.Background()
	purged, partner := uuid.New(), uuid.New()
	seedActiveProfile(t, st, purged)
	seedActiveProfile(t, st, partner)
	matchID := d3Match(t, svc, purged, partner)
	if err := svc.PurgeProfile(ctx, purged); err != nil {
		t.Fatalf("purge: %v", err)
	}
	found := false
	for _, e := range eventsOfType(t, rec, "dating.match.closed") {
		// chat-service message-service internal/events/dating_consumer.go
		// matchClosedPayload.
		var p struct {
			MatchID string `json:"match_id"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("decode as chat does: %v", err)
		}
		if p.MatchID == matchID.String() {
			found = true
		}
	}
	if !found {
		t.Fatalf("no dating.match.closed chat can decode for match %s", matchID)
	}
}
