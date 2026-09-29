package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Tube channels (2026-09-05). One channel per account, owned by post-service.
//
// The rows live in the shared `channels` table that user-service's phase-6
// DDL created (see migrations/041_channels.sql for why it is adapted rather
// than duplicated). `about` is the table's `description` column; the
// surrogate `id` is kept only for the video_series / playlists FKs and is
// never exposed — user_id is the identity the API speaks.

// Channel is the stored channel row.
type Channel struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	Name          string
	Handle        string
	About         string
	AvatarMediaID *uuid.UUID
	// SubscriberCount is channels.subscriber_count, kept in step by the
	// trigger migration 046 installs (see channel_subscriptions.go).
	SubscriberCount int
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// Branding (migration 051 part C). Links is never nil after a scan.
	BannerMediaID  *uuid.UUID
	Links          []ChannelLink
	ContactEmail   string
	FeaturedPostID *uuid.UUID
}

// ChannelLink is one entry of channels.links (JSONB array, at most 10).
type ChannelLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// ChannelPatch is a partial update. A nil field is "leave as is";
// ClearAvatar removes the avatar (JSON `"avatar_media_id": null`), and
// ClearBanner / ClearFeatured do the same for their fields.
type ChannelPatch struct {
	Name          *string
	Handle        *string
	About         *string
	AvatarMediaID *uuid.UUID
	ClearAvatar   bool
	// Branding (2026-09-27).
	BannerMediaID  *uuid.UUID
	ClearBanner    bool
	Links          *[]ChannelLink
	ContactEmail   *string
	FeaturedPostID *uuid.UUID
	ClearFeatured  bool
}

// ChannelContentCounts is the public content tally on a channel page.
type ChannelContentCounts struct {
	Videos      int `json:"video_count"`
	Shorts      int `json:"short_count"`
	Live        int `json:"live_count"`
	Collections int `json:"collection_count"`
}

var (
	// ErrChannelExists: the account already has its one channel.
	ErrChannelExists = errors.New("channel already exists for this account")
	// ErrHandleTaken: another channel owns the handle.
	ErrHandleTaken = errors.New("channel handle is taken")
	// ErrChannelNotFound: no channel for the user / handle.
	ErrChannelNotFound = errors.New("channel not found")
	// ErrChannelOwnerUnknown: the owner has no row in the shared users
	// table (the FK user-service placed on channels.user_id).
	ErrChannelOwnerUnknown = errors.New("channel owner is not a known user")
)

const channelColumns = `id, user_id, name, handle, description, avatar_media_id, subscriber_count, created_at, updated_at,
	banner_media_id, COALESCE(links, '[]'::jsonb), COALESCE(contact_email, ''), featured_post_id`

// channelScanDestinations is the one scan order for channelColumns; links
// lands in a raw JSON buffer that finishChannelScan decodes.
func channelScanDestinations(ch *Channel, links *[]byte) []any {
	return []any{&ch.ID, &ch.UserID, &ch.Name, &ch.Handle, &ch.About, &ch.AvatarMediaID, &ch.SubscriberCount, &ch.CreatedAt, &ch.UpdatedAt,
		&ch.BannerMediaID, links, &ch.ContactEmail, &ch.FeaturedPostID}
}

func finishChannelScan(ch *Channel, links []byte) error {
	ch.Links = []ChannelLink{}
	if len(links) == 0 {
		return nil
	}
	return json.Unmarshal(links, &ch.Links)
}

