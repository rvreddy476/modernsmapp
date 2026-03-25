package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/atpost/shared/transport"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	db, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		slog.Error("failed to connect to postgres", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	rdb, err := transport.NewRedisClientFromEnv(redisAddr)
	if err != nil {
		slog.Error("failed to configure redis client", "err", err)
		os.Exit(1)
	}
	defer rdb.Close()

	slog.Info("scheduled post worker started")
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("scheduler shutting down")
			return
		case <-ticker.C:
			if err := publishScheduledPosts(ctx, db, rdb); err != nil {
				slog.Error("scheduled post publish error", "err", err)
			}
		}
	}
}

func publishScheduledPosts(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client) error {
	// Fetch posts due for publishing (up to 100 at a time)
	rows, err := db.Query(ctx, `
		SELECT id FROM posts
		WHERE status = 'scheduled' AND publish_at <= NOW()
		ORDER BY publish_at ASC
		LIMIT 100`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var postIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		postIDs = append(postIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, postID := range postIDs {
		// Publish: update status to 'published'
		_, err := db.Exec(ctx, `
			UPDATE posts SET status = 'published', updated_at = NOW()
			WHERE id = $1 AND status = 'scheduled'`, postID)
		if err != nil {
			slog.Error("failed to publish scheduled post", "post_id", postID, "err", err)
			continue
		}
		// Remove from Redis sorted set if present
		rdb.ZRem(ctx, "scheduled_posts", postID)
		slog.Info("published scheduled post", "post_id", postID)
	}
	return nil
}
