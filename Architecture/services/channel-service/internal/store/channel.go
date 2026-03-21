package store

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

type BroadcastChannel struct {
	ID                    uuid.UUID  `json:"id"`
	OwnerID               uuid.UUID  `json:"owner_id"`
	Handle                string     `json:"handle"`
	Name                  string     `json:"name"`
	Description           string     `json:"description"`
	AvatarMediaID         *uuid.UUID `json:"avatar_media_id,omitempty"`
	BannerMediaID         *uuid.UUID `json:"banner_media_id,omitempty"`
	ChannelType           string     `json:"channel_type"`
	Category              string     `json:"category"`
	Language              string     `json:"language"`
	CommentMode           string     `json:"comment_mode"`
	ReactionMode          string     `json:"reaction_mode"`
	ForwardAllowed        bool       `json:"forward_allowed"`
	PaidAccess            bool       `json:"paid_access"`
	SubscriptionPriceCents int       `json:"subscription_price_cents"`
	PostScheduleEnabled   bool       `json:"post_schedule_enabled"`
	SubscriberCountVisible bool      `json:"subscriber_count_visible"`
	AllowPreviewPosts     int        `json:"allow_preview_posts"`
	IsVerified            bool       `json:"is_verified"`
	SubscriberCount       int64      `json:"subscriber_count"`
	UpdateCount           int64      `json:"update_count"`
	Status                string     `json:"status"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	DeletedAt             *time.Time `json:"deleted_at,omitempty"`
}

type ChannelMember struct {
	ChannelID    uuid.UUID  `json:"channel_id"`
	UserID       uuid.UUID  `json:"user_id"`
	Role         string     `json:"role"`
	NotifyOn     string     `json:"notify_on"`
	MutedUntil   *time.Time `json:"muted_until,omitempty"`
	SnoozedUntil *time.Time `json:"snoozed_until,omitempty"`
	Paid         bool       `json:"paid"`
	SubscribedAt time.Time  `json:"subscribed_at"`
}

type ChannelUpdate struct {
	ID            uuid.UUID        `json:"id"`
	ChannelID     uuid.UUID        `json:"channel_id"`
	AuthorID      uuid.UUID        `json:"author_id"`
	UpdateType    string           `json:"update_type"`
	Title         *string          `json:"title,omitempty"`
	Body          string           `json:"body"`
	MediaIDs      []uuid.UUID      `json:"media_ids"`
	Metadata      json.RawMessage  `json:"metadata,omitempty"`
	IsPinned      bool             `json:"is_pinned"`
	ScheduledAt   *time.Time       `json:"scheduled_at,omitempty"`
	PublishedAt   *time.Time       `json:"published_at,omitempty"`
	Status        string           `json:"status"`
	ViewCount     int64            `json:"view_count"`
	ReactionCount int64            `json:"reaction_count"`
	CommentCount  int64            `json:"comment_count"`
	ForwardCount  int64            `json:"forward_count"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

type UpdateComment struct {
	ID        uuid.UUID  `json:"id"`
	UpdateID  uuid.UUID  `json:"update_id"`
	AuthorID  uuid.UUID  `json:"author_id"`
	Body      string     `json:"body"`
	ParentID  *uuid.UUID `json:"parent_id,omitempty"`
	IsPinned  bool       `json:"is_pinned"`
	CreatedAt time.Time  `json:"created_at"`
}

type PollResult struct {
	OptionIndex int   `json:"option_index"`
	VoteCount   int64 `json:"vote_count"`
}

type PollResultsResponse struct {
	Results   []PollResult `json:"results"`
	UserVoted bool         `json:"user_voted"`
}

type EventAttendee struct {
	UserID    uuid.UUID `json:"user_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// --- Channel CRUD ---

func (s *Store) CreateChannel(ctx context.Context, ch *BroadcastChannel) error {
	query := `
		INSERT INTO broadcast_channels (
			id, owner_id, handle, name, description, avatar_media_id, banner_media_id,
			channel_type, category, language, comment_mode, reaction_mode,
			forward_allowed, paid_access, subscription_price_cents,
			post_schedule_enabled, subscriber_count_visible, allow_preview_posts,
			is_verified, subscriber_count, update_count, status
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12,
			$13, $14, $15,
			$16, $17, $18,
			$19, $20, $21, $22
		) RETURNING created_at, updated_at`
	return s.db.QueryRow(ctx, query,
		ch.ID, ch.OwnerID, ch.Handle, ch.Name, ch.Description, ch.AvatarMediaID, ch.BannerMediaID,
		ch.ChannelType, ch.Category, ch.Language, ch.CommentMode, ch.ReactionMode,
		ch.ForwardAllowed, ch.PaidAccess, ch.SubscriptionPriceCents,
		ch.PostScheduleEnabled, ch.SubscriberCountVisible, ch.AllowPreviewPosts,
		ch.IsVerified, ch.SubscriberCount, ch.UpdateCount, ch.Status,
	).Scan(&ch.CreatedAt, &ch.UpdatedAt)
}

func (s *Store) GetChannelByID(ctx context.Context, id uuid.UUID) (*BroadcastChannel, error) {
	query := `SELECT id, owner_id, handle, name, description, avatar_media_id, banner_media_id,
		channel_type, category, language, comment_mode, reaction_mode,
		forward_allowed, paid_access, subscription_price_cents,
		post_schedule_enabled, subscriber_count_visible, allow_preview_posts,
		is_verified, subscriber_count, update_count, status, created_at, updated_at, deleted_at
		FROM broadcast_channels WHERE id = $1 AND status != 'deleted'`
	ch := &BroadcastChannel{}
	err := s.db.QueryRow(ctx, query, id).Scan(
		&ch.ID, &ch.OwnerID, &ch.Handle, &ch.Name, &ch.Description, &ch.AvatarMediaID, &ch.BannerMediaID,
		&ch.ChannelType, &ch.Category, &ch.Language, &ch.CommentMode, &ch.ReactionMode,
		&ch.ForwardAllowed, &ch.PaidAccess, &ch.SubscriptionPriceCents,
		&ch.PostScheduleEnabled, &ch.SubscriberCountVisible, &ch.AllowPreviewPosts,
		&ch.IsVerified, &ch.SubscriberCount, &ch.UpdateCount, &ch.Status, &ch.CreatedAt, &ch.UpdatedAt, &ch.DeletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("channel not found")
	}
	return ch, err
}

func (s *Store) GetChannelByHandle(ctx context.Context, handle string) (*BroadcastChannel, error) {
	query := `SELECT id, owner_id, handle, name, description, avatar_media_id, banner_media_id,
		channel_type, category, language, comment_mode, reaction_mode,
		forward_allowed, paid_access, subscription_price_cents,
		post_schedule_enabled, subscriber_count_visible, allow_preview_posts,
		is_verified, subscriber_count, update_count, status, created_at, updated_at, deleted_at
		FROM broadcast_channels WHERE handle = $1 AND status != 'deleted'`
	ch := &BroadcastChannel{}
	err := s.db.QueryRow(ctx, query, handle).Scan(
		&ch.ID, &ch.OwnerID, &ch.Handle, &ch.Name, &ch.Description, &ch.AvatarMediaID, &ch.BannerMediaID,
		&ch.ChannelType, &ch.Category, &ch.Language, &ch.CommentMode, &ch.ReactionMode,
		&ch.ForwardAllowed, &ch.PaidAccess, &ch.SubscriptionPriceCents,
		&ch.PostScheduleEnabled, &ch.SubscriberCountVisible, &ch.AllowPreviewPosts,
		&ch.IsVerified, &ch.SubscriberCount, &ch.UpdateCount, &ch.Status, &ch.CreatedAt, &ch.UpdatedAt, &ch.DeletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("channel not found")
	}
	return ch, err
}

func (s *Store) UpdateChannel(ctx context.Context, ch *BroadcastChannel) error {
	query := `UPDATE broadcast_channels SET
		name = $2, description = $3, avatar_media_id = $4, banner_media_id = $5,
		channel_type = $6, category = $7, language = $8, comment_mode = $9, reaction_mode = $10,
		forward_allowed = $11, paid_access = $12, subscription_price_cents = $13,
		post_schedule_enabled = $14, subscriber_count_visible = $15, allow_preview_posts = $16,
		updated_at = NOW()
		WHERE id = $1 AND status != 'deleted'
		RETURNING updated_at`
	return s.db.QueryRow(ctx, query,
		ch.ID, ch.Name, ch.Description, ch.AvatarMediaID, ch.BannerMediaID,
		ch.ChannelType, ch.Category, ch.Language, ch.CommentMode, ch.ReactionMode,
		ch.ForwardAllowed, ch.PaidAccess, ch.SubscriptionPriceCents,
		ch.PostScheduleEnabled, ch.SubscriberCountVisible, ch.AllowPreviewPosts,
	).Scan(&ch.UpdatedAt)
}

func (s *Store) DeleteChannel(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE broadcast_channels SET status = 'deleted', deleted_at = NOW(), updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, query, id)
	return err
}

// --- Member operations ---

func (s *Store) AddMember(ctx context.Context, m *ChannelMember) error {
	query := `INSERT INTO channel_members (channel_id, user_id, role, notify_on, paid)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (channel_id, user_id) DO UPDATE SET role = $3, notify_on = $4`
	_, err := s.db.Exec(ctx, query, m.ChannelID, m.UserID, m.Role, m.NotifyOn, m.Paid)
	return err
}

func (s *Store) GetMember(ctx context.Context, channelID, userID uuid.UUID) (*ChannelMember, error) {
	query := `SELECT channel_id, user_id, role, notify_on, muted_until, snoozed_until, paid, subscribed_at
		FROM channel_members WHERE channel_id = $1 AND user_id = $2`
	m := &ChannelMember{}
	err := s.db.QueryRow(ctx, query, channelID, userID).Scan(
		&m.ChannelID, &m.UserID, &m.Role, &m.NotifyOn, &m.MutedUntil, &m.SnoozedUntil, &m.Paid, &m.SubscribedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

func (s *Store) UpdateMemberRole(ctx context.Context, channelID, userID uuid.UUID, role string) error {
	query := `UPDATE channel_members SET role = $3 WHERE channel_id = $1 AND user_id = $2`
	tag, err := s.db.Exec(ctx, query, channelID, userID, role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("member not found")
	}
	return nil
}

func (s *Store) RemoveMember(ctx context.Context, channelID, userID uuid.UUID) error {
	query := `DELETE FROM channel_members WHERE channel_id = $1 AND user_id = $2`
	_, err := s.db.Exec(ctx, query, channelID, userID)
	return err
}

func (s *Store) BanMember(ctx context.Context, channelID, userID uuid.UUID) error {
	query := `UPDATE channel_members SET role = 'banned' WHERE channel_id = $1 AND user_id = $2`
	_, err := s.db.Exec(ctx, query, channelID, userID)
	return err
}

func (s *Store) SetMutedUntil(ctx context.Context, channelID, userID uuid.UUID, mutedUntil *time.Time) error {
	query := `UPDATE channel_members SET muted_until = $3 WHERE channel_id = $1 AND user_id = $2`
	_, err := s.db.Exec(ctx, query, channelID, userID, mutedUntil)
	return err
}

func (s *Store) ListSubscribers(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]ChannelMember, error) {
	query := `SELECT channel_id, user_id, role, notify_on, muted_until, snoozed_until, paid, subscribed_at
		FROM channel_members WHERE channel_id = $1 AND role != 'banned'
		ORDER BY subscribed_at DESC LIMIT $2 OFFSET $3`
	rows, err := s.db.Query(ctx, query, channelID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []ChannelMember
	for rows.Next() {
		var m ChannelMember
		if err := rows.Scan(&m.ChannelID, &m.UserID, &m.Role, &m.NotifyOn, &m.MutedUntil, &m.SnoozedUntil, &m.Paid, &m.SubscribedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func (s *Store) CountSubscribers(ctx context.Context, channelID uuid.UUID) (int64, error) {
	var count int64
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM channel_members WHERE channel_id = $1 AND role != 'banned'`, channelID).Scan(&count)
	return count, err
}

