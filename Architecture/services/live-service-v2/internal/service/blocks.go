package service

// A block between a viewer and a live host takes the viewer out of the room
// at once (audit LV-M1, 7 Nov 2026). graph-service publishes UserBlocked
// after the block committed (internal/blockevents feeds it here). Without
// this, a block landed only on the viewer's next token or next reconnect.
//
// Either direction counts, as in canSee: the host blocking a viewer, or a
// viewer blocking the host. For each stream the host has on air where the
// other person is present, the room gate is asked again (MayWatch) and the
// participant is removed unless it now admits them (the block was lifted
// before this ran). An undecidable gate removes them anyway: the event says
// a block was made, and failing open here would leave them watching.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// OnUserBlocked applies a block made by blockerID against blockedID to every
// live room either of them hosts. It returns the first error it met (after
// trying every stream) so the caller may retry; a retry is harmless.
func (s *Service) OnUserBlocked(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	if blockerID == uuid.Nil || blockedID == uuid.Nil || blockerID == blockedID {
		return nil
	}
	// The relationship cache must not answer "not blocked" for the next
	// join or token request (relationshipCacheTTL).
	s.forgetRelationship(ctx, blockerID, blockedID)

	var firstErr error
	note := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	for _, pair := range [2][2]uuid.UUID{{blockerID, blockedID}, {blockedID, blockerID}} {
		host, viewer := pair[0], pair[1]
		ids, err := s.store.ActiveStreamsOf(ctx, host)
		if err != nil {
			note(fmt.Errorf("active streams of the host: %w", err))
			continue
		}
		for _, id := range ids {
			if err := s.removeBlockedViewer(ctx, id, viewer); err != nil {
				note(err)
			}
		}
	}
	return firstErr
}

// removeBlockedViewer removes viewer from the stream's room when they are in
// it and the room gate no longer admits them.
func (s *Service) removeBlockedViewer(ctx context.Context, streamID, viewer uuid.UUID) error {
	st, err := s.store.GetByID(ctx, streamID)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load stream: %w", err)
	}
	present, err := s.store.IsViewerPresent(ctx, streamID, viewer)
	if err != nil {
		return fmt.Errorf("viewer presence: %w", err)
	}
	if !present {
		// Not in the room (yet): a join that lands later meets the gate in
		// applyPresence, with the cache already cleared.
		return nil
	}
	ok, err := s.MayWatch(ctx, streamID, viewer)
	if err == nil && ok {
		return nil // admitted again: the block was lifted before this ran
	}
	if err != nil {
		slog.WarnContext(ctx, "live-v2: room gate undecided after a block; viewer removed", "stream_id", streamID, "err", err)
	}
	s.kick(ctx, st, viewer)
	return nil
}

// forgetRelationship drops the cached relationship both ways between a and b.
func (s *Service) forgetRelationship(ctx context.Context, a, b uuid.UUID) {
	if s.redis == nil {
		return
	}
	if err := s.redis.Del(ctx, relationshipCacheKey(a, b), relationshipCacheKey(b, a)).Err(); err != nil {
		slog.WarnContext(ctx, "live-v2: drop cached relationship after a block", "err", err)
	}
}
