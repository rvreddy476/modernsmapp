package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/group-service/internal/mediaclaims"
	"github.com/atpost/group-service/internal/mediaclient"
	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
)

// ── V2 Events ────────────────────────────────────────────────

func (s *Service) CreateGroupEvent(ctx context.Context, actorID, groupID uuid.UUID, event *store.GroupEvent) error {
	if _, err := s.authorizeRank(ctx, groupID, actorID, rankModerator,
		errors.New("forbidden: only admins or moderators can create events")); err != nil {
		return err
	}
	event.GroupID = groupID
	event.CreatorID = actorID.String()
	// The id is fixed before the write so the cover can be claimed against
	// it; the cover must be the actor's own finished upload (media_claims.go).
	event.ID = uuid.New()
	add, _ := mediaclaims.OptionalDiff(nil, event.CoverMediaID)
	return s.media.Attach(ctx, actorID, []mediaclaims.Change{
		{Kind: mediaclient.KindGroupEventCover, Ref: event.ID, Add: add},
	}, func() error {
		return s.store.CreateGroupEvent(ctx, event)
	})
}

func (s *Service) ListGroupEvents(ctx context.Context, actorID, groupID uuid.UUID, limit, offset int) ([]store.GroupEvent, error) {
	if _, err := s.authorize(ctx, groupID, actorID, accessRead); err != nil {
		return nil, err
	}
	return s.store.ListGroupEvents(ctx, groupID, limit, offset)
}

func (s *Service) GetGroupEvent(ctx context.Context, actorID, groupID, eventID uuid.UUID) (*store.GroupEvent, error) {
	if _, err := s.authorize(ctx, groupID, actorID, accessRead); err != nil {
		return nil, err
	}
	e, err := s.store.GetGroupEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if e.GroupID != groupID {
		return nil, fmt.Errorf("not found: event not found in this group")
	}
	return e, nil
}

func (s *Service) DeleteGroupEvent(ctx context.Context, actorID, groupID, eventID uuid.UUID) error {
	if _, err := s.authorizeRank(ctx, groupID, actorID, rankModerator,
		errors.New("forbidden: only admins or moderators can delete events")); err != nil {
		return err
	}
	e, err := s.store.GetGroupEvent(ctx, eventID)
	if err != nil {
		return err
	}
	if e.GroupID != groupID {
		return fmt.Errorf("not found: event not found in this group")
	}
	if err := s.store.DeleteGroupEvent(ctx, eventID); err != nil {
		return err
	}
	// A cancelled event is never shown again: release its cover.
	if e.CoverMediaID != nil {
		s.media.Release(ctx, mediaclient.KindGroupEventCover, eventID, nil)
	}
	return nil
}

func (s *Service) RSVPGroupEvent(ctx context.Context, actorID, groupID, eventID uuid.UUID, status string) error {
	if status != "going" && status != "maybe" && status != "not_going" {
		return fmt.Errorf("invalid RSVP status: %s", status)
	}
	// Must be an active, unbanned member of the path group.
	if _, err := s.authorize(ctx, groupID, actorID, accessWrite); err != nil {
		return err
	}
	e, err := s.store.GetGroupEvent(ctx, eventID)
	if err != nil {
		return err
	}
	if e.GroupID != groupID {
		return fmt.Errorf("not found: event not found in this group")
	}
	if !e.RSVPEnabled {
		return fmt.Errorf("RSVP is disabled for this event")
	}
	return s.store.RSVPGroupEvent(ctx, eventID, actorID.String(), status)
}
