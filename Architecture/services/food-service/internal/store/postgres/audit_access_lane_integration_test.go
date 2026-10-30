package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Access-and-data lane of the 2026-10-30 food audit, on TEST_PG_DSN
// (food_it_test, -p 1). Skipped without it (foodTestStore).

func menuItemIDs(cats []MenuCategory) map[uuid.UUID]bool {
	out := map[uuid.UUID]bool{}
	for _, c := range cats {
		for _, it := range c.Items {
			out[it.ID] = true
		}
	}
	return out
}

// H1: an owner cannot file an item under another restaurant's category, and
// a row already mis-linked that way shows on neither menu of the victim.
func TestAuditH1_MenuItemCategoryMustBeTheRestaurants(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	attacker, attackerRID := seedDraftRestaurant(t, s)
	victim, victimRID := seedDraftRestaurant(t, s)
	victimCat, err := s.CreateMenuCategory(ctx, victim, victimRID, MenuCategoryInput{Name: "Biryani", SortOrder: 1})
	if err != nil {
		t.Fatal(err)
	}
	ownCat, err := s.CreateMenuCategory(ctx, attacker, attackerRID, MenuCategoryInput{Name: "Mains", SortOrder: 1})
	if err != nil {
		t.Fatal(err)
	}
	victimItem, err := s.CreateMenuItem(ctx, victim, victimRID, victimCat.ID, MenuItemInput{Name: "Dum Biryani", FoodType: "NON_VEG", BasePrice: 450})
	if err != nil {
		t.Fatalf("victim's own item: %v", err)
	}

	// The attack: attacker's restaurant, victim's (public) category id.
	_, err = s.CreateMenuItem(ctx, attacker, attackerRID, victimCat.ID, MenuItemInput{Name: "Kitchen closed - order from X", FoodType: "VEG", BasePrice: 1})
	if !errors.Is(err, ErrMenuCategoryNotFound) {
		t.Fatalf("cross-restaurant category: err = %v, want ErrMenuCategoryNotFound", err)
	}
	var n int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM food.menu_items WHERE category_id = $1 AND restaurant_id <> $2`, victimCat.ID, victimRID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d foreign item(s) inserted under the victim's category", n)
	}
	// Nor through the victim's restaurant id: the owner check still applies.
	if _, err := s.CreateMenuItem(ctx, attacker, victimRID, victimCat.ID, MenuItemInput{Name: "x", FoodType: "VEG", BasePrice: 1}); err == nil {
		t.Fatal("a stranger created an item in the victim's restaurant")
	}
	// The attacker's own category still works; a deleted one does not.
	if _, err := s.CreateMenuItem(ctx, attacker, attackerRID, ownCat.ID, MenuItemInput{Name: "Thali", FoodType: "VEG", BasePrice: 150}); err != nil {
		t.Fatalf("own category: %v", err)
	}
	if err := s.DeleteMenuCategory(ctx, attacker, ownCat.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMenuItem(ctx, attacker, attackerRID, ownCat.ID, MenuItemInput{Name: "Late", FoodType: "VEG", BasePrice: 150}); !errors.Is(err, ErrMenuCategoryNotFound) {
		t.Fatalf("deleted own category: err = %v, want ErrMenuCategoryNotFound", err)
	}

	// A row mis-linked before the fix (written straight to the table).
	var planted uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.menu_items (restaurant_id, category_id, name, food_type, base_price)
		VALUES ($1, $2, 'Planted', 'VEG', 1) RETURNING id
	`, attackerRID, victimCat.ID).Scan(&planted); err != nil {
		t.Fatal(err)
	}
	public, err := s.GetMenu(ctx, victimRID)
	if err != nil {
		t.Fatal(err)
	}
	ids := menuItemIDs(public)
	if ids[planted] || !ids[victimItem.ID] {
		t.Fatalf("public menu: planted shown=%v, own item shown=%v", ids[planted], ids[victimItem.ID])
	}
	partner, err := s.ListMenuCategories(ctx, victim, victimRID)
	if err != nil {
		t.Fatal(err)
	}
	ids = menuItemIDs(partner)
	if ids[planted] || !ids[victimItem.ID] {
		t.Fatalf("partner menu: planted shown=%v, own item shown=%v", ids[planted], ids[victimItem.ID])
	}
	for _, c := range partner {
		if c.ID == victimCat.ID && (c.ItemCount == nil || *c.ItemCount != 1) {
			t.Fatalf("partner item_count counts the planted row: %v", c.ItemCount)
		}
	}
}

