package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

/*
	Post screens parity (2026-10-18) — the photo-post details screen.

	The create route (POST /v1/posts), PATCH /v1/posts/:id and the composer
	drafts (/v1/posts/drafts) read the fields below through these rules, so
	the three can never disagree about what a post may carry:

	  visibility "friends"   mutual follows: the viewer follows the author AND
	                         the author follows the viewer. "Only you" is
	                         'private' with an empty share list — not a value.
	  cover_media_id         for post_type=image, one of the post's media_ids
	                         (400 INVALID_COVER otherwise); cover_index is the
	                         0-based alternative and must agree with it.
	  comment_access         everyone | followers | nobody (400 INVALID_COMMENT_ACCESS),
	                         enforced on comment create (commentAccessGate).
	  remix_setting          allow | allow_audio_only | disallow (400 INVALID_REMIX_SETTING)
	                         — the "Allow reuse of content" switch.
	  link_url               https only, at most 500 characters, a host and no
	                         user info (400 INVALID_LINK_URL; 422 on PATCH).
	  ai_generated           boolean, default false.
	  is_branded             boolean next to paid_promotion (content disclosure).
	  location_place_id      the place provider's id, at most 255 characters of
	                         [A-Za-z0-9_-:.] (400 INVALID_LOCATION_PLACE_ID).
*/

// PostVisibilityFriends is the mutual-follows audience.
const PostVisibilityFriends = "friends"

// MaxLinkURLLength caps link_url (migration 066's CHECK says the same).
const MaxLinkURLLength = 500

// MaxLocationPlaceIDLength caps location_place_id (migration 066).
const MaxLocationPlaceIDLength = 255

var (
	// ErrInvalidCover: on a photo post, the cover is not one of its media,
	// or cover_index is out of range or disagrees with cover_media_id.
	ErrInvalidCover = errors.New("cover_media_id must be one of the post's media_ids")
	// ErrInvalidLinkURL: link_url is not an https URL of at most 500 characters.
	ErrInvalidLinkURL = fmt.Errorf("link_url must be an https URL of at most %d characters", MaxLinkURLLength)
	// ErrInvalidLocationPlaceID: location_place_id has the wrong shape.
	ErrInvalidLocationPlaceID = fmt.Errorf("location_place_id must be 1-%d characters of letters, digits, '-', '_', ':' or '.'", MaxLocationPlaceIDLength)
)

// repostableVisibility is IsRepostable: a private or friends-only post
// cannot be reposted or quoted to the reposter's own audience.
func repostableVisibility(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "private", PostVisibilityFriends:
		return false
	}
	return true
}

// NormalizeLinkURL trims raw and returns nil for "" (no link). Anything
// else must be an absolute https URL with a host, no user info (the
// "https://bank.example@evil.example" trick), no whitespace or control
// characters, and at most MaxLinkURLLength characters. javascript:, data:,
// http: and every other scheme are refused.
func NormalizeLinkURL(raw string) (*string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(v) > MaxLinkURLLength {
		return nil, ErrInvalidLinkURL
	}
	for _, r := range v {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return nil, ErrInvalidLinkURL
		}
	}
	u, err := url.Parse(v)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Opaque != "" ||
		u.User != nil || u.Hostname() == "" {
		return nil, ErrInvalidLinkURL
	}
	// The DB CHECK matches a literal "https://" prefix; "https:host" parses
	// with an empty Host above, so only the canonical form reaches here.
	if !strings.HasPrefix(strings.ToLower(v), "https://") {
		return nil, ErrInvalidLinkURL
	}
	return &v, nil
}

// NormalizeLocationPlaceID trims raw and returns nil for "" (no place id).
func NormalizeLocationPlaceID(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*raw)
	if v == "" {
		return nil, nil
	}
	if len(v) > MaxLocationPlaceIDLength {
		return nil, ErrInvalidLocationPlaceID
	}
	for _, r := range v {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '-' || r == '_' || r == ':' || r == '.'
		if !ok {
			return nil, ErrInvalidLocationPlaceID
		}
	}
	return &v, nil
}

