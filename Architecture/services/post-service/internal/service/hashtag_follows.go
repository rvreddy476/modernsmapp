package service

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Followed hashtags (2026-10-24, migration 074).
//
//	POST   /v1/hashtags/:tag/follow     follow (idempotent)   -> {tag, following:true}
//	DELETE /v1/hashtags/:tag/follow     unfollow (idempotent) -> {tag, following:false}
//	GET    /v1/hashtags/:tag/follow     the caller's state    -> {tag, following}
//	GET    /v1/hashtags/followed        the caller's followed tags, newest first
//	GET    /v1/hashtags/followed/posts  recent public posts carrying any of them
//
// The tag is normalised exactly as a post's hashtags are
// (NormalizeExplicitHashtags: a leading # dropped, letters / digits /
// underscore / combining marks only, 1-50 characters, lowercased), so a
// follow of "#Cricket" matches posts stored with "cricket". A tag moderation
// blocked (hashtags.is_blocked) cannot be followed — posts never carry it —
// but can always be unfollowed.
//
// The followed-posts read is the hashtag page's read over several tags: the
// same eligibility in SQL (public, approved, published, live), the viewer's
// own posts left out, nothing older than followedPostsWindow, then the same
// author gate (private accounts, blocks in either direction, deactivated
// authors) and the same members-only redaction. It is a separate read, not
// part of the home feed: feed-service's home timeline is the fan-out in
// Scylla with a cursor over timeline keys, and has no hook for a
// post-service-owned candidate source.

const (
	// MaxFollowedHashtags is the per-user ceiling on followed tags.
	MaxFollowedHashtags = 500
	// followedPostsWindow is how far back the followed-posts read looks.
	followedPostsWindow = 30 * 24 * time.Hour
)

var (
	// ErrHashtagUnavailable: the tag is blocked by moderation.
	ErrHashtagUnavailable = errors.New("this hashtag is unavailable")
	// ErrHashtagFollowLimit: the caller follows MaxFollowedHashtags tags.
	ErrHashtagFollowLimit = postgres.ErrHashtagFollowLimit
)

// hashtagFollowStore is the slice of the Postgres store the follow routes
// use; an interface so the rules can be tested without a database.
type hashtagFollowStore interface {
	FollowHashtag(ctx context.Context, userID uuid.UUID, tag string, max int) (bool, error)
	UnfollowHashtag(ctx context.Context, userID uuid.UUID, tag string) (bool, error)
	IsFollowingHashtag(ctx context.Context, userID uuid.UUID, tag string) (bool, error)
	ListFollowedHashtags(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]postgres.FollowedHashtag, string, error)
	FollowedHashtagTags(ctx context.Context, userID uuid.UUID) ([]string, error)
	GetPostsByHashtags(ctx context.Context, tags []string, viewerID uuid.UUID, limit int, cursor string, since time.Time) ([]postgres.Post, string, error)
	GetBlockedHashtags(ctx context.Context, tags []string) ([]string, error)
}

func (s *Service) followStore() (hashtagFollowStore, error) {
	if s.hashtagFollows != nil {
		return s.hashtagFollows, nil
	}
	if s.pgStore != nil {
		return s.pgStore, nil
	}
	return nil, ErrAuthoringStoreUnavailable
}

// NormalizeFollowTag is the one tag a follow route addresses, in the form
// posts.hashtags stores (ErrInvalidHashtag otherwise).
func NormalizeFollowTag(raw string) (string, error) {
	tags, err := NormalizeExplicitHashtags([]string{raw})
	if err != nil {
		return "", err
	}
	if len(tags) != 1 {
		return "", ErrInvalidHashtag
	}
	return tags[0], nil
}

// HashtagFollowState is the follow routes' answer.
type HashtagFollowState struct {
	Tag         string `json:"tag"`
	DisplayName string `json:"display_name"`
	Following   bool   `json:"following"`
}

func followState(tag string, following bool) *HashtagFollowState {
	return &HashtagFollowState{Tag: tag, DisplayName: "#" + tag, Following: following}
}

