package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ReelDraft represents a reel draft row in the reel_drafts table.
type ReelDraft struct {
	ID                 uuid.UUID  `json:"id"`
	AuthorID           uuid.UUID  `json:"author_id"`
	MediaID            *uuid.UUID `json:"media_id,omitempty"`
	Title              string     `json:"title"`
	Caption            string     `json:"caption"`
	Hashtags           []string   `json:"hashtags"`
	Tags               []string   `json:"tags"`
	Visibility         string     `json:"visibility"`
	TopicID            *int       `json:"topic_id,omitempty"`
	Category           string     `json:"category"`
	Language           string     `json:"language"`
	SEOTitle           string     `json:"seo_title"`
	CrossPostPostbook  bool       `json:"cross_post_postbook"`
	CrossPostPosttube  bool       `json:"cross_post_posttube"`
	PublishToFeed      bool       `json:"publish_to_feed"`
	IsMadeForKids      bool       `json:"is_made_for_kids"`
	PaidPromotion      bool       `json:"paid_promotion"`
	AlteredContent     bool       `json:"altered_content"`
	AutoChapters       bool       `json:"auto_chapters"`
	FeaturedPlaces     bool       `json:"featured_places"`
	AutoConcepts       bool       `json:"auto_concepts"`
	License            string     `json:"license"`
	AllowEmbedding     bool       `json:"allow_embedding"`
	RemixSetting       string     `json:"remix_setting"`
	LikesEnabled       bool       `json:"likes_enabled"`
	CommentsEnabled    bool       `json:"comments_enabled"`
	CommentModeration  string     `json:"comment_moderation"`
	CommentAccess      string     `json:"comment_access"`
	RecordingDate      *time.Time `json:"recording_date,omitempty"`
	RecordingLocation  string     `json:"recording_location"`
	AudioTrackID       *string    `json:"audio_track_id,omitempty"`
	AudioStartMs       int        `json:"audio_start_ms"`
	OriginalAudioVol   float32    `json:"original_audio_volume"`
	OverlayAudioVol    float32    `json:"overlay_audio_volume"`
	CoverMediaID       *uuid.UUID `json:"cover_media_id,omitempty"`
	ScheduleAt         *time.Time `json:"schedule_at,omitempty"`
	Status             string     `json:"status"`
	ModerationStatus   string     `json:"moderation_status"`
	PublishedPostID    *uuid.UUID `json:"published_post_id,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

const draftCols = `id, author_id, media_id,
	title, caption, hashtags, tags,
	visibility, topic_id, category, language, seo_title,
	cross_post_postbook, cross_post_posttube, publish_to_feed,
	is_made_for_kids, paid_promotion, altered_content,
	auto_chapters, featured_places, auto_concepts,
	license, allow_embedding, remix_setting,
	likes_enabled, comments_enabled, comment_moderation, comment_access,
	recording_date, recording_location,
	audio_track_id, audio_start_ms, original_audio_volume, overlay_audio_volume,
	cover_media_id, schedule_at,
	status, moderation_status, published_post_id,
	created_at, updated_at`

func scanDraft(row pgx.Row) (*ReelDraft, error) {
	var d ReelDraft
	err := row.Scan(
		&d.ID, &d.AuthorID, &d.MediaID,
		&d.Title, &d.Caption, &d.Hashtags, &d.Tags,
		&d.Visibility, &d.TopicID, &d.Category, &d.Language, &d.SEOTitle,
		&d.CrossPostPostbook, &d.CrossPostPosttube, &d.PublishToFeed,
		&d.IsMadeForKids, &d.PaidPromotion, &d.AlteredContent,
		&d.AutoChapters, &d.FeaturedPlaces, &d.AutoConcepts,
		&d.License, &d.AllowEmbedding, &d.RemixSetting,
		&d.LikesEnabled, &d.CommentsEnabled, &d.CommentModeration, &d.CommentAccess,
		&d.RecordingDate, &d.RecordingLocation,
		&d.AudioTrackID, &d.AudioStartMs, &d.OriginalAudioVol, &d.OverlayAudioVol,
		&d.CoverMediaID, &d.ScheduleAt,
		&d.Status, &d.ModerationStatus, &d.PublishedPostID,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func scanDraftRows(rows pgx.Rows) ([]ReelDraft, error) {
	var drafts []ReelDraft
	for rows.Next() {
		var d ReelDraft
		if err := rows.Scan(
			&d.ID, &d.AuthorID, &d.MediaID,
			&d.Title, &d.Caption, &d.Hashtags, &d.Tags,
			&d.Visibility, &d.TopicID, &d.Category, &d.Language, &d.SEOTitle,
			&d.CrossPostPostbook, &d.CrossPostPosttube, &d.PublishToFeed,
			&d.IsMadeForKids, &d.PaidPromotion, &d.AlteredContent,
			&d.AutoChapters, &d.FeaturedPlaces, &d.AutoConcepts,
			&d.License, &d.AllowEmbedding, &d.RemixSetting,
			&d.LikesEnabled, &d.CommentsEnabled, &d.CommentModeration, &d.CommentAccess,
			&d.RecordingDate, &d.RecordingLocation,
			&d.AudioTrackID, &d.AudioStartMs, &d.OriginalAudioVol, &d.OverlayAudioVol,
			&d.CoverMediaID, &d.ScheduleAt,
			&d.Status, &d.ModerationStatus, &d.PublishedPostID,
			&d.CreatedAt, &d.UpdatedAt,
		); err != nil {
			return nil, err
		}
		drafts = append(drafts, d)
	}
	return drafts, rows.Err()
}

// CreateDraft inserts a new reel draft.
func (s *Store) CreateDraft(ctx context.Context, d *ReelDraft) error {
	now := time.Now().UTC()
	d.CreatedAt = now
	d.UpdatedAt = now
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	if d.Status == "" {
		d.Status = "draft"
	}
	if d.ModerationStatus == "" {
		d.ModerationStatus = "pending"
	}

	_, err := s.db.Exec(ctx, `
		INSERT INTO reel_drafts (`+draftCols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,$40,$41)`,
		d.ID, d.AuthorID, d.MediaID,
		d.Title, d.Caption, d.Hashtags, d.Tags,
		d.Visibility, d.TopicID, d.Category, d.Language, d.SEOTitle,
		d.CrossPostPostbook, d.CrossPostPosttube, d.PublishToFeed,
		d.IsMadeForKids, d.PaidPromotion, d.AlteredContent,
		d.AutoChapters, d.FeaturedPlaces, d.AutoConcepts,
		d.License, d.AllowEmbedding, d.RemixSetting,
		d.LikesEnabled, d.CommentsEnabled, d.CommentModeration, d.CommentAccess,
		d.RecordingDate, d.RecordingLocation,
		d.AudioTrackID, d.AudioStartMs, d.OriginalAudioVol, d.OverlayAudioVol,
		d.CoverMediaID, d.ScheduleAt,
		d.Status, d.ModerationStatus, d.PublishedPostID,
		d.CreatedAt, d.UpdatedAt,
	)
	return err
}

