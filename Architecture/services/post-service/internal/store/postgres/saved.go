package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SavedItem represents a user's saved post/video/reel.
type SavedItem struct {
	ID             uuid.UUID `json:"id"`
	UserID         uuid.UUID `json:"user_id"`
	TargetType     string    `json:"target_type"`
	TargetID       uuid.UUID `json:"target_id"`
	CollectionName string    `json:"collection_name"`
	CreatedAt      time.Time `json:"created_at"`
}

// SavedCollection represents a named collection with item count.
type SavedCollection struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// SaveItem adds an item to a user's saved collection.
func (s *Store) SaveItem(ctx context.Context, userID uuid.UUID, targetType string, targetID uuid.UUID, collectionName string) (*SavedItem, error) {
	if collectionName == "" {
		collectionName = "All Saved"
	}

	item := &SavedItem{
		ID:             uuid.New(),
		UserID:         userID,
		TargetType:     targetType,
		TargetID:       targetID,
		CollectionName: collectionName,
		CreatedAt:      time.Now(),
	}

	_, err := s.db.Exec(ctx, `
		INSERT INTO saved_items (id, user_id, target_type, target_id, collection_name, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, target_type, target_id) DO UPDATE SET collection_name = $5
	`, item.ID, item.UserID, item.TargetType, item.TargetID, item.CollectionName, item.CreatedAt)
	if err != nil {
		return nil, err
	}

	return item, nil
}

// UnsaveItem removes a saved item by ID. Returns error if not found or not owned by user.
func (s *Store) UnsaveItem(ctx context.Context, savedID, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM saved_items WHERE id = $1 AND user_id = $2
	`, savedID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("SAVED_ITEM_NOT_FOUND")
	}
	return nil
}

// ListSavedItems returns paginated saved items for a user, optionally filtered by collection.
func (s *Store) ListSavedItems(ctx context.Context, userID uuid.UUID, collectionName string, limit int, cursor string) ([]SavedItem, string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var args []interface{}
	args = append(args, userID, limit+1)

	query := `SELECT id, user_id, target_type, target_id, collection_name, created_at
		FROM saved_items
		WHERE user_id = $1`

	argIdx := 3
	if collectionName != "" {
		query += fmt.Sprintf(` AND collection_name = $%d`, argIdx)
		args = append(args, collectionName)
		argIdx++
	}

	if cursor != "" {
		cursorTime, err := time.Parse(time.RFC3339Nano, cursor)
		if err == nil {
			query += fmt.Sprintf(` AND created_at < $%d`, argIdx)
			args = append(args, cursorTime)
		}
	}

	query += ` ORDER BY created_at DESC LIMIT $2`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var items []SavedItem
	for rows.Next() {
		var item SavedItem
		if err := rows.Scan(&item.ID, &item.UserID, &item.TargetType, &item.TargetID,
			&item.CollectionName, &item.CreatedAt); err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(items) > limit {
		nextCursor = items[limit-1].CreatedAt.Format(time.RFC3339Nano)
		items = items[:limit]
	}

	return items, nextCursor, nil
}

// ListCollections returns all collection names with item counts for a user.
func (s *Store) ListCollections(ctx context.Context, userID uuid.UUID) ([]SavedCollection, error) {
	rows, err := s.db.Query(ctx, `
		SELECT collection_name, COUNT(*) as count
		FROM saved_items
		WHERE user_id = $1
		GROUP BY collection_name
		ORDER BY count DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var collections []SavedCollection
	for rows.Next() {
		var c SavedCollection
		if err := rows.Scan(&c.Name, &c.Count); err != nil {
			return nil, err
		}
		collections = append(collections, c)
	}
	return collections, rows.Err()
}

