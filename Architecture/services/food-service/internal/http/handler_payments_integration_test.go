package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/atpost/food-service/database"
	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Lane B7 over the routes, on TEST_PG_DSN (food_it_test, -p 1): the intent
// response has the fixture's shape against the real store, and
// GET /orders/:id/payment is confirming -> paid only through ApplyPaymentEvent,
// and 404 for anyone but the customer.

func b7IntegrationRouter(t *testing.T, pc *payContractClient) (*gin.Engine, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping food-service HTTP integration tests")
	}
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("TEST_PG_DSN must parse and name a database ending in _test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(service.New(postgres.New(pool)).WithPayments(pc).WithPaymentFlags(payments.Flags{})).RegisterRoutes(router)
	return router, pool
}

// seedB7Order inserts a 250.00 ONLINE order in PAYMENT_PENDING with one
// pending food.payments row.
func seedB7Order(t *testing.T, pool *pgxpool.Pool) (orderID, customerID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	owner, customerID := uuid.New(), uuid.New()
	var partnerID, restaurantID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO food.restaurant_partners (owner_user_id, legal_name, status)
		VALUES ($1, 'B7 Partner', 'APPROVED') RETURNING id`, owner).Scan(&partnerID); err != nil {
		t.Fatalf("seed partner: %v", err)
	}
	suffix := uuid.NewString()[:8]
	if err := pool.QueryRow(ctx, `
		INSERT INTO food.restaurants
			(partner_id, name, slug, owner_user_id, status, is_open, is_accepting_orders,
			 address_line1, city, state, tax_category, min_order_amount, packaging_fee,
			 avg_preparation_minutes, commission_percentage)
		VALUES ($1, $2, $3, $4, 'ACTIVE', TRUE, TRUE,
			'1 Test Lane', 'Bengaluru', 'Karnataka', 'RESTAURANT_STANDALONE', 0, 0, 20, 10)
		RETURNING id`, partnerID, "B7 "+suffix, "b7-"+suffix, owner).Scan(&restaurantID); err != nil {
		t.Fatalf("seed restaurant: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO food.orders
			(order_number, user_id, restaurant_id, status, payment_status, payment_method,
			 restaurant_name_snapshot, restaurant_address_snapshot, delivery_address_snapshot,
			 item_subtotal, final_amount, commission_percentage_snapshot, commission_amount, metadata)
		VALUES ($1, $2, $3, 'PAYMENT_PENDING', 'PENDING', 'ONLINE', 'B7', '{}', '{}', 250, 250, 10, 25,
			'{"payment_instrument":"upi"}')
		RETURNING id`, "B7-"+suffix, customerID, restaurantID).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO food.payments (order_id, payment_method, status, provider, amount, currency)
		VALUES ($1, 'ONLINE', 'PENDING', 'payments-service', 250, 'INR')`, orderID); err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	return orderID, customerID
}

func dataKeys(t *testing.T, raw []byte) map[string][]string {
	t.Helper()
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("not an envelope: %v (%s)", err, raw)
	}
	out := map[string][]string{}
	var top []string
	for k, v := range env.Data {
		top = append(top, k)
		var nested map[string]any
		if json.Unmarshal(v, &nested) == nil {
			var nk []string
			for n := range nested {
				nk = append(nk, n)
			}
			sort.Strings(nk)
			out[k] = nk
		}
	}
	sort.Strings(top)
	out[""] = top
	return out
}

func paymentStatusOver(t *testing.T, r *gin.Engine, orderID, userID uuid.UUID) (int, string, []byte) {
	t.Helper()
	rec := doJSON(r, http.MethodGet, "/v1/food/orders/"+orderID.String()+"/payment", ``, userID, false)
	var env struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	return rec.Code, env.Data.Status, rec.Body.Bytes()
}

func TestOrderPaymentOverRoutes(t *testing.T) {
	pc := &payContractClient{session: ctRazorpaySession()}
	r, pool := b7IntegrationRouter(t, pc)
	ctx := context.Background()
	orderID, customerID := seedB7Order(t, pool)

	// The intent against the real store has exactly the fixture's shape.
	rec := doJSON(r, http.MethodPost, "/v1/food/orders/"+orderID.String()+"/payments/intents", `{"method":"upi"}`, customerID, false)
	expectStatus(t, rec, http.StatusCreated, "intent")
	fixture, err := os.ReadFile(filepath.Join(contractsDir, "payment_intent_post_201_client_session.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, want := dataKeys(t, rec.Body.Bytes()), dataKeys(t, fixture)
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("intent shape drifted from the fixture\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
	if strings.Contains(rec.Body.String(), ctRazorpaySecret) {
		t.Fatal("intent response relays the key secret")
	}

	// Confirming until the event; foreign and missing are the same 404.
	if code, status, _ := paymentStatusOver(t, r, orderID, customerID); code != http.StatusOK || status != payments.CustomerStatusConfirming {
		t.Fatalf("before the event: %d %s", code, status)
	}
	fCode, _, foreign := paymentStatusOver(t, r, orderID, uuid.New())
	mCode, _, missing := paymentStatusOver(t, r, uuid.New(), customerID)
	if fCode != http.StatusNotFound || mCode != http.StatusNotFound || !bytes.Equal(foreign, missing) {
		t.Fatalf("foreign %d %s / missing %d %s", fCode, foreign, mCode, missing)
	}

	var intentID string
	if err := pool.QueryRow(ctx, `SELECT provider_payment_id FROM food.payments WHERE order_id = $1 ORDER BY created_at DESC LIMIT 1`, orderID).Scan(&intentID); err != nil {
		t.Fatal(err)
	}
	if intentID != ctPayIntent.String() {
		t.Fatalf("attached intent = %s", intentID)
	}
	applied, err := postgres.New(pool).ApplyPaymentEvent(ctx, payments.Event{
		EventID: "evt-b7-" + uuid.NewString(), EventType: events.EventPaymentSucceeded, IntentID: intentID,
		OrderID: orderID, PayerID: customerID, AmountMinor: 25000, Currency: "INR", Status: "succeeded",
	})
	if err != nil || applied.Decision.Outcome != payments.OutcomeConfirmed {
		t.Fatalf("apply: %+v %v", applied, err)
	}
	if code, status, body := paymentStatusOver(t, r, orderID, customerID); code != http.StatusOK || status != payments.CustomerStatusPaid {
		t.Fatalf("after the event: %d %s", code, body)
	}
	if code, _, _ := paymentStatusOver(t, r, orderID, uuid.New()); code != http.StatusNotFound {
		t.Fatalf("foreign read of a paid order: %d", code)
	}
}
