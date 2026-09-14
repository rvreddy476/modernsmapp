package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Rider navigation over the routes, on TEST_PG_DSN (food_it_test, -p 1).

const itRnDropSnapshot = `{"receiver_name":"Asha Rao","phone":"+919812345678","address_line1":"42 Lake View Road",` +
	`"address_line2":"Flat 3B","landmark":"Opp. City Park","city":"Bengaluru","postal_code":"560038","latitude":12.978449,"longitude":77.640812}`

// A rider reads their own accepted job with the drop-off; another rider gets
// the same 404 as before and not a byte of the address.
func TestForeignRiderCannotReadAnotherRidersJobOverRoutes(t *testing.T) {
	r, pool := b3IntegrationRouter(t)
	ctx := context.Background()
	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	owner, customer, riderUser, strangerUser := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	var partnerID, restaurantID, orderID, riderRow, assignmentID uuid.UUID
	must(pool.QueryRow(ctx, `
		INSERT INTO food.restaurant_partners (owner_user_id, legal_name, status)
		VALUES ($1, 'Test Partner', 'APPROVED') RETURNING id`, owner).Scan(&partnerID), "seed partner")
	must(pool.QueryRow(ctx, `
		INSERT INTO food.restaurants
			(partner_id, name, slug, owner_user_id, status, is_open, is_accepting_orders,
			 address_line1, city, state, tax_category, latitude, longitude, min_order_amount, packaging_fee,
			 avg_preparation_minutes, commission_percentage)
		VALUES ($1, $2, $3, $4, 'ACTIVE', TRUE, TRUE, '1 Test Lane', 'Bengaluru', 'Karnataka', 'RESTAURANT_STANDALONE',
			12.9716, 77.5946, 0, 0, 20, 10)
		RETURNING id`, partnerID, "Nav Kitchen "+uuid.NewString()[:8], "nav-kitchen-"+uuid.NewString()[:8], owner).Scan(&restaurantID), "seed restaurant")
	must(pool.QueryRow(ctx, `
		INSERT INTO food.orders
			(order_number, user_id, restaurant_id, status, payment_status, payment_method,
			 restaurant_name_snapshot, restaurant_address_snapshot, delivery_address_snapshot,
			 item_subtotal, final_amount, commission_percentage_snapshot, commission_amount)
		VALUES ($1, $2, $3, 'DELIVERY_ASSIGNED', 'CAPTURED', 'ONLINE', 'Nav Kitchen', '{}'::jsonb, $4::jsonb, 250, 250, 10, 25)
		RETURNING id`, "NAV-"+uuid.NewString()[:8], customer, restaurantID, itRnDropSnapshot).Scan(&orderID), "seed order")
	for _, u := range []uuid.UUID{riderUser, strangerUser} {
		var id uuid.UUID
		must(pool.QueryRow(ctx, `
			INSERT INTO food.delivery_partners (user_id, full_name, phone, status, vehicle_type, city, is_online)
			VALUES ($1, 'Test Rider', '08040000001', 'ACTIVE', 'bike', 'Bengaluru', TRUE) RETURNING id`, u).Scan(&id), "seed rider")
		if u == riderUser {
			riderRow = id
		}
	}
	must(pool.QueryRow(ctx, `
		INSERT INTO food.delivery_assignments
			(order_id, delivery_partner_id, status, assigned_at, accepted_at, delivery_fee, delivery_partner_payout)
		VALUES ($1, $2, 'ACCEPTED', NOW(), NOW(), 29.00, 23.20) RETURNING id`, orderID, riderRow).Scan(&assignmentID), "seed assignment")

	path := "/v1/food/delivery/assignments/" + assignmentID.String() + "/tracking"
	rec := doJSON(r, http.MethodGet, path, ``, strangerUser, false)
	expectStatus(t, rec, http.StatusNotFound, "foreign rider tracking")
	if errorCode(t, rec) != "FOOD_ASSIGNMENT_TRACKING_NOT_FOUND" || strings.Contains(rec.Body.String(), "Lake View") {
		t.Fatalf("foreign rider body = %s", rec.Body.String())
	}

	rec = doJSON(r, http.MethodGet, path, ``, riderUser, false)
	expectStatus(t, rec, http.StatusOK, "own tracking")
	var body struct {
		Data struct {
			Assignment struct {
				Drop *struct {
					AddressLine1      string `json:"address_line1"`
					CustomerFirstName string `json:"customer_first_name"`
				} `json:"drop"`
				Navigation *struct {
					PickupURL string `json:"pickup_url"`
					DropURL   string `json:"drop_url"`
				} `json:"navigation"`
				PayoutPaise int64 `json:"payout_paise"`
			} `json:"assignment"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	a := body.Data.Assignment
	if a.Drop == nil || a.Drop.AddressLine1 != "42 Lake View Road" || a.Drop.CustomerFirstName != "Asha" || a.PayoutPaise != 2320 ||
		a.Navigation == nil || !strings.Contains(a.Navigation.DropURL, "destination=12.978449,77.640812") ||
		!strings.Contains(a.Navigation.PickupURL, "destination=12.9716,77.5946") {
		t.Fatalf("own tracking = %s", rec.Body.String())
	}
	for _, banned := range []string{"Rao", "9812345678", "receiver_name"} {
		if strings.Contains(rec.Body.String(), banned) {
			t.Fatalf("own tracking carries %q: %s", banned, rec.Body.String())
		}
	}
}
