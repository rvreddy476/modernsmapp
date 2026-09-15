package http

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atpost/dating-service/database"
	"github.com/atpost/dating-service/internal/payments"
	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/events"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Golden contract fixtures for lane P2 (Premium through payments-service):
// the catalogue, a purchase with its client_session, a repeat of the same
// key, the refusals (client price, reused key, no payments client), the
// payment status for its buyer and the 404 for anyone else, the
// entitlements view, and the 410 for a retired subscription route. Same
// harness as d3_contract_test.go; regenerate with UPDATE_CONTRACTS=1 and
// review.

var p2Fixtures = []string{
	"premium_catalogue_get_200",
	"premium_purchase_post_201",
	"premium_purchase_post_200_repeat",
	"premium_purchase_post_400_client_price",
	"premium_purchase_post_409_key_reused",
	"premium_purchase_post_503_unavailable",
	"premium_payment_get_200_confirming",
	"premium_payment_get_200_paid",
	"premium_payment_get_404_not_owner",
	"premium_me_get_200",
	"premium_checkout_post_410",
}

// contractPayments is payments-service for the HTTP contract: a fixed
// provider order and publishable key, the same intent for the same purchase.
type contractPayments struct{ intents map[uuid.UUID]uuid.UUID }

func (c *contractPayments) CreateIntent(_ context.Context, in payments.CreateIntentInput) (*payments.Intent, error) {
	id, ok := c.intents[in.PurchaseID]
	if !ok {
		id = uuid.New()
		c.intents[in.PurchaseID] = id
	}
	return &payments.Intent{ID: id, Status: "pending", AmountMinor: in.AmountMinor, Currency: "INR", Method: in.Method,
		ProviderRef: "order_contract_1", ReferenceType: payments.RefTypeDatingPremium, ReferenceID: in.PurchaseID, PayerID: in.PayerID,
		ClientSession: map[string]string{"provider": "razorpay", "order_id": "order_contract_1", "key_id": "rzp_test_contract", "merchant_display_name": "Momentum Dating"}}, nil
}

// setupPremiumRouter is setupTestRouter with a payments client (nil for none).
func setupPremiumRouter(t *testing.T, client service.PremiumPaymentsClient) (*gin.Engine, *store.Store) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping premium contract tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := database.BootstrapSchema(ctx, pool); err != nil {
		t.Fatalf("bootstrap schema: %v", err)
	}
	st := store.New(pool)
	st.SetPII(testPII(t))
	svc := service.New(st, nil)
	svc.SetMessageClient(&stubMessageClient{})
	if client != nil {
		svc.SetPremiumPayments(client, "")
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(svc).RegisterRoutes(r)
	return r, st
}