func scanChannel(row pgx.Row) (*Channel, error) {
	var ch Channel
	var links []byte
	if err := row.Scan(channelScanDestinations(&ch, &links)...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if err := finishChannelScan(&ch, links); err != nil {
		return nil, err
	}
	return &ch, nil
}

func channelPayload(ch *Channel) events.ChannelPayload {
	p := events.ChannelPayload{
		UserID:    ch.UserID.String(),
		Name:      ch.Name,
		Handle:    ch.Handle,
		About:     ch.About,
		CreatedAt: ch.CreatedAt,
		UpdatedAt: ch.UpdatedAt,
	}
	if ch.AvatarMediaID != nil {
		s := ch.AvatarMediaID.String()
		p.AvatarMediaID = &s
	}
	return p
}

// mapChannelWriteError turns the unique / FK violations the channels table
// can raise into the typed errors the service maps to 409 / 400.
func mapChannelWriteError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "23505": // unique_violation
		if strings.Contains(pgErr.ConstraintName, "handle") {
			return ErrHandleTaken
		}
		if strings.Contains(pgErr.ConstraintName, "user") {
			return ErrChannelExists
		}
		return ErrHandleTaken
	case "23503": // foreign_key_violation (channels.user_id -> users.id)
		return ErrChannelOwnerUnknown
	}
	return err
}

// CreateChannel inserts the account's channel and the tube.channel.created
// outbox event in one transaction. ch is filled in from the inserted row.
func (s *Store) CreateChannel(ctx context.Context, ch *Channel) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	row := tx.QueryRow(ctx, `
		INSERT INTO channels (user_id, name, handle, description, avatar_media_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+channelColumns,
		ch.UserID, ch.Name, ch.Handle, ch.About, ch.AvatarMediaID)
	created, err := scanChannel(row)
	if err != nil {
		return mapChannelWriteError(err)
	}
	*ch = *created

	if err := InsertOutboxEventTx(ctx, tx, events.TubeChannelCreated, "channel", ch.UserID, channelPayload(ch)); err != nil {
		return fmt.Errorf("enqueue channel.created: %w", err)
	}
	return tx.Commit(ctx)
}

// UpdateChannel applies a partial update to the account's channel and
// enqueues tube.channel.updated in the same transaction. Returns
// ErrChannelNotFound when the account has no channel.
func (s *Store) UpdateChannel(ctx context.Context, userID uuid.UUID, patch ChannelPatch) (*Channel, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var linksArg interface{}
	if patch.Links != nil {
		links := *patch.Links
		if links == nil {
			links = []ChannelLink{}
		}
		b, err := json.Marshal(links)
		if err != nil {
			return nil, fmt.Errorf("marshal channel links: %w", err)
		}
		linksArg = b
	}
	row := tx.QueryRow(ctx, `
		UPDATE channels SET
			name             = COALESCE($2, name),
			handle           = COALESCE($3, handle),
			description      = COALESCE($4, description),
			avatar_media_id  = CASE WHEN $6 THEN NULL ELSE COALESCE($5, avatar_media_id) END,
			banner_media_id  = CASE WHEN $8 THEN NULL ELSE COALESCE($7, banner_media_id) END,
			links            = COALESCE($9::jsonb, links),
			contact_email    = COALESCE($10, contact_email),
			featured_post_id = CASE WHEN $12 THEN NULL ELSE COALESCE($11, featured_post_id) END,
			updated_at       = now()
		WHERE user_id = $1
		RETURNING `+channelColumns,
		userID, patch.Name, patch.Handle, patch.About, patch.AvatarMediaID, patch.ClearAvatar,
		patch.BannerMediaID, patch.ClearBanner, linksArg, patch.ContactEmail, patch.FeaturedPostID, patch.ClearFeatured)
	updated, err := scanChannel(row)
	if err != nil {
		return nil, mapChannelWriteError(err)
	}
	if updated == nil {
		return nil, ErrChannelNotFound
	}

	if err := InsertOutboxEventTx(ctx, tx, events.TubeChannelUpdated, "channel", updated.UserID, channelPayload(updated)); err != nil {
		return nil, fmt.Errorf("enqueue channel.updated: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return updated, nil
}

// GetChannelByUserID returns the account's channel, or nil when it has none.
func (s *Store) GetChannelByUserID(ctx context.Context, userID uuid.UUID) (*Channel, error) {
	return scanChannel(s.db.QueryRow(ctx, `SELECT `+channelColumns+` FROM channels WHERE user_id = $1`, userID))
}

// GetChannelByHandle returns the channel owning a (lowercase) handle, or nil.
func (s *Store) GetChannelByHandle(ctx context.Context, handle string) (*Channel, error) {
	return scanChannel(s.db.QueryRow(ctx, `SELECT `+channelColumns+` FROM channels WHERE handle = $1`, handle))
}

// ChannelHandleExists reports whether any channel owns the handle.
func (s *Store) ChannelHandleExists(ctx context.Context, handle string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM channels WHERE handle = $1)`, handle).Scan(&exists)
	return exists, err
}

