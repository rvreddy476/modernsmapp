package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/onboarding"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Lane B8 on TEST_PG_DSN (food_it_test, -p 1).

func b8Item(t *testing.T, s *Store, ownerID, restaurantID uuid.UUID, name string) (categoryID, itemID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	cat, err := s.CreateMenuCategory(ctx, ownerID, restaurantID, MenuCategoryInput{Name: "Cat " + name, SortOrder: 1})
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	item, err := s.CreateMenuItem(ctx, ownerID, restaurantID, cat.ID, MenuItemInput{
		Name: name, FoodType: "VEG", BasePrice: 120.5, PreparationMinutes: 10, TaxPercentage: 5,
	})
	if err != nil {
		t.Fatalf("item: %v", err)
	}
	return cat.ID, item.ID
}

func b8OrderOwner(t *testing.T, s *Store, orderID uuid.UUID) (ownerID, restaurantID uuid.UUID) {
	t.Helper()
	if err := s.db.QueryRow(context.Background(), `
		SELECT r.owner_user_id, r.id FROM food.orders o JOIN food.restaurants r ON r.id = o.restaurant_id WHERE o.id = $1
	`, orderID).Scan(&ownerID, &restaurantID); err != nil {
		t.Fatal(err)
	}
	return ownerID, restaurantID
}

func strPtr(v string) *string { return &v }