func TestP2Contracts(t *testing.T) {
	r, st := setupPremiumRouter(t, &contractPayments{intents: map[uuid.UUID]uuid.UUID{}})
	ctx := context.Background()

	purchase := func(t *testing.T, u uuid.UUID, body string) (int, map[string]any) {
		t.Helper()
		rec := contractDo(r, http.MethodPost, "/v1/dating/premium/purchases", body, u)
		var env struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		return rec.Code, env.Data
	}
	purchaseID := func(t *testing.T, data map[string]any) uuid.UUID {
		t.Helper()
		p, _ := data["purchase"].(map[string]any)
		id, err := uuid.Parse(p["id"].(string))
		if err != nil {
			t.Fatalf("purchase id: %v (%v)", err, data)
		}
		return id
	}

	t.Run("premium_catalogue_get_200", func(t *testing.T) {
		rec := contractDo(r, http.MethodGet, "/v1/dating/premium/catalogue", ``, uuid.New())
		assertContract(t, rec, http.StatusOK, "premium_catalogue_get_200", nil)
	})

	t.Run("premium_purchase_post_201_and_200_repeat", func(t *testing.T) {
		u := uuid.New()
		body := `{"product":"pass_30d","idempotency_key":"contract-key-1","method":"upi"}`
		rec := contractDo(r, http.MethodPost, "/v1/dating/premium/purchases", body, u)
		var env struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		pid := purchaseID(t, env.Data)
		labels := map[uuid.UUID]string{u: "<user>", pid: "<purchase>"}
		assertContract(t, rec, http.StatusCreated, "premium_purchase_post_201", labels)
		rec = contractDo(r, http.MethodPost, "/v1/dating/premium/purchases", body, u)
		assertContract(t, rec, http.StatusOK, "premium_purchase_post_200_repeat", labels)
	})

	// Mutation guard: a client-supplied price is refused, never used.
	t.Run("premium_purchase_post_400_client_price", func(t *testing.T) {
		u := uuid.New()
		for _, body := range []string{
			`{"product":"pass_365d","idempotency_key":"k1","amount_minor":100}`,
			`{"product":"pass_365d","idempotency_key":"k2","price":1}`,
			`{"product":"pass_365d","idempotency_key":"k3","currency":"USD"}`,
		} {
			rec := contractDo(r, http.MethodPost, "/v1/dating/premium/purchases", body, u)
			assertContract(t, rec, http.StatusBadRequest, "premium_purchase_post_400_client_price", map[uuid.UUID]string{u: "<user>"})
		}
		if list, _ := st.ListPremiumPurchasesForUser(ctx, u); len(list) != 0 {
			t.Fatalf("a priced request wrote %d purchases", len(list))
		}
	})

	t.Run("premium_purchase_post_409_key_reused", func(t *testing.T) {
		u := uuid.New()
		if code, _ := purchase(t, u, `{"product":"boost","idempotency_key":"contract-key-2"}`); code != http.StatusCreated {
			t.Fatalf("first purchase: %d", code)
		}
		rec := contractDo(r, http.MethodPost, "/v1/dating/premium/purchases", `{"product":"pass_90d","idempotency_key":"contract-key-2"}`, u)
		assertContract(t, rec, http.StatusConflict, "premium_purchase_post_409_key_reused", map[uuid.UUID]string{u: "<user>"})
	})

	t.Run("premium_payment_get_200_confirming_paid_and_404", func(t *testing.T) {
		owner, stranger := uuid.New(), uuid.New()
		code, data := purchase(t, owner, `{"product":"boost","idempotency_key":"contract-key-3"}`)
		if code != http.StatusCreated {
			t.Fatalf("purchase: %d", code)
		}
		pid := purchaseID(t, data)
		labels := map[uuid.UUID]string{owner: "<user>", pid: "<purchase>"}
		path := "/v1/dating/premium/purchases/" + pid.String() + "/payment"
		assertContract(t, contractDo(r, http.MethodGet, path, ``, owner), http.StatusOK, "premium_payment_get_200_confirming", labels)
		// Mutation guard: another user's purchase is a 404.
		assertContract(t, contractDo(r, http.MethodGet, path, ``, stranger), http.StatusNotFound, "premium_payment_get_404_not_owner",
			map[uuid.UUID]string{stranger: "<user>", pid: "<purchase>"})

		p, err := st.GetPremiumPurchaseForUser(ctx, owner, pid)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.ApplyPremiumPaymentEvent(ctx, payments.Event{
			EventID: "evt_" + uuid.NewString(), EventType: events.EventPaymentSucceeded, IntentID: p.PaymentIntentID.String(),
			PurchaseID: pid, PayerID: owner, AmountMinor: p.AmountMinor, Currency: "INR", Status: "succeeded",
		}); err != nil {
			t.Fatal(err)
		}
		assertContract(t, contractDo(r, http.MethodGet, path, ``, owner), http.StatusOK, "premium_payment_get_200_paid", labels)
	})

	t.Run("premium_me_get_200", func(t *testing.T) {
		u := uuid.New()
		for i, product := range []string{"pass_30d", "boost"} {
			code, data := purchase(t, u, `{"product":"`+product+`","idempotency_key":"contract-me-`+string(rune('a'+i))+`"}`)
			if code != http.StatusCreated {
				t.Fatalf("purchase %s: %d", product, code)
			}
			pid := purchaseID(t, data)
			p, _ := st.GetPremiumPurchaseForUser(ctx, u, pid)
			if _, err := st.ApplyPremiumPaymentEvent(ctx, payments.Event{
				EventID: "evt_" + uuid.NewString(), EventType: events.EventPaymentSucceeded, IntentID: p.PaymentIntentID.String(),
				PurchaseID: pid, PayerID: u, AmountMinor: p.AmountMinor, Currency: "INR", Status: "succeeded",
			}); err != nil {
				t.Fatal(err)
			}
		}
		assertContract(t, contractDo(r, http.MethodGet, "/v1/dating/premium/me", ``, u), http.StatusOK, "premium_me_get_200", map[uuid.UUID]string{u: "<user>"})
	})

	t.Run("premium_checkout_post_410", func(t *testing.T) {
		u := uuid.New()
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/premium/checkout", `{"plan_id":"monthly_399"}`, u), http.StatusGone, "premium_checkout_post_410", nil)
		if rec := contractDo(r, http.MethodPost, "/v1/dating/premium/cancel", ``, u); rec.Code != http.StatusGone {
			t.Fatalf("cancel: %d", rec.Code)
		}
		if rec := contractDo(r, http.MethodGet, "/v1/dating/premium/plans", ``, u); rec.Code != http.StatusGone {
			t.Fatalf("plans: %d", rec.Code)
		}
		if rec := contractDo(r, http.MethodPost, "/v1/dating/premium/webhook", `{}`, u); rec.Code != http.StatusNotFound {
			t.Fatalf("the Razorpay webhook route still answers: %d", rec.Code)
		}
	})
}

func TestP2Contract_UnavailableWithoutPaymentsClient(t *testing.T) {
	r, st := setupPremiumRouter(t, nil)
	u := uuid.New()
	rec := contractDo(r, http.MethodPost, "/v1/dating/premium/purchases", `{"product":"pass_30d","idempotency_key":"contract-503"}`, u)
	assertContract(t, rec, http.StatusServiceUnavailable, "premium_purchase_post_503_unavailable", map[uuid.UUID]string{u: "<user>"})
	if list, _ := st.ListPremiumPurchasesForUser(context.Background(), u); len(list) != 0 {
		t.Fatalf("an unavailable purchase wrote a row")
	}
	// Reads that need no payments client still work.
	if rec := contractDo(r, http.MethodGet, "/v1/dating/premium/me", ``, u); rec.Code != http.StatusOK {
		t.Fatalf("me without a payments client: %d", rec.Code)
	}
}

// TestP2ContractFixturesWellFormed runs without a database.
func TestP2ContractFixturesWellFormed(t *testing.T) {
	for _, name := range p2Fixtures {
		raw, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: not JSON: %v", name, err)
		}
		if _, ok := doc["data"]; !ok {
			if _, ok := doc["error"]; !ok {
				t.Fatalf("%s: neither data nor error", name)
			}
		}
		if contractUUIDRe.Match(raw) || contractTimeRe.Match(raw) {
			t.Fatalf("%s: carries a raw uuid or timestamp", name)
		}
		for _, secret := range []string{"key_secret", "payer_id", "payee_id", "user_id"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("%s: exposes %s", name, secret)
			}
		}
	}
}