func (s *Store) IncrementSubscriberCount(ctx context.Context, channelID uuid.UUID, delta int) error {
	query := `UPDATE broadcast_channels SET subscriber_count = subscriber_count + $2, updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, query, channelID, delta)
	return err
}

func (s *Store) IncrementUpdateCount(ctx context.Context, channelID uuid.UUID, delta int) error {
	query := `UPDATE broadcast_channels SET update_count = update_count + $2, updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, query, channelID, delta)
	return err
}

// --- Update operations ---

func (s *Store) CreateUpdate(ctx context.Context, u *ChannelUpdate) error {
	query := `INSERT INTO channel_updates (
		id, channel_id, author_id, update_type, title, body, media_ids, metadata,
		is_pinned, scheduled_at, published_at, status
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	RETURNING created_at, updated_at`
	return s.db.QueryRow(ctx, query,
		u.ID, u.ChannelID, u.AuthorID, u.UpdateType, u.Title, u.Body, u.MediaIDs, u.Metadata,
		u.IsPinned, u.ScheduledAt, u.PublishedAt, u.Status,
	).Scan(&u.CreatedAt, &u.UpdatedAt)
}

func (s *Store) GetUpdate(ctx context.Context, id uuid.UUID) (*ChannelUpdate, error) {
	query := `SELECT id, channel_id, author_id, update_type, title, body, media_ids, metadata,
		is_pinned, scheduled_at, published_at, status,
		view_count, reaction_count, comment_count, forward_count,
		created_at, updated_at
		FROM channel_updates WHERE id = $1 AND status != 'deleted'`
	u := &ChannelUpdate{}
	err := s.db.QueryRow(ctx, query, id).Scan(
		&u.ID, &u.ChannelID, &u.AuthorID, &u.UpdateType, &u.Title, &u.Body, &u.MediaIDs, &u.Metadata,
		&u.IsPinned, &u.ScheduledAt, &u.PublishedAt, &u.Status,
		&u.ViewCount, &u.ReactionCount, &u.CommentCount, &u.ForwardCount,
		&u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("update not found")
	}
	if err == nil && u.MediaIDs == nil {
		u.MediaIDs = []uuid.UUID{}
	}
	return u, err
}

