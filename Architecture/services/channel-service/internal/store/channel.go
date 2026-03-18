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
