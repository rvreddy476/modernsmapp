package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

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

	  * Members-only videos (2026-10-02). A post gated on a membership tier
	    (tier_required_id) plays only for its author and for a viewer who is
	    entitled to that tier. The watch page always showed the join card to
	    everyone else, but the byte gate did not ask: a signed-in non-member
	    holding the media id could fetch the video. The membership clause is
	    part of the one decision now (postMediaDecision.allowed), answered by
	    the same lookup the offline grant uses (viewerEntitled ->
	    CheckEntitlement, monetization-service). A lookup that does not answer
	    refuses that asset and is never cached. The post's poster — its cover
	    image, and a thumbnail still of the video — stays visible to a
	    signed-in viewer who may see the post, because the join card shows it.

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
	v, err := s.postMediaDecisions(ctx, viewerID, posts)
	if err != nil {
		return nil, err
	}
	return v.allowed, nil
}

// mediaMembershipTimeout bounds the membership lookup on the byte path:
// media-service gives this whole decision three seconds, and a slow
// monetization-service must become a refusal of one asset, not a held
// connection.
const mediaMembershipTimeout = 2 * time.Second

// MediaAccessPurposePoster is the wire value media-service sends when the
// read names a thumbnail still of the asset rather than something that
// plays it. Any other value, and none, is playback.
const MediaAccessPurposePoster = "poster"

// postMediaDecision is postMediaJudge's decision with the facts its callers
// need on top of the yes/no: the two halves it is made of, whether a refusal
// is a block (so an offline copy's owner is told "blocked", not "private"),
// and whether the account gate behind the decision actually answered (so a
// graph outage is a retry, never a reason to delete a stored copy). The
// decision itself is `allowed`, and it is the only one there is.
type postMediaDecision struct {
	// allowed is the playback decision: audience AND membership. A
	// membership lookup that did not answer is a refusal here.
	allowed func(*postgres.Post) bool
	// audience is everything but the membership clause: may this viewer see
	// the post at all (review state, schedule, account privacy, hidden
	// authors, blocks and mutes, the post's audience, shares, the 18+ gate).
	// It is what the watch page's join card is shown under.
	audience func(*postgres.Post) bool
	// entitled is the membership clause on its own: (true, nil) for a post
	// with no tier, for its author and for an entitled viewer; (false, nil)
	// for a resolved no; an error when monetization-service did not answer.
	// One lookup per (author, tier) per decision.
	entitled func(*postgres.Post) (bool, error)
	// blocked reports a block in either direction between the viewer and
	// the post's author. Always false for the signed-out viewer.
	blocked func(*postgres.Post) bool
	// resolved is false when the account gate fell back to its fail-closed
	// denial because graph-service or the hidden-author list did not answer.
	resolved bool
}

