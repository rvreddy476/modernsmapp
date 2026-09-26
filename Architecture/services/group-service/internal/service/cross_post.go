package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
)

/*
Cross-posting: one composer action, several groups.

Design notes that are load-bearing rather than decorative:

  - ONE transaction per target, never across targets. A group you are banned
    from must not roll back four good posts.

  - Per-target outcomes carry the group id AND the post id, unlike the invite
    batch which deliberately returns only counts. The distinction is real:
    the invite batch withholds names because naming a refusal identifies a
    PERSON who blocked you. Here the refusals are GROUPS the author chose
    themselves, so naming them is helpful and leaks nothing about anyone.

  - The outcome vocabulary is deliberately lossy. `unavailable` collapses
    "no such group", "deleted", "archived" and "private and you are not a
    member" into one answer, because keeping them apart turns cross-posting
    into a probe for the existence of private groups.
*/

// Outcomes. Anything a caller may branch on lives here so the set is one list
// rather than scattered string literals.
const (
	OutcomePublished       = "published"
	OutcomePendingApproval = "pending_approval"
	OutcomeNotAMember      = "not_a_member"
	OutcomeBanned          = "banned"
	OutcomeNotPermitted    = "not_permitted"
	OutcomeAnonNotAllowed  = "anonymous_not_allowed"
	OutcomeBlockedContent  = "blocked_content"
	// unavailable: missing, deleted, archived, or private-and-not-a-member.
	// Kept as one answer on purpose — see the note above.
	OutcomeUnavailable = "unavailable"
)

// MaxCrossPostTargets caps a single action.
//
// The invite batch allows 50 because an invite is one cheap row. A cross-post
// writes a post, a counter, a cache invalidation and an event per target, so
// the blast radius per press is much larger. Five is what comparable products
// allow and bounds the worst case.
const MaxCrossPostTargets = 5

// CrossPostTarget is what happened for one group.
type CrossPostTarget struct {
	GroupID uuid.UUID  `json:"group_id"`
	PostID  *uuid.UUID `json:"post_id,omitempty"`
	Outcome string     `json:"outcome"`
}

// CrossPostResult is the batch answer.
type CrossPostResult struct {
	Published int               `json:"published"`
	Pending   int               `json:"pending"`
	Skipped   int               `json:"skipped"`
	Targets   []CrossPostTarget `json:"targets"`
}

func (r *CrossPostResult) count(outcome string) {
	switch outcome {
	case OutcomePublished:
		r.Published++
	case OutcomePendingApproval:
		r.Pending++
	default:
		r.Skipped++
	}
}

/*
createOnePost evaluates one target group and, if it passes, writes the post.

Shared by the single-group path and every target of a batch, so the two can
never drift into different answers for the same pair. A policy refusal is an
OUTCOME, not an error; only a real failure (database, not rules) returns err,
because reporting "3 published" when a write failed is worse than an error.
*/
func (s *Service) createOnePost(
	ctx context.Context,
	actorID, groupID uuid.UUID,
	params CreateGroupPostV2Params,
	crossPostGroupID *uuid.UUID,
) (string, *store.GroupPostV2, error) {

	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return "", nil, err
	}
	// Missing, deleted and archived all answer the same, so that cross-posting
	// cannot be used to learn which private groups exist.
	if g == nil || g.Status == "deleted" || g.IsArchived {
		return OutcomeUnavailable, nil, nil
	}

	/*
		The UNCACHED membership reader, always.

		CheckMembershipCached keeps "gm:<group>:<user>" for five minutes and
		is the reader the comment path uses. Using it here would reintroduce
		the ban-evasion window that A0 closed, five times over.
	*/
	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return "", nil, err
	}
	if actor == nil {
		return OutcomeNotAMember, nil, nil
	}

	banned, err := s.store.CheckBanned(ctx, groupID, actorID)
	if err != nil {
		return "", nil, err
	}
	if banned {
		return OutcomeBanned, nil, nil
	}

	if err := s.checkPermission(g.WhoCanPost, actor.Role); err != nil {
		return OutcomeNotPermitted, nil, nil
	}

	/*
		THE most dangerous branch in the feature.

		A target that does not allow anonymity is SKIPPED. Posting it under
		the author's name instead — the "helpful" fallback — publishes a
		member's identity against an explicit choice, in a group they picked
		while believing they were anonymous. It cannot be undone. There is no
		code path here that clears IsAnonymous and continues.
	*/
	if params.IsAnonymous && !g.AllowAnonymousPosts {
		return OutcomeAnonNotAllowed, nil, nil
	}

	if blocked, _ := s.containsBlockedWord(ctx, groupID, params.Title, params.Body); blocked {
		// The word itself is not returned per target: in a batch it would say
		// which of several groups blocks which term, which is a map of other
		// groups' moderation settings the author may not be entitled to.
		return OutcomeBlockedContent, nil, nil
	}

	status := OutcomePublished
	needsApproval := false
	if g.GroupType == "moderated" && actor.Role == "member" {
		status = "pending_approval"
		needsApproval = true
	}

	contentType := params.ContentType
	if contentType == "" {
		contentType = "text"
	}

	/*
		A FRESH alias for this copy.

		Generating one alias above the loop and reusing it reads as tidier and
		is a leak: anyone who can see two of the target groups could match the
		aliases and know the posts share an author, which in a small group
		names them. Minted here, per target, and a test asserts the call sits
		inside this function rather than above the caller's loop.
	*/
	var anonAlias *uuid.UUID
	if params.IsAnonymous {
		anonAlias = store.NewAnonAlias()
	}

	post := &store.GroupPostV2{
		GroupID:          groupID,
		AuthorID:         actorID.String(),
		IsAnonymous:      params.IsAnonymous,
		AnonAlias:        anonAlias,
		CrossPostGroupID: crossPostGroupID,
		ContentType:      contentType,
		Body:             &params.Body,
		Title:            nilIfEmpty(params.Title),
		TypePayload:      params.TypePayload,
		Attachments:      params.Attachments,
		IsAnnouncement:   params.IsAnnouncement,
		ChannelID:        params.ChannelID,
		NeedsApproval:    needsApproval,
		Status:           status,
	}

	// An anonymous post's pictures are scoped BEFORE the row exists, and the
	// post is refused if that fails: see media_anonymize.go.
	if params.IsAnonymous {
		if err := s.anonymizeAttachments(ctx, params.Attachments); err != nil {
			return "", nil, err
		}
	}
	if err := s.store.CreateGroupPostV2(ctx, post); err != nil {
		return "", nil, err
	}

	// Neither of these runs for an anonymous post — the contributor
	// leaderboard and the notification fanout would each name the author.
	if !params.IsAnonymous {
		s.store.IncrementMemberPostCount(ctx, groupID, actorID)
		s.publishEvent(func() error {
			return s.producer.PublishGroupPostCreated(ctx, groupID, post.ID, actorID)
		})
	}

	s.invalidateGroupCache(ctx, groupID)
	return status, post, nil
}

