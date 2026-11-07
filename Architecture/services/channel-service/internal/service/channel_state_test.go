package service

import (
	"context"
	"testing"
	"time"

	channelevents "github.com/atpost/channel-service/internal/events"
	"github.com/atpost/channel-service/internal/store"
	"github.com/google/uuid"
)

// recordedState records what was announced.
type recordedState struct {
	snaps []channelevents.ChannelSnapshot
}

func (r *recordedState) PublishChannelUpdated(_ context.Context, snap channelevents.ChannelSnapshot, _ *uuid.UUID) error {
	r.snaps = append(r.snaps, snap)
	return nil
}

// The snapshot is the row: every field search filters on comes from it.
func TestChannelSnapshotOfCarriesStatusAndType(t *testing.T) {
	ch := &store.BroadcastChannel{
		ID: uuid.New(), OwnerID: uuid.New(), Handle: "h", Name: "n", Description: "d",
		ChannelType: "paid", Category: "c", IsVerified: true, SubscriberCount: 7,
		Status: "suspended", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	s := channelSnapshotOf(ch)
	if s.ChannelID != ch.ID.String() || s.OwnerID != ch.OwnerID.String() || s.Handle != "h" ||
		s.Name != "n" || s.Description != "d" || s.Category != "c" || !s.IsVerified || s.SubscriberCount != 7 {
		t.Fatalf("snapshot = %+v", s)
	}
	if s.Status != "suspended" {
		t.Fatalf("status = %q: a suspended channel must be announced as suspended", s.Status)
	}
	if s.ChannelType != "paid" || s.Visibility != "private" {
		t.Fatalf("type %q visibility %q: a paid channel is private", s.ChannelType, s.Visibility)
	}
}

func TestAnnounceChannelStateUsesTheSeam(t *testing.T) {
	rec := &recordedState{}
	s := &Service{stateSeam: rec}
	ch := &store.BroadcastChannel{ID: uuid.New(), OwnerID: uuid.New(), ChannelType: "public", Status: "active"}
	s.announceChannelState(context.Background(), ch, nil)
	if len(rec.snaps) != 1 || rec.snaps[0].ChannelID != ch.ID.String() {
		t.Fatalf("announced %+v", rec.snaps)
	}
	// No producer, no seam: nothing to do, and no panic.
	(&Service{}).announceChannelState(context.Background(), ch, nil)
}
