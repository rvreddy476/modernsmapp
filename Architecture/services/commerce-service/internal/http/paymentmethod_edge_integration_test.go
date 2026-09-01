//go:build integration

package http

// C3-LB-3, the HTTP half — the launch vocabulary refused at the public edge,
// through the REAL registered route and the REAL fence middleware.
//
// The store-level proof (internal/store/postgres/paymentmethod_c3_integration_test.go)
// asserts that nothing durable is created. This one asserts the other half of
// the same rule: that a direct client — the caller Android's narrowed enum
// cannot speak for — is refused before it ever reaches the transaction, with a
// diagnosable code rather than a 500.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/http/... -v

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var edgePool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("COMMERCE_TEST_DSN")
	if dsn == "" {
		fmt.Println("COMMERCE_TEST_DSN not set; skipping the checkout-edge integration proofs")
		os.Exit(0)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		fmt.Printf("connect: %v\n", err)
		os.Exit(1)
	}
	edgePool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

// liveEngine wires the engine the way cmd/server/main.go does, over a real
// store and a real database.
func liveEngine(t *testing.T) *gin.Engine {
	t.Helper()
	// B4: address writes now REFUSE without a cipher rather than storing a
	// customer's name and street in plaintext, so the engine needs one — the
	// same development cipher journeyEngine uses.
	cipher, err := pii.New(devKeyProvider{}, []byte("edge-test-salt-16"))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	r := gin.New()
	r.Use(FenceMiddleware())
	h := New(service.New(postgres.New(edgePool), nil, "").WithPII(cipher))
	h.RegisterRoutes(r)
	h.RegisterP0Routes(r)
	return r
}

func postCheckout(t *testing.T, r *gin.Engine, userID uuid.UUID, method string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"address_id":           uuid.NewString(),
		"quote_id":             uuid.NewString(),
		"payment_method":       method,
		"expected_total_minor": 104000,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/commerce/v2/orders/checkout", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", userID.String())
	req.Header.Set("Idempotency-Key", "edge-"+uuid.NewString())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func ordersFor(t *testing.T, userID uuid.UUID) int {
	t.Helper()
	var n int
	if err := edgePool.QueryRow(context.Background(),
		`SELECT count(*) FROM orders WHERE customer_user_id=$1`, userID).Scan(&n); err != nil {
		t.Fatalf("counting orders: %v", err)
	}
	return n
}

// Every non-launch method is refused at the edge with a stable 4xx, and
// nothing reaches the database.
//
// `net_banking` is the one that mattered: it used to pass this handler, pass
// the gated CHECK, commit an order and hold stock.
func TestC3EdgeRefusesEveryNonLaunchMethod(t *testing.T) {
	r := liveEngine(t)
	for _, method := range []string{
		"net_banking", "wallet", "emi", "bnpl", "escrow",
		"", "   ", "UPI", "Card", "upi ", "credit",
	} {
		t.Run("method="+method, func(t *testing.T) {
			userID := uuid.New()
			w := postCheckout(t, r, userID, method)

			if w.Code < 400 || w.Code >= 500 {
				t.Fatalf("payment_method %q returned %d; want a 4xx — a client that sent the "+
					"wrong thing must be told so, not handed a 500", method, w.Code)
			}
			var env struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &env)
			if env.Error.Code != "PAYMENT_METHOD_NOT_SUPPORTED" && env.Error.Code != "INVALID_BODY" {
				t.Fatalf("payment_method %q returned code %q; want PAYMENT_METHOD_NOT_SUPPORTED "+
					"(or INVALID_BODY for an absent field)", method, env.Error.Code)
			}
			if n := ordersFor(t, userID); n != 0 {
				t.Fatalf("payment_method %q created %d order(s) at the edge", method, n)
			}
		})
	}
}

// COD keeps its own code all the way to the edge, because the client renders
// dedicated copy for it.
func TestC3EdgeReportsCODSeparately(t *testing.T) {
	w := postCheckout(t, liveEngine(t), uuid.New(), "cod")
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Error.Code != "COD_NOT_SUPPORTED" {
		t.Fatalf("cod returned code %q, want COD_NOT_SUPPORTED", env.Error.Code)
	}
}

// The positive control: a launch method passes the vocabulary check and fails
// LATER, on the fabricated address/quote this request carries.
//
// Without this, the refusals above would be indistinguishable from an edge
// that rejects every checkout for some unrelated reason.
func TestC3EdgeLetsLaunchMethodsThroughToTheRealCheckout(t *testing.T) {
	r := liveEngine(t)
	for _, method := range []string{"upi", "card"} {
		t.Run("method="+method, func(t *testing.T) {
			w := postCheckout(t, r, uuid.New(), method)
			var env struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &env)
			if env.Error.Code == "PAYMENT_METHOD_NOT_SUPPORTED" {
				t.Fatalf("the launch method %q was refused by the vocabulary check", method)
			}
		})
	}
}

// ─── The address contract the Android client actually sends ──────────

// A new buyer must be able to save a delivery address.
//
// The server's request struct required `full_name`; the column, the read path
// and the Android client all use `contact_name`. So every address the app
// tried to save was rejected with a 400 before it reached the service, and a
// new buyer could not check out at all.
//
// No integration test caught it because every fixture in this repo — this
// file's own included — seeds addresses with raw SQL. The one path a real
// user takes was the one path nothing exercised.
func TestAddressAcceptsTheClientsContactNameField(t *testing.T) {
	r := liveEngine(t)
	userID := uuid.New()

	// Byte-for-byte the body CommerceRepository.addAddress builds from
	// AddressDto: contact_name, no full_name.
	body := map[string]any{
		"label":          "Home",
		"contact_name":   "Asha Menon",
		"phone":          "9111111111",
		"address_line_1": "5 Main St",
		"city":           "Bengaluru",
		"state":          "KA",
		"postal_code":    "560002",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/commerce/addresses", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", userID.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("the client's own request body returned %d: %s\n\n"+
			"A new buyer cannot save a delivery address, so they cannot check out.",
			w.Code, w.Body.String())
	}

	var stored string
	if err := edgePool.QueryRow(context.Background(),
		`SELECT contact_name FROM customer_addresses WHERE user_id = $1`, userID).Scan(&stored); err != nil {
		t.Fatalf("reading the saved address: %v", err)
	}
	if stored == "" {
		t.Fatal("the address saved with an empty contact name; a courier has nothing to deliver to")
	}
}

// The older wire name keeps working, so fixing the app does not break an
// existing web caller.
func TestAddressStillAcceptsTheLegacyFullNameField(t *testing.T) {
	r := liveEngine(t)
	userID := uuid.New()

	b, _ := json.Marshal(map[string]any{
		"full_name":      "Legacy Caller",
		"phone":          "9222222222",
		"address_line_1": "9 Old Rd",
		"city":           "Bengaluru",
		"state":          "KA",
		"postal_code":    "560003",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/commerce/addresses", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", userID.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("the legacy body returned %d: %s", w.Code, w.Body.String())
	}
}

// An address with no name under EITHER field is still refused. The fix widens
// what is accepted; it does not remove the requirement.
func TestAddressStillRequiresAContactName(t *testing.T) {
	r := liveEngine(t)
	b, _ := json.Marshal(map[string]any{
		"phone":          "9333333333",
		"address_line_1": "1 Nameless Way",
		"city":           "Bengaluru",
		"state":          "KA",
		"postal_code":    "560004",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/commerce/addresses", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusCreated {
		t.Fatal("an address with no contact name was accepted")
	}
}
