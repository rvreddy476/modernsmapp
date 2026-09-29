package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

/*
	Media access — the content-authority half of protected byte delivery
	(Module 4 M4-P0-5), extended on 2026-09-29 for launch safety:

	  * Signed-out playback (founder decision 2). A viewer with no identity
	    is a real audience category now, not a malformed request: media-service
	    sends viewer_id "" (or the nil UUID) and this service decides. The
	    anonymous rule is deliberately NARROWER than the signed-in one — see
	    anonymousMayAccessPost — because "unlisted" is link-only and a private
	    account, a hidden author, an 18+ post and a members-only post have no
	    signed-out reading at all.

	  * The media-gate gaps (copyright-match-plan section 4, P-8 a/c/d):
	    account privacy, hidden (deactivated / pending-deletion) authors and
	    the schedule now bind the byte gate for signed-in viewers too, exactly
	    as they bind GET /v1/posts/:id (viewerMayViewPost + hiddenFromViewer);
	    the retired `circle` audience is author-only on both gates.

	Every rule here is pure and testable: the storage the gate reads is the
	narrow mediaAccessStore slice, relationships and the account gate are
	resolved ONCE per request by postMediaJudge, and the per-post decision is
	a function of the row plus those resolved facts.
*/

// mediaAccessStore is the storage slice the media gate reads
// (store/postgres: story_create.go, posts.go, channels.go). A narrow
// interface rather than the concrete *postgres.Store so every rule below is
// testable without a database; New() sets it to pgStore.
type mediaAccessStore interface {
	GetMediaAccessFacts(ctx context.Context, mediaID uuid.UUID) (*postgres.MediaAccessFacts, error)
	GetMediaAccessFactsBatch(ctx context.Context, mediaIDs []uuid.UUID) (map[uuid.UUID]postgres.MediaAccessFacts, error)
	ChannelAvatarOwners(ctx context.Context, mediaIDs []uuid.UUID) (map[uuid.UUID]uuid.UUID, error)
	StoryForMedia(ctx context.Context, mediaID uuid.UUID) (*postgres.Story, error)
	StoriesForMediaBatch(ctx context.Context, mediaIDs []uuid.UUID) (map[uuid.UUID]*postgres.Story, error)
	PostIDsByMediaID(ctx context.Context, mediaID uuid.UUID) ([]uuid.UUID, error)
	PostIDsByMediaIDs(ctx context.Context, mediaIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error)
	GetPost(ctx context.Context, id uuid.UUID) (*postgres.Post, error)
	GetPostsByIDs(ctx context.Context, ids []uuid.UUID) ([]postgres.Post, error)
}

// errMediaAccessUnwired is the unresolved answer of a Service with no store
// behind the gate (a handler test rig, a misbuilt binary). Unresolved, never
// denied: media-service retries a 503 and must not cache a denial produced
// by wiring.
func errMediaAccessUnwired() error {
	return fmt.Errorf("%w: media access store not configured", ErrStoryPolicyUnresolved)
}

// postMediaInputs is everything the per-post playback rule needs that is not
// on the posts row, resolved once per request by postMediaJudge.
type postMediaInputs struct {
	// rel is the viewer's relationship to the post's author (blocks, mutes,
	// follow); the zero value is "stranger".
	rel ViewerRelationship
	// shared is "the viewer is on this post's private share list".
	shared bool
	// authorVisible is the account-level gate: false when the author's
	// account is private and the viewer does not follow it, or the author
	// is hidden (deactivated / pending deletion). canViewPosts resolves it;
	// the author's own is always true.
	authorVisible bool
}

// evaluatePostMediaVisibility is the per-post playback rule for a SIGNED-IN
// viewer. It mirrors the detail gate (viewerMayViewPost + hiddenFromViewer):
// the author always plays their own; everyone else needs an approved, live,
// unscheduled post by an account they may see, no block or mute, and the
// post's audience.
func evaluatePostMediaVisibility(viewerID uuid.UUID, p *postgres.Post, in postMediaInputs) bool {
	if p == nil {
		return false
	}
	if p.AuthorID == viewerID {
		// Owner preview: pending, private, scheduled and 18+ are all theirs.
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(p.EffectiveReviewStatus()), "approved") {
		return false
	}
	if p.DeletedAt != nil {
		// The store already excludes soft-deleted rows; a row that arrives
		// deleted through another path must not play.
		return false
	}
	if p.PublishAt != nil {
		// Scheduled: author-only until the schedule worker clears publish_at
		// at publication (hiddenWhileScheduled). A past value the worker has
		// not cleared yet is still "not announced", so the test is non-nil,
		// not "in the future".
		return false
	}
	if !in.authorVisible {
		// Private account the viewer does not follow, or a hidden author.
		return false
	}
	if in.rel.Blocked || in.rel.BlockedBy || in.rel.Muted {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(p.Visibility)) {
	case "", "public", "unlisted":
		return true
	case "followers":
		return in.rel.Follows
	case "private":
		return in.shared
	default:
		// "circle", "trusted" and "close_friends" were retired on 21 Sep
		// (graph-service migration 012) and are author-only here AND on the
		// detail gate (post.go viewerMayViewPost) since 2026-09-29: the
		// audience the author picked no longer exists, and widening it to
		// every follower would show close-friends content to people the
		// author excluded. "staged" and unknown values fail closed too.
		return false
	}
}

