//go:build integration

package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/atpost/analytics-service/database"
	"github.com/atpost/analytics-service/internal/service"
	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLiveContentAnalyticsAreOwnerOnlyAndNonEnumerating(t *testing.T) {
	dsn := os.Getenv("ANALYTICS_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ANALYTICS_POSTGRES_DSN is required")
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
	if _, err := pool.Exec(ctx, `TRUNCATE analytics.content_hourly_agg, analytics.content_ownership CASCADE`); err != nil {
		t.Fatal(err)
	}
	owner, outsider, content := uuid.New(), uuid.New(), uuid.New()
	store := pgstore.New(pool)
	if err := store.UpsertContentOwnership(ctx, pgstore.ContentOwnership{
		ContentID: content, CreatorID: owner, ContentType: "reel", CreatedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO analytics.content_hourly_agg
			(content_id, hour_bucket, creator_id, content_type, views_display)
		VALUES ($1, date_trunc('hour', NOW()), $2, 'reel', 17)`, content, owner); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sharedmiddleware.RequireInternalKey("m6-exact-key"))
	NewDashboardHandler(pgstore.NewAggregateStore(pool)).RegisterRoutes(router.Group("/v1/analytics"))
	request := func(user, contentID, key string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/analytics/dashboard/content/"+contentID, nil)
		req.Header.Set("X-User-Id", user)
		req.Header.Set("X-Internal-Service-Key", key)
		router.ServeHTTP(w, req)
		return w
	}
	if got := request(owner.String(), content.String(), "m6-exact-key"); got.Code != http.StatusOK {
		t.Fatalf("owner status=%d body=%s", got.Code, got.Body.String())
	}
	blocked := request(outsider.String(), content.String(), "m6-exact-key")
	unknown := request(outsider.String(), uuid.New().String(), "m6-exact-key")
	if blocked.Code != http.StatusNotFound || unknown.Code != http.StatusNotFound || blocked.Body.String() != unknown.Body.String() {
		t.Fatalf("non-enumeration mismatch blocked=%d %q unknown=%d %q", blocked.Code, blocked.Body.String(), unknown.Code, unknown.Body.String())
	}
	if got := request(owner.String(), content.String(), "wrong"); got.Code != http.StatusUnauthorized {
		t.Fatalf("wrong internal key status=%d body=%s", got.Code, got.Body.String())
	}

	creatorRouter := gin.New()
	New(nil, nil).
		WithCreatorService(service.NewCreatorService(pgstore.NewAggregateStore(pool))).
		WithInternalKey("m6-exact-key").
		RegisterRoutes(creatorRouter)
	creatorRequest := httptest.NewRequest(http.MethodGet, "/v1/analytics/creator/me?period=7d", nil)
	creatorRequest.Header.Set("X-User-Id", owner.String())
	creatorRequest.Header.Set("X-Internal-Service-Key", "m6-exact-key")
	creatorResponse := httptest.NewRecorder()
	creatorRouter.ServeHTTP(creatorResponse, creatorRequest)
	if creatorResponse.Code != http.StatusOK {
		t.Fatalf("creator stats status=%d body=%s", creatorResponse.Code, creatorResponse.Body.String())
	}
	var stats struct {
		Views int64 `json:"views"`
	}
	if err := json.Unmarshal(creatorResponse.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Views != 17 {
		t.Fatalf("creator stats views=%d want=17", stats.Views)
	}
	publicRequest := httptest.NewRequest(http.MethodGet, "/v1/analytics/creator/"+owner.String(), nil)
	publicRequest.Header.Set("X-User-Id", outsider.String())
	publicRequest.Header.Set("X-Internal-Service-Key", "m6-exact-key")
	publicResponse := httptest.NewRecorder()
	creatorRouter.ServeHTTP(publicResponse, publicRequest)
	if publicResponse.Code != http.StatusNotFound {
		t.Fatalf("public creator route status=%d", publicResponse.Code)
	}
}