// M3: the rider is a party only inside the drop-off window; the customer and
// the restaurant owner are parties; an unknown order has no parties.
func TestAuditM3_OrderPartyMembershipRiderWindow(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()

	onWay, _, customer := seedOrderWithItem(t, s, "PICKED_UP")
	owner, _ := b8OrderOwner(t, s, onWay)
	riderUser, riderID := seedDeliveryPartnerWithStatus(t, s, "ACTIVE", true)
	seedDeliveryAssignmentWithStatus(t, s, onWay, &riderID, "PICKED_UP")

	m, err := s.OrderPartyMembership(ctx, onWay, riderUser)
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsDeliveryPartner || m.IsCustomer || m.IsRestaurantOwner {
		t.Fatalf("rider on the way: %+v, want delivery only", m)
	}
	if m, _ := s.OrderPartyMembership(ctx, onWay, customer); !m.IsCustomer {
		t.Fatalf("customer: %+v", m)
	}
	if m, _ := s.OrderPartyMembership(ctx, onWay, owner); !m.IsRestaurantOwner {
		t.Fatalf("owner: %+v", m)
	}

	// The same rider on an order delivered earlier is no longer a party.
	delivered, _, _ := seedOrderWithItem(t, s, "DELIVERED")
	seedDeliveryAssignmentWithStatus(t, s, delivered, &riderID, "DELIVERED")
	if m, _ := s.OrderPartyMembership(ctx, delivered, riderUser); m.IsDeliveryPartner {
		t.Fatal("rider still a chat party after delivery")
	}
	// Nor is a rider merely assigned (not yet accepted).
	assigned, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	seedDeliveryAssignmentWithStatus(t, s, assigned, &riderID, "ASSIGNED")
	if m, _ := s.OrderPartyMembership(ctx, assigned, riderUser); m.IsDeliveryPartner {
		t.Fatal("merely assigned rider is a chat party")
	}
	m, err = s.OrderPartyMembership(ctx, uuid.New(), customer)
	if err != nil || m.IsCustomer || m.IsRestaurantOwner || m.IsDeliveryPartner {
		t.Fatalf("unknown order: %+v, %v", m, err)
	}
}

