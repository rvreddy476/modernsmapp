package service

import (
	"context"
	"errors"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

/*
	Heart only, and no reposting a reel (founder decision, 24 Oct 2026).

	"6 emoji reactions are only for comments. Feed posts, reels and videos
	use a TikTok-style heart only. Feed keeps repost; Reels don't have
	repost."

	WRITES

	Every post-reaction write takes ONE reaction, the heart ("like"), on a
	post of every content type:

	  POST /v1/posts/:id/reactions   {reaction}       React
	  POST /v1/posts/:id/react       {reaction_type}  ToggleReaction
	  POST /v1/reels/:id/react       {reaction}       ReactToReel
	  POST /v1/posts/:id/like        (no type)        ToggleLike, already a heart

	Anything else is 400 REACTION_NOT_ALLOWED {details: {allowed: ["like"]}}.
	The check is the first thing each write does, before the post is read,
	so it cannot depend on the post's content type. Comment reactions
	(PUT /v1/comments/:id/reaction, POST /v1/comments/:id/like) keep their
	emoji set and never pass through here.

	Reposting short-form (flick, or the legacy "reel" spelling) is refused
	with 400 REPOST_NOT_ALLOWED {details: {content_type}} on every repost
	route:

	  POST /v1/posts/:id/repost  {type: plain|quote}            CreateRepost
	  POST /v1/posts/:id/share   {share_type: repost|quote}     SharePost
	  POST /v1/reels/:id/share   {share_type: repost|quote}     ShareReel

	An external / direct share (a link sent elsewhere) is not a repost and
	is unchanged. Feed posts and long videos keep repost. Existing reel
	reposts are left as they are (undo still works, counts still read).

	READS

	No stored reaction is rewritten. A viewer whose old reaction was love,
	haha, wow, sad or angry reads as having given the heart:
	viewer_reaction = "like", has_reacted = true. counts.likes (Scylla
	post_counters / reel_counts) already counted one per person whatever
	the type, so it is unchanged. The breakdown GET /v1/posts/:id/reactions/
	counts folds every emoji into "like". "tune" is the private dislike, not
	a reaction, and never reads as a heart.
*/

// PostReactionHeart is the one reaction a post accepts.
const PostReactionHeart = "like"

// postReactionsAllowed is the REACTION_NOT_ALLOWED details list.
var postReactionsAllowed = []string{PostReactionHeart}

// PostReactionsAllowed returns a fresh copy of the allowed list for the
// error details (callers cannot mutate the package's slice).
func PostReactionsAllowed() []string {
	return append([]string(nil), postReactionsAllowed...)
}

// ErrReactionNotAllowed refuses any post reaction but the heart.
var ErrReactionNotAllowed = errors.New("REACTION_NOT_ALLOWED")

// checkPostReaction is the write guard every post-reaction route runs first.
func checkPostReaction(reaction string) error {
	if reaction == PostReactionHeart {
		return nil
	}
	return ErrReactionNotAllowed
}

// heartViewerReaction is what a viewer's stored reaction on a post reads
// as: "like" for any reaction they gave (an old emoji included), "" for none
// and for the private dislike.
func heartViewerReaction(stored string) string {
	switch stored {
	case "", "tune":
		return ""
	}
	return PostReactionHeart
}

// applyViewerReaction sets viewer_reaction and has_reacted on a post read
// from the viewer's stored reaction ("" for none).
func applyViewerReaction(d *PostDetail, stored string) {
	if r := heartViewerReaction(stored); r != "" {
		d.ViewerReaction = &r
		d.HasReacted = true
	}
}

// foldPostReactionCounts is the post reaction breakdown under the heart-only
// rule: every emoji someone gave counts as a like, the other buckets read
// zero and total equals like. Nil-safe.
func foldPostReactionCounts(c *postgres.ReactionCounts) *postgres.ReactionCounts {
	if c == nil {
		return nil
	}
	hearts := c.Like + c.Love + c.Haha + c.Wow + c.Sad + c.Angry
	return &postgres.ReactionCounts{Like: hearts, Total: hearts}
}

// RepostNotAllowedError refuses a repost of short-form content.
type RepostNotAllowedError struct {
	ContentType string
}

func (e *RepostNotAllowedError) Error() string { return "REPOST_NOT_ALLOWED" }

// isShortFormContentType is a reel: "flick", or the legacy "reel" spelling.
func isShortFormContentType(ct string) bool {
	return ct == "flick" || ct == "reel"
}

// checkRepostContentType is the repost guard: short-form is never reposted.
func checkRepostContentType(contentType string) error {
	if isShortFormContentType(contentType) {
		return &RepostNotAllowedError{ContentType: contentType}
	}
	return nil
}

// isRepostShareType reports whether a share route's share_type is a repost
// (plain or quote) rather than a link sent elsewhere.
func isRepostShareType(shareType string) bool {
	return shareType == "repost" || shareType == "quote"
}

// repostableContent is is_repostable on a post read: the visibility rule,
// and never for short-form.
func repostableContent(contentType, visibility string) bool {
	return !isShortFormContentType(contentType) && repostableVisibility(visibility)
}

// loadRepostTarget is the post a repost route reads (nil, nil when absent).
func (s *Service) loadRepostTarget(ctx context.Context, postID uuid.UUID) (*postgres.Post, error) {
	if s.repostTargetSeam != nil {
		return s.repostTargetSeam(ctx, postID)
	}
	return s.pgStore.GetPost(ctx, postID)
}
