package service

import "github.com/google/uuid"

// Instant publish (product decision 2026-09-04) on every feed surface.
//
// A reel is created the moment its upload is confirmed; transcoding and
// the safety scan run afterwards. Until every attached asset is
// ready+passed, post-service marks the post `is_processing` and returns it
// ONLY to its author. Fanout already wrote the follower timeline rows at
// create time, so the timeline is not the gate — hydration is.
//
// post-service's batch endpoint drops such rows for non-authors. This
// service also fronts that endpoint with a five-minute per-viewer
// hydration cache, so — exactly like block/mute, keywords and private
// accounts — the hydration tail re-applies the rule, where a cached row
// cannot slip through. There is no remote lookup here: the fact travels on
// the post itself, so the step is pure and cannot fail open.
//
// The author keeps their own processing post on home and reels
// immediately; the moment media-service flips the row, post-service stops
// marking it and the post reaches everyone with no further action.

// applyProcessingFilter drops every hydrated post that is still processing
// — or still SCHEDULED (publish_at set, 2026-09-05; same author-only rule,
// same reason: the fact travels on the post) — unless the viewer is its
// author. Reposts are judged by the original author: a repost of a
// processing post is that post, re-shared.
func applyProcessingFilter(viewerID uuid.UUID, posts []HydratedPost) []HydratedPost {
	out := posts[:0]
	for _, p := range posts {
		if (p.IsProcessing || p.IsScheduled) && p.AuthorID != viewerID {
			continue
		}
		out = append(out, p)
	}
	return out
}