// normalizeCreateEnum is the create-side reading of an enum the edit route
// already validates: "" keeps the caller's default, anything else must be
// in allowed (lowercased, trimmed) or it is bad.
func normalizeCreateEnum(raw string, allowed []string, def string, bad error) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return def, nil
	}
	v, ok := normalizeEnum(raw, allowed)
	if !ok {
		return "", bad
	}
	return v, nil
}

// ResolvePostCover settles the cover of a new post.
//
// cover_index picks the cover by its 0-based position in media_ids and is
// only meaningful on a photo post; when cover_media_id is sent too, the two
// must name the same asset. On a photo post (post_type=image) the cover must
// be one of the post's own media: a carousel's cover is one of its pages.
// Other kinds keep their own rule (a video's cover is an uploaded frame,
// checked by verifyCoverOnWrite).
func ResolvePostCover(postType string, mediaIDs []uuid.UUID, cover *uuid.UUID, coverIndex *int) (*uuid.UUID, error) {
	isImage := strings.EqualFold(strings.TrimSpace(postType), postTypeImage)
	if coverIndex != nil {
		if !isImage || *coverIndex < 0 || *coverIndex >= len(mediaIDs) {
			return nil, ErrInvalidCover
		}
		picked := mediaIDs[*coverIndex]
		if cover != nil && *cover != picked {
			return nil, ErrInvalidCover
		}
		cover = &picked
	}
	if cover == nil || !isImage {
		return cover, nil
	}
	for _, id := range mediaIDs {
		if id == *cover {
			return cover, nil
		}
	}
	return nil, ErrInvalidCover
}

// createVisibilities is what a composer may choose on create and in a
// draft: the edit enum (PostVisibilities). 'staged' and the retired
// circle values are the pipeline's or nobody's.
func createVisibilityAllowed(v string) bool {
	return editVisibilityAllowed(strings.ToLower(strings.TrimSpace(v)))
}

// ErrCommentsFollowersOnly: the post's comment_access is "followers" and the
// commenter does not follow its author (or the graph could not say).
var ErrCommentsFollowersOnly = errors.New("only followers can comment on this post")

// commentAccessAllows enforces the post's comment_access on every comment
// write (top-level, reply, reel comment). It was stored and shown, and never
// asked. "nobody" is comments off, like no_comments; "followers" admits the
// author and the people who follow them, failing closed on a graph outage;
// an unknown stored value refuses, as every audience rule here does.
func (s *Service) commentAccessAllows(ctx context.Context, post *postgres.Post, commenterID uuid.UUID) error {
	switch strings.ToLower(strings.TrimSpace(post.CommentAccess)) {
	case "", "everyone":
		return nil
	case "followers":
		if commenterID == post.AuthorID {
			return nil
		}
		follows, err := s.checkViewerFollowsAuthor(ctx, commenterID, post.AuthorID)
		if err != nil || !follows {
			return ErrCommentsFollowersOnly
		}
		return nil
	default: // "nobody" and anything unrecognised
		return ErrCommentsDisabled
	}
}

// profileGridAllows is the profile grid's per-post audience rule for a
// viewer who is not the author: the same answers as viewerMayViewPost, with
// the graph asked lazily (one lookup per page, and only when a restricted
// post is on it). private, the retired circle values and anything unknown
// are the author's alone.
func profileGridAllows(visibility string, follows, mutual func() bool) bool {
	switch strings.ToLower(visibility) {
	case "", "public", "unlisted":
		return true
	case "followers":
		return follows()
	case PostVisibilityFriends:
		return mutual()
	default:
		return false
	}
}

// shareRestricted is SharePost's audience rule: a private post is never
// shared, and a followers or friends post only outside the app (external
// link), never re-posted into someone else's audience.
func shareRestricted(visibility, shareType string) bool {
	switch visibility {
	case "private":
		return true
	case "followers", PostVisibilityFriends:
		return shareType != "external"
	}
	return false
}