// postMediaDecisions resolves the facts behind postMediaJudge once for posts.
func (s *Service) postMediaDecisions(ctx context.Context, viewerID uuid.UUID, posts []*postgres.Post) (*postMediaDecision, error) {
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
	authorOK, resolved := s.canViewPostsResolved(ctx, viewer, authors)

	if viewer == nil {
		// anonymousMayAccessPost already refuses a members-only post, cover
		// and stills included: a paid audience is never an anonymous one.
		anonymous := func(p *postgres.Post) bool {
			return p != nil && anonymousMayAccessPost(p, authorOK[p.AuthorID])
		}
		return &postMediaDecision{
			allowed:  anonymous,
			audience: anonymous,
			entitled: func(p *postgres.Post) (bool, error) {
				return p != nil && p.TierRequiredID == nil, nil
			},
			blocked:  func(*postgres.Post) bool { return false },
			resolved: resolved,
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
	audience := func(p *postgres.Post) bool {
		if p == nil {
			return false
		}
		in := postMediaInputs{
			rel:           rels[p.AuthorID.String()],
			shared:        shared[p.ID],
			authorVisible: authorOK[p.AuthorID],
		}
		return evaluatePostMediaVisibility(viewerID, p, in) && ageOK(p)
	}
	entitled := s.membershipClause(ctx, viewerID)
	return &postMediaDecision{
		allowed: func(p *postgres.Post) bool {
			if !audience(p) {
				return false
			}
			// Members-only: the author, or a viewer entitled to the tier. A
			// lookup that failed is a refusal; it is not remembered.
			ok, err := entitled(p)
			return err == nil && ok
		},
		audience: audience,
		entitled: entitled,
		blocked: func(p *postgres.Post) bool {
			if p == nil || p.AuthorID == viewerID {
				return false
			}
			rel := rels[p.AuthorID.String()]
			return rel.Blocked || rel.BlockedBy
		},
		resolved: resolved,
	}, nil
}

// viewerEntitled answers the members-only rule for one viewer and one post:
// (true, nil) for an ungated post, for its author and for an entitled
// viewer, (false, nil) for a resolved no, and an error when
// monetization-service could not answer. It is the one membership lookup:
// playback (membershipClause) and the offline grant and check
// (offline_copies.go) both ask it. A signed-out viewer is never entitled.
func (s *Service) viewerEntitled(ctx context.Context, viewerID uuid.UUID, p *postgres.Post) (bool, error) {
	if p.TierRequiredID == nil || p.AuthorID == viewerID {
		return true, nil
	}
	if viewerID == uuid.Nil {
		return false, nil
	}
	if s.offlineEntitlement != nil {
		return s.offlineEntitlement(ctx, viewerID, p)
	}
	allowed, _, err := s.CheckEntitlement(ctx, viewerID, p.AuthorID, p.TierRequiredID)
	if err != nil {
		return false, err
	}
	return allowed, nil
}

// membershipClause is viewerEntitled for one decision: asked lazily (only
// for a gated post that reached the clause), bounded by
// mediaMembershipTimeout, and once per (author, tier) however many posts and
// assets of a page share it. Failures are remembered for the decision only,
// so one page does not ask a service that is down once per card; nothing
// outlives the request.
func (s *Service) membershipClause(ctx context.Context, viewerID uuid.UUID) func(*postgres.Post) (bool, error) {
	type key struct{ author, tier uuid.UUID }
	type answer struct {
		ok  bool
		err error
	}
	asked := map[key]answer{}
	return func(p *postgres.Post) (bool, error) {
		if p == nil {
			return false, nil
		}
		if p.TierRequiredID == nil || p.AuthorID == viewerID {
			return true, nil
		}
		k := key{p.AuthorID, *p.TierRequiredID}
		if a, done := asked[k]; done {
			return a.ok, a.err
		}
		lookupCtx, cancel := context.WithTimeout(ctx, mediaMembershipTimeout)
		ok, err := s.viewerEntitled(lookupCtx, viewerID, p)
		cancel()
		if err != nil {
			ok = false
			slog.WarnContext(ctx, "media access: membership lookup failed; refusing members-only media",
				"viewer_id", viewerID, "post_id", p.ID, "author_id", p.AuthorID, "err", err)
		}
		asked[k] = answer{ok: ok, err: err}
		return ok, err
	}
}

// postCoverOnly reports that mediaID reaches p as its cover image and is not
// one of the things the post plays. A cover is the poster of the join card:
// it is shown to every signed-in viewer who may see the post, members or not.
func postCoverOnly(p *postgres.Post, mediaID uuid.UUID) bool {
	if p == nil || p.CoverMediaID == nil || *p.CoverMediaID != mediaID {
		return false
	}
	for _, m := range p.Media {
		if m.MediaID == mediaID {
			return false
		}
	}
	return true
}

// mediaAllowed is the decision for ONE asset reached through ONE post: the
// audience first, then the membership clause — which a poster does not need.
// poster is true for a read that named a thumbnail still of the asset; the
// post's cover image is a poster whatever the read. The error is a
// membership lookup that did not answer; the asset is refused either way.
func (d *postMediaDecision) mediaAllowed(p *postgres.Post, mediaID uuid.UUID, poster bool) (bool, error) {
	if p == nil || !d.audience(p) {
		return false, nil
	}
	if poster || postCoverOnly(p, mediaID) {
		return true, nil
	}
	return d.entitled(p)
}

// postMediaAnswer is what the posts carrying an asset say about it.
type postMediaAnswer int

const (
	postMediaRefused postMediaAnswer = iota
	postMediaPlays
	// postMediaMembersOnly: the viewer may see a post carrying the asset,
	// and is not a member of the tier it is gated on.
	postMediaMembersOnly
	// postMediaMembershipUnresolved: as above, but monetization-service did
	// not answer. Refused, and the caller must not cache it.
	postMediaMembershipUnresolved
)

// judgePostMedia folds mediaAllowed over the posts carrying mediaID: one
// post that plays it is enough.
func (d *postMediaDecision) judgePostMedia(posts []*postgres.Post, mediaID uuid.UUID, poster bool) postMediaAnswer {
	answer := postMediaRefused
	for _, p := range posts {
		ok, err := d.mediaAllowed(p, mediaID, poster)
		switch {
		case err != nil:
			answer = postMediaMembershipUnresolved
		case ok:
			return postMediaPlays
		case p != nil && d.audience(p) && answer == postMediaRefused:
			answer = postMediaMembersOnly
		}
	}
	return answer
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
//
// poster is true when the read named a thumbnail still (see mediaAllowed).
func (s *Service) viewerMayAccessPostMedia(ctx context.Context, viewerID, mediaID uuid.UUID, poster bool) (postMediaAnswer, error) {
	posts, err := s.approvedPostsForMedia(ctx, mediaID)
	if err != nil {
		return postMediaRefused, err
	}
	if len(posts) == 0 {
		return postMediaRefused, nil
	}
	decision, err := s.postMediaDecisions(ctx, viewerID, posts)
	if err != nil {
		return postMediaRefused, err
	}
	return decision.judgePostMedia(posts, mediaID, poster), nil
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

// membersOnlyDenied is the resolved denial of a members-only asset to a
// signed-in viewer who may see the post and is not a member: the same shape
// as every other denial, with a reason of its own for the log line.
func membersOnlyDenied(ctx context.Context, viewerID, mediaID uuid.UUID) MediaAccessResult {
	slog.InfoContext(ctx, "media access excluded: members-only post, viewer not entitled",
		"viewer_id", viewerID,
		"media_id", mediaID,
		"reason", "members_only")
	return MediaAccessResult{Allowed: false, Decision: DecisionDenied, Reason: "members_only"}
}

// membershipUnresolvedDenied is the batch answer for a members-only asset
// whose membership lookup did not answer: that one asset is refused and the
// rest of the page is unaffected. (The single route answers 503 instead, so
// a player retries.) Nothing is stored: the next request asks again.
func membershipUnresolvedDenied(ctx context.Context, viewerID, mediaID uuid.UUID) MediaAccessResult {
	slog.WarnContext(ctx, "media access excluded: membership lookup unresolved",
		"viewer_id", viewerID,
		"media_id", mediaID,
		"reason", "membership_unresolved")
	return MediaAccessResult{Allowed: false, Decision: DecisionDenied, Reason: "membership_unresolved"}
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