// anonymousMayAccessPost is the SIGNED-OUT playback rule (founder decision
// 2, 2026-09-29): an asset plays for a viewer with no identity only through
// a post that is public to the entire internet. Every clause is a refusal
// somebody would otherwise get wrong:
//
//   - visibility exactly "public". Not "unlisted" (link-only; a search index
//     is the leak), not a blank one (post-service reads a blank as public for
//     a signed-in viewer, but an unstated audience is not a public one), not
//     "staged", not a value added later.
//   - review_status approved, deleted_at null, publish_at null — the same
//     "live" tests the signed-in rule applies.
//   - not age_restricted: the 18+ gate needs a date of birth, and a stranger
//     has none (age_gate.go answers 401 AGE_RESTRICTED_SIGN_IN).
//   - not members-gated (tier_required_id): a paid audience is never an
//     anonymous one.
//   - authorVisible: the author's account is public (graph asked as the nil
//     stranger) and the author is not hidden (deactivated / pending deletion).
func anonymousMayAccessPost(p *postgres.Post, authorVisible bool) bool {
	if p == nil {
		return false
	}
	if p.DeletedAt != nil {
		return false
	}
	if p.PublishAt != nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(p.EffectiveReviewStatus()), "approved") {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(p.Visibility), "public") {
		return false
	}
	if p.AgeRestricted {
		return false
	}
	if p.TierRequiredID != nil {
		return false
	}
	return authorVisible
}

// postMediaJudge resolves, once for a set of candidate posts, every fact the
// per-post rule needs — the account gate (privacy + hidden authors) for the
// posts' authors, and for a signed-in viewer the relationships, the private
// share list and the age allowance — and returns the per-post decision.
//
// viewerID == uuid.Nil is the anonymous stranger: no relationship lookup
// (there is nobody to be blocked by), no share list, no age; the account gate
// is asked as the nil viewer, which graph-service resolves like a stranger.
func (s *Service) postMediaJudge(ctx context.Context, viewerID uuid.UUID, posts []*postgres.Post) (func(*postgres.Post) bool, error) {
	authors := make([]uuid.UUID, 0, len(posts))
	authorStrings := make([]string, 0, len(posts))
	seen := make(map[uuid.UUID]bool, len(posts))
	for _, p := range posts {
		if p == nil || seen[p.AuthorID] {
			continue
		}
		seen[p.AuthorID] = true
		authors = append(authors, p.AuthorID)
		if p.AuthorID != viewerID {
			authorStrings = append(authorStrings, p.AuthorID.String())
		}
	}

	var viewer *uuid.UUID
	if viewerID != uuid.Nil {
		v := viewerID
		viewer = &v
	}
	// Account privacy and hidden authors, fail-closed (privacy_gate.go): an
	// author the graph did not answer for, or whose hidden lookup failed, is
	// not visible.
	authorOK := s.canViewPosts(ctx, viewer, authors)

	if viewer == nil {
		return func(p *postgres.Post) bool {
			return p != nil && anonymousMayAccessPost(p, authorOK[p.AuthorID])
		}, nil
	}

	rels := map[string]ViewerRelationship{}
	if len(authorStrings) > 0 {
		var err error
		rels, err = s.storyAudience.Relationships(ctx, viewerID.String(), authorStrings)
		if err != nil {
			return nil, err
		}
	}
	shared := s.privateSharedSet(ctx, viewerID, privatePostIDs(posts, viewerID))
	ageOK := s.ageAllowance(ctx, viewer)
	return func(p *postgres.Post) bool {
		if p == nil {
			return false
		}
		in := postMediaInputs{
			rel:           rels[p.AuthorID.String()],
			shared:        shared[p.ID],
			authorVisible: authorOK[p.AuthorID],
		}
		return evaluatePostMediaVisibility(viewerID, p, in) && ageOK(p)
	}, nil
}

