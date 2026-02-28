package pipeline

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// AffinityPipeline computes author affinity scores from user interaction data
// and warms them into Redis for the ranking middleware to consume.
type AffinityPipeline struct {
	db  *pgxpool.Pool
	rdb *redis.Client
}

// NewAffinityPipeline creates a new AffinityPipeline backed by Postgres and Redis.
func NewAffinityPipeline(db *pgxpool.Pool, rdb *redis.Client) *AffinityPipeline {
	return &AffinityPipeline{db: db, rdb: rdb}
}

// Run reads computed affinity scores from the user_interactions table and warms
// them into Redis hashes keyed by viewer ID. Each hash maps author IDs to their
// total affinity score. A 25-hour TTL keeps data fresh across daily pipeline runs.
func (p *AffinityPipeline) Run(ctx context.Context) error {
	const batchSize = 1000
	var offset int
	var totalWarmed int

	for {
		rows, err := p.db.Query(ctx, `
			SELECT viewer_id, author_id, total_score
			FROM user_interactions
			WHERE total_score > 0
			ORDER BY viewer_id, author_id
			LIMIT $1 OFFSET $2
		`, batchSize, offset)
		if err != nil {
			return fmt.Errorf("query user_interactions: %w", err)
		}

		batchCount := 0
		for rows.Next() {
			var viewerID, authorID string
			var totalScore float64

			if err := rows.Scan(&viewerID, &authorID, &totalScore); err != nil {
				rows.Close()
				return fmt.Errorf("scan user_interactions row: %w", err)
			}

			key := fmt.Sprintf("user:affinities:%s", viewerID)

			if err := p.rdb.HSet(ctx, key, authorID, totalScore).Err(); err != nil {
				rows.Close()
				return fmt.Errorf("HSET affinity for viewer %s author %s: %w", viewerID, authorID, err)
			}

			// Set TTL to 25 hours so daily runs keep it fresh
			if err := p.rdb.Expire(ctx, key, 25*time.Hour).Err(); err != nil {
				rows.Close()
				return fmt.Errorf("EXPIRE affinity key %s: %w", key, err)
			}

			batchCount++
			totalWarmed++
		}
		rows.Close()

		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate user_interactions rows: %w", err)
		}

		// If we got fewer than batchSize, we've processed all rows
		if batchCount < batchSize {
			break
		}
		offset += batchSize
	}

	log.Printf("AffinityPipeline: warmed %d affinities into Redis", totalWarmed)
	return nil
}

// ComputeFromImpressions derives author affinity scores from the post_impressions
// table for the last 90 days. It counts likes, comments, and shares per
// (viewer, author) pair, computes weighted rates, and upserts results into the
// user_interactions table. After computation it calls Run to warm Redis.
func (p *AffinityPipeline) ComputeFromImpressions(ctx context.Context) error {
	rows, err := p.db.Query(ctx, `
		SELECT
			pi.user_id                                          AS viewer_id,
			p.author_id                                         AS author_id,
			COUNT(*)                                            AS total_impressions,
			COUNT(*) FILTER (WHERE pi.action = 'like')          AS like_count,
			COUNT(*) FILTER (WHERE pi.action = 'comment')       AS comment_count,
			COUNT(*) FILTER (WHERE pi.action = 'share')         AS share_count
		FROM post_impressions pi
		JOIN posts p ON p.id = pi.post_id
		WHERE pi.created_at >= NOW() - INTERVAL '90 days'
		GROUP BY pi.user_id, p.author_id
	`)
	if err != nil {
		return fmt.Errorf("query post_impressions: %w", err)
	}
	defer rows.Close()

	var upsertCount int

	for rows.Next() {
		var viewerID, authorID string
		var totalImpressions, likeCount, commentCount, shareCount int64

		if err := rows.Scan(&viewerID, &authorID, &totalImpressions, &likeCount, &commentCount, &shareCount); err != nil {
			return fmt.Errorf("scan impression row: %w", err)
		}

		if totalImpressions == 0 {
			continue
		}

		likeRate := float64(likeCount) / float64(totalImpressions)
		commentRate := float64(commentCount) / float64(totalImpressions)
		shareRate := float64(shareCount) / float64(totalImpressions)
		totalScore := 0.5*likeRate + 0.3*commentRate + 0.2*shareRate
		interactionCount := likeCount + commentCount + shareCount

		_, err := p.db.Exec(ctx, `
			INSERT INTO user_interactions (viewer_id, author_id, like_rate, comment_rate, share_rate, total_score, interaction_count, computed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now())
			ON CONFLICT (viewer_id, author_id) DO UPDATE SET
				like_rate = EXCLUDED.like_rate,
				comment_rate = EXCLUDED.comment_rate,
				share_rate = EXCLUDED.share_rate,
				total_score = EXCLUDED.total_score,
				interaction_count = EXCLUDED.interaction_count,
				computed_at = now()
		`, viewerID, authorID, likeRate, commentRate, shareRate, totalScore, interactionCount)
		if err != nil {
			return fmt.Errorf("upsert user_interactions for viewer %s author %s: %w", viewerID, authorID, err)
		}

		upsertCount++
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate impression rows: %w", err)
	}

	log.Printf("AffinityPipeline: computed and upserted %d affinity scores from impressions", upsertCount)

	// Warm Redis with the freshly computed scores
	return p.Run(ctx)
}
