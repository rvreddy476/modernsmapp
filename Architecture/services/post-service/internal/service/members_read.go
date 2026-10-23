package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Members-only reads (2026-10-23 audit).
//
// A creator gates a post on a membership tier (posts.tier_required_id, PUT
// /v1/posts/:postId/membership). Until this file the gate was applied on the
// direct read alone: POST /v1/posts/batch — which every feed-service surface
// hydrates from — and every list in this service returned the full body to
// anyone the post's visibility admitted.
//
// redactForViewer is now THE rule for a page of post rows, and every surface
// that answers post rows goes through it (members_read_inventory_test.go
// fails the build otherwise):
//
//   - an ungated row (tier_required_id NULL) is untouched;
//   - the author always sees their own row in full;
//   - a signed-out viewer always gets the redacted row;
//   - anyone else is asked of monetization-service ONCE per distinct
//     (creator, tier) on the page, never per row, and gets the full row only
//     on a definite yes. A lookup that fails or times out redacts (fail
//     closed): a paid body never leaks because a service was down.
//
// The redacted row (pinned; clients draw the lock from it):
//
//   - cleared: text, rich_text, media, poll, and everything derived from the
//     body — hashtags, mentions (usernames and ids), tags, the tagged people
//     (tagged_user_ids), link_url,
//     location (name, lat/lng, place id, recording location), and on the
//     detail chapters (stored or description-derived) and credits, each an
//     empty array where the read had one;
//   - added: body_redacted: true, and duration_ms — the longest attached
//     video's duration, taken before media is cleared (absent when none);
//   - kept: title, cover, author, content_type, counts, tier_required_id,
//     the sound reference (audio_track_id / audio_start_ms / sound — public
//     catalogue data), genres, release_year, production_house.
//
// tier_required_id stays on every gated row, for members and non-members
// alike, so a member's client can show the badge and a non-member's the join
// card, and with it tier_required_price_paise — that tier's monthly price,
// absent when it could not be read (members_tiers.go). body_redacted (and
// duration_ms) are absent on any row that was not redacted.

// ErrMembersOnly refuses a non-member a members-only post's comment thread
// (403 MEMBERS_ONLY at the routes). Counts stay readable on the post itself.
// The gate's own refusal is a *MembersOnlyError, which matches it.
var ErrMembersOnly = errors.New("MEMBERS_ONLY")

// MembersOnlyError is ErrMembersOnly with the gate that refused: the routes
// answer it as details {creator_id, tier_required_id}, so the client can
// open the Join sheet on the right creator and level.
type MembersOnlyError struct {
	CreatorID      uuid.UUID
	TierRequiredID uuid.UUID
}

func (e *MembersOnlyError) Error() string { return ErrMembersOnly.Error() }

// Is matches ErrMembersOnly.
func (e *MembersOnlyError) Is(target error) bool { return target == ErrMembersOnly }

// membersLookupTimeout bounds one (creator, tier) membership question so a
// slow monetization-service cannot hold a feed page; past it the rows are
// redacted like any other failed lookup.
const membersLookupTimeout = mediaMembershipTimeout

// redactForViewer applies the members-only rule to a page of rows, in place.
// nil rows are skipped. See the file comment for the rule.
func (s *Service) redactForViewer(ctx context.Context, viewerID *uuid.UUID, posts []*postgres.Post) {
	viewer := uuid.Nil
	if viewerID != nil {
		viewer = *viewerID
	}
	type key struct{ author, tier uuid.UUID }
	answers := map[key]bool{}
	for _, p := range posts {
		if p == nil || p.TierRequiredID == nil || p.BodyRedacted {
			continue
		}
		// The author (and a signed-out viewer's "no") is answered by
		// viewerEntitled without asking monetization-service.
		k := key{p.AuthorID, *p.TierRequiredID}
		ok, asked := answers[k]
		if !asked {
			ok = s.viewerIsMember(ctx, viewer, p)
			answers[k] = ok
		}
		if !ok {
			RedactGatedPost(p)
		}
	}
	// Every gated row, member or not, says what its tier costs (absent when
	// unknown), so the Join sheet can show which levels unlock it.
	s.attachTierPrices(ctx, posts)
}

// viewerIsMember is one bounded, fail-closed membership answer for a gated
// row: false on any lookup error. The lookup itself is viewerEntitled, the
// one membership question this service asks (playback and offline copies ask
// the same one); it answers a signed-out viewer (uuid.Nil) no without asking
// monetization-service.
func (s *Service) viewerIsMember(ctx context.Context, viewer uuid.UUID, p *postgres.Post) bool {
	lookupCtx, cancel := context.WithTimeout(ctx, membersLookupTimeout)
	defer cancel()
	ok, err := s.viewerEntitled(lookupCtx, viewer, p)
	if err != nil {
		slog.WarnContext(ctx, "members-only read: membership lookup failed; redacting",
			"viewer_id", viewer, "post_id", p.ID, "author_id", p.AuthorID, "err", err)
		return false
	}
	return ok
}

