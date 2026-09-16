package postgres

// Admin console, Wave 2 — Content (Social and Tube). Store support for the
// admin-service token family /v1/posts/internal/admin: the post_admin_audit
// writer (migration 049), content kinds, the review queue, a post's
// moderation history, and the dashboard counts.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrAdminAuditActor: an audited admin write was attempted without an actor.
// The write is refused, never left unaudited.
var ErrAdminAuditActor = errors.New("admin audit: actor required")

// ErrPostNotFound: no posts row with that id (soft-deleted rows still exist).
var ErrPostNotFound = errors.New("post not found")

// insertAdminAudit appends one post_admin_audit row inside the caller's
// transaction, so the change and its audit commit or roll back together.
func insertAdminAudit(ctx context.Context, tx pgx.Tx, actor uuid.UUID, action, targetType string, targetID uuid.UUID, previous, next, note string) error {
	if actor == uuid.Nil {
		return ErrAdminAuditActor
	}
	var notePtr *string
	if n := strings.TrimSpace(note); n != "" {
		notePtr = &n
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO post_admin_audit (actor_user_id, action, target_type, target_id, previous_value, new_value, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, actor, action, targetType, targetID, previous, next, notePtr); err != nil {
		return fmt.Errorf("admin audit %s: %w", action, err)
	}
	return nil
}

// AdminAuditEntry is one post_admin_audit row.
type AdminAuditEntry struct {
	ID            uuid.UUID `json:"id"`
	ActorUserID   uuid.UUID `json:"actor_user_id"`
	Action        string    `json:"action"`
	TargetType    string    `json:"target_type"`
	TargetID      uuid.UUID `json:"target_id"`
	PreviousValue string    `json:"previous_value"`
	NewValue      string    `json:"new_value"`
	Note          *string   `json:"note,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// ListAdminAudit returns the audit rows for one target, newest first.
func (s *Store) ListAdminAudit(ctx context.Context, targetType string, targetID uuid.UUID) ([]AdminAuditEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, actor_user_id, action, target_type, target_id, previous_value, new_value, note, created_at
		FROM post_admin_audit WHERE target_type = $1 AND target_id = $2
		ORDER BY created_at DESC, id
	`, targetType, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminAuditEntry
	for rows.Next() {
		var e AdminAuditEntry
		if err := rows.Scan(&e.ID, &e.ActorUserID, &e.Action, &e.TargetType, &e.TargetID,
			&e.PreviousValue, &e.NewValue, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Content kinds for admin permissions. Videos are Tube; reels and every other
// post (text, poll, voice, crosspost embeds) are Social.
const (
	ContentKindPost  = "post"
	ContentKindReel  = "reel"
	ContentKindVideo = "video"
)

// reelContentTypes and videoContentTypes follow shared/postclassify
// (IsShortForm / IsLongForm) including the legacy synonyms rows still carry.
var (
	reelContentTypes  = []string{"flick", "reel", "short"}
	videoContentTypes = []string{"long_video", "video"}
)

// ContentKindOf maps a posts.content_type to its admin kind.
func ContentKindOf(contentType string) string {
	for _, t := range reelContentTypes {
		if contentType == t {
			return ContentKindReel
		}
	}
	for _, t := range videoContentTypes {
		if contentType == t {
			return ContentKindVideo
		}
	}
	return ContentKindPost
}

// kindPredicate is a SQL predicate on column (a content_type) selecting one
// kind, with param the placeholder bound to kindTypes(kind).
func kindPredicate(kind, column, param string) string {
	if kind == ContentKindReel || kind == ContentKindVideo {
		return column + " = ANY(" + param + "::text[])"
	}
	return "NOT (" + column + " = ANY(" + param + "::text[]))"
}

// kindTypes is the content_type array kindPredicate compares against.
func kindTypes(kind string) []string {
	switch kind {
	case ContentKindReel:
		return reelContentTypes
	case ContentKindVideo:
		return videoContentTypes
	default:
		return append(append([]string{}, reelContentTypes...), videoContentTypes...)
	}
}

// PostContentType returns a post's content_type, soft-deleted rows included
// (ErrPostNotFound when there is no row). Admin routes use it to pick the
// permission a post needs before touching it.
func (s *Store) PostContentType(ctx context.Context, postID uuid.UUID) (string, error) {
	var ct string
	err := s.db.QueryRow(ctx, `SELECT content_type FROM posts WHERE id = $1`, postID).Scan(&ct)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrPostNotFound
	}
	return ct, err
}

// ReviewQueueItem is one post awaiting a moderator: flagged for review, or
// staged waiting for its visibility to be finalised.
type ReviewQueueItem struct {
	ID           uuid.UUID `json:"id"`
	AuthorID     uuid.UUID `json:"author_id"`
	ContentType  string    `json:"content_type"`
	Kind         string    `json:"kind"`
	Visibility   string    `json:"visibility"`
	ReviewStatus string    `json:"review_status"`
	Text         string    `json:"text"`
	CreatedAt    time.Time `json:"created_at"`
}

// ListReviewQueue lists live posts of one kind in one queue ("flagged":
// review_status flagged; "staged": visibility staged), newest first, before
// the cursor. Text is cut to 500 characters.
func (s *Store) ListReviewQueue(ctx context.Context, queue, kind string, before time.Time, limit int) ([]ReviewQueueItem, error) {
	var queuePred string
	switch queue {
	case "flagged":
		queuePred = "review_status = 'flagged'"
	case "staged":
		queuePred = "visibility = 'staged'"
	default:
		return nil, fmt.Errorf("unknown review queue %q", queue)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if before.IsZero() {
		before = time.Now()
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, author_id, content_type, visibility, review_status, LEFT(COALESCE(text, ''), 500), created_at
		FROM posts
		WHERE deleted_at IS NULL AND `+queuePred+` AND `+kindPredicate(kind, "content_type", "$1")+`
		  AND created_at < $2
		ORDER BY created_at DESC
		LIMIT $3
	`, kindTypes(kind), before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewQueueItem{}
	for rows.Next() {
		var it ReviewQueueItem
		if err := rows.Scan(&it.ID, &it.AuthorID, &it.ContentType, &it.Visibility, &it.ReviewStatus, &it.Text, &it.CreatedAt); err != nil {
			return nil, err
		}
		it.Kind = ContentKindOf(it.ContentType)
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListModerationDecisions returns a post's post_moderation_decisions rows,
// newest first.
func (s *Store) ListModerationDecisions(ctx context.Context, postID uuid.UUID) ([]ModerationDecision, error) {
	rows, err := s.db.Query(ctx, `
		SELECT decision_id, post_id, actor_id, action, reason, source, source_ref_id,
		       previous_status, resulting_status, changed, created_at
		FROM post_moderation_decisions WHERE post_id = $1
		ORDER BY created_at DESC, decision_id
	`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModerationDecision
	for rows.Next() {
		var d ModerationDecision
		if err := rows.Scan(&d.DecisionID, &d.PostID, &d.ActorID, &d.Action, &d.Reason, &d.Source,
			&d.SourceRefID, &d.PreviousStatus, &d.ResultingStatus, &d.Changed, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SocialAdminStats are the Social dashboard counts (social:stats.read).
// "Today" starts at midnight India time. Takedowns are changes TO rejected
// (post_moderation_decisions reject that changed status, plus post_review_audit
// review_status → rejected) and comment changes to hidden or removed.
type SocialAdminStats struct {
	FlaggedPostsPending           int `json:"flagged_posts_pending"`
	FlaggedReelsPending           int `json:"flagged_reels_pending"`
	ReelReviewQueuePending        int `json:"reel_review_queue_pending"`
	OpenContentReports            int `json:"open_content_reports"`
	PostsCreatedToday             int `json:"posts_created_today"`
	PostsCreatedLast7Days         int `json:"posts_created_last_7_days"`
	ReelsCreatedToday             int `json:"reels_created_today"`
	ReelsCreatedLast7Days         int `json:"reels_created_last_7_days"`
	PostTakedownsLast7Days        int `json:"post_takedowns_last_7_days"`
	ReelTakedownsLast7Days        int `json:"reel_takedowns_last_7_days"`
	CommentTakedownsLast7Days     int `json:"comment_takedowns_last_7_days"`
	StagedPostsAwaitingVisibility int `json:"staged_posts_awaiting_visibility"`

	DayStartsAt time.Time `json:"day_starts_at"`
	GeneratedAt time.Time `json:"generated_at"`
}

// TubeAdminStats are the Tube dashboard counts (tube:stats.read), same rules.
type TubeAdminStats struct {
	FlaggedVideosPending           int `json:"flagged_videos_pending"`
	OpenVideoReports               int `json:"open_video_reports"`
	VideosCreatedToday             int `json:"videos_created_today"`
	VideosCreatedLast7Days         int `json:"videos_created_last_7_days"`
	VideoTakedownsLast7Days        int `json:"video_takedowns_last_7_days"`
	StagedVideosAwaitingVisibility int `json:"staged_videos_awaiting_visibility"`
	ChannelsCreatedLast7Days       int `json:"channels_created_last_7_days"`

	DayStartsAt time.Time `json:"day_starts_at"`
	GeneratedAt time.Time `json:"generated_at"`
}

// statsBounds is the shared CTE: IST day start, 7 days back, now.
const statsBounds = `
	WITH bounds AS (
		SELECT (date_trunc('day', now() AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata') AS day_start,
		       now() - interval '7 days' AS week_start,
		       now() AS generated_at
	)`

// takedownsSQL counts post takedowns in the last 7 days for posts of one kind:
// %s is kindPredicate on p.content_type.
const takedownsSQL = `(
		(SELECT COUNT(*) FROM post_moderation_decisions d JOIN posts p ON p.id = d.post_id, bounds
			WHERE d.action = 'reject' AND d.changed AND d.created_at >= bounds.week_start AND %[1]s)
		+
		(SELECT COUNT(*) FROM post_review_audit a JOIN posts p ON p.id = a.post_id, bounds
			WHERE a.field = 'review_status' AND a.new_value = 'rejected' AND a.created_at >= bounds.week_start AND %[1]s)
	)::int`

// SocialAdminStats reads the Social dashboard counts in one round trip.
// $1 is the post-kind array, $2 the reel-kind array.
func (s *Store) SocialAdminStats(ctx context.Context) (*SocialAdminStats, error) {
	post := kindPredicate(ContentKindPost, "content_type", "$1")
	reel := kindPredicate(ContentKindReel, "content_type", "$2")
	out := &SocialAdminStats{}
	err := s.db.QueryRow(ctx, statsBounds+`
		SELECT
			(SELECT COUNT(*) FROM posts WHERE deleted_at IS NULL AND review_status = 'flagged' AND `+post+`)::int,
			(SELECT COUNT(*) FROM posts WHERE deleted_at IS NULL AND review_status = 'flagged' AND `+reel+`)::int,
			(SELECT COUNT(*) FROM (
				SELECT DISTINCT ON (reel_id) decision FROM moderation_reviews ORDER BY reel_id, created_at DESC
			) latest WHERE latest.decision IN ('flagged', 'pending_review'))::int,
			(SELECT COUNT(*) FROM content_reports WHERE status = 'pending' AND target_type IN ('post', 'comment', 'reel'))::int,
			(SELECT COUNT(*) FROM posts, bounds WHERE posts.created_at >= bounds.day_start AND `+post+`)::int,
			(SELECT COUNT(*) FROM posts, bounds WHERE posts.created_at >= bounds.week_start AND `+post+`)::int,
			(SELECT COUNT(*) FROM posts, bounds WHERE posts.created_at >= bounds.day_start AND `+reel+`)::int,
			(SELECT COUNT(*) FROM posts, bounds WHERE posts.created_at >= bounds.week_start AND `+reel+`)::int,
			`+fmt.Sprintf(takedownsSQL, kindPredicate(ContentKindPost, "p.content_type", "$1"))+`,
			`+fmt.Sprintf(takedownsSQL, kindPredicate(ContentKindReel, "p.content_type", "$2"))+`,
			(SELECT COUNT(*) FROM post_admin_audit, bounds
				WHERE action = 'comment.moderate' AND new_value IN ('hidden', 'removed')
				  AND previous_value NOT IN ('hidden', 'removed') AND post_admin_audit.created_at >= bounds.week_start)::int,
			(SELECT COUNT(*) FROM posts WHERE deleted_at IS NULL AND visibility = 'staged'
				AND (`+post+` OR `+reel+`))::int,
			bounds.day_start, bounds.generated_at
		FROM bounds`, kindTypes(ContentKindPost), kindTypes(ContentKindReel)).Scan(
		&out.FlaggedPostsPending, &out.FlaggedReelsPending, &out.ReelReviewQueuePending, &out.OpenContentReports,
		&out.PostsCreatedToday, &out.PostsCreatedLast7Days, &out.ReelsCreatedToday, &out.ReelsCreatedLast7Days,
		&out.PostTakedownsLast7Days, &out.ReelTakedownsLast7Days, &out.CommentTakedownsLast7Days,
		&out.StagedPostsAwaitingVisibility, &out.DayStartsAt, &out.GeneratedAt)
	if err != nil {
		return nil, fmt.Errorf("social admin stats: %w", err)
	}
	return out, nil
}

// TubeAdminStats reads the Tube dashboard counts in one round trip.
func (s *Store) TubeAdminStats(ctx context.Context) (*TubeAdminStats, error) {
	video := kindPredicate(ContentKindVideo, "content_type", "$1")
	out := &TubeAdminStats{}
	err := s.db.QueryRow(ctx, statsBounds+`
		SELECT
			(SELECT COUNT(*) FROM posts WHERE deleted_at IS NULL AND review_status = 'flagged' AND `+video+`)::int,
			(SELECT COUNT(*) FROM content_reports WHERE status = 'pending' AND target_type = 'video')::int,
			(SELECT COUNT(*) FROM posts, bounds WHERE posts.created_at >= bounds.day_start AND `+video+`)::int,
			(SELECT COUNT(*) FROM posts, bounds WHERE posts.created_at >= bounds.week_start AND `+video+`)::int,
			`+fmt.Sprintf(takedownsSQL, kindPredicate(ContentKindVideo, "p.content_type", "$1"))+`,
			(SELECT COUNT(*) FROM posts WHERE deleted_at IS NULL AND visibility = 'staged' AND `+video+`)::int,
			(SELECT COUNT(*) FROM channels, bounds WHERE channels.created_at >= bounds.week_start)::int,
			bounds.day_start, bounds.generated_at
		FROM bounds`, kindTypes(ContentKindVideo)).Scan(
		&out.FlaggedVideosPending, &out.OpenVideoReports, &out.VideosCreatedToday, &out.VideosCreatedLast7Days,
		&out.VideoTakedownsLast7Days, &out.StagedVideosAwaitingVisibility, &out.ChannelsCreatedLast7Days,
		&out.DayStartsAt, &out.GeneratedAt)
	if err != nil {
		return nil, fmt.Errorf("tube admin stats: %w", err)
	}
	return out, nil
}