func (s *Store) UpdateUpdate(ctx context.Context, u *ChannelUpdate) error {
	query := `UPDATE channel_updates SET
		update_type = $2, title = $3, body = $4, media_ids = $5, metadata = $6,
		scheduled_at = $7, updated_at = NOW()
		WHERE id = $1 AND status != 'deleted'
		RETURNING updated_at`
	return s.db.QueryRow(ctx, query,
		u.ID, u.UpdateType, u.Title, u.Body, u.MediaIDs, u.Metadata, u.ScheduledAt,
	).Scan(&u.UpdatedAt)
}

func (s *Store) DeleteUpdate(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE channel_updates SET status = 'deleted', updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, query, id)
	return err
}

func (s *Store) ListUpdates(ctx context.Context, channelID uuid.UUID, statusFilter string, limit, offset int) ([]ChannelUpdate, error) {
	query := `SELECT id, channel_id, author_id, update_type, title, body, media_ids, metadata,
		is_pinned, scheduled_at, published_at, status,
		view_count, reaction_count, comment_count, forward_count,
		created_at, updated_at
		FROM channel_updates
		WHERE channel_id = $1 AND status = $2
		ORDER BY COALESCE(published_at, created_at) DESC
		LIMIT $3 OFFSET $4`
	rows, err := s.db.Query(ctx, query, channelID, statusFilter, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var updates []ChannelUpdate
	for rows.Next() {
		var u ChannelUpdate
		if err := rows.Scan(
			&u.ID, &u.ChannelID, &u.AuthorID, &u.UpdateType, &u.Title, &u.Body, &u.MediaIDs, &u.Metadata,
			&u.IsPinned, &u.ScheduledAt, &u.PublishedAt, &u.Status,
			&u.ViewCount, &u.ReactionCount, &u.CommentCount, &u.ForwardCount,
			&u.CreatedAt, &u.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if u.MediaIDs == nil {
			u.MediaIDs = []uuid.UUID{}
		}
		updates = append(updates, u)
	}
	return updates, rows.Err()
}

func (s *Store) PinUpdate(ctx context.Context, id uuid.UUID, pinned bool) error {
	query := `UPDATE channel_updates SET is_pinned = $2, updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, query, id, pinned)
	return err
}

func (s *Store) PublishScheduledUpdates(ctx context.Context) ([]ChannelUpdate, error) {
	query := `UPDATE channel_updates
		SET status = 'published', published_at = NOW(), updated_at = NOW()
		WHERE status = 'scheduled' AND scheduled_at <= NOW()
		RETURNING id, channel_id, author_id, update_type, title, body, media_ids, metadata,
		is_pinned, scheduled_at, published_at, status,
		view_count, reaction_count, comment_count, forward_count,
		created_at, updated_at`
	rows, err := s.db.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var updates []ChannelUpdate
	for rows.Next() {
		var u ChannelUpdate
		if err := rows.Scan(
			&u.ID, &u.ChannelID, &u.AuthorID, &u.UpdateType, &u.Title, &u.Body, &u.MediaIDs, &u.Metadata,
			&u.IsPinned, &u.ScheduledAt, &u.PublishedAt, &u.Status,
			&u.ViewCount, &u.ReactionCount, &u.CommentCount, &u.ForwardCount,
			&u.CreatedAt, &u.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if u.MediaIDs == nil {
			u.MediaIDs = []uuid.UUID{}
		}
		updates = append(updates, u)
	}
	return updates, rows.Err()
}

// --- Query helpers ---

func (s *Store) GetMyChannels(ctx context.Context, ownerID uuid.UUID, limit, offset int) ([]BroadcastChannel, error) {
	query := `SELECT id, owner_id, handle, name, description, avatar_media_id, banner_media_id,
		channel_type, category, language, comment_mode, reaction_mode,
		forward_allowed, paid_access, subscription_price_cents,
		post_schedule_enabled, subscriber_count_visible, allow_preview_posts,
		is_verified, subscriber_count, update_count, status, created_at, updated_at, deleted_at
		FROM broadcast_channels WHERE owner_id = $1 AND status != 'deleted'
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	return s.scanChannels(ctx, query, ownerID, limit, offset)
}

// GetSubscribedChannels returns channels a user is subscribed to (as member, not owner).
func (s *Store) GetSubscribedChannels(ctx context.Context, userID uuid.UUID, limit, offset int) ([]BroadcastChannel, error) {
	query := `SELECT bc.id, bc.owner_id, bc.handle, bc.name, bc.description, bc.avatar_media_id, bc.banner_media_id,
		bc.channel_type, bc.category, bc.language, bc.comment_mode, bc.reaction_mode,
		bc.forward_allowed, bc.paid_access, bc.subscription_price_cents,
		bc.post_schedule_enabled, bc.subscriber_count_visible, bc.allow_preview_posts,
		bc.is_verified, bc.subscriber_count, bc.update_count, bc.status, bc.created_at, bc.updated_at, bc.deleted_at
		FROM broadcast_channels bc
		INNER JOIN channel_members cm ON cm.channel_id = bc.id
		WHERE cm.user_id = $1 AND bc.status != 'deleted'
		ORDER BY cm.subscribed_at DESC LIMIT $2 OFFSET $3`
	return s.scanChannels(ctx, query, userID, limit, offset)
}

// GetMemberRole returns the role of a user in a channel, or empty string if not a member.
func (s *Store) GetMemberRole(ctx context.Context, channelID, userID uuid.UUID) string {
	var role string
	err := s.db.QueryRow(ctx, `SELECT role FROM channel_members WHERE channel_id = $1 AND user_id = $2`, channelID, userID).Scan(&role)
	if err != nil {
		return ""
	}
	return role
}

func (s *Store) DiscoverChannels(ctx context.Context, limit, offset int) ([]BroadcastChannel, error) {
	query := `SELECT id, owner_id, handle, name, description, avatar_media_id, banner_media_id,
		channel_type, category, language, comment_mode, reaction_mode,
		forward_allowed, paid_access, subscription_price_cents,
		post_schedule_enabled, subscriber_count_visible, allow_preview_posts,
		is_verified, subscriber_count, update_count, status, created_at, updated_at, deleted_at
		FROM broadcast_channels
		WHERE status = 'active' AND channel_type IN ('public','creator','brand','education','official','topic')
		ORDER BY subscriber_count DESC, created_at DESC
		LIMIT $1 OFFSET $2`
	return s.scanChannels(ctx, query, limit, offset)
}

func (s *Store) scanChannels(ctx context.Context, query string, args ...any) ([]BroadcastChannel, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var channels []BroadcastChannel
	for rows.Next() {
		var ch BroadcastChannel
		if err := rows.Scan(
			&ch.ID, &ch.OwnerID, &ch.Handle, &ch.Name, &ch.Description, &ch.AvatarMediaID, &ch.BannerMediaID,
			&ch.ChannelType, &ch.Category, &ch.Language, &ch.CommentMode, &ch.ReactionMode,
			&ch.ForwardAllowed, &ch.PaidAccess, &ch.SubscriptionPriceCents,
			&ch.PostScheduleEnabled, &ch.SubscriberCountVisible, &ch.AllowPreviewPosts,
			&ch.IsVerified, &ch.SubscriberCount, &ch.UpdateCount, &ch.Status, &ch.CreatedAt, &ch.UpdatedAt, &ch.DeletedAt,
		); err != nil {
			return nil, err
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

// --- GDPR helpers ---

func (s *Store) RemoveUserFromAllChannels(ctx context.Context, userID uuid.UUID) error {
	query := `DELETE FROM channel_members WHERE user_id = $1`
	_, err := s.db.Exec(ctx, query, userID)
	return err
}

func (s *Store) ListChannelsWhereUserIsOnlyOwner(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	query := `SELECT cm.channel_id FROM channel_members cm
		WHERE cm.user_id = $1 AND cm.role = 'owner'
		AND NOT EXISTS (
			SELECT 1 FROM channel_members cm2
			WHERE cm2.channel_id = cm.channel_id AND cm2.role = 'owner' AND cm2.user_id != $1
		)`
	rows, err := s.db.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) ArchiveChannel(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE broadcast_channels SET status = 'archived', updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, query, id)
	return err
}

func (s *Store) CountChannelsByOwner(ctx context.Context, ownerID uuid.UUID, since time.Time) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM broadcast_channels WHERE owner_id = $1 AND created_at >= $2 AND status != 'deleted'`, ownerID, since).Scan(&count)
	return count, err
}

// --- Engagement operations ---

func (s *Store) SparkUpdate(ctx context.Context, updateID, userID uuid.UUID, isSupernova bool) error {
	weight := 1
	if isSupernova {
		weight = 5
	}
	query := `INSERT INTO update_sparks (update_id, user_id, is_supernova, weight, created_at)
		VALUES ($1, $2, $3, $4, NOW()) ON CONFLICT DO NOTHING`
	tag, err := s.db.Exec(ctx, query, updateID, userID, isSupernova, weight)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("already sparked this update")
	}
	_, err = s.db.Exec(ctx,
		`UPDATE channel_updates SET reaction_count = reaction_count + $2, updated_at = NOW() WHERE id = $1`,
		updateID, weight)
	return err
}

func (s *Store) UnsparkUpdate(ctx context.Context, updateID, userID uuid.UUID) error {
	var weight int
	err := s.db.QueryRow(ctx,
		`DELETE FROM update_sparks WHERE update_id = $1 AND user_id = $2 RETURNING weight`,
		updateID, userID).Scan(&weight)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("not found: spark not found")
	}
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx,
		`UPDATE channel_updates SET reaction_count = GREATEST(reaction_count - $2, 0), updated_at = NOW() WHERE id = $1`,
		updateID, weight)
	return err
}

func (s *Store) StashUpdate(ctx context.Context, updateID, userID uuid.UUID) error {
	query := `INSERT INTO update_stashes (update_id, user_id, created_at) VALUES ($1, $2, NOW()) ON CONFLICT DO NOTHING`
	tag, err := s.db.Exec(ctx, query, updateID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("already stashed this update")
	}
	_, err = s.db.Exec(ctx,
		`UPDATE channel_updates SET stash_count = stash_count + 1, updated_at = NOW() WHERE id = $1`,
		updateID)
	return err
}

func (s *Store) UnstashUpdate(ctx context.Context, updateID, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM update_stashes WHERE update_id = $1 AND user_id = $2`,
		updateID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("not found: stash not found")
	}
	_, err = s.db.Exec(ctx,
		`UPDATE channel_updates SET stash_count = GREATEST(stash_count - 1, 0), updated_at = NOW() WHERE id = $1`,
		updateID)
	return err
}

func (s *Store) EchoUpdate(ctx context.Context, updateID, userID uuid.UUID, echoType string) error {
	query := `INSERT INTO update_echoes (id, update_id, user_id, echo_type, created_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (update_id, user_id) DO NOTHING`
	tag, err := s.db.Exec(ctx, query, uuid.New(), updateID, userID, echoType)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("already echoed")
	}
	_, err = s.db.Exec(ctx,
		`UPDATE channel_updates SET forward_count = forward_count + 1, updated_at = NOW() WHERE id = $1`,
		updateID)
	return err
}

func (s *Store) UnechoUpdate(ctx context.Context, updateID, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM update_echoes WHERE update_id = $1 AND user_id = $2`,
		updateID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("not found: echo not found")
	}
	_, err = s.db.Exec(ctx,
		`UPDATE channel_updates SET forward_count = GREATEST(forward_count - 1, 0), updated_at = NOW() WHERE id = $1`,
		updateID)
	return err
}

func (s *Store) RecordView(ctx context.Context, updateID, userID uuid.UUID) error {
	query := `INSERT INTO update_views (update_id, user_id, created_at) VALUES ($1, $2, NOW()) ON CONFLICT DO NOTHING`
	tag, err := s.db.Exec(ctx, query, updateID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil // already viewed, no error
	}
	_, err = s.db.Exec(ctx,
		`UPDATE channel_updates SET view_count = view_count + 1, updated_at = NOW() WHERE id = $1`,
		updateID)
	return err
}

func (s *Store) ListComments(ctx context.Context, updateID uuid.UUID, sort string, limit, offset int) ([]UpdateComment, error) {
	orderClause := "is_pinned DESC, created_at DESC"
	if sort == "oldest" {
		orderClause = "is_pinned DESC, created_at ASC"
	}
	query := fmt.Sprintf(`SELECT id, update_id, author_id, body, parent_id, is_pinned, created_at
		FROM update_comments WHERE update_id = $1
		ORDER BY %s LIMIT $2 OFFSET $3`, orderClause)
	rows, err := s.db.Query(ctx, query, updateID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var comments []UpdateComment
	for rows.Next() {
		var c UpdateComment
		if err := rows.Scan(&c.ID, &c.UpdateID, &c.AuthorID, &c.Body, &c.ParentID, &c.IsPinned, &c.CreatedAt); err != nil {
			return nil, err
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

func (s *Store) ListCommentsSince(ctx context.Context, updateID uuid.UUID, since time.Time, limit int) ([]UpdateComment, error) {
	query := `SELECT id, update_id, author_id, body, parent_id, is_pinned, created_at
		FROM update_comments
		WHERE update_id = $1 AND created_at > $2
		ORDER BY created_at ASC
		LIMIT $3`
	rows, err := s.db.Query(ctx, query, updateID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var comments []UpdateComment
	for rows.Next() {
		var c UpdateComment
		if err := rows.Scan(&c.ID, &c.UpdateID, &c.AuthorID, &c.Body, &c.ParentID, &c.IsPinned, &c.CreatedAt); err != nil {
			return nil, err
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

func (s *Store) AddComment(ctx context.Context, updateID, userID uuid.UUID, body string, parentID *uuid.UUID) (*UpdateComment, error) {
	c := &UpdateComment{
		ID:       uuid.New(),
		UpdateID: updateID,
		AuthorID: userID,
		Body:     body,
		ParentID: parentID,
	}
	query := `INSERT INTO update_comments (id, update_id, author_id, body, parent_id, is_pinned, created_at)
		VALUES ($1, $2, $3, $4, $5, false, NOW()) RETURNING created_at`
	if err := s.db.QueryRow(ctx, query, c.ID, updateID, userID, body, parentID).Scan(&c.CreatedAt); err != nil {
		return nil, err
	}
	_, _ = s.db.Exec(ctx,
		`UPDATE channel_updates SET comment_count = comment_count + 1, updated_at = NOW() WHERE id = $1`,
		updateID)
	return c, nil
}

func (s *Store) DeleteComment(ctx context.Context, commentID, userID uuid.UUID, isOwner bool) error {
	var query string
	var args []any
	if isOwner {
		query = `DELETE FROM update_comments WHERE id = $1 RETURNING update_id`
		args = []any{commentID}
	} else {
		query = `DELETE FROM update_comments WHERE id = $1 AND author_id = $2 RETURNING update_id`
		args = []any{commentID, userID}
	}

	var updateID uuid.UUID
	err := s.db.QueryRow(ctx, query, args...).Scan(&updateID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("not found: comment not found or not authorized")
	}
	if err != nil {
		return err
	}
	_, _ = s.db.Exec(ctx,
		`UPDATE channel_updates SET comment_count = GREATEST(comment_count - 1, 0), updated_at = NOW() WHERE id = $1`,
		updateID)
	return nil
}

func (s *Store) PinComment(ctx context.Context, commentID uuid.UUID) error {
	// Get the update_id for this comment
	var updateID uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT update_id FROM update_comments WHERE id = $1`, commentID).Scan(&updateID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("not found: comment not found")
	}
	if err != nil {
		return err
	}
	// Unpin all other comments for this update
	_, _ = s.db.Exec(ctx, `UPDATE update_comments SET is_pinned = false WHERE update_id = $1`, updateID)
	// Pin the selected comment
	_, err = s.db.Exec(ctx, `UPDATE update_comments SET is_pinned = true WHERE id = $1`, commentID)
	return err
}

