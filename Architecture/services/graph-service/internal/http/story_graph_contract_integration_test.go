//go:build integration

package http

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/atpost/graph-service/internal/service"
	"github.com/atpost/graph-service/internal/store"
	"github.com/atpost/post-service/pkg/graphclient"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestStoryFollowingWireContract binds the production graph handler to the
// exact client post-service uses. It is intentionally cross-module: separate
// handler/client unit tests stayed green while their JSON contracts disagreed.
func TestStoryFollowingWireContract(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS follows (
		follower_id UUID NOT NULL,
		followee_id UUID NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (follower_id, followee_id)
	)`); err != nil {
		t.Fatalf("ensure follows: %v", err)
	}

	viewer, first, second := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO follows (follower_id, followee_id, created_at)
		VALUES ($1,$2,NOW()-INTERVAL '1 second'),($1,$3,NOW())`, viewer, first, second); err != nil {
		t.Fatalf("seed follows: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM follows WHERE follower_id=$1`, viewer)
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(service.New(store.New(pool), nil, nil)).RegisterRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	got, err := graphclient.New(server.URL, "", server.Client()).Following(ctx, viewer.String())
	if err != nil {
		t.Fatalf("real handler -> real client: %v", err)
	}
	if len(got) != 2 || got[0] != second.String() || got[1] != first.String() {
		t.Fatalf("following round trip = %v, want [%s %s]", got, second, first)
	}
}
