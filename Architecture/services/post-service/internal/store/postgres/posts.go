package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Post struct {
	ID             uuid.UUID       `json:"id"`
	AuthorID       uuid.UUID       `json:"author_id"`
	Text           string          `json:"text"`
	Visibility     string          `json:"visibility"`
	ContentType    string          `json:"content_type"`
	IsPinned       bool            `json:"is_pinned"`
	Feeling        *string         `json:"feeling,omitempty"`
	Activity       *string         `json:"activity,omitempty"`
	ActivityDetail *string         `json:"activity_detail,omitempty"`
	RichText       json.RawMessage `json:"rich_text,omitempty"`
	NoComments     bool            `json:"no_comments"`
	NoLikes        bool            `json:"no_likes"`
	Hashtags       []string        `json:"hashtags,omitempty"`
	Mentions       []uuid.UUID     `json:"mentions,omitempty"`
	LocationName   *string         `json:"location_name,omitempty"`
	LocationLat    *float64        `json:"location_lat,omitempty"`
	LocationLng    *float64        `json:"location_lng,omitempty"`
	PostType       string          `json:"post_type"`
	AppOrigin       string          `json:"app_origin"`
	ShareToPostbook bool            `json:"share_to_postbook"`
	ReviewStatus   string          `json:"review_status"` // "approved", "flagged", "rejected"
	Title              string      `json:"title,omitempty"`
	Tags               []string    `json:"tags,omitempty"`
	Category           string      `json:"category,omitempty"`
	Language           string      `json:"language,omitempty"`
	SEOTitle           string      `json:"seo_title,omitempty"`
	PaidPromotion      bool        `json:"paid_promotion"`
	AlteredContent     bool        `json:"altered_content"`
	IsMadeForKids      bool        `json:"is_made_for_kids"`
	License            string      `json:"license,omitempty"`
	AllowEmbedding     bool        `json:"allow_embedding"`
	PublishToFeed      bool        `json:"publish_to_feed"`
	RemixSetting       string      `json:"remix_setting,omitempty"`
	CommentModeration  string      `json:"comment_moderation,omitempty"`
	CommentAccess      string      `json:"comment_access,omitempty"`
	RecordingDate      *time.Time  `json:"recording_date,omitempty"`
	RecordingLocation  string      `json:"recording_location,omitempty"`
	CoverMediaID       *uuid.UUID  `json:"cover_media_id,omitempty"`
	OriginalAudioVol   float32     `json:"original_audio_volume"`
	OverlayAudioVol    float32     `json:"overlay_audio_volume"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Media          []PostMedia     `json:"media,omitempty"`
	Poll           *PollData       `json:"poll,omitempty"`
}

type PostMedia struct {
	MediaID uuid.UUID `json:"media_id"`
	Kind    string    `json:"kind"`
}

// Poll types

type PollData struct {
	Question       string       `json:"question"`
	AllowsMultiple bool         `json:"allows_multiple"`
	EndsAt         *time.Time   `json:"ends_at,omitempty"`
	Options        []PollOption `json:"options"`
	TotalVotes     int64        `json:"total_votes"`
	ViewerVotes    []uuid.UUID  `json:"viewer_votes,omitempty"`
	HasEnded       bool         `json:"has_ended"`
}

type PollOption struct {
	ID         uuid.UUID `json:"id"`
	Label      string    `json:"label"`
	VoteCount  int64     `json:"vote_count"`
	Percentage float64   `json:"percentage"`
	SortOrder  int       `json:"-"`
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

const postCols = `id, author_id, text, visibility, content_type, is_pinned,
	feeling, activity, activity_detail, rich_text,
	no_comments, no_likes,
	hashtags, mentions, location_name, location_lat, location_lng,
	post_type, app_origin, share_to_postbook,
	title, tags, category, language, seo_title,
	paid_promotion, altered_content, is_made_for_kids,
	license, allow_embedding, publish_to_feed, remix_setting,
	comment_moderation, comment_access,
	recording_date, recording_location,
	cover_media_id, original_audio_volume, overlay_audio_volume,
	created_at, updated_at`

func scanPost(row pgx.Row) (*Post, error) {
	var p Post
	err := row.Scan(
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
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func scanPostRows(rows pgx.Rows) ([]Post, error) {
	var posts []Post
	for rows.Next() {
		var p Post
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
		); err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// ResolveMediaKind looks up the file_type from media_assets for a given media ID.
// Returns "image" or "video" (defaults to "image" if not found).
func (s *Store) ResolveMediaKind(ctx context.Context, mediaID uuid.UUID) string {
	var fileType string
	err := s.db.QueryRow(ctx, `SELECT file_type FROM media_assets WHERE id = $1`, mediaID).Scan(&fileType)
	if err != nil || fileType == "" {
		return "image"
	}
	return fileType
}

// ResolveMediaDuration queries media_assets for the video duration in seconds.
// Returns 0 if the media is not found, not a video, or duration is not yet set.
func (s *Store) ResolveMediaDuration(ctx context.Context, mediaID uuid.UUID) int {
	var dur *int
	err := s.db.QueryRow(ctx,
		`SELECT duration_seconds FROM media_assets WHERE id = $1 AND file_type = 'video'`,
		mediaID,
	).Scan(&dur)
	if err != nil || dur == nil {
		return 0
	}
	return *dur
}


// CreatePost inserts a post with optional media and poll in a single transaction.
func (s *Store) CreatePost(ctx context.Context, p *Post) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	reviewStatus := p.ReviewStatus
	if reviewStatus == "" {
		reviewStatus = "approved"
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO posts (id, author_id, text, visibility, content_type,
			feeling, activity, activity_detail, rich_text,
			no_comments, no_likes,
			hashtags, mentions, location_name, location_lat, location_lng,
			post_type, app_origin, share_to_postbook, review_status,
			title, tags, category, language, seo_title,
			paid_promotion, altered_content, is_made_for_kids,
			license, allow_embedding, publish_to_feed, remix_setting,
			comment_moderation, comment_access,
			recording_date, recording_location,
			cover_media_id, original_audio_volume, overlay_audio_volume,
			created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
			$12, $13, $14, $15, $16, $17, $18, $19, $20,
			$21, $22, $23, $24, $25,
			$26, $27, $28,
			$29, $30, $31, $32,
			$33, $34,
			$35, $36,
			$37, $38, $39,
			$40, $40)
	`, p.ID, p.AuthorID, p.Text, p.Visibility, p.ContentType,
		p.Feeling, p.Activity, p.ActivityDetail, p.RichText,
		p.NoComments, p.NoLikes,
		p.Hashtags, p.Mentions, p.LocationName, p.LocationLat, p.LocationLng,
		p.PostType, p.AppOrigin, p.ShareToPostbook, reviewStatus,
		p.Title, p.Tags, p.Category, p.Language, p.SEOTitle,
		p.PaidPromotion, p.AlteredContent, p.IsMadeForKids,
		p.License, p.AllowEmbedding, p.PublishToFeed, p.RemixSetting,
		p.CommentModeration, p.CommentAccess,
		p.RecordingDate, p.RecordingLocation,
		p.CoverMediaID, p.OriginalAudioVol, p.OverlayAudioVol,
		p.CreatedAt)
	if err != nil {
		return err
	}

	// Insert media attachments
	for _, m := range p.Media {
		_, err = tx.Exec(ctx, `
			INSERT INTO post_media (post_id, media_id, kind)
			VALUES ($1, $2, $3)
		`, p.ID, m.MediaID, m.Kind)
		if err != nil {
			return err
		}
	}

	// Insert poll if present
	if p.Poll != nil {
		_, err = tx.Exec(ctx, `
			INSERT INTO polls (post_id, question, allows_multiple, ends_at)
			VALUES ($1, $2, $3, $4)
		`, p.ID, p.Poll.Question, p.Poll.AllowsMultiple, p.Poll.EndsAt)
		if err != nil {
			return err
		}
		for i, opt := range p.Poll.Options {
			optID := uuid.New()
			_, err = tx.Exec(ctx, `
				INSERT INTO poll_options (id, post_id, label, sort_order)
				VALUES ($1, $2, $3, $4)
			`, optID, p.ID, opt.Label, i)
			if err != nil {
				return err
			}
			p.Poll.Options[i].ID = optID
		}
	}

	return tx.Commit(ctx)
}

