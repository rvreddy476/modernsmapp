//go:build integration

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/monetization-service/database"
	"github.com/atpost/monetization-service/internal/service"
	pgstore "github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLiveRecordedLedgerExactPaiseAndWritesDisabled(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}
	userID := uuid.New()
	updatedAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `
		INSERT INTO creator_ledger
			(user_id, balance, lifetime_earnings, pending_payout, currency, is_frozen, created_at, updated_at)
		VALUES ($1, 12345, 987654, 4567, 'INR', false, $2, $2)`, userID, updatedAt); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := New(service.New(pgstore.New(pool), nil)).
		WithInternalKey("m6-money-key").
		WithWritesEnabled(false)
	handler.RegisterRoutes(router)

	requestAs := func(method, path, key string, requestUserID uuid.UUID, body []byte) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", requestUserID.String())
		req.Header.Set("X-Internal-Service-Key", key)
		router.ServeHTTP(w, req)
		return w
	}
	request := func(method, path, key string, body []byte) *httptest.ResponseRecorder {
		return requestAs(method, path, key, userID, body)
	}

	ledger := request(http.MethodGet, "/v1/monetization/creator-ledger", "m6-money-key", nil)
	if ledger.Code != http.StatusOK {
		t.Fatalf("ledger status=%d body=%s", ledger.Code, ledger.Body.String())
	}
	var response struct {
		Data struct {
			BalancePaise          int64   `json:"balance_paise"`
			LifetimeEarningsPaise int64   `json:"lifetime_earnings_paise"`
			PendingPayoutPaise    int64   `json:"pending_payout_paise"`
			Currency              string  `json:"currency"`
			HasActivity           bool    `json:"has_activity"`
			CreatedAt             *string `json:"created_at"`
			UpdatedAt             *string `json:"updated_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(ledger.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.BalancePaise != 12345 || response.Data.LifetimeEarningsPaise != 987654 || response.Data.PendingPayoutPaise != 4567 || response.Data.Currency != "INR" || !response.Data.HasActivity {
		t.Fatalf("ledger changed precision/shape: %+v", response.Data)
	}

	unknownUserID := uuid.New()
	empty := requestAs(http.MethodGet, "/v1/monetization/creator-ledger", "m6-money-key", unknownUserID, nil)
	if empty.Code != http.StatusOK {
		t.Fatalf("empty ledger status=%d body=%s", empty.Code, empty.Body.String())
	}
	var emptyResponse struct {
		Data struct {
			BalancePaise          int64   `json:"balance_paise"`
			LifetimeEarningsPaise int64   `json:"lifetime_earnings_paise"`
			PendingPayoutPaise    int64   `json:"pending_payout_paise"`
			Currency              string  `json:"currency"`
			HasActivity           bool    `json:"has_activity"`
			CreatedAt             *string `json:"created_at"`
			UpdatedAt             *string `json:"updated_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(empty.Body.Bytes(), &emptyResponse); err != nil {
		t.Fatal(err)
	}
	if emptyResponse.Data.BalancePaise != 0 || emptyResponse.Data.LifetimeEarningsPaise != 0 || emptyResponse.Data.PendingPayoutPaise != 0 || emptyResponse.Data.Currency != "INR" || emptyResponse.Data.HasActivity {
		t.Fatalf("empty ledger is not explicit: %+v", emptyResponse.Data)
	}
	if emptyResponse.Data.CreatedAt != nil || emptyResponse.Data.UpdatedAt != nil {
		t.Fatalf("empty ledger fabricated timestamps: %+v", emptyResponse.Data)
	}
	var createdRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM creator_ledger WHERE user_id=$1`, unknownUserID).Scan(&createdRows); err != nil {
		t.Fatal(err)
	}
	if createdRows != 0 {
		t.Fatalf("read-only ledger GET created %d durable rows", createdRows)
	}

	wrongKey := request(http.MethodGet, "/v1/monetization/creator-ledger", "wrong", nil)
	if wrongKey.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key status=%d body=%s", wrongKey.Code, wrongKey.Body.String())
	}
	mutation := request(http.MethodPost, "/v1/monetization/payouts", "m6-money-key", []byte(`{"amount_paise":100,"payout_method_id":"`+uuid.New().String()+`"}`))
	if mutation.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled mutation status=%d body=%s", mutation.Code, mutation.Body.String())
	}
	var transactions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transactions WHERE wallet_id=$1`, userID).Scan(&transactions); err != nil {
		t.Fatal(err)
	}
	if transactions != 0 {
		t.Fatalf("disabled payout created %d transactions", transactions)
	}
}

// While payouts are off, the earnings and statement reads are open and
// every one of them says so: "estimate": true, "withdrawable": false.
// The number a creator sees in beta is the number they will believe they
// are owed, so the label travels with the figure, not in a footnote.
func TestBetaEstimatesCarryFlags(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(service.New(pgstore.New(pool), nil)).
		WithInternalKey("m6-money-key").
		WithWritesEnabled(false).
		RegisterRoutes(router)

	userID := uuid.New()
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-User-Id", userID.String())
		req.Header.Set("X-Internal-Service-Key", "m6-money-key")
		router.ServeHTTP(w, req)
		return w
	}
	for _, path := range []string{
		"/v1/monetization/creator-fund/earnings?days=7",
		"/v1/monetization/creator-fund/statements",
	} {
		w := get(path)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, w.Code, w.Body.String())
		}
		var body struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if est, ok := body.Data["estimate"].(bool); !ok || !est {
			t.Fatalf("%s: estimate=%v, want true while payouts are off: %s", path, body.Data["estimate"], w.Body.String())
		}
		if wd, ok := body.Data["withdrawable"].(bool); !ok || wd {
			t.Fatalf("%s: withdrawable=%v, want false while payouts are off: %s", path, body.Data["withdrawable"], w.Body.String())
		}
	}
	// A statement that does not exist is a 404, not a 503: the route is
	// open, the period is simply unsettled.
	if w := get("/v1/monetization/creator-fund/statements/2020-01"); w.Code != http.StatusNotFound {
		t.Fatalf("missing statement status=%d body=%s", w.Code, w.Body.String())
	}
}

// With writes on but payouts off, a provider callback is kept, not acted
// on: 202, an audit row carrying the raw body, and no payout row touched.
func TestPayoutWebhookStoredWhilePayoutsDisabled(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(service.New(pgstore.New(pool), nil)).
		WithWritesEnabled(true).
		WithPayoutsEnabled(false).
		RegisterRoutes(router)

	ref := "probe-" + uuid.NewString()
	body := []byte(`{"provider_reference":"` + ref + `","status":"settled"}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/monetization/webhooks/payout", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s, want 202 while payouts are off", w.Code, w.Body.String())
	}
	var stored int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM monetization_audit_log
		WHERE operation = 'webhook_stored_payouts_disabled' AND new_data->>'provider_reference' = $1`, ref).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Fatalf("audit rows for the stored webhook = %d, want 1", stored)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM monetization_audit_log WHERE operation = 'webhook_stored_payouts_disabled' AND new_data->>'provider_reference' = $1`, ref)
	})
}
