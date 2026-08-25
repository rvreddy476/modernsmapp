//go:build integration

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
	dsn := os.Getenv("MONETIZATION_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MONETIZATION_POSTGRES_DSN is required")
	}
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