// RedactDetailForViewer is the members-only rule for a handler holding one
// detail (GET /v1/posts/:postId, restore): the row, and the per-viewer
// detail fields derived from the body (chapters, credits).
func (s *Service) RedactDetailForViewer(ctx context.Context, viewerID *uuid.UUID, d *PostDetail) {
	s.redactDetailPtrsForViewer(ctx, viewerID, []*PostDetail{d})
}

// redactDetailsForViewer is redactForViewer over a page of details.
func (s *Service) redactDetailsForViewer(ctx context.Context, viewerID *uuid.UUID, details []PostDetail) {
	ptrs := make([]*PostDetail, len(details))
	for i := range details {
		ptrs[i] = &details[i]
	}
	s.redactDetailPtrsForViewer(ctx, viewerID, ptrs)
}

// redactDetailPtrsForViewer is redactForViewer over a page of detail
// pointers (the batch read, the watch-progress and playlist hydrations),
// then redactGatedDetail on every row that came out redacted.
func (s *Service) redactDetailPtrsForViewer(ctx context.Context, viewerID *uuid.UUID, details []*PostDetail) {
	posts := make([]*postgres.Post, 0, len(details))
	for _, d := range details {
		if d != nil {
			posts = append(posts, d.Post)
		}
	}
	s.redactForViewer(ctx, viewerID, posts)
	for _, d := range details {
		redactGatedDetail(d)
	}
}

// redactGatedDetail clears the detail fields derived from a redacted row's
// body: chapters (stored, or parsed out of the description) and the cast &
// crew credits, which a gated video shows only to members and its author.
// Both keep their shape — an array where there was one, now empty — so a
// client that always reads an array on the direct read still gets one.
// Every other detail field (counts, channel, author, sound, view count)
// stays.
func redactGatedDetail(d *PostDetail) {
	if d == nil || d.Post == nil || !d.Post.BodyRedacted {
		return
	}
	if d.Chapters != nil {
		d.Chapters = []ChapterRef{}
	}
	if d.Credits != nil {
		none := []CreditView{}
		d.Credits = &none
	}
}

// redactPostRowsForViewer is redactForViewer over plain rows (the thread
// read answers []postgres.Post).
func (s *Service) redactPostRowsForViewer(ctx context.Context, viewerID *uuid.UUID, rows []postgres.Post) {
	posts := make([]*postgres.Post, len(rows))
	for i := range rows {
		posts[i] = &rows[i]
	}
	s.redactForViewer(ctx, viewerID, posts)
}

// tierRequiredIDString is the gate as the events carry it: the tier's UUID
// string, "" for an open post.
func tierRequiredIDString(tier *uuid.UUID) string {
	if tier == nil {
		return ""
	}
	return tier.String()
}

// ---------------------------------------------------------------------------
// Comments on a members-only post
// ---------------------------------------------------------------------------

// postGateOf is a post's author and tier gate. postGateSeam replaces the
// store in tests; with neither, the gate cannot be decided and the caller
// fails closed.
func (s *Service) postGateOf(ctx context.Context, postID uuid.UUID) (uuid.UUID, *uuid.UUID, error) {
	if s.postGateSeam != nil {
		return s.postGateSeam(ctx, postID)
	}
	if s.pgStore == nil {
		return uuid.Nil, nil, ErrAuthoringStoreUnavailable
	}
	author, tier, err := s.pgStore.GetPostAuthorAndGate(ctx, postID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil, ErrPostNotFound
	}
	return author, tier, err
}

// visibleComment is the visible-comment read the comment-id routes start
// from; commentLookupSeam replaces the store in tests.
func (s *Service) visibleComment(ctx context.Context, commentID uuid.UUID) (*postgres.Comment, error) {
	if s.commentLookupSeam != nil {
		return s.commentLookupSeam(ctx, commentID)
	}
	if s.pgStore == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	return s.pgStore.GetVisibleCommentByID(ctx, commentID)
}

// requireMemberOf is the comment thread's members-only rule for a post whose
// author and gate the caller already holds: nil for an ungated post, for its
// author and for a member; ErrMembersOnly for everyone else, a signed-out
// viewer and a failed lookup included.
func (s *Service) requireMemberOf(ctx context.Context, p *postgres.Post, viewerID *uuid.UUID) error {
	if p == nil {
		return nil
	}
	viewer := uuid.Nil
	if viewerID != nil {
		viewer = *viewerID
	}
	// viewerEntitled opens an ungated post and its author's own without a
	// lookup, and refuses a signed-out viewer without one.
	if !s.viewerIsMember(ctx, viewer, p) {
		refused := &MembersOnlyError{CreatorID: p.AuthorID}
		if p.TierRequiredID != nil {
			refused.TierRequiredID = *p.TierRequiredID
		}
		return refused
	}
	return nil
}

// postMembersGate is requireMemberOf for a caller holding only the post
// id (the comment list, the comment-id routes). The gate lookup failing is
// an error, never an open thread.
func (s *Service) postMembersGate(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) error {
	author, tier, err := s.postGateOf(ctx, postID)
	if err != nil {
		return err
	}
	return s.requireMemberOf(ctx, &postgres.Post{ID: postID, AuthorID: author, TierRequiredID: tier}, viewerID)
}
