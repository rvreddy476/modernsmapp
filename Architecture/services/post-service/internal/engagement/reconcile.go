package engagement

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Reconciler corrects Redis engagement counters from authoritative ScyllaDB
// and PostgreSQL sources. Runs periodically for posts in the hot:posts set.
type Reconciler struct {
	rdb     *redis.Client
	scylla  *gocql.Session
	pg      *pgxpool.Pool
}

// NewReconciler creates a new counter reconciliation worker.
func NewReconciler(rdb *redis.Client, scylla *gocql.Session, pg *pgxpool.Pool) *Reconciler {
	return &Reconciler{rdb: rdb, scylla: scylla, pg: pg}
}

// Start runs the reconciliation loop every interval. Blocks until ctx is canceled.
func (r *Reconciler) Start(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("[reconciler] started, interval=%s", interval)

	for {
		select {
		case <-ctx.Done():
			log.Println("[reconciler] stopping")
			return
		case <-ticker.C:
			r.reconcileHotPosts(ctx)
		}
	}
}

func (r *Reconciler) reconcileHotPosts(ctx context.Context) {
	// Pop all hot posts atomically
	members, err := r.rdb.SMembers(ctx, "hot:posts").Result()
	if err != nil || len(members) == 0 {
		return
	}

	// Clear the set so new activity accumulates fresh
	r.rdb.Del(ctx, "hot:posts")

	corrected := 0
	for _, postIDStr := range members {
		postID, err := uuid.Parse(postIDStr)
		if err != nil {
			continue
		}

		if r.reconcilePost(ctx, postID) {
			corrected++
		}
	}

	if corrected > 0 {
		log.Printf("[reconciler] corrected %d/%d hot posts", corrected, len(members))
	}
}

// reconcilePost compares ScyllaDB/PG authoritative counts with Redis and corrects drift.
// Returns true if any correction was made.
func (r *Reconciler) reconcilePost(ctx context.Context, postID uuid.UUID) bool {
	engKey := fmt.Sprintf("post:eng:%s", postID)
	corrected := false

	// --- Likes: ScyllaDB engagement_counters is authoritative ---
	scyllaLikes := r.getScyllaCounter("post", postID, "likes")
	redisLikes := r.getRedisCounter(ctx, engKey, "likes")
	if scyllaLikes != redisLikes {
		r.rdb.HSet(ctx, engKey, "likes", strconv.FormatInt(scyllaLikes, 10))
		corrected = true
	}

	// --- Shares: ScyllaDB engagement_counters is authoritative ---
	scyllaShares := r.getScyllaCounter("post", postID, "shares")
	redisShares := r.getRedisCounter(ctx, engKey, "shares")
	if scyllaShares != redisShares {
		r.rdb.HSet(ctx, engKey, "shares", strconv.FormatInt(scyllaShares, 10))
		corrected = true
	}

	// --- Comments: PostgreSQL post_engagement_counts is authoritative ---
	pgComments := r.getPGCommentCount(ctx, postID)
	redisComments := r.getRedisCounter(ctx, engKey, "comments")
	if pgComments != redisComments {
		r.rdb.HSet(ctx, engKey, "comments", strconv.FormatInt(pgComments, 10))
		corrected = true
	}

	// --- Also sync PG post_engagement_counts from ScyllaDB for likes/shares ---
	r.syncPGFromScylla(ctx, postID, scyllaLikes, scyllaShares)

	return corrected
}

func (r *Reconciler) getScyllaCounter(targetType string, targetID uuid.UUID, counterType string) int64 {
	var count int64
	if err := r.scylla.Query(`
		SELECT count FROM engagement_counters
		WHERE target_type = ? AND target_id = ? AND counter_type = ?`,
		targetType, targetID, counterType,
	).Scan(&count); err != nil {
		return 0
	}
	return count
}

func (r *Reconciler) getRedisCounter(ctx context.Context, engKey, field string) int64 {
	val, err := r.rdb.HGet(ctx, engKey, field).Result()
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(val, 10, 64)
	return n
}

func (r *Reconciler) getPGCommentCount(ctx context.Context, postID uuid.UUID) int64 {
	var count int64
	err := r.pg.QueryRow(ctx, `
		SELECT comment_count FROM post_engagement_counts WHERE post_id = $1`,
		postID,
	).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

// syncPGFromScylla updates the PG post_engagement_counts table with authoritative
// ScyllaDB values for likes and shares. PG comments are already authoritative.
func (r *Reconciler) syncPGFromScylla(ctx context.Context, postID uuid.UUID, likes, shares int64) {
	_, err := r.pg.Exec(ctx, `
		INSERT INTO post_engagement_counts (post_id, like_count, share_count)
		VALUES ($1, $2, $3)
		ON CONFLICT (post_id) DO UPDATE SET
			like_count = $2,
			share_count = $3,
			updated_at = now()`,
		postID, likes, shares,
	)
	if err != nil {
		log.Printf("[reconciler] pg sync failed for %s: %v", postID, err)
	}
}

// CleanupEventLog removes old engagement event log entries from PostgreSQL.
// Should be called periodically (e.g., hourly).
func CleanupEventLog(ctx context.Context, pg *pgxpool.Pool, maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	tag, err := pg.Exec(ctx, `DELETE FROM engagement_event_log WHERE processed_at < $1`, cutoff)
	if err != nil {
		log.Printf("[cleanup] event log cleanup failed: %v", err)
		return
	}
	if tag.RowsAffected() > 0 {
		log.Printf("[cleanup] removed %d old event log entries", tag.RowsAffected())
	}
}
