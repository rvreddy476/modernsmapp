package service

import (
	"strings"

	"github.com/google/uuid"
)

// Unlisted in listings (founder decision, 31 Oct 2026; audit M5).
//
// "Unlisted" means reachable by its link and nowhere else. The single-post
// gates (viewerMayViewPost, batchVisibility, the end-screen / related card,
// media access) keep opening an unlisted video to anyone holding the link.
// A LISTING of someone's videos never shows one to anyone but its author:
// for listings, unlisted is private.
//
// listedTo is that one rule, applied on every listing post-service answers
// that shows a creator's videos to other people:
//
//	profile grid and channel tabs   GET /v1/posts/by-author/:id      (post.go)
//	a playlist's items              GET /v1/playlists/:id/items       (playlists.go, SQL + Go)
//	an end screen's playlist frame  the borrowed first-item thumbnail (end_screens.go)
//	series episodes                 GET /v1/video-series/:id/episodes (series_visibility.go)
//	the watch page's series panel   GET /v1/posts/:id/series          (video_series.go)
//	a reels series                  GET /v1/series/:id/episodes       (series_visibility.go)
//	someone's reposts               GET /v1/creators/:id/reposts      (creator_reposts.go)
//
// Already public-only in SQL, so an unlisted row never reaches them: search
// (shared/events.SearchEligible), trending, recent, hashtag pages, sounds,
// live recordings, Appears in, the channel RSS feed, the end screen's
// latest / popular picks and the channel's public counts. Feeds and related
// videos are feed-service's listings, which drop unlisted at fan-out and
// at hydration (feed-service listingvisibility.go).
//
// Not listings, so unchanged: a playlist's OWNER reading their own playlist
// (a Watch later of a link they were sent), the viewer's own history,
// continue watching, likes and bookmarks, and links an author placed on
// purpose (an end-screen or card video element, the related-post card).

// isUnlistedVisibility reports the unlisted audience, spelled as the
// single-post gates spell it (case and padding ignored).
func isUnlistedVisibility(visibility string) bool {
	return strings.EqualFold(strings.TrimSpace(visibility), "unlisted")
}

// listedTo reports whether a post with this visibility and author may be
// shown in a listing to viewerID: every audience the caller's own gates
// already admitted, except an unlisted post, which is listed to its author
// alone. A signed-out viewer is nobody's author.
func listedTo(visibility string, authorID uuid.UUID, viewerID *uuid.UUID) bool {
	if !isUnlistedVisibility(visibility) {
		return true
	}
	return viewerID != nil && *viewerID == authorID && authorID != uuid.Nil
}