// IsSaved checks if a specific target is saved by the user.
func (s *Store) IsSaved(ctx context.Context, userID uuid.UUID, targetType string, targetID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM saved_items WHERE user_id = $1 AND target_type = $2 AND target_id = $3)
	`, userID, targetType, targetID).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return exists, err
}

// HashtagSortMode controls ordering for hashtag-filtered post queries.
type HashtagSortMode string

const (
	HashtagSortRecent HashtagSortMode = "recent"
	HashtagSortTop    HashtagSortMode = "top"
)

// hashtagTopCursor is what we serialise into the "top" sort cursor.
// Score: like + comment*2 + share*3 + bookmark*2 + views*0.1 - reports*5 + freshness.
// Notes:
//   - bookmark_count covers the "saves" signal — there is no separate saves
//     concept in this codebase, so the spec's saves_count term collapses into it.
//   - views_count is a placeholder column maintained at 0 until a view-tracking
//     writer is added; it contributes nothing to the score for now but the
//     formula won't have to change once a writer ships.
//   - reports_count is kept current by the trg_post_reports trigger on
//     content_reports (see ensureSchema in post-service main.go).
type hashtagTopCursor struct {
	Score  float64 `json:"score"`
	PostID string  `json:"post_id"`
}

const hashtagTopScoreExpr = `
	(COALESCE(c.like_count, 0) * 1
	+ COALESCE(c.comment_count, 0) * 2
	+ COALESCE(c.share_count, 0) * 3
	+ COALESCE(c.bookmark_count, 0) * 2
	+ COALESCE(c.views_count, 0) * 0.1
	- COALESCE(c.reports_count, 0) * 5
	+ CASE
		WHEN p.created_at >= NOW() - INTERVAL '1 hour'  THEN 50
		WHEN p.created_at >= NOW() - INTERVAL '6 hours' THEN 30
		WHEN p.created_at >= NOW() - INTERVAL '24 hours' THEN 10
		ELSE 0
	  END)`

// GetPostsByHashtag returns posts containing a specific hashtag, paginated.
// Supports sort=recent (default, by created_at DESC) and sort=top (by simplified
// trending score DESC). Cursor format depends on the sort mode:
//   - recent: RFC3339Nano timestamp
//   - top:    base64(JSON{score, post_id})
func (s *Store) GetPostsByHashtag(ctx context.Context, hashtag string, limit int, cursor string, sort HashtagSortMode) ([]Post, string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if sort != HashtagSortTop {
		sort = HashtagSortRecent
	}

	if sort == HashtagSortTop {
		return s.getPostsByHashtagTop(ctx, hashtag, limit, cursor)
	}
	return s.getPostsByHashtagRecent(ctx, hashtag, limit, cursor)
}

func (s *Store) getPostsByHashtagRecent(ctx context.Context, hashtag string, limit int, cursor string) ([]Post, string, error) {
	var args []interface{}
	args = append(args, hashtag, limit+1)

	query := `SELECT ` + postCols + `
		FROM posts p
		WHERE $1 = ANY(p.hashtags) AND p.deleted_at IS NULL AND p.visibility = 'public'`

	if cursor != "" {
		cursorTime, err := time.Parse(time.RFC3339Nano, cursor)
		if err == nil {
			query += ` AND p.created_at < $3`
			args = append(args, cursorTime)
		}
	}

	query += ` ORDER BY p.created_at DESC LIMIT $2`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	posts, err := scanPostRows(rows)
	if err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(posts) > limit {
		nextCursor = posts[limit-1].CreatedAt.Format(time.RFC3339Nano)
		posts = posts[:limit]
	}

	// Batch-fetch media
	if len(posts) > 0 {
		postIDs := make([]uuid.UUID, len(posts))
		for i, p := range posts {
			postIDs[i] = p.ID
		}
		mediaRows, err := s.db.Query(ctx, `
			SELECT post_id, media_id, kind FROM post_media WHERE post_id = ANY($1)
		`, postIDs)
		if err == nil {
			defer mediaRows.Close()
			mediaMap := make(map[uuid.UUID][]PostMedia)
			for mediaRows.Next() {
				var postID uuid.UUID
				var m PostMedia
				if err := mediaRows.Scan(&postID, &m.MediaID, &m.Kind); err == nil {
					mediaMap[postID] = append(mediaMap[postID], m)
				}
			}
			for i := range posts {
				posts[i].Media = mediaMap[posts[i].ID]
			}
		}
	}

	return posts, nextCursor, nil
}

func (s *Store) getPostsByHashtagTop(ctx context.Context, hashtag string, limit int, cursor string) ([]Post, string, error) {
	args := []interface{}{hashtag, limit + 1}

	prefixedCols := strings.ReplaceAll(
		strings.ReplaceAll(postCols, "\n", " "),
		"  ", " ",
	)
	// Prefix every bare column name with `p.` so the JOIN below isn't ambiguous.
	// Cheap and contained: postCols is a static list, no user input.
	cols := make([]string, 0)
	for _, c := range strings.Split(prefixedCols, ",") {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		cols = append(cols, "p."+c)
	}
	selectList := strings.Join(cols, ", ")

	query := `SELECT ` + selectList + `, ` + hashtagTopScoreExpr + ` AS top_score
		FROM posts p
		LEFT JOIN post_engagement_counts c ON c.post_id = p.id
		WHERE $1 = ANY(p.hashtags) AND p.deleted_at IS NULL AND p.visibility = 'public'`

	if cursor != "" {
		if cur, err := decodeHashtagTopCursor(cursor); err == nil {
			postUUID, err2 := uuid.Parse(cur.PostID)
			if err2 == nil {
				query += ` AND (` + hashtagTopScoreExpr + `, p.id) < ($3, $4)`
				args = append(args, cur.Score, postUUID)
			}
		}
	}

	query += ` ORDER BY ` + hashtagTopScoreExpr + ` DESC, p.id DESC LIMIT $2`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	posts, scores, err := scanPostRowsWithScore(rows)
	if err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(posts) > limit {
		nextCursor = encodeHashtagTopCursor(scores[limit-1], posts[limit-1].ID.String())
		posts = posts[:limit]
	}

	if len(posts) > 0 {
		postIDs := make([]uuid.UUID, len(posts))
		for i, p := range posts {
			postIDs[i] = p.ID
		}
		mediaRows, err := s.db.Query(ctx, `
			SELECT post_id, media_id, kind FROM post_media WHERE post_id = ANY($1)
		`, postIDs)
		if err == nil {
			defer mediaRows.Close()
			mediaMap := make(map[uuid.UUID][]PostMedia)
			for mediaRows.Next() {
				var postID uuid.UUID
				var m PostMedia
				if err := mediaRows.Scan(&postID, &m.MediaID, &m.Kind); err == nil {
					mediaMap[postID] = append(mediaMap[postID], m)
				}
			}
			for i := range posts {
				posts[i].Media = mediaMap[posts[i].ID]
			}
		}
	}

	return posts, nextCursor, nil
}

// HashtagSuggestion is a single result from prefix-search over posts.hashtags.
type HashtagSuggestion struct {
	NormalizedName string `json:"normalized_name"`
	DisplayName    string `json:"display_name"`
	PostCount      int64  `json:"post_count"`
}

// SearchHashtags returns hashtag suggestions matching a prefix, ordered by
// post count desc. Reads directly from `posts.hashtags TEXT[]` via unnest()
// since no normalized hashtag table exists.
func (s *Store) SearchHashtags(ctx context.Context, query string, limit int) ([]HashtagSuggestion, error) {
	q := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(query), "#"))
	if q == "" {
		return []HashtagSuggestion{}, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 10
	}

	// Use ILIKE for prefix; LIKE escape '%' / '_' in user input.
	escaped := strings.ReplaceAll(q, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, "%", `\%`)
	escaped = strings.ReplaceAll(escaped, "_", `\_`)
	pattern := escaped + "%"

	const sql = `
		SELECT lower(h)        AS normalized_name,
		       MIN(h)          AS display_sample,
		       COUNT(*)::BIGINT AS post_count
		FROM posts p, unnest(p.hashtags) AS h
		WHERE p.deleted_at IS NULL
		  AND p.visibility = 'public'
		  AND lower(h) LIKE $1 ESCAPE '\'
		GROUP BY lower(h)
		ORDER BY post_count DESC, normalized_name ASC
		LIMIT $2`

	rows, err := s.db.Query(ctx, sql, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]HashtagSuggestion, 0, limit)
	for rows.Next() {
		var item HashtagSuggestion
		var sample string
		if err := rows.Scan(&item.NormalizedName, &sample, &item.PostCount); err != nil {
			return nil, err
		}
		// Display name = "#" + first-seen casing (sample). If the sample is
		// already lowercase, this is the same as the normalized name.
		item.DisplayName = "#" + sample
		out = append(out, item)
	}
	return out, rows.Err()
}

// HashtagTrending24h is the shape returned by GetTrendingHashtags24h.
// Distinct from postgres.TrendingHashtag (in hashtags.go) which is the
// reel-specific shape.
type HashtagTrending24h struct {
	NormalizedName string `json:"normalized_name"`
	DisplayName    string `json:"display_name"`
	PostCount      int64  `json:"post_count"`
}

// GetTrendingHashtags24h returns the most-used hashtags from the last 24h,
// used as a fallback when the Redis sorted set `trending:hashtags:{date}` is
// empty (cold start, cache wipe, or no posts yet today). The Redis writer
// lives in service.CreatePost.
func (s *Store) GetTrendingHashtags24h(ctx context.Context, limit int) ([]HashtagTrending24h, error) {
	if limit <= 0 || limit > 30 {
		limit = 15
	}

	const sql = `
		SELECT lower(h)        AS normalized_name,
		       MIN(h)          AS display_sample,
		       COUNT(*)::BIGINT AS post_count
		FROM posts p, unnest(p.hashtags) AS h
		WHERE p.deleted_at IS NULL
		  AND p.visibility = 'public'
		  AND p.created_at >= NOW() - INTERVAL '24 hours'
		GROUP BY lower(h)
		ORDER BY post_count DESC, normalized_name ASC
		LIMIT $1`

	rows, err := s.db.Query(ctx, sql, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]HashtagTrending24h, 0, limit)
	for rows.Next() {
		var item HashtagTrending24h
		var sample string
		if err := rows.Scan(&item.NormalizedName, &sample, &item.PostCount); err != nil {
			return nil, err
		}
		item.DisplayName = "#" + sample
		out = append(out, item)
	}
	return out, rows.Err()
}

// scanPostRowsWithScore scans rows where the SELECT list is `postCols, top_score`
// (the extra trailing float column produced by getPostsByHashtagTop). Returns
// posts and scores in parallel slices so callers can emit cursor values.
func scanPostRowsWithScore(rows pgx.Rows) ([]Post, []float64, error) {
	var posts []Post
	var scores []float64
	for rows.Next() {
		var p Post
		var topScore float64
		if err := rows.Scan(
			&p.ID, &p.AuthorID, &p.Text, &p.Visibility, &p.ContentType, &p.IsPinned,
			&p.Feeling, &p.Activity, &p.ActivityDetail, &p.RichText,
			&p.NoComments, &p.NoLikes,
			&p.Hashtags, &p.Mentions, &p.LocationName, &p.LocationLat, &p.LocationLng,
			&p.PostType, &p.AppOrigin, &p.ShareToPostbook,
			&p.Title, &p.Tags, &p.Category, &p.Language, &p.SEOTitle,
			&p.PaidPromotion, &p.AlteredContent, &p.IsMadeForKids,
			&p.License, &p.AllowEmbedding, &p.PublishToFeed, &p.RemixSetting,
			&p.CommentModeration, &p.CommentAccess,
			&p.RecordingDate, &p.RecordingLocation,
			&p.CoverMediaID, &p.OriginalAudioVol, &p.OverlayAudioVol,
			&p.CreatedAt, &p.UpdatedAt,
			&topScore,
		); err != nil {
			return nil, nil, err
		}
		posts = append(posts, p)
		scores = append(scores, topScore)
	}
	return posts, scores, rows.Err()
}

func encodeHashtagTopCursor(score float64, postID string) string {
	b, _ := json.Marshal(hashtagTopCursor{Score: score, PostID: postID})
	return base64.URLEncoding.EncodeToString(b)
}

func decodeHashtagTopCursor(s string) (hashtagTopCursor, error) {
	var cur hashtagTopCursor
	raw, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return cur, err
	}
	if err := json.Unmarshal(raw, &cur); err != nil {
		return cur, err
	}
	return cur, nil
}