func (s *Store) GetPost(ctx context.Context, id uuid.UUID) (*Post, error) {
	p, err := scanPost(s.db.QueryRow(ctx, `
		SELECT `+postCols+`
		FROM posts
		WHERE id = $1 AND deleted_at IS NULL
	`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	// Load media
	rows, err := s.db.Query(ctx, `SELECT media_id, kind FROM post_media WHERE post_id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m PostMedia
		if err := rows.Scan(&m.MediaID, &m.Kind); err != nil {
			return nil, err
		}
		p.Media = append(p.Media, m)
	}

	return p, nil
}

// GetPostsByAuthor returns paginated posts by author, optionally filtered by content type.
func (s *Store) GetPostsByAuthor(ctx context.Context, authorID uuid.UUID, contentType string, limit int, cursor string) ([]Post, string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var args []interface{}
	args = append(args, authorID, limit+1)

	query := `SELECT ` + postCols + `
		FROM posts
		WHERE author_id = $1 AND deleted_at IS NULL`

	argIdx := 3
	if contentType != "" && contentType != "all" {
		query += fmt.Sprintf(` AND content_type = $%d`, argIdx)
		args = append(args, contentType)
		argIdx++
	}

	if cursor != "" {
		cursorTime, err := time.Parse(time.RFC3339Nano, cursor)
		if err == nil {
			query += fmt.Sprintf(` AND created_at < $%d`, argIdx)
			args = append(args, cursorTime)
		}
	}

	query += ` ORDER BY is_pinned DESC, created_at DESC LIMIT $2`

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

	// Batch-fetch media for all posts
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

// GetRecentPosts returns recent public posts from all users, paginated by cursor.
// If excludeAuthor is non-nil, posts by that author are excluded.
func (s *Store) GetRecentPosts(ctx context.Context, excludeAuthor *uuid.UUID, limit int, cursor string) ([]Post, string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var args []interface{}
	args = append(args, limit+1)

	query := `SELECT ` + postCols + `
		FROM posts
		WHERE visibility = 'public' AND deleted_at IS NULL`

	argIdx := 2
	if excludeAuthor != nil {
		query += fmt.Sprintf(` AND author_id != $%d`, argIdx)
		args = append(args, *excludeAuthor)
		argIdx++
	}

	if cursor != "" {
		cursorTime, err := time.Parse(time.RFC3339Nano, cursor)
		if err == nil {
			query += fmt.Sprintf(` AND created_at < $%d`, argIdx)
			args = append(args, cursorTime)
		}
	}

	query += ` ORDER BY created_at DESC LIMIT $1`

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

	// Batch-fetch media for all posts
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

// GetPostCountsByAuthor returns post counts grouped by content type.
func (s *Store) GetPostCountsByAuthor(ctx context.Context, authorID uuid.UUID) (map[string]int64, error) {
	rows, err := s.db.Query(ctx, `
		SELECT content_type, COUNT(*) as count
		FROM posts
		WHERE author_id = $1 AND deleted_at IS NULL
		GROUP BY content_type
	`, authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int64)
	var total int64
	for rows.Next() {
		var ct string
		var count int64
		if err := rows.Scan(&ct, &count); err != nil {
			return nil, err
		}
		counts[ct] = count
		total += count
	}
	counts["total"] = total

	return counts, rows.Err()
}

// SetPinned sets or unsets the pinned status of a post.
func (s *Store) SetPinned(ctx context.Context, postID, authorID uuid.UUID, pinned bool) error {
	_, err := s.db.Exec(ctx, `
		UPDATE posts SET is_pinned = $1, updated_at = NOW()
		WHERE id = $2 AND author_id = $3 AND deleted_at IS NULL
	`, pinned, postID, authorID)
	return err
}

// CountPinnedByAuthor returns the number of pinned posts for an author.
func (s *Store) CountPinnedByAuthor(ctx context.Context, authorID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM posts WHERE author_id = $1 AND is_pinned = true AND deleted_at IS NULL
	`, authorID).Scan(&count)
	return count, err
}

// --- Poll methods ---

// GetPoll loads poll data for a post, including options and vote counts.
func (s *Store) GetPoll(ctx context.Context, postID uuid.UUID) (*PollData, error) {
	var poll PollData
	err := s.db.QueryRow(ctx, `
		SELECT question, allows_multiple, ends_at FROM polls WHERE post_id = $1
	`, postID).Scan(&poll.Question, &poll.AllowsMultiple, &poll.EndsAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	if poll.EndsAt != nil && poll.EndsAt.Before(time.Now()) {
		poll.HasEnded = true
	}

	// Load options
	optRows, err := s.db.Query(ctx, `
		SELECT id, label, sort_order FROM poll_options
		WHERE post_id = $1 ORDER BY sort_order
	`, postID)
	if err != nil {
		return nil, err
	}
	defer optRows.Close()

	for optRows.Next() {
		var opt PollOption
		if err := optRows.Scan(&opt.ID, &opt.Label, &opt.SortOrder); err != nil {
			return nil, err
		}
		poll.Options = append(poll.Options, opt)
	}

	// Load vote counts per option
	countRows, err := s.db.Query(ctx, `
		SELECT option_id, COUNT(*) FROM poll_votes
		WHERE post_id = $1 GROUP BY option_id
	`, postID)
	if err != nil {
		return nil, err
	}
	defer countRows.Close()

	voteCounts := make(map[uuid.UUID]int64)
	for countRows.Next() {
		var optID uuid.UUID
		var count int64
		if err := countRows.Scan(&optID, &count); err != nil {
			continue
		}
		voteCounts[optID] = count
		poll.TotalVotes += count
	}

	// Apply counts + percentages
	for i := range poll.Options {
		poll.Options[i].VoteCount = voteCounts[poll.Options[i].ID]
		if poll.TotalVotes > 0 {
			poll.Options[i].Percentage = float64(poll.Options[i].VoteCount) / float64(poll.TotalVotes) * 100
		}
	}

	return &poll, nil
}

// GetUserPollVotes returns which option IDs a user voted for on a poll.
func (s *Store) GetUserPollVotes(ctx context.Context, postID, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT option_id FROM poll_votes WHERE post_id = $1 AND user_id = $2
	`, postID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var votes []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		votes = append(votes, id)
	}
	return votes, nil
}

// CastVote records a vote. Uses ON CONFLICT to prevent duplicates.
func (s *Store) CastVote(ctx context.Context, postID, optionID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO poll_votes (post_id, option_id, user_id, created_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (post_id, user_id, option_id) DO NOTHING
	`, postID, optionID, userID)
	return err
}

// HasPoll checks if a post has an associated poll.
func (s *Store) HasPoll(ctx context.Context, postID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM polls WHERE post_id = $1)`, postID).Scan(&exists)
	return exists, err
}

// UpdatePostContentType updates the content_type of a post.
func (s *Store) UpdatePostContentType(ctx context.Context, postID uuid.UUID, contentType string) error {
	_, err := s.db.Exec(ctx, `UPDATE posts SET content_type = $2, updated_at = NOW() WHERE id = $1`, postID, contentType)
	return err
}

// GetPostAuthorID returns the author_id for a post.
func (s *Store) GetPostAuthorID(ctx context.Context, postID uuid.UUID) (uuid.UUID, error) {
	var authorID uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT author_id FROM posts WHERE id = $1 AND deleted_at IS NULL`, postID).Scan(&authorID)
	return authorID, err
}