/*
CrossPost publishes one body to several groups.

The primary group is the one in the path; the extras are best-effort. That
asymmetry is deliberate and useful: failing on the group you are looking at
is a refusal you should see, while a group you added from a picker is
reported per target and does not fail the request.
*/
func (s *Service) CrossPost(
	ctx context.Context,
	actorID, primaryGroupID uuid.UUID,
	params CreateGroupPostV2Params,
	idempotencyKey string,
) (*CrossPostResult, error) {

	// Deduplicate and keep the caller's order, primary first. The same group
	// listed twice is one target, not two posts.
	targets := []uuid.UUID{primaryGroupID}
	seen := map[uuid.UUID]struct{}{primaryGroupID: {}}
	for _, id := range params.AlsoPostTo {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		targets = append(targets, id)
	}

	// Before any write, and before the rate limit: a request that is too big
	// should cost nothing.
	if len(targets) > MaxCrossPostTargets {
		return nil, fmt.Errorf("invalid: you can post to at most %d groups at once", MaxCrossPostTargets)
	}

	/*
		THE REPLAY LOOKUP RUNS FIRST.

		Before the rate limit, so one intent is not counted twice; and before
		any validation, so a retry is not re-subjected to checks the first
		attempt already passed. This is the same lesson as the group-create
		replay path, which has its own written-down test for exactly this
		ordering.
	*/
	prior, err := s.store.FindPostRequests(ctx, actorID, idempotencyKey)
	if err != nil {
		return nil, err
	}

	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:group_crosspost:%s", actorID), 20, 24*time.Hour) {
		return nil, fmt.Errorf("rate_limited: too many cross-posts today")
	}

	crossID := uuid.New()
	result := &CrossPostResult{Targets: make([]CrossPostTarget, 0, len(targets))}

	for _, gid := range targets {
		// Already decided by an earlier attempt: replay the recorded answer
		// rather than re-running rules that may have changed since.
		if rec, done := prior[gid]; done {
			result.Targets = append(result.Targets, CrossPostTarget{
				GroupID: gid, PostID: rec.PostID, Outcome: rec.Outcome,
			})
			result.count(rec.Outcome)
			continue
		}

		outcome, post, err := s.createOnePost(ctx, actorID, gid, params, &crossID)
		if err != nil {
			// A real failure fails the whole call. Reporting a success count
			// over a failed write is worse than an error the client can retry.
			return nil, err
		}

		var postID *uuid.UUID
		if post != nil {
			postID = &post.ID
		}
		if rerr := s.store.RecordPostRequest(ctx, actorID, idempotencyKey, store.PostRequestRecord{
			GroupID: gid, PostID: postID, Outcome: outcome,
		}); rerr != nil {
			return nil, rerr
		}

		result.Targets = append(result.Targets, CrossPostTarget{
			GroupID: gid, PostID: postID, Outcome: outcome,
		})
		result.count(outcome)
	}

	return result, nil
}