// GetChannelsByUserIDs loads the channels of a page of authors in one query.
// Authors without a channel are simply absent from the map.
func (s *Store) GetChannelsByUserIDs(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]*Channel, error) {
	out := make(map[uuid.UUID]*Channel, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT `+channelColumns+` FROM channels WHERE user_id = ANY($1)`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ch Channel
		var links []byte
		if err := rows.Scan(channelScanDestinations(&ch, &links)...); err != nil {
			return nil, err
		}
		if err := finishChannelScan(&ch, links); err != nil {
			return nil, err
		}
		c := ch
		out[c.UserID] = &c
	}
	return out, rows.Err()
}

// CountChannelContent is the channel page's public tally in ONE query:
// live, approved, public long videos and shorts by the owner, the posts
// that came from a live stream among them, and the owner's public user
// playlists. The service caches it for 60 s.
func (s *Store) CountChannelContent(ctx context.Context, userID uuid.UUID) (ChannelContentCounts, error) {
	var c ChannelContentCounts
	err := s.db.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN content_type IN ('long_video', 'video') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN content_type IN ('flick', 'reel') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN source = 'live' THEN 1 ELSE 0 END), 0),
			(SELECT COUNT(*) FROM playlists WHERE creator_id = $1 AND kind = 'user' AND visibility = 'public')
		FROM posts
		WHERE author_id = $1
		  AND deleted_at IS NULL
		  AND visibility = 'public'
		  AND `+viewerApprovedSQL+`
		  AND publish_at IS NULL`, userID,
	).Scan(&c.Videos, &c.Shorts, &c.Live, &c.Collections)
	return c, err
}

// channelVideoCountWhere is what "a video on the channel" means for the
// public count: a live, approved, public long video by the owner.
const channelVideoCountWhere = `
	author_id = ANY($1)
	AND content_type IN ('long_video', 'video')
	AND deleted_at IS NULL
	AND visibility = 'public'
	AND `+viewerApprovedSQL+`
	AND publish_at IS NULL`

// channelFeedWhere is what "a video in the channel's published feed" means
// (RSS, 2026-09-29): channelVideoCountWhere for ONE owner ($1) plus the two
// clauses only the signed-out audience needs. The feed is one document for
// the whole internet, so every row must be readable by a viewer with no
// identity: no date of birth to pass the 18+ gate, no membership to pass a
// tier. The Go mirror is service.anonymousMayAccessPost, which re-checks
// every row this predicate returns.
const channelFeedWhere = `
	author_id = $1
	AND content_type IN ('long_video', 'video')
	AND deleted_at IS NULL
	AND visibility = 'public'
	AND ` + viewerApprovedSQL + `
	AND publish_at IS NULL
	AND age_restricted = FALSE
	AND tier_required_id IS NULL`

// MaxChannelFeedVideos is the feed's page: the newest 50, no cursor.
const MaxChannelFeedVideos = 50

// ChannelFeedVideo is one feed row: the whole post (so the service can
// re-judge it) and its primary video asset as media_assets describes it.
// MediaID is nil when the post attaches no video at all.
type ChannelFeedVideo struct {
	Post             Post
	MediaID          *uuid.UUID
	DurationMs       int
	ProcessingStatus string
	ModerationStatus string
}

