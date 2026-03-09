package scylla

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// ─── Reel Reactions (with viral sharding) ───────────────────────────

// ReactToReel adds a reaction to a reel. Uses sharding for viral reel hot partition mitigation.
// Partition: (reel_id, shard) where shard = hash(user_id) % numShards
func (s *InteractionStore) ReactToReel(ctx context.Context, reelID, userID uuid.UUID, reaction string) error {
	rid, uid := reelID.String(), userID.String()
	shard := shardForUser(uid, 16)

	// Idempotent check
	var existing string
	if err := s.session.Query(`SELECT reaction FROM reel_reactions WHERE reel_id = ? AND shard = ? AND user_id = ?`,
		rid, shard, uid).Scan(&existing); err != nil && err != gocql.ErrNotFound {
		return err
	}
	if existing != "" {
		return nil
	}

	if err := s.session.Query(`
		INSERT INTO reel_reactions (reel_id, shard, user_id, reaction, ts)
		VALUES (?, ?, ?, ?, now())
	`, rid, shard, uid, reaction).Exec(); err != nil {
		return err
	}

	return s.session.Query(`UPDATE reel_counts SET like_count = like_count + 1 WHERE reel_id = ?`, rid).Exec()
}

// UnreactToReel removes a reaction from a reel.
func (s *InteractionStore) UnreactToReel(ctx context.Context, reelID, userID uuid.UUID) error {
	rid, uid := reelID.String(), userID.String()
	shard := shardForUser(uid, 16)

	var existing string
	if err := s.session.Query(`SELECT reaction FROM reel_reactions WHERE reel_id = ? AND shard = ? AND user_id = ?`,
		rid, shard, uid).Scan(&existing); err != nil {
		if err == gocql.ErrNotFound {
			return nil
		}
		return err
	}

	if err := s.session.Query(`DELETE FROM reel_reactions WHERE reel_id = ? AND shard = ? AND user_id = ?`,
		rid, shard, uid).Exec(); err != nil {
		return err
	}

	return s.session.Query(`UPDATE reel_counts SET like_count = like_count - 1 WHERE reel_id = ?`, rid).Exec()
}

// GetReelReaction returns the viewer's reaction type for a reel.
func (s *InteractionStore) GetReelReaction(ctx context.Context, reelID, userID uuid.UUID) (string, error) {
	rid, uid := reelID.String(), userID.String()
	shard := shardForUser(uid, 16)

	var reaction string
	if err := s.session.Query(`SELECT reaction FROM reel_reactions WHERE reel_id = ? AND shard = ? AND user_id = ?`,
		rid, shard, uid).Scan(&reaction); err != nil {
		if err == gocql.ErrNotFound {
			return "", nil
		}
		return "", err
	}
	return reaction, nil
}

// ─── Reel Comments (bucketed by YYYYMM + timeuuid DESC) ────────────

// AddReelComment inserts a comment on a reel.
func (s *InteractionStore) AddReelComment(ctx context.Context, reelID, userID uuid.UUID, text string) (uuid.UUID, error) {
	rid := reelID.String()
	bucket := currentBucket()
	commentID := uuid.New()

	if err := s.session.Query(`
		INSERT INTO reel_comments_by_reel (reel_id, bucket, ts, comment_id, author_id, text, is_deleted)
		VALUES (?, ?, now(), ?, ?, ?, false)
	`, rid, bucket, commentID.String(), userID.String(), text).Exec(); err != nil {
		return uuid.Nil, err
	}

	if err := s.session.Query(`UPDATE reel_counts SET comment_count = comment_count + 1 WHERE reel_id = ?`, rid).Exec(); err != nil {
		return uuid.Nil, err
	}

	return commentID, nil
}

