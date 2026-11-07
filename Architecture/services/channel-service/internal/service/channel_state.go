package service

import (
	"context"
	"log/slog"

	channelevents "github.com/atpost/channel-service/internal/events"
	"github.com/atpost/channel-service/internal/store"
	"github.com/google/uuid"
)

// Telling consumers what a channel now is (2026-11-07).
//
// channel.updated used to carry the channel id, the actor and a timestamp,
// and suspensions and archives announced nothing at all. search-service
// could therefore not apply a rename, a change of type or a suspension: an
// edited channel kept its old name, a channel made private stayed findable,
// and a suspended one stayed in results until somebody ran the backfill.
// Every change consumers care about now publishes channel.updated with the
// whole row as committed (channelevents.ChannelSnapshot).

// channelStatePublisher is the producer's channel.updated, behind an
// interface so tests can record what was announced.
type channelStatePublisher interface {
	PublishChannelUpdated(ctx context.Context, snap channelevents.ChannelSnapshot, actorID *uuid.UUID) error
}

func (s *Service) statePublisher() channelStatePublisher {
	if s.stateSeam != nil {
		return s.stateSeam
	}
	if s.producer != nil {
		return s.producer
	}
	return nil
}

// channelSnapshotOf is the one conversion from the row to the event. Pure.
func channelSnapshotOf(ch *store.BroadcastChannel) channelevents.ChannelSnapshot {
	return channelevents.ChannelSnapshot{
		ChannelID:       ch.ID.String(),
		OwnerID:         ch.OwnerID.String(),
		Handle:          ch.Handle,
		Name:            ch.Name,
		Description:     ch.Description,
		ChannelType:     ch.ChannelType,
		Visibility:      VisibilityOf(ch.ChannelType),
		Category:        ch.Category,
		IsVerified:      ch.IsVerified,
		SubscriberCount: ch.SubscriberCount,
		Status:          ch.Status,
		CreatedAt:       ch.CreatedAt,
		UpdatedAt:       ch.UpdatedAt,
	}
}

// announceChannelState publishes channel.updated for a row the caller has
// just written. Best effort like every channel event: the change is
// committed, and a lost event is repaired by the search backfill.
func (s *Service) announceChannelState(ctx context.Context, ch *store.BroadcastChannel, actorID *uuid.UUID) {
	pub := s.statePublisher()
	if pub == nil || ch == nil {
		return
	}
	if err := pub.PublishChannelUpdated(ctx, channelSnapshotOf(ch), actorID); err != nil {
		slog.Warn("failed to publish channel.updated event", "channel_id", ch.ID, "error", err)
	}
}

// announceChannelStateByID re-reads the row first, for the writes that
// change the status only (suspend, unsuspend). GetChannelByID reads
// suspended and archived rows; a deleted row announces channel.deleted
// instead (DeleteChannel).
func (s *Service) announceChannelStateByID(ctx context.Context, channelID uuid.UUID, actorID *uuid.UUID) {
	if s.statePublisher() == nil || s.store == nil {
		return
	}
	ch, err := s.store.GetChannelByID(ctx, channelID)
	if err != nil {
		slog.Warn("channel.updated not published: re-reading the channel failed",
			"channel_id", channelID, "error", err)
		return
	}
	s.announceChannelState(ctx, ch, actorID)
}

// AnnounceSystemChange publishes channel.updated after a change no person
// made (the account-deletion consumer's archive). cmd/server wires it as the
// consumer's archived hook.
func (s *Service) AnnounceSystemChange(ctx context.Context, channelID uuid.UUID) {
	s.announceChannelStateByID(ctx, channelID, nil)
}
