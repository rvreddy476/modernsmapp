package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestValidateAddonSelection(t *testing.T) {
	item := uuid.New()
	g := addonGroupRule{ID: uuid.New(), Name: "Extras", MinSelect: 0, MaxSelect: 2}
	r := addonGroupRule{ID: uuid.New(), Name: "Size", MinSelect: 0, MaxSelect: 1, Required: true}
	groups := []addonGroupRule{g, r}
	mk := func(group uuid.UUID, owner uuid.UUID, available bool) addonChoice {
		return addonChoice{AddonID: uuid.New(), GroupID: group, MenuItemID: owner, Available: available}
	}
	a1, a2, a3 := mk(g.ID, item, true), mk(g.ID, item, true), mk(g.ID, item, true)
	r1 := mk(r.ID, item, true)
	foreign := mk(uuid.New(), uuid.New(), true)
	unavailable := mk(g.ID, item, false)
	found := map[uuid.UUID]addonChoice{}
	for _, c := range []addonChoice{a1, a2, a3, r1, foreign, unavailable} {
		found[c.AddonID] = c
	}
	sel := func(cs ...addonChoice) []CartAddonInput {
		out := []CartAddonInput{}
		for _, c := range cs {
			out = append(out, CartAddonInput{AddonID: c.AddonID, Quantity: 1})
		}
		return out
	}
	cases := []struct {
		name string
		req  []CartAddonInput
		ok   bool
	}{
		{"required + optional", sel(r1, a1), true},
		{"required only", sel(r1), true},
		{"missing required group", sel(a1), false},
		{"over group max", sel(r1, a1, a2, a3), false},
		{"foreign add-on", sel(r1, foreign), false},
		{"unavailable add-on", sel(r1, unavailable), false},
		{"unknown add-on", append(sel(r1), CartAddonInput{AddonID: uuid.New(), Quantity: 1}), false},
		{"duplicate add-on", sel(r1, a1, a1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAddonSelection(item, groups, found, tc.req)
			if tc.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrAddonInvalid) {
				t.Fatalf("want ErrAddonInvalid, got %v", err)
			}
		})
	}
}

func seedAddon(t *testing.T, s *Store, menuItemID uuid.UUID, minSel, maxSel int, required bool, name string, price float64) (groupID, addonID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.menu_item_addon_groups (menu_item_id, name, min_select, max_select, is_required)
		VALUES ($1, 'Extras', $2, $3, $4) RETURNING id
	`, menuItemID, minSel, maxSel, required).Scan(&groupID); err != nil {
		t.Fatalf("seed addon group: %v", err)
	}
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.menu_item_addons (addon_group_id, name, price) VALUES ($1, $2, $3) RETURNING id
	`, groupID, name, price).Scan(&addonID); err != nil {
		t.Fatalf("seed addon: %v", err)
	}
	return groupID, addonID
}

func TestCartAndOrder_AddonPricingAndSnapshots(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	lat, lng := 12.9, 77.6

	customer, _, _, item := seedPlaceableCart(t, s, f64(lat), f64(lng))
	_, cheese := seedAddon(t, s, item, 0, 2, false, "Extra cheese", 30)
	if err := s.ClearCart(ctx, customer); err != nil {
		t.Fatal(err)
	}
	cart, err := s.AddCartItem(ctx, customer, AddCartItemInput{
		MenuItemID: item, Quantity: 2, Addons: []CartAddonInput{{AddonID: cheese, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("add with add-on: %v", err)
	}
	// 2 x 250 = 500 base; cheese 30 x 1 x 2 = 60; tax 5% of 560 = 28;
	// delivery 29 + platform 5 + packaging 0 -> 622.
	tot := cart.Totals
	if tot.ItemSubtotal != 500 || tot.AddonTotal != 60 || tot.TaxTotal != 28 || tot.FinalAmount != 622 {
		t.Fatalf("cart totals = %+v, want subtotal 500 addon 60 tax 28 final 622", tot)
	}
	if len(cart.Items) != 1 || len(cart.Items[0].Addons) != 1 || cart.Items[0].AddonTotal != 60 {
		t.Fatalf("cart item add-ons not loaded: %+v", cart.Items)
	}

	addr := seedAddress(t, s, customer, f64(kmNorth(lat, 1)), f64(lng))
	order, err := s.PlaceOrder(ctx, customer, PlaceOrderInput{AddressID: addr, PaymentMethod: "COD"}, uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	if order.Totals.AddonTotal != 60 || order.Totals.FinalAmount != 622 {
		t.Fatalf("order totals = %+v", order.Totals)
	}
	var name string
	var unit, line float64
	var qty int
	if err := s.db.QueryRow(ctx, `
		SELECT oia.addon_name_snapshot, oia.unit_price_snapshot::float8, oia.quantity, oia.line_total::float8
		FROM food.order_item_addons oia
		JOIN food.order_items oi ON oi.id = oia.order_item_id
		WHERE oi.order_id = $1
	`, order.ID).Scan(&name, &unit, &qty, &line); err != nil {
		t.Fatalf("read add-on snapshot: %v", err)
	}
	if name != "Extra cheese" || unit != 30 || qty != 2 || line != 60 {
		t.Fatalf("snapshot = %s %v x%d = %v", name, unit, qty, line)
	}
}

func TestAddCartItem_RefusesForeignAddon(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	customer, _, _, item := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	_, _, _, otherItem := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	_, foreign := seedAddon(t, s, otherItem, 0, 2, false, "Someone else's sauce", 10)
	_, err := s.AddCartItem(ctx, customer, AddCartItemInput{
		MenuItemID: item, Quantity: 1, Addons: []CartAddonInput{{AddonID: foreign, Quantity: 1}},
	})
	if !errors.Is(err, ErrAddonInvalid) {
		t.Fatalf("want ErrAddonInvalid, got %v", err)
	}
	cart, err := s.GetCart(ctx, customer)
	if err != nil {
		t.Fatal(err)
	}
	if len(cart.Items) != 1 || cart.Totals.AddonTotal != 0 {
		t.Fatalf("refused add changed the cart: %+v", cart)
	}
}

func TestAddCartItem_RequiredGroupEnforced(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	customer, _, _, item := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	_, size := seedAddon(t, s, item, 1, 1, true, "Large", 40)
	if _, err := s.AddCartItem(ctx, customer, AddCartItemInput{MenuItemID: item, Quantity: 1}); !errors.Is(err, ErrAddonInvalid) {
		t.Fatalf("want ErrAddonInvalid without the required choice, got %v", err)
	}
	if _, err := s.AddCartItem(ctx, customer, AddCartItemInput{
		MenuItemID: item, Quantity: 1, Addons: []CartAddonInput{{AddonID: size}},
	}); err != nil {
		t.Fatalf("with required choice: %v", err)
	}
}