// FollowHashtag follows raw for the caller. Idempotent.
func (s *Service) FollowHashtag(ctx context.Context, userID uuid.UUID, raw string) (*HashtagFollowState, error) {
	tag, err := NormalizeFollowTag(raw)
	if err != nil {
		return nil, err
	}
	st, err := s.followStore()
	if err != nil {
		return nil, err
	}
	blocked, err := st.GetBlockedHashtags(ctx, []string{tag})
	if err != nil {
		return nil, err
	}
	if len(blocked) > 0 {
		return nil, ErrHashtagUnavailable
	}
	if _, err := st.FollowHashtag(ctx, userID, tag, MaxFollowedHashtags); err != nil {
		return nil, err
	}
	return followState(tag, true), nil
}

// UnfollowHashtag unfollows raw for the caller. Idempotent, and open for a
// blocked tag.
func (s *Service) UnfollowHashtag(ctx context.Context, userID uuid.UUID, raw string) (*HashtagFollowState, error) {
	tag, err := NormalizeFollowTag(raw)
	if err != nil {
		return nil, err
	}
	st, err := s.followStore()
	if err != nil {
		return nil, err
	}
	if _, err := st.UnfollowHashtag(ctx, userID, tag); err != nil {
		return nil, err
	}
	return followState(tag, false), nil
}

// HashtagFollowStateFor is whether the caller follows raw.
func (s *Service) HashtagFollowStateFor(ctx context.Context, userID uuid.UUID, raw string) (*HashtagFollowState, error) {
	tag, err := NormalizeFollowTag(raw)
	if err != nil {
		return nil, err
	}
	st, err := s.followStore()
	if err != nil {
		return nil, err
	}
	ok, err := st.IsFollowingHashtag(ctx, userID, tag)
	if err != nil {
		return nil, err
	}
	return followState(tag, ok), nil
}

// FollowedHashtagView is one entry of GET /v1/hashtags/followed.
type FollowedHashtagView struct {
	Tag         string    `json:"tag"`
	DisplayName string    `json:"display_name"`
	FollowedAt  time.Time `json:"followed_at"`
}

// ListFollowedHashtags is the caller's followed tags, newest follow first.
func (s *Service) ListFollowedHashtags(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]FollowedHashtagView, string, error) {
	st, err := s.followStore()
	if err != nil {
		return nil, "", err
	}
	rows, next, err := st.ListFollowedHashtags(ctx, userID, limit, cursor)
	if err != nil {
		return nil, "", err
	}
	out := make([]FollowedHashtagView, 0, len(rows))
	for _, r := range rows {
		out = append(out, FollowedHashtagView{Tag: r.Tag, DisplayName: "#" + r.Tag, FollowedAt: r.FollowedAt})
	}
	return out, next, nil
}

// FollowedHashtagPosts is the recent public posts with any tag the caller
// follows, newest first, through the hashtag page's author gate and
// members-only redaction. The page may hold fewer than limit posts when the
// gate removed some; next_cursor still continues after the SQL page.
func (s *Service) FollowedHashtagPosts(ctx context.Context, viewerID uuid.UUID, limit int, cursor string) ([]PostDetail, string, error) {
	st, err := s.followStore()
	if err != nil {
		return nil, "", err
	}
	tags, err := st.FollowedHashtagTags(ctx, viewerID)
	if err != nil {
		return nil, "", err
	}
	if len(tags) == 0 {
		return []PostDetail{}, "", nil
	}
	posts, next, err := st.GetPostsByHashtags(ctx, tags, viewerID, limit, cursor, s.clock().Add(-followedPostsWindow))
	if err != nil {
		return nil, "", err
	}
	viewer := viewerID
	posts = s.hashtagPostsVisibleTo(ctx, &viewer, posts)
	details := make([]PostDetail, 0, len(posts))
	for i := range posts {
		details = append(details, PostDetail{Post: &posts[i]})
	}
	if s.followedPostsDecorate != nil {
		details, err = s.followedPostsDecorate(ctx, details)
	} else {
		details, err = s.decorateHashtagDetails(ctx, details)
	}
	if err != nil {
		return nil, "", err
	}
	// Members-only rows, for the real viewer: members and the author read
	// them in full (members_read.go), everyone else the redacted card.
	s.redactDetailsForViewer(ctx, &viewer, details)
	return details, next, nil
}

// decorateHashtagDetails is the hashtag page's per-row counts and media
// state (GetPostsByHashtag).
func (s *Service) decorateHashtagDetails(ctx context.Context, details []PostDetail) ([]PostDetail, error) {
	for i := range details {
		counts, _ := s.countsForPost(ctx, details[i].Post.ID)
		details[i].Counts = counts
	}
	return s.attachMediaStateToDetails(ctx, details, nil)
}
