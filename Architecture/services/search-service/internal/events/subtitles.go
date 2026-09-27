package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/atpost/shared/events"
)

// has_subtitles on post documents (2026-09-27).
//
// media-service announces the published-caption state of one asset
// (events.MediaSubtitlesChanged, on `media.events`). post-service does not
// know caption state, so nothing else ever set the flag behind the `cc`
// feature filter. This handler resolves the asset to the posts that attach
// it (post-service GET /v1/internal/posts/by-media/:mediaId) and sets the
// flag on each post document (search.Store.SetPostHasSubtitles).
//
// The payload is a state snapshot: setting it twice is the same as once,
// and a snapshot older than the one already applied is dropped by the store
// (ordered by envelope.OccurredAt, which media-service stamps under the
// asset's row lock).

// PostsByMedia resolves a media id to the ids of the posts that attach it.
// *postclient.Client implements it.
type PostsByMedia interface {
	PostIDsByMedia(ctx context.Context, mediaID string) ([]string, error)
}

// postSubtitlesWriter is the one store write this handler makes.
// *search.Store implements it; tests substitute a fake.
type postSubtitlesWriter interface {
	SetPostHasSubtitles(ctx context.Context, postID string, has bool, version int64) error
}

// errPostsByMediaNotConfigured: a caption snapshot cannot be placed without
// the post lookup. An error, not a skip — a silent skip would leave the flag
// wrong with nothing to show for it; an error retries and dead-letters.
var errPostsByMediaNotConfigured = errors.New("search: MediaSubtitlesChanged needs the post-service lookup (POST_SERVICE_URL)")

// WithPostsByMedia wires the post lookup the caption handler needs.
func (c *Consumer) WithPostsByMedia(p PostsByMedia) *Consumer {
	c.postsByMedia = p
	return c
}

// handleSubtitlesChanged applies one MediaSubtitlesChanged envelope.
func (c *Consumer) handleSubtitlesChanged(ctx context.Context, envelope events.EventEnvelope) error {
	var p events.MediaSubtitlesChangedPayload
	if err := unmarshalPayload(envelope.Payload, &p); err != nil {
		return err
	}
	var writer postSubtitlesWriter
	if c.store != nil {
		writer = c.store
	}
	return applySubtitlesChanged(ctx, c.postsByMedia, writer, p, subtitlesVersion(envelope))
}

// subtitlesVersion is the snapshot's order: its OccurredAt in microseconds
// (the database's precision). A producer that sent none is version 0 — older
// than anything stamped, so it can fill an unset flag but never overwrite a
// stamped one.
func subtitlesVersion(envelope events.EventEnvelope) int64 {
	if envelope.OccurredAt.IsZero() {
		return 0
	}
	return envelope.OccurredAt.UnixMicro()
}

func applySubtitlesChanged(ctx context.Context, lookup PostsByMedia, writer postSubtitlesWriter, p events.MediaSubtitlesChangedPayload, version int64) error {
	if p.MediaID == "" {
		// Undecodable intent: nothing to place. Logged, not retried —
		// retrying cannot make a media id appear.
		slog.Warn("search: MediaSubtitlesChanged without media_id; ignored")
		return nil
	}
	if lookup == nil {
		return errPostsByMediaNotConfigured
	}
	if writer == nil {
		return errors.New("search: no post store for MediaSubtitlesChanged")
	}
	postIDs, err := lookup.PostIDsByMedia(ctx, p.MediaID)
	if err != nil {
		return fmt.Errorf("resolve posts for media %s: %w", p.MediaID, err)
	}
	for _, postID := range postIDs {
		if err := writer.SetPostHasSubtitles(ctx, postID, p.HasPublishedSubtitles, version); err != nil {
			return fmt.Errorf("has_subtitles on post %s: %w", postID, err)
		}
	}
	slog.Debug("search: caption state applied", "media_id", p.MediaID,
		"has_subtitles", p.HasPublishedSubtitles, "posts", len(postIDs), "version", version)
	return nil
}