func deref(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// A stranger reads none of another owner's onboarding forms, restaurant,
// order, menu item or extras; the owner reads all of them.
func TestB8ReadsAreOwnerOnly(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	ownerID, rid := seedDraftRestaurant(t, s)
	if _, err := s.SetRestaurantCompliance(ctx, ownerID, rid, testSealedCompliance()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRestaurantLocation(ctx, ownerID, rid, testLocation()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceOperatingHours(ctx, ownerID, rid, []OperatingHoursInput{{DayOfWeek: dayPtr(1), OpensAt: "10:00", ClosesAt: "22:00"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitRestaurantFSSAI(ctx, ownerID, rid, testFSSAI(time.Now().AddDate(1, 0, 0))); err != nil {
		t.Fatal(err)
	}
	_, itemID := b8Item(t, s, ownerID, rid, "Idli")
	orderID, _, _ := seedOrderWithItem(t, s, "CONFIRMED")
	orderOwner, _ := b8OrderOwner(t, s, orderID)
	stranger := uuid.New()

	checks := []struct {
		name  string
		owner uuid.UUID
		call  func(uuid.UUID) error
	}{
		{"readiness", ownerID, func(u uuid.UUID) error { _, err := s.GetRestaurantReadiness(ctx, u, rid); return err }},
		{"compliance", ownerID, func(u uuid.UUID) error { _, err := s.GetRestaurantCompliance(ctx, u, rid); return err }},
		{"location", ownerID, func(u uuid.UUID) error { _, err := s.GetRestaurantLocation(ctx, u, rid); return err }},
		{"operating hours", ownerID, func(u uuid.UUID) error { _, err := s.GetOperatingHours(ctx, u, rid); return err }},
		{"fssai", ownerID, func(u uuid.UUID) error { _, err := s.GetRestaurantFSSAI(ctx, u, rid); return err }},
		{"restaurant", ownerID, func(u uuid.UUID) error { _, err := s.GetPartnerRestaurant(ctx, u, rid); return err }},
		{"order", orderOwner, func(u uuid.UUID) error { _, err := s.GetPartnerOrder(ctx, u, orderID); return err }},
		{"menu item", ownerID, func(u uuid.UUID) error { _, err := s.GetPartnerMenuItem(ctx, u, itemID); return err }},
		{"variants", ownerID, func(u uuid.UUID) error { _, err := s.ListMenuVariants(ctx, u, itemID); return err }},
		{"addon groups", ownerID, func(u uuid.UUID) error { _, err := s.ListAddonGroups(ctx, u, itemID); return err }},
		{"categories", ownerID, func(u uuid.UUID) error { _, err := s.ListMenuCategories(ctx, u, rid); return err }},
	}
	for _, c := range checks {
		if err := c.call(stranger); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("a stranger read %s: err = %v, want pgx.ErrNoRows", c.name, err)
		}
		if err := c.call(c.owner); err != nil {
			t.Errorf("the owner could not read %s: %v", c.name, err)
		}
	}

	comp, err := s.GetRestaurantCompliance(ctx, ownerID, rid)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(comp)
	if comp.PANMasked != "****000Z" || strings.Contains(string(raw), "opaque-sealed-blob") || strings.Contains(string(raw), "lookup-hash") {
		t.Fatalf("compliance read-back is not masked-only: %s", raw)
	}
}

// Before a step is saved its read-back says so; afterwards it returns what the
// PUT returned.
func TestB8ReadBacksReturnWhatWasSaved(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	ownerID, rid := seedDraftRestaurant(t, s)

	notSaved := map[string]func() error{
		onboarding.StepCompliance:     func() error { _, err := s.GetRestaurantCompliance(ctx, ownerID, rid); return err },
		onboarding.StepLocation:       func() error { _, err := s.GetRestaurantLocation(ctx, ownerID, rid); return err },
		onboarding.StepOperatingHours: func() error { _, err := s.GetOperatingHours(ctx, ownerID, rid); return err },
		onboarding.StepFSSAI:          func() error { _, err := s.GetRestaurantFSSAI(ctx, ownerID, rid); return err },
	}
	for step, call := range notSaved {
		var nse *StepNotSavedError
		if err := call(); !errors.As(err, &nse) || nse.Step != step {
			t.Errorf("%s before saving: %v", step, err)
		}
	}

	putLoc, err := s.SetRestaurantLocation(ctx, ownerID, rid, testLocation())
	if err != nil {
		t.Fatal(err)
	}
	gotLoc, err := s.GetRestaurantLocation(ctx, ownerID, rid)
	if err != nil || !reflect.DeepEqual(*gotLoc, *putLoc) {
		t.Fatalf("location read-back %+v (err %v), want %+v", gotLoc, err, putLoc)
	}

	putHours, err := s.ReplaceOperatingHours(ctx, ownerID, rid, []OperatingHoursInput{
		{DayOfWeek: dayPtr(5), OpensAt: "18:00", ClosesAt: "02:00"},
		{DayOfWeek: dayPtr(1), OpensAt: "18:00", ClosesAt: "23:00"},
		{DayOfWeek: dayPtr(1), OpensAt: "11:00", ClosesAt: "15:00"},
		{DayOfWeek: dayPtr(0), IsClosed: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	gotHours, err := s.GetOperatingHours(ctx, ownerID, rid)
	if err != nil || !reflect.DeepEqual(gotHours.Windows, putHours.Windows) || gotHours.Timezone != putHours.Timezone {
		t.Fatalf("hours read-back %+v (err %v), want %+v", gotHours, err, putHours)
	}

	putFSSAI, err := s.SubmitRestaurantFSSAI(ctx, ownerID, rid, testFSSAI(time.Now().AddDate(1, 0, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdminDecideRestaurantDocument(ctx, uuid.New(), rid, putFSSAI.Document.ID, "REJECTED", "Licence photo is unreadable"); err != nil {
		t.Fatal(err)
	}
	gotFSSAI, err := s.GetRestaurantFSSAI(ctx, ownerID, rid)
	if err != nil {
		t.Fatal(err)
	}
	if gotFSSAI.LicenceNumber != putFSSAI.LicenceNumber || deref(gotFSSAI.ExpiresAt) != putFSSAI.ExpiresAt ||
		deref(gotFSSAI.DocumentStatus) != "REJECTED" || deref(gotFSSAI.ReviewReason) != "Licence photo is unreadable" ||
		gotFSSAI.Document == nil || gotFSSAI.Document.ID != putFSSAI.Document.ID {
		t.Fatalf("fssai read-back %+v", gotFSSAI)
	}

	if _, err := s.SetRestaurantCompliance(ctx, ownerID, rid, testSealedCompliance()); err != nil {
		t.Fatal(err)
	}
	comp, err := s.GetRestaurantCompliance(ctx, ownerID, rid)
	if err != nil || comp.TaxCategory != "RESTAURANT_STANDALONE" || comp.LegalName != "Test Kitchens LLP" || comp.ComplianceSubmittedAt == "" {
		t.Fatalf("compliance read-back %+v (err %v)", comp, err)
	}
}

// GET .../readiness and POST .../submit report the same missing steps at every
// stage, because both call restaurantMissingSteps.
func TestB8ReadinessAgreesWithSubmit(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	ownerID, rid := seedDraftRestaurant(t, s)

	agree := func(stage string) *RestaurantReadiness {
		t.Helper()
		r, err := s.GetRestaurantReadiness(ctx, ownerID, rid)
		if err != nil {
			t.Fatalf("%s: readiness: %v", stage, err)
		}
		_, submitErr := s.SubmitRestaurantForReview(ctx, ownerID, rid)
		var notReady *onboarding.NotReadyError
		switch {
		case len(r.Missing) == 0:
			if submitErr != nil || !r.Ready || !r.CanSubmit {
				t.Fatalf("%s: readiness says ready (%+v) but submit said %v", stage, r, submitErr)
			}
		case errors.As(submitErr, &notReady):
			if !reflect.DeepEqual(notReady.Missing, r.Missing) || r.Ready || r.CanSubmit {
				t.Fatalf("%s: readiness missing %v, submit missing %v", stage, r.Missing, notReady.Missing)
			}
		default:
			t.Fatalf("%s: readiness missing %v, but submit returned %v", stage, r.Missing, submitErr)
		}
		return r
	}

	fresh := agree("fresh")
	all := []string{onboarding.StepLocation, onboarding.StepState, onboarding.StepOperatingHours, onboarding.StepCompliance,
		onboarding.StepFSSAI, onboarding.StepPayoutAccount, onboarding.StepMenuItem}
	if !reflect.DeepEqual(fresh.Missing, all) || fresh.Status != "DRAFT" {
		t.Fatalf("fresh readiness = %+v, want every step", fresh)
	}
	steps := []struct {
		name string
		do   func() error
	}{
		{"location", func() error { _, err := s.SetRestaurantLocation(ctx, ownerID, rid, testLocation()); return err }},
		{"hours", func() error {
			_, err := s.ReplaceOperatingHours(ctx, ownerID, rid, []OperatingHoursInput{{DayOfWeek: dayPtr(1), OpensAt: "10:00", ClosesAt: "22:00"}})
			return err
		}},
		{"compliance", func() error { _, err := s.SetRestaurantCompliance(ctx, ownerID, rid, testSealedCompliance()); return err }},
		{"fssai", func() error {
			_, err := s.SubmitRestaurantFSSAI(ctx, ownerID, rid, testFSSAI(time.Now().AddDate(1, 0, 0)))
			return err
		}},
		{"payout", func() error { _, err := s.UpsertRestaurantPayoutAccount(ctx, ownerID, rid, testSealedPayout()); return err }},
		{"menu item", func() error { b8Item(t, s, ownerID, rid, "Dosa"); return nil }},
	}
	for _, st := range steps {
		if err := st.do(); err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		agree("after " + st.name)
	}
	after, err := s.GetRestaurantReadiness(ctx, ownerID, rid)
	if err != nil || !after.Ready || after.CanSubmit || after.Status != "PENDING_REVIEW" || len(after.Missing) != 0 {
		t.Fatalf("after submit: %+v (err %v)", after, err)
	}
}

// The profile PATCH writes only what it names; null clears only clearable
// fields.
func TestB8PatchRestaurantKeepsAbsentFields(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	ownerID := uuid.New()
	created, err := s.CreatePartnerRestaurant(ctx, ownerID, PartnerRestaurantInput{
		Name: "Patch " + ownerID.String()[:8], Slug: "patch-" + ownerID.String(), Description: "Old description",
		Phone: "+919000000001", Email: "old@example.test", AddressLine1: "1 Test Lane", City: "Bengaluru",
		LegalName: "Patch Legal LLP", DisplayName: "Patch Display", MinOrderAmount: 99, PackagingFee: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	rid := created.ID
	patch := func(in PartnerRestaurantInput, keys ...string) (*PartnerRestaurant, error) {
		in.Present, in.Null = map[string]bool{}, map[string]bool{}
		for _, k := range keys {
			if strings.HasPrefix(k, "null:") {
				k = strings.TrimPrefix(k, "null:")
				in.Null[k] = true
			}
			in.Present[k] = true
		}
		return s.UpdatePartnerRestaurant(ctx, ownerID, rid, in)
	}

	got, err := patch(PartnerRestaurantInput{Description: "New description"}, "description")
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "New description" || got.Name != created.Name || got.Slug != created.Slug ||
		deref(got.Phone) != "+919000000001" || deref(got.Email) != "old@example.test" ||
		got.MinOrderAmount != 99 || got.PackagingFee != 10 ||
		deref(got.LegalName) != "Patch Legal LLP" || deref(got.DisplayName) != "Patch Display" {
		t.Fatalf("a description-only PATCH changed other fields: %+v", got)
	}

	got, err = patch(PartnerRestaurantInput{}, "null:phone")
	if err != nil {
		t.Fatal(err)
	}
	if got.Phone != nil || deref(got.Email) != "old@example.test" || got.Description != "New description" {
		t.Fatalf("clearing phone: %+v", got)
	}

	got, err = patch(PartnerRestaurantInput{LegalName: "New Legal LLP"}, "legal_name", "null:display_name")
	if err != nil {
		t.Fatal(err)
	}
	if deref(got.LegalName) != "New Legal LLP" || got.DisplayName != nil || got.Name != created.Name {
		t.Fatalf("partner fields: %+v", got)
	}

	for _, key := range []string{"name", "legal_name", "min_order_amount", "packaging_fee"} {
		if _, err := patch(PartnerRestaurantInput{}, "null:"+key); err == nil {
			t.Errorf("null %s was accepted", key)
		}
	}
	got, err = patch(PartnerRestaurantInput{})
	if err != nil || deref(got.LegalName) != "New Legal LLP" || got.PackagingFee != 10 || got.Name != created.Name {
		t.Fatalf("empty PATCH: %+v (err %v)", got, err)
	}
}

// Variants, add-on groups and add-ons are managed only through an item the
// caller owns; nothing a stranger sends sticks.
func TestB8MenuExtrasAreOwnerOnly(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	ownerID, rid := seedDraftRestaurant(t, s)
	_, itemID := b8Item(t, s, ownerID, rid, "Thali")
	stranger, strangerRid := seedDraftRestaurant(t, s)
	_, strangerItem := b8Item(t, s, stranger, strangerRid, "Other")

	if _, err := s.CreateMenuVariant(ctx, stranger, itemID, MenuPriceInput{Name: strPtr("Half"), PricePaise: i64(1)}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("a stranger created a variant: %v", err)
	}
	if _, err := s.CreateAddonGroup(ctx, stranger, itemID, MenuAddonGroupInput{Name: strPtr("Extras")}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("a stranger created an add-on group: %v", err)
	}

	v, err := s.CreateMenuVariant(ctx, ownerID, itemID, MenuPriceInput{Name: strPtr("Half"), Price: f64(120)})
	if err != nil || v.PricePaise != 12000 || v.Price != 120 {
		t.Fatalf("owner variant: %+v %v", v, err)
	}
	g, err := s.CreateAddonGroup(ctx, ownerID, itemID, MenuAddonGroupInput{Name: strPtr("Extras"), MaxSelect: intPtr(2)})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAddon(ctx, ownerID, itemID, g.ID, MenuPriceInput{Name: strPtr("Cheese"), PricePaise: i64(3000)})
	if err != nil || a.Price != 30 {
		t.Fatalf("owner add-on: %+v %v", a, err)
	}

	refused := map[string]func() error{
		"update variant":              func() error { _, err := s.UpdateMenuVariant(ctx, stranger, itemID, v.ID, MenuPriceInput{PricePaise: i64(1)}); return err },
		"update variant via own item": func() error { _, err := s.UpdateMenuVariant(ctx, stranger, strangerItem, v.ID, MenuPriceInput{PricePaise: i64(1)}); return err },
		"delete variant":              func() error { return s.DeleteMenuVariant(ctx, stranger, itemID, v.ID) },
		"update group":                func() error { _, err := s.UpdateAddonGroup(ctx, stranger, itemID, g.ID, MenuAddonGroupInput{MaxSelect: intPtr(9)}); return err },
		"delete group":                func() error { return s.DeleteAddonGroup(ctx, stranger, itemID, g.ID) },
		"create add-on":               func() error { _, err := s.CreateAddon(ctx, stranger, itemID, g.ID, MenuPriceInput{Name: strPtr("X"), PricePaise: i64(1)}); return err },
		"create add-on via own item":  func() error { _, err := s.CreateAddon(ctx, stranger, strangerItem, g.ID, MenuPriceInput{Name: strPtr("X"), PricePaise: i64(1)}); return err },
		"update add-on":               func() error { _, err := s.UpdateAddon(ctx, stranger, itemID, g.ID, a.ID, MenuPriceInput{PricePaise: i64(1)}); return err },
		"update add-on via own item":  func() error { _, err := s.UpdateAddon(ctx, stranger, strangerItem, g.ID, a.ID, MenuPriceInput{PricePaise: i64(1)}); return err },
		"delete add-on":               func() error { return s.DeleteAddon(ctx, stranger, itemID, g.ID, a.ID) },
	}
	for name, call := range refused {
		if err := call(); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("stranger %s: err = %v, want pgx.ErrNoRows", name, err)
		}
	}

	item, err := s.GetPartnerMenuItem(ctx, ownerID, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Variants) != 1 || item.Variants[0].PricePaise != 12000 || len(item.AddonGroups) != 1 ||
		item.AddonGroups[0].MaxSelect != 2 || len(item.AddonGroups[0].Addons) != 1 || item.AddonGroups[0].Addons[0].PricePaise != 3000 {
		t.Fatalf("the owner's extras changed: %+v", item)
	}
	var strangerRows int
	if err := s.db.QueryRow(ctx, `
		SELECT (SELECT COUNT(*) FROM food.menu_item_variants WHERE menu_item_id = $1)
			+ (SELECT COUNT(*) FROM food.menu_item_addon_groups WHERE menu_item_id = $1)::int
	`, strangerItem).Scan(&strangerRows); err != nil || strangerRows != 0 {
		t.Fatalf("rows landed on the stranger's item: %d (%v)", strangerRows, err)
	}

	// The owner's partial updates keep what they do not name.
	v2, err := s.UpdateMenuVariant(ctx, ownerID, itemID, v.ID, MenuPriceInput{PricePaise: i64(15050)})
	if err != nil || v2.Name != "Half" || v2.Price != 150.5 || v2.PricePaise != 15050 {
		t.Fatalf("variant partial update: %+v %v", v2, err)
	}
	if _, err := s.UpdateAddonGroup(ctx, ownerID, itemID, g.ID, MenuAddonGroupInput{MinSelect: intPtr(3)}); fieldCodeOf(err) != CodeAddonGroupSelectInvalid {
		t.Fatalf("min_select above max_select: %v", err)
	}
}

func intPtr(v int) *int { return &v }

// Deleting an add-on (or its group) that a cart still holds takes it out of
// the cart instead of failing on cart_item_addons' RESTRICT.
func TestB8DeletingAnAddonTakesItOutOfCarts(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	customerID, _, ownerID, itemID := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	g, err := s.CreateAddonGroup(ctx, ownerID, itemID, MenuAddonGroupInput{Name: strPtr("Extras"), MaxSelect: intPtr(2)})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAddon(ctx, ownerID, itemID, g.ID, MenuPriceInput{Name: strPtr("Cheese"), PricePaise: i64(3000)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateAddon(ctx, ownerID, itemID, g.ID, MenuPriceInput{Name: strPtr("Olives"), PricePaise: i64(2000)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCartItem(ctx, customerID, AddCartItemInput{MenuItemID: itemID, Quantity: 1,
		Addons: []CartAddonInput{{AddonID: a.ID, Quantity: 1}, {AddonID: b.ID, Quantity: 1}}}); err != nil {
		t.Fatalf("add to cart: %v", err)
	}
	count := func(where string, arg uuid.UUID) int {
		var n int
		if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM food.cart_item_addons WHERE `+where, arg).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count("addon_id = $1", a.ID) != 1 {
		t.Fatal("the cart does not hold the add-on")
	}
	if err := s.DeleteAddon(ctx, ownerID, itemID, g.ID, a.ID); err != nil {
		t.Fatalf("delete add-on in a cart: %v", err)
	}
	if count("addon_id = $1", a.ID) != 0 {
		t.Fatal("the deleted add-on is still in the cart")
	}
	if err := s.DeleteAddonGroup(ctx, ownerID, itemID, g.ID); err != nil {
		t.Fatalf("delete group in a cart: %v", err)
	}
	if count("addon_id = $1", b.ID) != 0 {
		t.Fatal("the deleted group's add-on is still in the cart")
	}
}

// A new category with no items is listed on the partner menu, with its count.
func TestB8EmptyCategoryIsListed(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	ownerID, rid := seedDraftRestaurant(t, s)
	empty, err := s.CreateMenuCategory(ctx, ownerID, rid, MenuCategoryInput{Name: "Desserts", SortOrder: 2})
	if err != nil {
		t.Fatal(err)
	}
	mains, err := s.CreateMenuCategory(ctx, ownerID, rid, MenuCategoryInput{Name: "Mains", SortOrder: 1})
	if err != nil {
		t.Fatal(err)
	}
	var deleted uuid.UUID
	for _, name := range []string{"Dal", "Rice", "Gone"} {
		item, err := s.CreateMenuItem(ctx, ownerID, rid, mains.ID, MenuItemInput{Name: name, FoodType: "VEG", BasePrice: 99.99, TaxPercentage: 5})
		if err != nil {
			t.Fatal(err)
		}
		deleted = item.ID
	}
	if err := s.DeleteMenuItem(ctx, ownerID, deleted); err != nil {
		t.Fatal(err)
	}

	cats, err := s.ListMenuCategories(ctx, ownerID, rid)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[uuid.UUID]MenuCategory{}
	for _, c := range cats {
		byID[c.ID] = c
	}
	e, ok := byID[empty.ID]
	if !ok || e.ItemCount == nil || *e.ItemCount != 0 || e.Items == nil || len(e.Items) != 0 {
		t.Fatalf("the empty category is not listed with item_count 0: %+v (all %+v)", e, cats)
	}
	m := byID[mains.ID]
	if m.ItemCount == nil || *m.ItemCount != 2 || len(m.Items) != 2 || m.Items[0].BasePricePaise != 9999 {
		t.Fatalf("mains = %+v", m)
	}
	if len(cats) != 2 || cats[0].ID != mains.ID {
		t.Fatalf("categories not in sort order: %+v", cats)
	}

	customerMenu, err := s.GetMenu(ctx, rid)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range customerMenu {
		if c.ID == empty.ID || c.ItemCount != nil {
			t.Fatalf("the customer menu changed: %+v", customerMenu)
		}
	}
}

// The partner order read is the restaurant owner's only, and never carries
// the customer's drop-off code.
func TestB8PartnerOrderIsOwnerOnlyAndHidesTheDeliveryCode(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	orderID, _, customerID := seedOrderWithItem(t, s, "OUT_FOR_DELIVERY")
	_, partnerID := seedDeliveryPartner(t, s)
	seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "PICKED_UP")
	if _, err := s.db.Exec(ctx, `UPDATE food.delivery_assignments SET delivery_code = '4321' WHERE order_id = $1`, orderID); err != nil {
		t.Fatal(err)
	}
	ownerID, _ := b8OrderOwner(t, s, orderID)

	customerView, err := s.GetOrder(ctx, customerID, orderID)
	if err != nil || customerView.DeliveryCode != "4321" {
		t.Fatalf("precondition: the customer sees the code: %+v %v", customerView, err)
	}
	got, err := s.GetPartnerOrder(ctx, ownerID, orderID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DeliveryCode != "" || got.Status != "OUT_FOR_DELIVERY" || len(got.Items) != 1 || got.Money == nil && got.Totals.FinalAmount != 250 {
		t.Fatalf("partner order = %+v", got)
	}
	for _, u := range []uuid.UUID{uuid.New(), customerID} {
		if _, err := s.GetPartnerOrder(ctx, u, orderID); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("non-owner %s read the partner order: %v", u, err)
		}
	}
}

// Kitchen queue rows carry final_amount_paise and an RFC 3339 deadline next to
// the unchanged Postgres-text one.
func TestB8KitchenQueueCarriesPaiseAndRFC3339(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	orderID, _, _ := seedOrderWithItem(t, s, "CONFIRMED")
	ownerID, rid := b8OrderOwner(t, s, orderID)
	var deadline time.Time
	var deadlineText string
	if err := s.db.QueryRow(ctx, `
		UPDATE food.orders SET accept_deadline_at = date_trunc('second', NOW()) + INTERVAL '5 minutes'
		WHERE id = $1
		RETURNING accept_deadline_at, accept_deadline_at::text
	`, orderID).Scan(&deadline, &deadlineText); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListKitchenQueue(ctx, ownerID, rid)
	if err != nil || len(rows) != 1 {
		t.Fatalf("queue = %+v (err %v)", rows, err)
	}
	k := rows[0]
	if k.FinalAmountPaise != 25000 || k.FinalAmount != 250 {
		t.Fatalf("final amount %v / %d paise", k.FinalAmount, k.FinalAmountPaise)
	}
	if k.AcceptDeadlineAt == nil || *k.AcceptDeadlineAt != deadlineText {
		t.Fatalf("accept_deadline_at changed: %v, want %q", deref(k.AcceptDeadlineAt), deadlineText)
	}
	parsed, err := time.Parse(time.RFC3339, deref(k.AcceptDeadlineAtRFC3339))
	if err != nil || !parsed.Equal(deadline) {
		t.Fatalf("accept_deadline_at_rfc3339 = %v (err %v), want %v", deref(k.AcceptDeadlineAtRFC3339), err, deadline)
	}
}

// Before a rider accepts, the customer's order detail moves a stale ETA
// forward, at most once per claim interval; once a rider has accepted, only
// the rider pings do.
func TestB8OrderDetailRefreshesAPreAcceptETA(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	past := time.Now().Add(-5 * time.Minute).UTC().Truncate(time.Second)
	stale := func(orderID uuid.UUID) uuid.UUID {
		t.Helper()
		var customerID uuid.UUID
		if err := s.db.QueryRow(ctx, `
			UPDATE food.orders
			SET eta_at = $2, eta_source = 'haversine', eta_computed_at = NOW() - INTERVAL '61 seconds'
			WHERE id = $1
			RETURNING user_id
		`, orderID, past).Scan(&customerID); err != nil {
			t.Fatal(err)
		}
		return customerID
	}

	// Confirmed 30 minutes ago with 20 minutes of prep: the food was ready 10
	// minutes ago, so pickup is now and the ETA is now plus the ~5 km ride.
	confirmed := seedTrackableOrder(t, s, "CONFIRMED", 30*time.Minute)
	customerID := stale(confirmed)
	before := time.Now()
	order, err := s.GetOrder(ctx, customerID, confirmed)
	if err != nil {
		t.Fatal(err)
	}
	eta, err := time.Parse(time.RFC3339, order.ETAAt)
	if err != nil || !eta.After(before.Add(5*time.Minute)) || eta.After(before.Add(40*time.Minute)) || order.ETASource != "haversine" {
		t.Fatalf("refreshed eta_at %q (%v) source %q, want now + the ride", order.ETAAt, err, order.ETASource)
	}

	// Inside the claim interval a second read does not recompute.
	if _, err := s.db.Exec(ctx, `UPDATE food.orders SET eta_at = $2 WHERE id = $1`, confirmed, past); err != nil {
		t.Fatal(err)
	}
	again, err := s.GetOrder(ctx, customerID, confirmed)
	if err != nil || again.ETAAt != FormatETA(past) {
		t.Fatalf("second read inside the interval recomputed: %q (err %v)", again.ETAAt, err)
	}

	// A rider has accepted: the detail read leaves the ETA to the pings.
	assigned := seedTrackableOrder(t, s, "DELIVERY_ASSIGNED", 30*time.Minute)
	_, partnerID := seedDeliveryPartner(t, s)
	seedDeliveryAssignmentWithStatus(t, s, assigned, &partnerID, "ACCEPTED")
	assignedCustomer := stale(assigned)
	got, err := s.GetOrder(ctx, assignedCustomer, assigned)
	if err != nil || got.ETAAt != FormatETA(past) {
		t.Fatalf("an accepted order's detail recomputed the eta: %q (err %v)", got.ETAAt, err)
	}
}

// image_media_id is stored with the item and read back; a later update without
// one replaces it, as the full-replace item update always did for image_url.
func TestB8MenuItemImageMediaIDPersists(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	ownerID, rid := seedDraftRestaurant(t, s)
	cat, err := s.CreateMenuCategory(ctx, ownerID, rid, MenuCategoryInput{Name: "Mains"})
	if err != nil {
		t.Fatal(err)
	}
	media := uuid.New()
	url := "/v1/media/" + media.String() + "/serve"
	item, err := s.CreateMenuItem(ctx, ownerID, rid, cat.ID, MenuItemInput{Name: "Idli", FoodType: "VEG", BasePrice: 99.99, ImageURL: url, ImageMediaID: &media})
	if err != nil || item.ImageMediaID == nil || *item.ImageMediaID != media || item.ImageURL != url || item.BasePricePaise != 9999 {
		t.Fatalf("create = %+v (err %v)", item, err)
	}
	got, err := s.GetPartnerMenuItem(ctx, ownerID, item.ID)
	if err != nil || got.ImageMediaID == nil || *got.ImageMediaID != media || got.ImageURL != url || got.CategoryID != cat.ID {
		t.Fatalf("read = %+v (err %v)", got, err)
	}
	updated, err := s.UpdateMenuItem(ctx, ownerID, item.ID, MenuItemInput{Name: "Idli", BasePrice: 99.99, ImageURL: "https://cdn.example.test/idli.jpg"})
	if err != nil || updated.ImageMediaID != nil || updated.ImageURL != "https://cdn.example.test/idli.jpg" {
		t.Fatalf("update = %+v (err %v)", updated, err)
	}
}