// ListChannelFeedVideos lists an owner's feed-eligible long videos, newest
// first by publication (never by pin), optionally narrowed to one taxonomy
// id. limit <= 0 or above MaxChannelFeedVideos reads MaxChannelFeedVideos.
func (s *Store) ListChannelFeedVideos(ctx context.Context, ownerID uuid.UUID, category string, limit int) ([]ChannelFeedVideo, error) {
	if limit <= 0 || limit > MaxChannelFeedVideos {
		limit = MaxChannelFeedVideos
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+postCols+`
		FROM posts
		WHERE `+channelFeedWhere+`
		  AND ($2 = '' OR category = $2)
		ORDER BY COALESCE(published_at, created_at) DESC, id DESC
		LIMIT $3`, ownerID, category, limit)
	if err != nil {
		return nil, err
	}
	posts, err := scanPostRows(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]ChannelFeedVideo, 0, len(posts))
	if len(posts) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, 0, len(posts))
	for i := range posts {
		ids = append(ids, posts[i].ID)
	}
	primary, err := s.channelFeedPrimaryVideos(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range posts {
		row := ChannelFeedVideo{Post: posts[i]}
		if v, ok := primary[posts[i].ID]; ok {
			id := v.mediaID
			row.MediaID = &id
			row.DurationMs = v.durationMs
			row.ProcessingStatus = v.processingStatus
			row.ModerationStatus = v.moderationStatus
		}
		out = append(out, row)
	}
	return out, nil
}

type channelFeedPrimary struct {
	mediaID          uuid.UUID
	durationMs       int
	processingStatus string
	moderationStatus string
}

