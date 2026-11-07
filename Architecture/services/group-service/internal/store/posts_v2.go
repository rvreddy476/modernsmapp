package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

// V2 rich group post (migration 006 schema)
type GroupPostV2 struct {
	ID             uuid.UUID       `json:"id"`
	GroupID        uuid.UUID       `json:"group_id"`
	ChannelID      *uuid.UUID      `json:"channel_id,omitempty"`
	AuthorID       string          `json:"author_id"`
	ContentType    string          `json:"content_type"`
	Title          *string         `json:"title,omitempty"`
	Body           *string         `json:"body,omitempty"`
	BodyHTML       *string         `json:"body_html,omitempty"`
	TypePayload    json.RawMessage `json:"type_payload,omitempty"`
	Attachments    json.RawMessage `json:"attachments,omitempty"`
	NeedsApproval  bool            `json:"needs_approval"`
	IsPinned       bool            `json:"is_pinned"`
	IsAnnouncement bool            `json:"is_announcement"`
	Status         string          `json:"status"`
	SparkCount     int             `json:"spark_count"`
	CommentCount   int             `json:"comment_count"`
	EchoCount      int             `json:"echo_count"`
	ViewCount      int             `json:"view_count"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	// Posted without the author's name shown to other members. The row still
	// carries the real AuthorID — bans, rate limits and ownership need it —
	// and MarshalJSON (anonymous.go) substitutes AnonAlias on the wire.
	IsAnonymous bool `json:"is_anonymous"`
	// The per-post pseudonym. json:"-" because it must never appear beside
	// the masked author_id: carrying both would let a client correlate two
	// posts by the same author, which is the whole thing the alias prevents.
	AnonAlias *uuid.UUID `json:"-"`
	// Set when this post was one target of a cross-post. The copies are
	// independent rows sharing this id; nothing reads it on the feed path,
	// deliberately — see the migration.
	CrossPostGroupID *uuid.UUID `json:"cross_post_group_id,omitempty"`
	// Viewer-relative engagement: whether the signed-in viewer has already
	// sparked / echoed / stashed this post. Readers that have a viewer in
	// scope populate these; an anonymous or non-reacting viewer gets false.
	ViewerSparked bool `json:"viewer_sparked"`
	ViewerEchoed  bool `json:"viewer_echoed"`
	ViewerStashed bool `json:"viewer_stashed"`
	// The viewer's one current reaction ("like", "love", …) or null. A legacy
	// heart reads as "like". ViewerSparked stays true whenever this is set.
	ViewerReaction *string `json:"viewer_reaction"`
	// People per reaction, e.g. {"like":3,"love":1}; {} when nobody has
	// reacted. Filled by attachReactionCounts on every client read surface;
	// never null on the wire (MarshalJSON normalises nil to {}).
	ReactionCounts map[string]int `json:"reaction_counts"`
}

// GroupMediaItem is one media-bearing post: attachments holds the media
// IDs the client renders via /v1/media/{id}/serve.
type GroupMediaItem struct {
	PostID      uuid.UUID       `json:"post_id"`
	AuthorID    string          `json:"author_id"`
	ContentType string          `json:"content_type"`
	Attachments json.RawMessage `json:"attachments"`
	CreatedAt   time.Time       `json:"created_at"`
	IsAnonymous bool            `json:"is_anonymous"`
}

// ListGroupMedia returns posts that carry attachments. Space posts live
// in the v2 group_posts table (attachments JSONB of media IDs) — the
// previous implementation joined the legacy posts table and therefore
// never matched anything.
func (s *Store) ListGroupMedia(ctx context.Context, groupID uuid.UUID, limit, offset int) ([]GroupMediaItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	/*
		The media grid masks anonymous authors too.

		This is the leak a reviewer misses: GroupMediaItem is its own struct,
		so it does NOT go through GroupPostV2.MarshalJSON. An anonymous post
		with a photo would have its author named here while the same post is
		masked in the feed — one endpoint quietly undoing the other.

		Masked in SQL rather than in Go because this projection has no
		GroupPostV2 to marshal: COALESCE hands back the alias for an anonymous
		row and the real author otherwise, so nothing downstream has to
		remember.
	*/
	rows, err := s.db.Query(ctx, `
		SELECT id,
		       CASE WHEN is_anonymous THEN COALESCE(anon_alias::text, '') ELSE author_id END,
		       content_type, attachments, created_at, is_anonymous
		FROM group_posts
		WHERE group_id = $1 AND status = 'published'
		  AND attachments IS NOT NULL AND jsonb_array_length(attachments) > 0
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, groupID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []GroupMediaItem
	for rows.Next() {
		var it GroupMediaItem
		if err := rows.Scan(&it.PostID, &it.AuthorID, &it.ContentType, &it.Attachments, &it.CreatedAt, &it.IsAnonymous); err != nil {
			return nil, err
		}
		if it.Attachments == nil {
			it.Attachments = json.RawMessage(`[]`)
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// ==================== V2 Group Posts ====================

const groupPostV2Columns = `id, group_id, channel_id, author_id, content_type, title, body, body_html,
	type_payload, attachments, needs_approval, is_pinned, is_announcement, status,
	spark_count, comment_count, echo_count, view_count, created_at, updated_at,
	is_anonymous, anon_alias, cross_post_group_id`

// groupPostV2ColumnsP is groupPostV2Columns qualified with the `p` alias, for
// the readers that join the per-viewer engagement tables.
const groupPostV2ColumnsP = `p.id, p.group_id, p.channel_id, p.author_id, p.content_type, p.title, p.body, p.body_html,
	p.type_payload, p.attachments, p.needs_approval, p.is_pinned, p.is_announcement, p.status,
	p.spark_count, p.comment_count, p.echo_count, p.view_count, p.created_at, p.updated_at,
	p.is_anonymous, p.anon_alias, p.cross_post_group_id`

// viewerEngagementColumns are the three viewer_* flags, in the order
// scanGroupPostV2WithViewer expects them. Always select these together with
// viewerEngagementJoins.
const viewerEngagementColumns = `(vs.user_id IS NOT NULL) AS viewer_sparked,
	(ve.user_id IS NOT NULL) AS viewer_echoed,
	(vt.user_id IS NOT NULL) AS viewer_stashed,
	vs.reaction AS viewer_reaction`

// viewerTextKey renders a viewer uuid as the TEXT user id the engagement
// tables store. An unset viewer becomes "", which matches no row.
func viewerTextKey(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

// viewerEngagementJoins left-joins the spark/echo/stash rows belonging to one
// viewer onto the `p` alias. param is the placeholder holding the viewer id
// (TEXT in this service, so callers pass actorID.String()). Each table is
// unique on (post_id, user_id), so these joins never duplicate a post row, and
// an empty viewer id simply matches nothing — an anonymous viewer gets false on
// every flag rather than an error.
func viewerEngagementJoins(param string) string {
	return `LEFT JOIN group_post_sparks vs ON vs.post_id = p.id AND vs.user_id = ` + param + `
		LEFT JOIN group_post_echoes ve ON ve.post_id = p.id AND ve.user_id = ` + param + `
		LEFT JOIN group_post_stashes vt ON vt.post_id = p.id AND vt.user_id = ` + param
}

func scanGroupPostV2(row pgx.Row) (*GroupPostV2, error) {
	var p GroupPostV2
	err := row.Scan(&p.ID, &p.GroupID, &p.ChannelID, &p.AuthorID, &p.ContentType,
		&p.Title, &p.Body, &p.BodyHTML, &p.TypePayload, &p.Attachments,
		&p.NeedsApproval, &p.IsPinned, &p.IsAnnouncement, &p.Status,
		&p.SparkCount, &p.CommentCount, &p.EchoCount, &p.ViewCount,
		&p.CreatedAt, &p.UpdatedAt, &p.IsAnonymous, &p.AnonAlias, &p.CrossPostGroupID)
	if err != nil {
		return nil, err
	}
	if p.TypePayload == nil {
		p.TypePayload = json.RawMessage(`{}`)
	}
	if p.Attachments == nil {
		p.Attachments = json.RawMessage(`[]`)
	}
	return &p, nil
}

// scanGroupPostV2WithViewer scans the groupPostV2ColumnsP +
// viewerEngagementColumns projection, in that order.
func scanGroupPostV2WithViewer(row pgx.Row) (*GroupPostV2, error) {
	var p GroupPostV2
	err := row.Scan(&p.ID, &p.GroupID, &p.ChannelID, &p.AuthorID, &p.ContentType,
		&p.Title, &p.Body, &p.BodyHTML, &p.TypePayload, &p.Attachments,
		&p.NeedsApproval, &p.IsPinned, &p.IsAnnouncement, &p.Status,
		&p.SparkCount, &p.CommentCount, &p.EchoCount, &p.ViewCount,
		&p.CreatedAt, &p.UpdatedAt, &p.IsAnonymous, &p.AnonAlias, &p.CrossPostGroupID,
		&p.ViewerSparked, &p.ViewerEchoed, &p.ViewerStashed, &p.ViewerReaction)
	if err != nil {
		return nil, err
	}
	if p.TypePayload == nil {
		p.TypePayload = json.RawMessage(`{}`)
	}
	if p.Attachments == nil {
		p.Attachments = json.RawMessage(`[]`)
	}
	return &p, nil
}

func (s *Store) CreateGroupPostV2(ctx context.Context, p *GroupPostV2) error {
	// A caller that claimed media against the post fixes its id first
	// (service createOnePost); otherwise the id is minted here.
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	p.CreatedAt = time.Now()
	p.UpdatedAt = p.CreatedAt
	if p.ContentType == "" {
		p.ContentType = "text"
	}
	if p.Status == "" {
		p.Status = "published"
	}
	if p.TypePayload == nil {
		p.TypePayload = json.RawMessage(`{}`)
	}
	if p.Attachments == nil {
		p.Attachments = json.RawMessage(`[]`)
	}

	/*
		The insert and the counter are ONE transaction.

		They used to be two bare Exec calls, so a failure between them left
		groups.post_count one ahead of reality for ever, with nothing to
		reconcile it. A single post drifting one is survivable; cross-posting
		makes the same path run five times per action, which is what turned a
		latent flaw into a reason to fix it.
	*/
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	// A rollback after a successful commit is a no-op, so this needs no flag.
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	query := `INSERT INTO group_posts (id, group_id, channel_id, author_id, content_type, title, body, body_html,
		type_payload, attachments, needs_approval, is_pinned, is_announcement, status,
		spark_count, comment_count, echo_count, view_count, created_at, updated_at,
		is_anonymous, anon_alias, cross_post_group_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,0,0,0,0,$15,$16,$17,$18,$19)`
	if _, err := tx.Exec(ctx, query,
		p.ID, p.GroupID, p.ChannelID, p.AuthorID, p.ContentType,
		p.Title, p.Body, p.BodyHTML, p.TypePayload, p.Attachments,
		p.NeedsApproval, p.IsPinned, p.IsAnnouncement, p.Status,
		p.CreatedAt, p.UpdatedAt, p.IsAnonymous, p.AnonAlias, p.CrossPostGroupID); err != nil {
		return err
	}

	/*
		A post awaiting approval is not IN the group yet, so it must not be
		counted as one. Before, every pending post inflated post_count and the
		number never came back down when the post was rejected.
	*/
	if p.Status == "published" {
		if _, err := tx.Exec(ctx,
			`UPDATE groups SET post_count = post_count + 1, updated_at = NOW() WHERE id = $1`, p.GroupID); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// GetGroupPostV2 reads one post without viewer context. The viewer_* flags are
// left false; ownership and membership checks use this. Read paths that render
// the post to a viewer should call GetGroupPostV2ForViewer instead.
func (s *Store) GetGroupPostV2(ctx context.Context, postID uuid.UUID) (*GroupPostV2, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+groupPostV2Columns+` FROM group_posts WHERE id = $1 AND status != 'deleted'`, postID)
	return scanGroupPostV2(row)
}

// GetGroupPostV2ForViewer is GetGroupPostV2 plus the viewer_* engagement flags.
// viewerID is the viewer's user id as TEXT ("" for anonymous, which yields
// false on every flag).
func (s *Store) GetGroupPostV2ForViewer(ctx context.Context, postID uuid.UUID, viewerID string) (*GroupPostV2, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+groupPostV2ColumnsP+`, `+viewerEngagementColumns+`
		FROM group_posts p
		`+viewerEngagementJoins("$2")+`
		WHERE p.id = $1 AND p.status != 'deleted'`, postID, viewerID)
	p, err := scanGroupPostV2WithViewer(row)
	if err != nil {
		return nil, err
	}
	one := []GroupPostV2{*p}
	if err := s.attachReactionCounts(ctx, one); err != nil {
		return nil, err
	}
	return &one[0], nil
}

// ListGroupPostsV2 lists a group's published posts. viewerID is the viewer's
// user id as TEXT ("" for anonymous) and drives the viewer_* flags.
/*
	ListPendingGroupPostsV2 returns the posts waiting for a moderator, read
	from group_posts itself.

	It does NOT read post_approval_queue. That table is written by nobody —
	store.AddToApprovalQueue has zero callers — so the queue endpoint has
	always returned an empty list while pending posts piled up invisibly, with
	no way for anyone to approve them. Its author_id column is also declared
	UUID in migration 003 and TEXT in setup.sql, so environments disagree
	about its shape.

	group_posts.status is the only place the truth has ever been, so that is
	what this reads.
*/
func (s *Store) ListPendingGroupPostsV2(ctx context.Context, groupID uuid.UUID, limit, offset int) ([]GroupPostV2, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx,
		`SELECT `+groupPostV2Columns+`
		FROM group_posts
		WHERE group_id = $1 AND status = 'pending_approval'
		ORDER BY created_at ASC LIMIT $2 OFFSET $3`,
		groupID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Oldest first: a moderation queue is worked from the front, and the
	// person who has waited longest should not be buried by newer arrivals.
	posts := []GroupPostV2{}
	for rows.Next() {
		p, err := scanGroupPostV2(rows)
		if err != nil {
			return nil, err
		}
		posts = append(posts, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.attachReactionCounts(ctx, posts); err != nil {
		return nil, err
	}
	return posts, nil
}

func (s *Store) ListGroupPostsV2(ctx context.Context, groupID uuid.UUID, channelID *uuid.UUID, viewerID string, limit, offset int) ([]GroupPostV2, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var rows pgx.Rows
	var err error
	if channelID != nil {
		rows, err = s.db.Query(ctx,
			`SELECT `+groupPostV2ColumnsP+`, `+viewerEngagementColumns+`
			FROM group_posts p
			`+viewerEngagementJoins("$5")+`
			WHERE p.group_id = $1 AND p.channel_id = $2 AND p.status = 'published'
			ORDER BY p.is_pinned DESC, p.created_at DESC LIMIT $3 OFFSET $4`,
			groupID, *channelID, limit, offset, viewerID)
	} else {
		rows, err = s.db.Query(ctx,
			`SELECT `+groupPostV2ColumnsP+`, `+viewerEngagementColumns+`
			FROM group_posts p
			`+viewerEngagementJoins("$4")+`
			WHERE p.group_id = $1 AND p.status = 'published'
			ORDER BY p.is_pinned DESC, p.created_at DESC LIMIT $2 OFFSET $3`,
			groupID, limit, offset, viewerID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []GroupPostV2
	for rows.Next() {
		p, err := scanGroupPostV2WithViewer(rows)
		if err != nil {
			return nil, err
		}
		posts = append(posts, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.attachReactionCounts(ctx, posts); err != nil {
		return nil, err
	}
	return posts, nil
}

func (s *Store) DeleteGroupPostV2(ctx context.Context, groupID, postID uuid.UUID) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE group_posts SET status = 'deleted', updated_at = NOW()
		WHERE id = $1 AND group_id = $2 AND status != 'deleted'`,
		postID, groupID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("not found: post not found")
	}
	_, err = s.db.Exec(ctx,
		`UPDATE groups SET post_count = GREATEST(post_count - 1, 0), updated_at = NOW() WHERE id = $1`, groupID)
	return err
}

func (s *Store) PinGroupPostV2(ctx context.Context, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE group_posts SET is_pinned = TRUE, updated_at = NOW() WHERE id = $1`, postID)
	return err
}

func (s *Store) UnpinGroupPostV2(ctx context.Context, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE group_posts SET is_pinned = FALSE, updated_at = NOW() WHERE id = $1`, postID)
	return err
}

// ==================== V2 Engagement ====================

// SparkGroupPost is the legacy heart. Since migration 016 the row it inserts
// is a 'like' reaction (the column default), so a heart from an old client
// and a Like from a new one are the same row and the same count. It keeps
// its 409 "already sparked" for ANY existing reaction — a legacy client that
// wants to change its mind unsparks first, as it always had to. Insert and
// count now commit together; they were two autocommit statements before.
func (s *Store) SparkGroupPost(ctx context.Context, postID uuid.UUID, userID string, isSupernova bool) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx,
		`INSERT INTO group_post_sparks (post_id, user_id, is_supernova, reaction, created_at, updated_at)
		 VALUES ($1, $2, $3, 'like', NOW(), NOW()) ON CONFLICT DO NOTHING`,
		postID, userID, isSupernova)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("already sparked")
	}
	weight := 1
	if isSupernova {
		weight = 5
	}
	if _, err := tx.Exec(ctx,
		`UPDATE group_posts SET spark_count = spark_count + $2, updated_at = NOW() WHERE id = $1`,
		postID, weight); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) UnsparkGroupPost(ctx context.Context, postID uuid.UUID, userID string) error {
	var isSupernova bool
	err := s.db.QueryRow(ctx,
		`DELETE FROM group_post_sparks WHERE post_id = $1 AND user_id = $2 RETURNING is_supernova`,
		postID, userID).Scan(&isSupernova)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("not found: spark not found")
	}
	if err != nil {
		return err
	}
	weight := 1
	if isSupernova {
		weight = 5
	}
	_, err = s.db.Exec(ctx,
		`UPDATE group_posts SET spark_count = GREATEST(spark_count - $2, 0), updated_at = NOW() WHERE id = $1`,
		postID, weight)
	return err
}

func (s *Store) StashGroupPost(ctx context.Context, postID uuid.UUID, userID string) error {
	query := `INSERT INTO group_post_stashes (post_id, user_id, created_at) VALUES ($1, $2, NOW()) ON CONFLICT DO NOTHING`
	tag, err := s.db.Exec(ctx, query, postID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("already stashed")
	}
	return nil
}

func (s *Store) UnstashGroupPost(ctx context.Context, postID uuid.UUID, userID string) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM group_post_stashes WHERE post_id = $1 AND user_id = $2`,
		postID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("not found: stash not found")
	}
	return nil
}

func (s *Store) RecordGroupPostView(ctx context.Context, postID uuid.UUID, userID string) error {
	query := `INSERT INTO group_post_views (post_id, user_id, viewed_at) VALUES ($1, $2, NOW()) ON CONFLICT DO NOTHING`
	tag, err := s.db.Exec(ctx, query, postID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil // already viewed
	}
	_, err = s.db.Exec(ctx,
		`UPDATE group_posts SET view_count = view_count + 1, updated_at = NOW() WHERE id = $1`, postID)
	return err
}

func (s *Store) EchoGroupPost(ctx context.Context, postID uuid.UUID, userID string, echoType string) error {
	query := `INSERT INTO group_post_echoes (id, post_id, user_id, echo_type, created_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (post_id, user_id) DO NOTHING`
	tag, err := s.db.Exec(ctx, query, uuid.New(), postID, userID, echoType)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("already echoed")
	}
	_, err = s.db.Exec(ctx,
		`UPDATE group_posts SET echo_count = echo_count + 1, updated_at = NOW() WHERE id = $1`,
		postID)
	return err
}

func (s *Store) UnechoGroupPost(ctx context.Context, postID uuid.UUID, userID string) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM group_post_echoes WHERE post_id = $1 AND user_id = $2`,
		postID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("not found: echo not found")
	}
	_, err = s.db.Exec(ctx,
		`UPDATE group_posts SET echo_count = GREATEST(echo_count - 1, 0), updated_at = NOW() WHERE id = $1`,
		postID)
	return err
}

// PostExistsInGroup validates a post belongs to a group using Redis cache.
func (s *Store) PostExistsInGroup(ctx context.Context, rdb *redis.Client, postID, groupID uuid.UUID) (bool, error) {
	cacheKey := fmt.Sprintf("gp:%s:%s", postID, groupID)
	if rdb != nil {
		val, err := rdb.Get(ctx, cacheKey).Result()
		if err == nil {
			return val == "1", nil
		}
	}
	// Cache miss → query DB (lightweight EXISTS check instead of full row scan)
	var exists bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM group_posts WHERE id = $1 AND group_id = $2 AND status != 'deleted')`,
		postID, groupID).Scan(&exists)
	if err != nil {
		return false, err
	}
	if exists && rdb != nil {
		rdb.Set(ctx, cacheKey, "1", 2*time.Minute)
	}
	return exists, nil
}