// ListReelComments returns the latest comments for a reel.
func (s *InteractionStore) ListReelComments(ctx context.Context, reelID uuid.UUID, limit int) ([]Comment, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	bucket := currentBucket()
	iter := s.session.Query(`
		SELECT comment_id, author_id, text, toTimestamp(ts) FROM reel_comments_by_reel
		WHERE reel_id = ? AND bucket = ?
		ORDER BY ts DESC
		LIMIT ?
	`, reelID.String(), bucket, limit).Iter()

	var comments []Comment
	var idStr, authorStr, text string
	var createdAt time.Time
	for iter.Scan(&idStr, &authorStr, &text, &createdAt) {
		cID, _ := uuid.Parse(idStr)
		aID, _ := uuid.Parse(authorStr)
		comments = append(comments, Comment{ID: cID, AuthorID: aID, Text: text, CreatedAt: createdAt})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return comments, nil
}

// ─── Reel Shares (timeuuid for uniqueness) ──────────────────────────

// ShareReel records a reel share event.
func (s *InteractionStore) ShareReel(ctx context.Context, reelID, userID uuid.UUID, shareType string) error {
	rid := reelID.String()

	if err := s.session.Query(`
		INSERT INTO reel_shares (reel_id, ts, share_id, user_id, share_type)
		VALUES (?, now(), ?, ?, ?)
	`, rid, uuid.New().String(), userID.String(), shareType).Exec(); err != nil {
		return err
	}

	return s.session.Query(`UPDATE reel_counts SET share_count = share_count + 1 WHERE reel_id = ?`, rid).Exec()
}

// ─── Reel Saves (membership + timeline by user) ────────────────────

// SaveReel saves a reel to a user's bookmarks.
func (s *InteractionStore) SaveReel(ctx context.Context, reelID, userID uuid.UUID) error {
	rid, uid := reelID.String(), userID.String()

	// Check if already saved
	var existing string
	if err := s.session.Query(`SELECT reel_id FROM reel_saves WHERE user_id = ? AND reel_id = ?`,
		uid, rid).Scan(&existing); err != nil && err != gocql.ErrNotFound {
		return err
	}
	if existing != "" {
		return nil
	}

	if err := s.session.Query(`
		INSERT INTO reel_saves (user_id, reel_id, saved_at)
		VALUES (?, ?, toTimestamp(now()))
	`, uid, rid).Exec(); err != nil {
		return err
	}

	return s.session.Query(`UPDATE reel_counts SET save_count = save_count + 1 WHERE reel_id = ?`, rid).Exec()
}

// UnsaveReel removes a reel from a user's bookmarks.
func (s *InteractionStore) UnsaveReel(ctx context.Context, reelID, userID uuid.UUID) error {
	rid, uid := reelID.String(), userID.String()

	var existing string
	if err := s.session.Query(`SELECT reel_id FROM reel_saves WHERE user_id = ? AND reel_id = ?`,
		uid, rid).Scan(&existing); err != nil {
		if err == gocql.ErrNotFound {
			return nil
		}
		return err
	}

	if err := s.session.Query(`DELETE FROM reel_saves WHERE user_id = ? AND reel_id = ?`,
		uid, rid).Exec(); err != nil {
		return err
	}

	return s.session.Query(`UPDATE reel_counts SET save_count = save_count - 1 WHERE reel_id = ?`, rid).Exec()
}

// IsReelSaved checks if a user has saved a reel.
func (s *InteractionStore) IsReelSaved(ctx context.Context, reelID, userID uuid.UUID) (bool, error) {
	var existing string
	if err := s.session.Query(`SELECT reel_id FROM reel_saves WHERE user_id = ? AND reel_id = ?`,
		userID.String(), reelID.String()).Scan(&existing); err != nil {
		if err == gocql.ErrNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ListSavedReels returns reel IDs saved by a user.
func (s *InteractionStore) ListSavedReels(ctx context.Context, userID uuid.UUID, limit int) ([]string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	iter := s.session.Query(`
		SELECT reel_id FROM reel_saves WHERE user_id = ?
		LIMIT ?
	`, userID.String(), limit).Iter()

	var reelIDs []string
	var rid string
	for iter.Scan(&rid) {
		reelIDs = append(reelIDs, rid)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return reelIDs, nil
}

// ─── Reel Views (sharded for viral reels) ───────────────────────────

// RecordReelView records a view event for analytics and dedup.
// Partition: (reel_id, view_date, shard) to handle viral reels.
func (s *InteractionStore) RecordReelView(ctx context.Context, reelID, viewerID uuid.UUID) error {
	rid := reelID.String()
	viewDate := time.Now().UTC().Format("2006-01-02")
	shard := rand.Intn(8) // 8 shards for views

	if err := s.session.Query(`
		INSERT INTO reel_views (reel_id, view_date, shard, ts, viewer_id)
		VALUES (?, ?, ?, now(), ?)
	`, rid, viewDate, shard, viewerID.String()).Exec(); err != nil {
		return err
	}

	return s.session.Query(`UPDATE reel_counts SET view_count = view_count + 1 WHERE reel_id = ?`, rid).Exec()
}

// ─── Reel Counts (aggregator-managed) ───────────────────────────────

// ReelCounts holds engagement counters for a reel.
type ReelCounts struct {
	Likes    int64 `json:"likes"`
	Comments int64 `json:"comments"`
	Shares   int64 `json:"shares"`
	Saves    int64 `json:"saves"`
	Views    int64 `json:"views"`
}

// GetReelCounts returns all engagement counts for a reel.
func (s *InteractionStore) GetReelCounts(ctx context.Context, reelID uuid.UUID) (*ReelCounts, error) {
	var c ReelCounts
	if err := s.session.Query(`
		SELECT like_count, comment_count, share_count, save_count, view_count
		FROM reel_counts WHERE reel_id = ?
	`, reelID.String()).Scan(&c.Likes, &c.Comments, &c.Shares, &c.Saves, &c.Views); err != nil {
		if err == gocql.ErrNotFound {
			return &ReelCounts{}, nil
		}
		return nil, err
	}
	return &c, nil
}

// BatchGetReelCounts returns engagement counts for multiple reels.
func (s *InteractionStore) BatchGetReelCounts(ctx context.Context, reelIDs []uuid.UUID) (map[string]*ReelCounts, error) {
	result := make(map[string]*ReelCounts, len(reelIDs))
	for _, id := range reelIDs {
		counts, err := s.GetReelCounts(ctx, id)
		if err != nil {
			result[id.String()] = &ReelCounts{}
			continue
		}
		result[id.String()] = counts
	}
	return result, nil
}

// ─── User Reel Likes Timeline ──────────────────────────────────────

// GetUserReelLikes returns reel IDs that a user has liked (for "Liked reels" tab).
func (s *InteractionStore) GetUserReelLikes(ctx context.Context, userID uuid.UUID, limit int) ([]string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	bucket := currentBucket()
	iter := s.session.Query(`
		SELECT reel_id FROM user_reel_likes WHERE user_id = ? AND bucket = ?
		ORDER BY ts DESC
		LIMIT ?
	`, userID.String(), bucket, limit).Iter()

	var reelIDs []string
	var rid string
	for iter.Scan(&rid) {
		reelIDs = append(reelIDs, rid)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return reelIDs, nil
}

// ─── Helpers ────────────────────────────────────────────────────────

// shardForUser returns a deterministic shard number for a user ID.
func shardForUser(userID string, numShards int) int {
	hash := 0
	for _, c := range userID {
		hash = hash*31 + int(c)
	}
	if hash < 0 {
		hash = -hash
	}
	return hash % numShards
}

// ─── Schema DDL ─────────────────────────────────────────────────────

// ReelEngagementDDL contains the CQL statements to create reel engagement tables.
// Execute these during service startup or via migration tool.
var ReelEngagementDDL = []string{
	// Reel reactions — sharded for viral reels
	`CREATE TABLE IF NOT EXISTS reel_reactions (
		reel_id text,
		shard int,
		user_id text,
		reaction text,
		ts timeuuid,
		PRIMARY KEY ((reel_id, shard), user_id)
	)`,

	// Reel comments — bucketed by month, ordered by timeuuid DESC
	`CREATE TABLE IF NOT EXISTS reel_comments_by_reel (
		reel_id text,
		bucket int,
		ts timeuuid,
		comment_id text,
		author_id text,
		text text,
		is_deleted boolean,
		PRIMARY KEY ((reel_id, bucket), ts)
	) WITH CLUSTERING ORDER BY (ts DESC)`,

	// Reel shares — timeuuid for uniqueness
	`CREATE TABLE IF NOT EXISTS reel_shares (
		reel_id text,
		ts timeuuid,
		share_id text,
		user_id text,
		share_type text,
		PRIMARY KEY (reel_id, ts)
	) WITH CLUSTERING ORDER BY (ts DESC)`,

	// Reel saves — user bookmark list
	`CREATE TABLE IF NOT EXISTS reel_saves (
		user_id text,
		reel_id text,
		saved_at timestamp,
		PRIMARY KEY (user_id, reel_id)
	)`,

	// Reel views — sharded for viral reels
	`CREATE TABLE IF NOT EXISTS reel_views (
		reel_id text,
		view_date text,
		shard int,
		ts timeuuid,
		viewer_id text,
		PRIMARY KEY ((reel_id, view_date, shard), ts)
	) WITH CLUSTERING ORDER BY (ts DESC)`,

	// Reel counters — aggregator-managed (not Scylla native counters to avoid edge cases)
	`CREATE TABLE IF NOT EXISTS reel_counts (
		reel_id text PRIMARY KEY,
		like_count counter,
		comment_count counter,
		share_count counter,
		save_count counter,
		view_count counter
	)`,

	// User reel likes timeline — for "Liked reels" tab
	`CREATE TABLE IF NOT EXISTS user_reel_likes (
		user_id text,
		bucket int,
		ts timeuuid,
		reel_id text,
		PRIMARY KEY ((user_id, bucket), ts)
	) WITH CLUSTERING ORDER BY (ts DESC)`,
}

// EnsureReelEngagementSchema creates the engagement tables if they don't exist.
func EnsureReelEngagementSchema(session *gocql.Session) error {
	for _, ddl := range ReelEngagementDDL {
		if err := session.Query(ddl).Exec(); err != nil {
			return fmt.Errorf("execute DDL: %w\nStatement: %s", err, ddl[:min(len(ddl), 80)])
		}
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// bucketFromDate returns the YYYYMM bucket for a given date.
func bucketFromDate(t time.Time) int {
	b, _ := strconv.Atoi(t.UTC().Format("200601"))
	return b
}