// channelFeedPrimaryVideos resolves each post's primary video: the first
// video attachment in carousel order, with the duration and pipeline state
// media_assets holds for it.
func (s *Store) channelFeedPrimaryVideos(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]channelFeedPrimary, error) {
	out := make(map[uuid.UUID]channelFeedPrimary, len(postIDs))
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT ON (pm.post_id) pm.post_id, pm.media_id,
		       COALESCE(ma.duration_ms, ma.duration_seconds * 1000, 0),
		       COALESCE(ma.processing_status, ''), COALESCE(ma.moderation_status, '')
		FROM post_media pm JOIN media_assets ma ON ma.id = pm.media_id
		WHERE pm.post_id = ANY($1) AND ma.file_type = 'video'
		ORDER BY pm.post_id, pm.position NULLS LAST, pm.media_id`, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			postID uuid.UUID
			v      channelFeedPrimary
		)
		if err := rows.Scan(&postID, &v.mediaID, &v.durationMs, &v.processingStatus, &v.moderationStatus); err != nil {
			return nil, err
		}
		out[postID] = v
	}
	return out, rows.Err()
}

// ChannelFeedFacts is what the feed says about the channel as a whole.
type ChannelFeedFacts struct {
	// Language is channels.language when the owner set one, else the
	// language most of the feed's videos carry; "" when neither is known.
	Language string
	// DominantCategory is the taxonomy id most of the feed's videos carry
	// (ties broken by id order); "" when none is categorised.
	DominantCategory string
}

// ChannelFeedFacts reads the feed-wide facts for one owner over the same
// rows ListChannelFeedVideos can return (channelFeedWhere, every category).
func (s *Store) ChannelFeedFacts(ctx context.Context, ownerID uuid.UUID) (ChannelFeedFacts, error) {
	var f ChannelFeedFacts
	err := s.db.QueryRow(ctx, `
		SELECT
			COALESCE(NULLIF((SELECT language FROM channels WHERE user_id = $1), ''),
			         (SELECT language FROM posts WHERE `+channelFeedWhere+` AND COALESCE(language, '') <> ''
			          GROUP BY language ORDER BY COUNT(*) DESC, language ASC LIMIT 1), ''),
			COALESCE((SELECT category FROM posts WHERE `+channelFeedWhere+` AND COALESCE(category, '') <> ''
			          GROUP BY category ORDER BY COUNT(*) DESC, category ASC LIMIT 1), '')`, ownerID,
	).Scan(&f.Language, &f.DominantCategory)
	return f, err
}

// CountChannelVideos returns the public long-video count for one owner.
func (s *Store) CountChannelVideos(ctx context.Context, userID uuid.UUID) (int, error) {
	counts, err := s.CountChannelVideosBatch(ctx, []uuid.UUID{userID})
	if err != nil {
		return 0, err
	}
	return counts[userID], nil
}

// CountChannelVideosBatch returns public long-video counts per owner.
func (s *Store) CountChannelVideosBatch(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	out := make(map[uuid.UUID]int, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT author_id, COUNT(*) FROM posts WHERE `+channelVideoCountWhere+` GROUP BY author_id`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// ChannelSearchHit is one channel search row with its public video count,
// so the search page does not pay one count query per row.
type ChannelSearchHit struct {
	Channel
	VideoCount int
}

// channelVideoCountCorrelated is channelVideoCountWhere for one channel row
// (correlated on channels.user_id) — the same definition of "a video on the
// channel" as the public count.
const channelVideoCountCorrelated = `
	SELECT COUNT(*) FROM posts p
	WHERE p.author_id = c.user_id
	AND p.content_type IN ('long_video', 'video')
	AND p.deleted_at IS NULL
	AND p.visibility = 'public'
	AND p.`+viewerApprovedSQL+`
	AND p.publish_at IS NULL`

// EscapeLikePattern makes q safe to embed in a LIKE pattern: the wildcard
// and escape characters become literals (backslash is Postgres' default
// LIKE escape character).
func EscapeLikePattern(q string) string {
	q = strings.ReplaceAll(q, `\`, `\\`)
	q = strings.ReplaceAll(q, `%`, `\%`)
	q = strings.ReplaceAll(q, `_`, `\_`)
	return q
}

// SearchChannels finds channels whose handle starts with q or whose name
// contains q (q already trimmed and lowercased; the handle column is
// lowercase by construction, the name is compared through lower()).
// Handle-prefix matches come first, then the most-published channels, then
// handle order for a stable page. limit is applied as given.
func (s *Store) SearchChannels(ctx context.Context, q string, limit int) ([]ChannelSearchHit, error) {
	if q == "" || limit <= 0 {
		return []ChannelSearchHit{}, nil
	}
	escaped := EscapeLikePattern(q)
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.user_id, c.name, c.handle, c.description, c.avatar_media_id, c.subscriber_count, c.created_at, c.updated_at,
		       c.banner_media_id, COALESCE(c.links, '[]'::jsonb), COALESCE(c.contact_email, ''), c.featured_post_id,
		       (`+channelVideoCountCorrelated+`) AS video_count,
		       (c.handle LIKE $1) AS handle_prefix
		FROM channels c
		WHERE c.handle LIKE $1 OR lower(c.name) LIKE $2
		ORDER BY handle_prefix DESC, video_count DESC, c.handle ASC
		LIMIT $3`, escaped+"%", "%"+escaped+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ChannelSearchHit, 0, limit)
	for rows.Next() {
		var hit ChannelSearchHit
		var handlePrefix bool
		var links []byte
		if err := rows.Scan(&hit.ID, &hit.UserID, &hit.Name, &hit.Handle, &hit.About, &hit.AvatarMediaID,
			&hit.SubscriberCount, &hit.CreatedAt, &hit.UpdatedAt,
			&hit.BannerMediaID, &links, &hit.ContactEmail, &hit.FeaturedPostID,
			&hit.VideoCount, &handlePrefix); err != nil {
			return nil, err
		}
		if err := finishChannelScan(&hit.Channel, links); err != nil {
			return nil, err
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

// ChannelAvatarOwners maps each media id that is some channel's avatar to
// that channel's owner. Used by the media-access authority: a channel avatar
// is public to every viewer.
func (s *Store) ChannelAvatarOwners(ctx context.Context, mediaIDs []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	out := make(map[uuid.UUID]uuid.UUID)
	if len(mediaIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT avatar_media_id, user_id FROM channels WHERE avatar_media_id = ANY($1)`, mediaIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mediaID, userID uuid.UUID
		if err := rows.Scan(&mediaID, &userID); err != nil {
			return nil, err
		}
		out[mediaID] = userID
	}
	return out, rows.Err()
}
