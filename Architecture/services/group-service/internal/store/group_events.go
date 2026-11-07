package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type GroupEvent struct {
	ID           uuid.UUID  `json:"id"`
	GroupID      uuid.UUID  `json:"group_id"`
	PostID       *uuid.UUID `json:"post_id,omitempty"`
	CreatorID    string     `json:"creator_id"`
	Title        string     `json:"title"`
	Description  *string    `json:"description,omitempty"`
	CoverMediaID *uuid.UUID `json:"cover_media_id,omitempty"`
	StartAt      time.Time  `json:"start_at"`
	EndAt        *time.Time `json:"end_at,omitempty"`
	Timezone     string     `json:"timezone"`
	IsAllDay     bool       `json:"is_all_day"`
	LocationType string     `json:"location_type"`
	Address      *string    `json:"address,omitempty"`
	OnlineLink   *string    `json:"online_link,omitempty"`
	RSVPEnabled  bool       `json:"rsvp_enabled"`
	MaxAttendees int        `json:"max_attendees"`
	GoingCount   int        `json:"going_count"`
	MaybeCount   int        `json:"maybe_count"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
}

// ==================== V2 Events ====================

// CreateGroupEvent inserts e. An id the caller fixed is kept (the cover's
// media claim was made against it); otherwise one is generated.
func (s *Store) CreateGroupEvent(ctx context.Context, e *GroupEvent) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	e.CreatedAt = time.Now()
	if e.Timezone == "" {
		e.Timezone = "UTC"
	}
	if e.LocationType == "" {
		e.LocationType = "online"
	}
	if e.Status == "" {
		e.Status = "upcoming"
	}
	query := `INSERT INTO group_events (id, group_id, post_id, creator_id, title, description,
		cover_media_id, start_at, end_at, timezone, is_all_day, location_type, address, online_link,
		rsvp_enabled, max_attendees, going_count, maybe_count, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,0,0,$17,$18)`
	_, err := s.db.Exec(ctx, query,
		e.ID, e.GroupID, e.PostID, e.CreatorID, e.Title, e.Description,
		e.CoverMediaID, e.StartAt, e.EndAt, e.Timezone, e.IsAllDay, e.LocationType,
		e.Address, e.OnlineLink, e.RSVPEnabled, e.MaxAttendees, e.Status, e.CreatedAt)
	return err
}

func (s *Store) GetGroupEvent(ctx context.Context, eventID uuid.UUID) (*GroupEvent, error) {
	var e GroupEvent
	err := s.db.QueryRow(ctx,
		`SELECT id, group_id, post_id, creator_id, title, description, cover_media_id,
		start_at, end_at, timezone, is_all_day, location_type, address, online_link,
		rsvp_enabled, max_attendees, going_count, maybe_count, status, created_at
		FROM group_events WHERE id = $1 AND status != 'cancelled'`, eventID).Scan(
		&e.ID, &e.GroupID, &e.PostID, &e.CreatorID, &e.Title, &e.Description, &e.CoverMediaID,
		&e.StartAt, &e.EndAt, &e.Timezone, &e.IsAllDay, &e.LocationType, &e.Address, &e.OnlineLink,
		&e.RSVPEnabled, &e.MaxAttendees, &e.GoingCount, &e.MaybeCount, &e.Status, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("not found: event not found")
	}
	return &e, err
}

func (s *Store) ListGroupEvents(ctx context.Context, groupID uuid.UUID, limit, offset int) ([]GroupEvent, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, group_id, post_id, creator_id, title, description, cover_media_id,
		start_at, end_at, timezone, is_all_day, location_type, address, online_link,
		rsvp_enabled, max_attendees, going_count, maybe_count, status, created_at
		FROM group_events
		WHERE group_id = $1 AND status != 'cancelled'
		ORDER BY start_at ASC LIMIT $2 OFFSET $3`,
		groupID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []GroupEvent
	for rows.Next() {
		var e GroupEvent
		if err := rows.Scan(&e.ID, &e.GroupID, &e.PostID, &e.CreatorID, &e.Title, &e.Description, &e.CoverMediaID,
			&e.StartAt, &e.EndAt, &e.Timezone, &e.IsAllDay, &e.LocationType, &e.Address, &e.OnlineLink,
			&e.RSVPEnabled, &e.MaxAttendees, &e.GoingCount, &e.MaybeCount, &e.Status, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) DeleteGroupEvent(ctx context.Context, eventID uuid.UUID) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE group_events SET status = 'cancelled' WHERE id = $1 AND status != 'cancelled'`, eventID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("not found: event not found")
	}
	return nil
}

func (s *Store) RSVPGroupEvent(ctx context.Context, eventID uuid.UUID, userID, status string) error {
	// Get previous RSVP if any
	var prevStatus *string
	err := s.db.QueryRow(ctx,
		`SELECT status FROM group_event_rsvps WHERE event_id = $1 AND user_id = $2`,
		eventID, userID).Scan(&prevStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	// Upsert RSVP
	_, err = s.db.Exec(ctx,
		`INSERT INTO group_event_rsvps (event_id, user_id, status, created_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (event_id, user_id) DO UPDATE SET status = EXCLUDED.status`,
		eventID, userID, status)
	if err != nil {
		return err
	}

	// Adjust counts: decrement previous, increment new
	if prevStatus != nil {
		switch *prevStatus {
		case "going":
			s.db.Exec(ctx, `UPDATE group_events SET going_count = GREATEST(going_count - 1, 0) WHERE id = $1`, eventID)
		case "maybe":
			s.db.Exec(ctx, `UPDATE group_events SET maybe_count = GREATEST(maybe_count - 1, 0) WHERE id = $1`, eventID)
		}
	}
	switch status {
	case "going":
		_, err = s.db.Exec(ctx, `UPDATE group_events SET going_count = going_count + 1 WHERE id = $1`, eventID)
	case "maybe":
		_, err = s.db.Exec(ctx, `UPDATE group_events SET maybe_count = maybe_count + 1 WHERE id = $1`, eventID)
	}
	return err
}
