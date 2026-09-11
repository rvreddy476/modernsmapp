//go:build integration

package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/monetization-service/database"
	"github.com/atpost/monetization-service/internal/service"
	pgstore "github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GET /payout-methods answered 500 on every call before plan Phase 4A:
// the store selected and ordered by payout_methods.is_default, a column
// no migration had ever added (Wave 3 found it and left it for the
// migration that reshapes the table). Migration 021 adds it.
func TestPayoutMethodsRouteNoLonger500s(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}

	userID := uuid.New()
	methodID := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM payout_methods WHERE user_id = $1`, userID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO payout_methods (id, user_id, method_type, details_encrypted, is_verified)
		VALUES ($1, $2, 'upi', 'enc:test', true)`, methodID, userID); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(service.New(pgstore.New(pool), nil)).
		WithWritesEnabled(true).
		RegisterRoutes(router)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/monetization/payout-methods", nil)
	req.Header.Set("X-User-Id", userID.String())
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /payout-methods status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	var resp struct {
		Data []struct {
			ID         string `json:"id"`
			MethodType string `json:"method_type"`
			IsDefault  bool   `json:"is_default"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != methodID.String() || resp.Data[0].MethodType != "upi" {
		t.Fatalf("payout methods = %+v, want the one seeded method", resp.Data)
	}
}
