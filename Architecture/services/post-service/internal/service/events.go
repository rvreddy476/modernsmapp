package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

type CreateEventInput struct {
	CreatorID    uuid.UUID
	Title        string
	Description  string
	StartsAt     time.Time
	EndsAt       *time.Time
	LocationName *string
	LocationLat  *float64
	LocationLng  *float64
	CoverMediaID *uuid.UUID
	IsTicketed   bool
	TicketPrice  *float64
	MaxAttendees *int
}

func (s *Service) CreateEvent(ctx context.Context, input CreateEventInput) (*postgres.Event, error) {
	if input.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if input.StartsAt.IsZero() {
		return nil, fmt.Errorf("starts_at is required")
	}
	e := &postgres.Event{
		CreatorID:    input.CreatorID,
		Title:        input.Title,
		Description:  input.Description,
		StartsAt:     input.StartsAt,
		EndsAt:       input.EndsAt,
		LocationName: input.LocationName,
		LocationLat:  input.LocationLat,
		LocationLng:  input.LocationLng,
		CoverMediaID: input.CoverMediaID,
		IsTicketed:   input.IsTicketed,
		TicketPrice:  input.TicketPrice,
		MaxAttendees: input.MaxAttendees,
		Status:       "upcoming",
	}
	return s.pgStore.CreateEvent(ctx, e)
}

func (s *Service) GetEvent(ctx context.Context, eventID uuid.UUID) (*postgres.Event, error) {
	e, err := s.pgStore.GetEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, fmt.Errorf("event not found")
	}
	return e, nil
}

func (s *Service) RSVPEvent(ctx context.Context, eventID, userID uuid.UUID, status string) error {
	validStatuses := map[string]bool{"going": true, "maybe": true, "not_going": true}
	if !validStatuses[status] {
		return fmt.Errorf("invalid status: must be going, maybe, or not_going")
	}
	return s.pgStore.UpsertEventRSVP(ctx, eventID, userID, status)
}

func (s *Service) GetEventRSVPs(ctx context.Context, eventID uuid.UUID, limit, offset int) ([]postgres.EventRSVP, error) {
	return s.pgStore.GetEventRSVPs(ctx, eventID, limit, offset)
}
