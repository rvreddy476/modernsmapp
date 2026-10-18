package service

import (
	"strings"

	"github.com/google/uuid"
)

// Per-post visibility on every feed surface.
//
// A feed is a LISTING. post-service's single-post gate (viewerMayViewPost)
// lets anyone with the link open an `unlisted` post, and lets the people a
// `private` post is shared with open that one post — but "no listing calls
// it": neither may be listed to anyone but the author. Fan-out used to write
// both into every follower's home timeline (FanoutPost only special-cased
// "trusted"), and post-service's batch endpoint drops only `private`, so an
// unlisted post reached every follower's home, Following and Tube tabs.
//
// FanoutPost now keeps both off other people's timelines (fansOutToAudience).
// This step is the read-side half, for the rows that already exist, for the
// follow backfill (which copies the author timeline, where every post lands)
// and for five-minute hydration-cache rows that predate a visibility edit.
// Like applyProcessingFilter it is pure: the fact travels on the post, so it
// cannot fail open.
//
// Allowed through for a non-author: public, followers (post-service's batch
// checks the follow edge), and empty (legacy rows written before the column
// was populated, and items such as Q&A questions that carry none). Anything
// else — unlisted, private, staged, the retired circle/trusted/close_friends
// values, an unknown value — is the author's alone, matching the default
// branch of viewerMayViewPost.

// listableVisibility reports whether a post with this visibility may be
// listed in a feed to someone other than its author.
func listableVisibility(visibility string) bool {
	switch strings.ToLower(strings.TrimSpace(visibility)) {
	// friends (2026-10-18): mutual follows. Like followers it fans out to
	// the author's followers and post-service's batch decides per viewer —
	// it drops a friends post for anyone the author does not follow back.
	case "", "public", "followers", "friends":
		return true
	}
	return false
}

// fansOutToAudience reports whether FanoutPost may write a post with this
// visibility into anyone's home timeline besides its author's. "trusted"
// keeps its own close-friends branch in FanoutPost.
func fansOutToAudience(visibility string) bool {
	if visibility == "trusted" {
		return true
	}
	return listableVisibility(visibility)
}

// applyListingVisibilityFilter drops every hydrated post the viewer did not
// write whose visibility is not listable. Reposts are judged by the original
// post, which is what the hydrated row describes.
func applyListingVisibilityFilter(viewerID uuid.UUID, posts []HydratedPost) []HydratedPost {
	out := posts[:0]
	for _, p := range posts {
		if p.AuthorID != viewerID && !listableVisibility(p.Visibility) {
			continue
		}
		out = append(out, p)
	}
	return out
}
