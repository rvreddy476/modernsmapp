package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Channel represents a creator video channel on Posttube.
type Channel struct {
	ID              uuid.UUID       `json:"id"`
	UserID          uuid.UUID       `json:"user_id"`
	Handle          string          `json:"handle"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	IconURL         string          `json:"icon_url,omitempty"`
	BannerURL       string          `json:"banner_url,omitempty"`
	Category        string          `json:"category,omitempty"`
	Country         string          `json:"country,omitempty"`
	Language        string          `json:"language,omitempty"`
	ContactEmail    string          `json:"contact_email,omitempty"`
	CollabStatus    string          `json:"collab_status,omitempty"` // open, closed
	ContentSchedule string          `json:"content_schedule,omitempty"`
	SubscriberCount int             `json:"subscriber_count"`
	IsVerified      bool            `json:"is_verified"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// ChannelLink represents a link on a channel profile.
type ChannelLink struct {
	ID        uuid.UUID `json:"id"`
	ChannelID uuid.UUID `json:"channel_id"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	SortOrder int       `json:"sort_order"`
}

// ChannelMilestone represents an achievement on a channel.
type ChannelMilestone struct {
	ID            uuid.UUID `json:"id"`
	ChannelID     uuid.UUID `json:"channel_id"`
	MilestoneType string    `json:"milestone_type"`
	Title         string    `json:"title"`
	AchievedAt    time.Time `json:"achieved_at"`
	IsPublic      bool      `json:"is_public"`
}

// CreateChannel creates a new channel.
func (s *Store) CreateChannel(ctx context.Context, ch *Channel) error {
	ch.ID = uuid.New()
	now := time.Now()
	ch.CreatedAt = now
	ch.UpdatedAt = now

	_, err := s.db.Exec(ctx, `
		INSERT INTO channels (id, user_id, handle, name, description, icon_url, banner_url,
			category, country, language, contact_email, collab_status, content_schedule,
			subscriber_count, is_verified, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`, ch.ID, ch.UserID, ch.Handle, ch.Name, ch.Description, ch.IconURL, ch.BannerURL,
		ch.Category, ch.Country, ch.Language, ch.ContactEmail, ch.CollabStatus, ch.ContentSchedule,
		ch.SubscriberCount, ch.IsVerified, ch.CreatedAt, ch.UpdatedAt)
	return err
}

// GetChannelByHandle returns a channel by its unique handle.
func (s *Store) GetChannelByHandle(ctx context.Context, handle string) (*Channel, error) {
	var ch Channel
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, handle, name, description, icon_url, banner_url,
			category, country, language, contact_email, collab_status, content_schedule,
			subscriber_count, is_verified, created_at, updated_at
		FROM channels WHERE handle = $1
	`, handle).Scan(
		&ch.ID, &ch.UserID, &ch.Handle, &ch.Name, &ch.Description, &ch.IconURL, &ch.BannerURL,
		&ch.Category, &ch.Country, &ch.Language, &ch.ContactEmail, &ch.CollabStatus, &ch.ContentSchedule,
		&ch.SubscriberCount, &ch.IsVerified, &ch.CreatedAt, &ch.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &ch, nil
}

// GetChannelByID returns a channel by ID.
func (s *Store) GetChannelByID(ctx context.Context, id uuid.UUID) (*Channel, error) {
	var ch Channel
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, handle, name, description, icon_url, banner_url,
			category, country, language, contact_email, collab_status, content_schedule,
			subscriber_count, is_verified, created_at, updated_at
		FROM channels WHERE id = $1
	`, id).Scan(
		&ch.ID, &ch.UserID, &ch.Handle, &ch.Name, &ch.Description, &ch.IconURL, &ch.BannerURL,
		&ch.Category, &ch.Country, &ch.Language, &ch.ContactEmail, &ch.CollabStatus, &ch.ContentSchedule,
		&ch.SubscriberCount, &ch.IsVerified, &ch.CreatedAt, &ch.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &ch, nil
}

// GetUserChannels returns all channels owned by a user.
func (s *Store) GetUserChannels(ctx context.Context, userID uuid.UUID) ([]Channel, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, handle, name, description, icon_url, banner_url,
			category, country, language, contact_email, collab_status, content_schedule,
			subscriber_count, is_verified, created_at, updated_at
		FROM channels WHERE user_id = $1 ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []Channel
	for rows.Next() {
		var ch Channel
		if err := rows.Scan(
			&ch.ID, &ch.UserID, &ch.Handle, &ch.Name, &ch.Description, &ch.IconURL, &ch.BannerURL,
			&ch.Category, &ch.Country, &ch.Language, &ch.ContactEmail, &ch.CollabStatus, &ch.ContentSchedule,
			&ch.SubscriberCount, &ch.IsVerified, &ch.CreatedAt, &ch.UpdatedAt,
		); err != nil {
			return nil, err
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

// UpdateChannel updates a channel's editable fields.
func (s *Store) UpdateChannel(ctx context.Context, ch *Channel) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE channels SET
			name = $2, description = $3, icon_url = $4, banner_url = $5,
			category = $6, country = $7, language = $8, contact_email = $9,
			collab_status = $10, content_schedule = $11, updated_at = NOW()
		WHERE id = $1 AND user_id = $12
	`, ch.ID, ch.Name, ch.Description, ch.IconURL, ch.BannerURL,
		ch.Category, ch.Country, ch.Language, ch.ContactEmail,
		ch.CollabStatus, ch.ContentSchedule, ch.UserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("CHANNEL_NOT_FOUND")
	}
	return nil
}

// DeleteChannel removes a channel.
func (s *Store) DeleteChannel(ctx context.Context, id, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `DELETE FROM channel_milestones WHERE channel_id = $1`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM channel_links WHERE channel_id = $1`, id)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM channels WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("CHANNEL_NOT_FOUND")
	}
	return tx.Commit(ctx)
}

// GetChannelLinks returns links for a channel.
func (s *Store) GetChannelLinks(ctx context.Context, channelID uuid.UUID) ([]ChannelLink, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, channel_id, title, url, sort_order
		FROM channel_links WHERE channel_id = $1 ORDER BY sort_order
	`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []ChannelLink
	for rows.Next() {
		var l ChannelLink
		if err := rows.Scan(&l.ID, &l.ChannelID, &l.Title, &l.URL, &l.SortOrder); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

// UpsertChannelLinks replaces all links for a channel.
func (s *Store) UpsertChannelLinks(ctx context.Context, channelID uuid.UUID, links []ChannelLink) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `DELETE FROM channel_links WHERE channel_id = $1`, channelID)
	if err != nil {
		return err
	}
	for _, l := range links {
		_, err = tx.Exec(ctx, `
			INSERT INTO channel_links (id, channel_id, title, url, sort_order)
			VALUES ($1, $2, $3, $4, $5)
		`, uuid.New(), channelID, l.Title, l.URL, l.SortOrder)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// GetChannelMilestones returns milestones for a channel.
func (s *Store) GetChannelMilestones(ctx context.Context, channelID uuid.UUID, publicOnly bool) ([]ChannelMilestone, error) {
	query := `
		SELECT id, channel_id, milestone_type, title, achieved_at, is_public
		FROM channel_milestones WHERE channel_id = $1`
	if publicOnly {
		query += ` AND is_public = TRUE`
	}
	query += ` ORDER BY achieved_at DESC`

	rows, err := s.db.Query(ctx, query, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var milestones []ChannelMilestone
	for rows.Next() {
		var m ChannelMilestone
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.MilestoneType, &m.Title, &m.AchievedAt, &m.IsPublic); err != nil {
			return nil, err
		}
		milestones = append(milestones, m)
	}
	return milestones, rows.Err()
}

// -- JSON helper for channel details response --

type ChannelDetail struct {
	Channel    Channel            `json:"channel"`
	Links      []ChannelLink      `json:"links"`
	Milestones []ChannelMilestone `json:"milestones"`
}

func (d ChannelDetail) MarshalJSON() ([]byte, error) {
	type Alias ChannelDetail
	return json.Marshal((Alias)(d))
}
