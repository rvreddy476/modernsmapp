package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Event struct {
	ID           uuid.UUID  `json:"id"`
	PostID       *uuid.UUID `json:"post_id,omitempty"`
	CreatorID    uuid.UUID  `json:"creator_id"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	StartsAt     time.Time  `json:"starts_at"`
	EndsAt       *time.Time `json:"ends_at,omitempty"`
	LocationName *string    `json:"location_name,omitempty"`
	LocationLat  *float64   `json:"location_lat,omitempty"`
	LocationLng  *float64   `json:"location_lng,omitempty"`
	CoverMediaID *uuid.UUID `json:"cover_media_id,omitempty"`
	IsTicketed   bool       `json:"is_ticketed"`
	TicketPrice  *float64   `json:"ticket_price,omitempty"`
	MaxAttendees *int       `json:"max_attendees,omitempty"`
	ChatConvID   *uuid.UUID `json:"chat_conv_id,omitempty"`
	RSVPCount    int        `json:"rsvp_count"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type EventRSVP struct {
	EventID   uuid.UUID `json:"event_id"`
	UserID    uuid.UUID `json:"user_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) CreateEvent(ctx context.Context, e *Event) (*Event, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO events (post_id, creator_id, title, description, starts_at, ends_at,
			location_name, location_lat, location_lng, cover_media_id, is_ticketed,
			ticket_price, max_attendees, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id, post_id, creator_id, title, description, starts_at, ends_at,
			location_name, location_lat, location_lng, cover_media_id, is_ticketed,
			ticket_price, max_attendees, chat_conv_id, rsvp_count, status, created_at, updated_at`,
		e.PostID, e.CreatorID, e.Title, e.Description, e.StartsAt, e.EndsAt,
		e.LocationName, e.LocationLat, e.LocationLng, e.CoverMediaID, e.IsTicketed,
		e.TicketPrice, e.MaxAttendees, e.Status,
	).Scan(&e.ID, &e.PostID, &e.CreatorID, &e.Title, &e.Description, &e.StartsAt, &e.EndsAt,
		&e.LocationName, &e.LocationLat, &e.LocationLng, &e.CoverMediaID, &e.IsTicketed,
		&e.TicketPrice, &e.MaxAttendees, &e.ChatConvID, &e.RSVPCount, &e.Status, &e.CreatedAt, &e.UpdatedAt)
	return e, err
}

func (s *Store) GetEvent(ctx context.Context, eventID uuid.UUID) (*Event, error) {
	e := &Event{}
	err := s.db.QueryRow(ctx, `
		SELECT id, post_id, creator_id, title, description, starts_at, ends_at,
			location_name, location_lat, location_lng, cover_media_id, is_ticketed,
			ticket_price, max_attendees, chat_conv_id, rsvp_count, status, created_at, updated_at
		FROM events WHERE id = $1`, eventID,
	).Scan(&e.ID, &e.PostID, &e.CreatorID, &e.Title, &e.Description, &e.StartsAt, &e.EndsAt,
		&e.LocationName, &e.LocationLat, &e.LocationLng, &e.CoverMediaID, &e.IsTicketed,
		&e.TicketPrice, &e.MaxAttendees, &e.ChatConvID, &e.RSVPCount, &e.Status, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return e, err
}

func (s *Store) UpsertEventRSVP(ctx context.Context, eventID, userID uuid.UUID, status string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var oldStatus *string
	_ = tx.QueryRow(ctx,
		`SELECT status FROM event_rsvps WHERE event_id = $1 AND user_id = $2`,
		eventID, userID).Scan(&oldStatus)

	_, err = tx.Exec(ctx, `
		INSERT INTO event_rsvps (event_id, user_id, status) VALUES ($1, $2, $3)
		ON CONFLICT (event_id, user_id) DO UPDATE SET status = EXCLUDED.status`,
		eventID, userID, status)
	if err != nil {
		return err
	}

	// Adjust rsvp_count: only count "going"
	if oldStatus == nil && status == "going" {
		_, err = tx.Exec(ctx, `UPDATE events SET rsvp_count = rsvp_count + 1 WHERE id = $1`, eventID)
	} else if oldStatus != nil && *oldStatus == "going" && status != "going" {
		_, err = tx.Exec(ctx, `UPDATE events SET rsvp_count = GREATEST(0, rsvp_count - 1) WHERE id = $1`, eventID)
	} else if oldStatus != nil && *oldStatus != "going" && status == "going" {
		_, err = tx.Exec(ctx, `UPDATE events SET rsvp_count = rsvp_count + 1 WHERE id = $1`, eventID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) GetEventRSVPs(ctx context.Context, eventID uuid.UUID, limit, offset int) ([]EventRSVP, error) {
	rows, err := s.db.Query(ctx,
		`SELECT event_id, user_id, status, created_at FROM event_rsvps WHERE event_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		eventID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rsvps []EventRSVP
	for rows.Next() {
		var r EventRSVP
		if err := rows.Scan(&r.EventID, &r.UserID, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		rsvps = append(rsvps, r)
	}
	return rsvps, rows.Err()
}
