package postgres

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"testing"
)

func TestRestaurantDecisionCommitsDurablePrivateNotice(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	order, _, _ := seedOrderWithItem(t, s, "CONFIRMED")
	var restaurant uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT restaurant_id FROM food.orders WHERE id=$1`, order).Scan(&restaurant); err != nil {
		t.Fatal(err)
	}
	// The event carries authoritative owner IDs, never administrator free text.
	reason := "synthetic private review note"
	if err := s.AdminSetRestaurantStatus(ctx, uuid.New(), restaurant, "SUSPENDED", reason); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := s.db.QueryRow(ctx, `SELECT payload FROM food.outbox_events WHERE event_type='food.restaurant.status_changed' ORDER BY created_at DESC LIMIT 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var e map[string]any
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	var owner uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT owner_user_id FROM food.restaurants WHERE id=$1`, restaurant).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if e["restaurant_owner_user_id"] != owner.String() || e["status"] != "SUSPENDED" || e["restaurant_id"] != restaurant.String() {
		t.Fatalf("event=%s", raw)
	}
	if _, ok := e["reason"]; ok {
		t.Fatal("private free text leaked")
	}
}