// GetDraft fetches a single draft by ID.
func (s *Store) GetDraft(ctx context.Context, draftID uuid.UUID) (*ReelDraft, error) {
	row := s.db.QueryRow(ctx, `SELECT `+draftCols+` FROM reel_drafts WHERE id = $1`, draftID)
	return scanDraft(row)
}

// UpdateDraft applies partial updates to a draft. Only non-nil fields in the map are updated.
func (s *Store) UpdateDraft(ctx context.Context, draftID uuid.UUID, authorID uuid.UUID, updates map[string]interface{}) error {
	updates["updated_at"] = time.Now().UTC()

	setClauses := ""
	args := []interface{}{}
	i := 1
	for col, val := range updates {
		if setClauses != "" {
			setClauses += ", "
		}
		setClauses += col + " = $" + itoa(i)
		args = append(args, val)
		i++
	}
	args = append(args, draftID, authorID)

	_, err := s.db.Exec(ctx,
		`UPDATE reel_drafts SET `+setClauses+` WHERE id = $`+itoa(i)+` AND author_id = $`+itoa(i+1),
		args...,
	)
	return err
}

// ListDrafts returns drafts for a given author, newest first.
func (s *Store) ListDrafts(ctx context.Context, authorID uuid.UUID, limit int, cursor *time.Time) ([]ReelDraft, *time.Time, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var rows pgx.Rows
	var err error
	if cursor != nil {
		rows, err = s.db.Query(ctx,
			`SELECT `+draftCols+` FROM reel_drafts
			 WHERE author_id = $1 AND status != 'deleted' AND created_at < $2
			 ORDER BY created_at DESC LIMIT $3`,
			authorID, *cursor, limit+1,
		)
	} else {
		rows, err = s.db.Query(ctx,
			`SELECT `+draftCols+` FROM reel_drafts
			 WHERE author_id = $1 AND status != 'deleted'
			 ORDER BY created_at DESC LIMIT $2`,
			authorID, limit+1,
		)
	}
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	drafts, err := scanDraftRows(rows)
	if err != nil {
		return nil, nil, err
	}

	var nextCursor *time.Time
	if len(drafts) > limit {
		t := drafts[limit].CreatedAt
		nextCursor = &t
		drafts = drafts[:limit]
	}
	return drafts, nextCursor, nil
}

// DeleteDraft soft-deletes a draft (sets status = 'deleted').
func (s *Store) DeleteDraft(ctx context.Context, draftID uuid.UUID, authorID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE reel_drafts SET status = 'deleted', updated_at = $1 WHERE id = $2 AND author_id = $3`,
		time.Now().UTC(), draftID, authorID,
	)
	return err
}

// GetScheduledDrafts returns drafts whose schedule_at has passed and are still in 'draft' status.
func (s *Store) GetScheduledDrafts(ctx context.Context, now time.Time, limit int) ([]ReelDraft, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+draftCols+` FROM reel_drafts
		 WHERE schedule_at IS NOT NULL AND schedule_at <= $1 AND status = 'draft'
		 ORDER BY schedule_at ASC LIMIT $2`,
		now, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDraftRows(rows)
}

// MarkDraftPublished updates a draft's status to 'published' and links it to the post.
func (s *Store) MarkDraftPublished(ctx context.Context, draftID uuid.UUID, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE reel_drafts SET status = 'published', published_post_id = $1, updated_at = $2 WHERE id = $3`,
		postID, time.Now().UTC(), draftID,
	)
	return err
}

// itoa converts an int to string without importing strconv.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
