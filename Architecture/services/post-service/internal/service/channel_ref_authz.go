package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// A series or playlist may name only its creator's own channel (lv-7,
// long-video readiness audit, 2026-11-17).
//
// POST /v1/video-series, PATCH /v1/video-series/:id and POST /v1/playlists
// stored any channel_id they were given. Channel ids are public (channel
// search answers them), and video_series.channel_id / playlists.channel_id
// reference channels(id), so a stranger who pointed a row at someone's
// channel made that person's account erasure fail on the foreign key for
// ever (user-service purge deletes the channel). Migration 086 makes the
// reference ON DELETE SET NULL; this check stops the reference being made.
//
// The rule: a channel_id, when given, is the caller's own channel — the one
// channels row whose user_id is the caller (one channel per account). Fail
// closed: no channel store, a lookup error, no channel at all, or someone
// else's channel are all refusals; only a positive match is allowed.

// ErrNotChannelOwner is the 403 NOT_OWN_CHANNEL refusal.
var ErrNotChannelOwner = errors.New("forbidden: channel_id must be your own channel")

// requireOwnChannelRef allows a nil channelID (no channel named) and the
// caller's own channel id, and refuses everything else.
func (s *Service) requireOwnChannelRef(ctx context.Context, callerID uuid.UUID, channelID *uuid.UUID) error {
	if channelID == nil {
		return nil
	}
	if s.channels == nil {
		return errors.New("channel store not configured")
	}
	if callerID == uuid.Nil {
		return ErrNotChannelOwner
	}
	own, err := s.channels.GetChannelByUserID(ctx, callerID)
	if err != nil {
		return fmt.Errorf("lookup caller channel: %w", err)
	}
	if own == nil || own.ID != *channelID {
		return ErrNotChannelOwner
	}
	return nil
}