func (s *Store) VoteOnPoll(ctx context.Context, updateID, userID uuid.UUID, optionIndexes []int) error {
	for _, idx := range optionIndexes {
		query := `INSERT INTO poll_votes (update_id, user_id, option_index, created_at) VALUES ($1, $2, $3, NOW())`
		if _, err := s.db.Exec(ctx, query, updateID, userID, idx); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetPollResults(ctx context.Context, updateID uuid.UUID) ([]PollResult, error) {
	query := `SELECT option_index, COUNT(*) as vote_count FROM poll_votes
		WHERE update_id = $1 GROUP BY option_index ORDER BY option_index`
	rows, err := s.db.Query(ctx, query, updateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []PollResult
	for rows.Next() {
		var r PollResult
		if err := rows.Scan(&r.OptionIndex, &r.VoteCount); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *Store) HasUserVoted(ctx context.Context, updateID, userID uuid.UUID) (bool, error) {
	var count int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM poll_votes WHERE update_id = $1 AND user_id = $2`, updateID, userID).Scan(&count)
	return count > 0, err
}

func (s *Store) RSVPEvent(ctx context.Context, updateID, userID uuid.UUID, status string) error {
	query := `INSERT INTO event_rsvps (update_id, user_id, status, created_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (update_id, user_id) DO UPDATE SET status = $3`
	_, err := s.db.Exec(ctx, query, updateID, userID, status)
	return err
}

func (s *Store) ListAttendees(ctx context.Context, updateID uuid.UUID, status string, limit, offset int) ([]EventAttendee, error) {
	var query string
	var args []any
	if status != "" {
		query = `SELECT user_id, status, created_at FROM event_rsvps
			WHERE update_id = $1 AND status = $2
			ORDER BY created_at DESC LIMIT $3 OFFSET $4`
		args = []any{updateID, status, limit, offset}
	} else {
		query = `SELECT user_id, status, created_at FROM event_rsvps
			WHERE update_id = $1
			ORDER BY created_at DESC LIMIT $2 OFFSET $3`
		args = []any{updateID, limit, offset}
	}
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var attendees []EventAttendee
	for rows.Next() {
		var a EventAttendee
		if err := rows.Scan(&a.UserID, &a.Status, &a.CreatedAt); err != nil {
			return nil, err
		}
		attendees = append(attendees, a)
	}
	return attendees, rows.Err()
}