// M3: a read is stamped only on a message of the named order.
func TestAuditM3_MarkMessageReadIsBoundToTheOrder(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	orderA, _, customerA := seedOrderWithItem(t, s, "PICKED_UP")
	orderB, _, _ := seedOrderWithItem(t, s, "PICKED_UP")
	msg, err := s.AppendOrderMessage(ctx, orderA, customerA, "customer", "gate code 4321")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.OrderMessageOrderID(ctx, msg.ID); err != nil || got != orderA {
		t.Fatalf("OrderMessageOrderID = %s, %v", got, err)
	}
	if _, err := s.OrderMessageOrderID(ctx, uuid.New()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unknown message: %v", err)
	}
	reader := uuid.New()
	if err := s.MarkMessageRead(ctx, orderB, msg.ID, reader, "customer"); err != nil {
		t.Fatal(err)
	}
	readBy := func() []map[string]any {
		var raw []byte
		if err := s.db.QueryRow(ctx, `SELECT read_by FROM food.order_messages WHERE id = $1`, msg.ID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var out []map[string]any
		_ = json.Unmarshal(raw, &out)
		return out
	}
	if got := readBy(); len(got) != 0 {
		t.Fatalf("a read under another order was stamped: %v", got)
	}
	if err := s.MarkMessageRead(ctx, orderA, msg.ID, reader, "restaurant"); err != nil {
		t.Fatal(err)
	}
	if got := readBy(); len(got) != 1 || got[0]["role"] != "restaurant" {
		t.Fatalf("read_by = %v", got)
	}
}

// M6: a reviewed rider's identity is frozen; email stays editable.
func TestAuditM6_ReviewedRiderIdentityIsLocked(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	user := uuid.New()
	in := DeliveryPartnerInput{FullName: "R. Kumar", Phone: "+919100000001", VehicleType: "BIKE", VehicleNumber: "KA01AB1234", City: "Bengaluru"}
	p, err := s.UpsertDeliveryPartner(ctx, user, in)
	if err != nil {
		t.Fatal(err)
	}
	// Before review: editable.
	in.FullName = "Ravi Kumar"
	if _, err := s.UpsertDeliveryPartner(ctx, user, in); err != nil {
		t.Fatalf("PENDING_REVIEW edit refused: %v", err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE food.delivery_partners SET status = 'ACTIVE' WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	swap := in
	swap.FullName, swap.Phone, swap.VehicleNumber = "Someone Else", "+919100000099", "KA09ZZ0001"
	_, err = s.UpsertDeliveryPartner(ctx, user, swap)
	var locked *RiderIdentityLockedError
	if !errors.As(err, &locked) {
		t.Fatalf("ACTIVE rider rewrote identity: err = %v", err)
	}
	got, err := s.GetDeliveryPartner(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if got.FullName != "Ravi Kumar" || got.Phone != in.Phone || got.VehicleNumber != in.VehicleNumber || got.Status != "ACTIVE" {
		t.Fatalf("refused save changed the row: %+v", got)
	}
	in.Email = "ravi@example.test"
	if _, err := s.UpsertDeliveryPartner(ctx, user, in); err != nil {
		t.Fatalf("email edit refused: %v", err)
	}
	if got, _ := s.GetDeliveryPartner(ctx, user); got.Email != "ravi@example.test" {
		t.Fatalf("email not saved: %+v", got)
	}
}

// L5: a generic key is bound to its request; a different request with the
// same key is ErrIdempotencyPayloadMismatch, the same one replays.
func TestAuditL5_GenericIdempotencyKeyIsBoundToTheRequest(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	user := uuid.New()
	key := "it-l5-" + uuid.NewString()
	first := WithRequestFingerprint(ctx, "POST /v1/food/partner/orders/A/status body:preparing")
	second := WithRequestFingerprint(ctx, "POST /v1/food/partner/orders/B/status body:preparing")

	run := func(c context.Context) (map[string]any, bool, error) {
		tx, err := s.db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		body, handled, err := s.lockGenericIdempotency(c, tx, user, key)
		if err != nil {
			return nil, false, err
		}
		if !handled {
			if err := s.completeGenericIdempotency(c, tx, user, key, 200, map[string]any{"order": "A"}); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return body, handled, nil
	}
	if _, handled, err := run(first); err != nil || handled {
		t.Fatalf("first use: handled=%v err=%v", handled, err)
	}
	if body, handled, err := run(first); err != nil || !handled || body["order"] != "A" {
		t.Fatalf("replay: body=%v handled=%v err=%v", body, handled, err)
	}
	if _, _, err := run(second); !errors.Is(err, ErrIdempotencyPayloadMismatch) {
		t.Fatalf("reuse for another order: err = %v, want ErrIdempotencyPayloadMismatch", err)
	}
	// Another caller's identical key is its own row.
	other := uuid.New()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, handled, err := s.lockGenericIdempotency(second, tx, other, key); err != nil || handled {
		t.Fatalf("other caller's key: handled=%v err=%v", handled, err)
	}
}
