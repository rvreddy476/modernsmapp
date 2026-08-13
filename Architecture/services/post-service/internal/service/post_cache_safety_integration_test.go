//go:build integration

package service

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/atpost/post-service/database"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func TestStaleApprovedCacheCannotBypassCanonicalModeration(t *testing.T) {
	dsn := os.Getenv("M7_POSTGRES_DSN")
	redisAddr := os.Getenv("M7_REDIS_ADDR")
	if dsn == "" || redisAddr == "" {
		t.Fatal("M7_POSTGRES_DSN and M7_REDIS_ADDR are required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, database.SetupSQL); err != nil {
		t.Fatalf("apply real setup.sql: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis ping: %v", err)
	}

	postID, authorID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO posts(id,author_id,text,visibility,content_type,review_status,created_at,updated_at)
		VALUES($1,$2,'cached safety proof','public','post','approved',NOW(),NOW())
	`, postID, authorID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM posts WHERE id=$1`, postID) })

	stale := &postgres.Post{
		ID: postID, AuthorID: authorID, Text: "cached safety proof",
		Visibility: "public", ContentType: "post", ReviewStatus: "approved",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	raw, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	key := postCacheKey(postID)
	if err := rdb.Set(ctx, key, raw, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Del(context.Background(), key).Err() })

	// Simulate a committed canonical moderation decision whose best-effort
	// Redis DEL was lost. The cache remains deliberately approved.
	if _, err := pool.Exec(ctx, `UPDATE posts SET review_status='rejected' WHERE id=$1`, postID); err != nil {
		t.Fatal(err)
	}
	svc := New(postgres.New(pool), nil, rdb)
	got, err := svc.getCachedPostBody(ctx, postID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ReviewStatus != "rejected" {
		t.Fatalf("cache returned review_status %v, want canonical rejected", got)
	}
}