// approvedPostsForMedia loads the live posts carrying mediaID that a
// non-author could ever play: soft-deleted rows are excluded by the store,
// unapproved ones here (the uploader's own preview was already answered by
// the canonical gate).
func (s *Service) approvedPostsForMedia(ctx context.Context, mediaID uuid.UUID) ([]*postgres.Post, error) {
	postIDs, err := s.mediaAccess.PostIDsByMediaID(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	posts := make([]*postgres.Post, 0, len(postIDs))
	for _, id := range postIDs {
		p, err := s.mediaAccess.GetPost(ctx, id)
		if err != nil {
			return nil, err
		}
		if p == nil || !strings.EqualFold(p.EffectiveReviewStatus(), "approved") {
			continue
		}
		posts = append(posts, p)
	}
	return posts, nil
}

// viewerMayAccessPostMedia extends protected delivery to the shared canonical
// media used by posts, Reels and PostTube. PostTube stays a separate product
// surface; this merely honors its existing post/media reference and never
// creates or merges a second video record.
func (s *Service) viewerMayAccessPostMedia(ctx context.Context, viewerID, mediaID uuid.UUID) (bool, error) {
	posts, err := s.approvedPostsForMedia(ctx, mediaID)
	if err != nil {
		return false, err
	}
	if len(posts) == 0 {
		return false, nil
	}
	judge, err := s.postMediaJudge(ctx, viewerID, posts)
	if err != nil {
		return false, err
	}
	for _, p := range posts {
		if judge(p) {
			return true, nil
		}
	}
	return false, nil
}

// privatePostIDs is the ids of the private posts in posts that viewerID
// does not own: the only ones whose share list matters to a media decision.
func privatePostIDs(posts []*postgres.Post, viewerID uuid.UUID) []uuid.UUID {
	var out []uuid.UUID
	for _, p := range posts {
		if p != nil && p.AuthorID != viewerID && strings.EqualFold(p.Visibility, "private") {
			out = append(out, p.ID)
		}
	}
	return out
}

// privateSharedSet is the subset of postIDs whose share list names viewerID.
// Fails closed: no store or a lookup error shares nothing.
func (s *Service) privateSharedSet(ctx context.Context, viewerID uuid.UUID, postIDs []uuid.UUID) map[uuid.UUID]bool {
	if len(postIDs) == 0 || s.privateShare == nil {
		return map[uuid.UUID]bool{}
	}
	shared, err := s.privateShare.PrivateSharedPostIDs(ctx, viewerID, postIDs)
	if err != nil {
		slog.WarnContext(ctx, "media access: private share lookup failed; denying shared posts", "viewer_id", viewerID, "err", err)
		return map[uuid.UUID]bool{}
	}
	return shared
}

// postMediaVerdict is the allowed answer for a post-visible asset, "not
// ready" while the pipeline is still running.
func postMediaVerdict(ctx context.Context, viewerID, mediaID uuid.UUID, processingStatus string) MediaAccessResult {
	if processingStatus != "ready" {
		slog.InfoContext(ctx, "media access permitted: post visible but asset not ready",
			"viewer_id", viewerID,
			"media_id", mediaID,
			"processing_status", processingStatus,
			"reason", "post_not_ready")
		return MediaAccessResult{Allowed: true, Decision: DecisionNotReady, Reason: "post_not_ready"}
	}
	slog.DebugContext(ctx, "media access allowed: post visible",
		"viewer_id", viewerID,
		"media_id", mediaID,
		"reason", "post_allowed")
	return MediaAccessResult{Allowed: true, Decision: DecisionAllowed, Reason: "post_allowed"}
}

// storyMediaVerdict is the allowed answer for a story-visible asset.
func storyMediaVerdict(ctx context.Context, viewerID, mediaID uuid.UUID, processingStatus string) MediaAccessResult {
	if processingStatus != "ready" {
		slog.InfoContext(ctx, "media access permitted: story visible but asset not ready",
			"viewer_id", viewerID,
			"media_id", mediaID,
			"processing_status", processingStatus,
			"reason", "story_not_ready")
		return MediaAccessResult{Allowed: true, Decision: DecisionNotReady, Reason: "story_not_ready"}
	}
	slog.DebugContext(ctx, "media access allowed: story visible",
		"viewer_id", viewerID,
		"media_id", mediaID,
		"reason", "story_allowed")
	return MediaAccessResult{Allowed: true, Decision: DecisionAllowed, Reason: "story_allowed"}
}

// noVisibleContent is the resolved denial when nothing the viewer may see
// carries the asset. The reason names the audience so media-service's log
// line tells a signed-out refusal from a signed-in one.
func noVisibleContent(ctx context.Context, viewerID, mediaID uuid.UUID) MediaAccessResult {
	reason := "no_visible_post_or_story"
	if viewerID == uuid.Nil {
		reason = "no_public_post"
	}
	slog.InfoContext(ctx, "media access excluded: no visible post or story for viewer",
		"viewer_id", viewerID,
		"media_id", mediaID,
		"reason", reason)
	return MediaAccessResult{Allowed: false, Decision: DecisionDenied, Reason: reason}
}