// UpdatePostCoverMedia updates the cover_media_id of a post.
func (s *Store) UpdatePostCoverMedia(ctx context.Context, postID uuid.UUID, coverMediaID *uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE posts SET cover_media_id = $2, updated_at = NOW() WHERE id = $1`, postID, coverMediaID)
	return err
}

// PublishPost sets publish status and published_at on a post.
func (s *Store) PublishPost(ctx context.Context, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE posts SET visibility = 'public', updated_at = NOW() WHERE id = $1
	`, postID)
	return err
}

// --- Bookmark methods ---

// AddBookmark adds a post to the user's bookmarks.
func (s *Store) AddBookmark(ctx context.Context, userID, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO bookmarks (user_id, post_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_id, post_id) DO NOTHING
	`, userID, postID)
	return err
}

// RemoveBookmark removes a post from the user's bookmarks.
func (s *Store) RemoveBookmark(ctx context.Context, userID, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM bookmarks WHERE user_id = $1 AND post_id = $2
	`, userID, postID)
	return err
}

// IsBookmarked checks whether a user has bookmarked a specific post.
func (s *Store) IsBookmarked(ctx context.Context, userID, postID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM bookmarks WHERE user_id = $1 AND post_id = $2)
	`, userID, postID).Scan(&exists)
	return exists, err
}

// GetBookmarks returns paginated bookmarked posts for a user.
func (s *Store) GetBookmarks(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]Post, string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var args []interface{}
	args = append(args, userID, limit+1)

	query := `SELECT ` + postCols + `
		FROM posts p
		INNER JOIN bookmarks b ON b.post_id = p.id
		WHERE b.user_id = $1 AND p.deleted_at IS NULL`

	if cursor != "" {
		cursorTime, err := time.Parse(time.RFC3339Nano, cursor)
		if err == nil {
			query += ` AND b.created_at < $3`
			args = append(args, cursorTime)
		}
	}

	query += ` ORDER BY b.created_at DESC LIMIT $2`

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

	return posts, nextCursor, nil
}

// GetPostsByIDs returns posts matching the given IDs (excluding soft-deleted).
func (s *Store) GetPostsByIDs(ctx context.Context, ids []uuid.UUID) ([]Post, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	rows, err := s.db.Query(ctx, `
		SELECT `+postCols+`
		FROM posts
		WHERE id = ANY($1) AND deleted_at IS NULL
	`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	posts, err := scanPostRows(rows)
	if err != nil {
		return nil, err
	}

	// Batch-fetch media for all posts
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

	return posts, nil
}
