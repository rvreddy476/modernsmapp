package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

// Instant publish (product decision 2026-09-04).
//
// A post is created the moment its media upload is CONFIRMED; transcoding
// and the safety scan run afterwards. Until every attached asset is
// `ready` + `passed` the post is `is_processing` and is returned ONLY to
// its author — on the direct read, on every batch, and (mirrored in
// feed-service's hydration tail) on every feed surface. The moment
// media-service flips the row the post is visible to everyone; nothing
// else has to happen, because the decision is made at read time from the
// live media_assets row rather than from an event.
//
// This is the same `ready` + `passed` rule the create gate used to enforce,
// moved from "may this post exist" to "who may see it".

// mediaConfirmed reports whether an asset's bytes have arrived and nothing
// has refused them — the create-time allowlist.
func mediaConfirmed(processingStatus string) bool {
	switch processingStatus {
	case "uploaded", "processing", mediaReady:
		return true
	}
	return false
}

// mediaPublishable is the author-only visibility rule: exact `ready` AND
// exact `passed`. Anything else — including an empty status from a missing
// row — is "not yet", never "close enough".
func mediaPublishable(processingStatus, moderationStatus string) bool {
	return processingStatus == mediaReady && moderationStatus == mediaPassed
}

// applyMediaState overlays the live pipeline state onto a post's media and
// derives IsProcessing. A media id absent from `state` is treated as NOT
// publishable: post_media has an ON DELETE RESTRICT foreign key, so an
// absent row is a fault, and a fault must hide the post rather than
// publish it.
//
// Pure, so it is unit-testable without a database.
func applyMediaState(p *postgres.Post, state map[uuid.UUID]postgres.MediaOwnership) {
	if p == nil {
		return
	}
	processing := false
	for i := range p.Media {
		m, ok := state[p.Media[i].MediaID]
		if ok {
			p.Media[i].ProcessingStatus = m.ProcessingStatus
			p.Media[i].ModerationStatus = m.ModerationStatus
		} else {
			p.Media[i].ProcessingStatus = ""
			p.Media[i].ModerationStatus = ""
		}
		if !mediaPublishable(p.Media[i].ProcessingStatus, p.Media[i].ModerationStatus) {
			processing = true
		}
	}
	p.IsProcessing = processing
}

// hiddenWhileProcessing is the read-side gate: a processing post is hidden
// from everyone but its author. An anonymous viewer is never the author.
func hiddenWhileProcessing(p *postgres.Post, viewerID *uuid.UUID) bool {
	if p == nil || !p.IsProcessing {
		return false
	}
	return viewerID == nil || *viewerID != p.AuthorID
}

// attachMediaState loads the live media_assets state for every asset on
// the given posts in ONE round trip and overlays it. It runs AFTER the
// post-body cache, deliberately: the cached body may be minutes old, and
// "is this reel done transcoding" is exactly the fact that must never be
// served stale.
//
// Fail closed: an unreadable media_assets table is an error, not
// permission to publish.
func (s *Service) attachMediaState(ctx context.Context, posts []*postgres.Post) error {
	var ids []uuid.UUID
	seen := make(map[uuid.UUID]struct{})
	for _, p := range posts {
		if p == nil {
			continue
		}
		for _, m := range p.Media {
			if _, ok := seen[m.MediaID]; ok {
				continue
			}
			seen[m.MediaID] = struct{}{}
			ids = append(ids, m.MediaID)
		}
	}
	var state map[uuid.UUID]postgres.MediaOwnership
	if len(ids) > 0 {
		var err error
		state, err = s.pgStore.BatchGetMediaOwnership(ctx, ids)
		if err != nil {
			return fmt.Errorf("load media processing state: %w", err)
		}
	}
	for _, p := range posts {
		applyMediaState(p, state)
	}
	return nil
}

// attachMediaStateToDetails is attachMediaState over a page of details and
// drops the rows the viewer may not see while they process. The page order
// is preserved.
func (s *Service) attachMediaStateToDetails(ctx context.Context, details []PostDetail, viewerID *uuid.UUID) ([]PostDetail, error) {
	posts := make([]*postgres.Post, 0, len(details))
	for i := range details {
		posts = append(posts, details[i].Post)
	}
	if err := s.attachMediaState(ctx, posts); err != nil {
		return nil, err
	}
	out := details[:0]
	for _, d := range details {
		if hiddenWhileProcessing(d.Post, viewerID) {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}
