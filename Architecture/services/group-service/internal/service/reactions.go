package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/atpost/group-service/internal/store"
)

/*
	Emoji reactions on group posts.

	The contract, in one place:
	  - PUT    /v1/groups/:groupId/posts/v2/:postId/reaction {reaction}
	    sets the viewer's ONE reaction on the post, replacing any previous one
	    atomically; the same reaction again is a no-op and still 200.
	  - DELETE /v1/groups/:groupId/posts/v2/:postId/reaction
	    removes it; nothing to remove is still 200.
	  - Both answer with the authoritative state after the write: the
	    viewer's reaction (null when none), the legacy spark_count, and the
	    per-reaction counts — read back from the rows, not echoed from the
	    request.
	  - Every post read (feed, single post, pending queue, search, cross-group
	    feed) carries the same two fields: viewer_reaction and reaction_counts.

	Legacy hearts are 'like'. POST /spark still works exactly as before —
	including its 409 "already sparked" when ANY reaction exists — and
	DELETE /spark removes the viewer's reaction whatever it is. spark_count
	keeps its legacy weighting (a supernova is 5) and is moved only when a
	row is created or removed; a replacement never moves it, so nothing is
	counted twice.

	A rejected request never touches a row: the allowlist, the group access
	rule, the ban and the post's membership of the group are all checked
	before the transaction opens.
*/

// ReactionAllowlist is the ONLY set of reactions the API accepts. The CHECK
// constraint in migration 016 (and setup.sql) carries the same list, and a
// test asserts the two agree. Adding one is a migration plus this line.
var ReactionAllowlist = []string{"like", "love", "smile", "wow", "sad", "angry"}

// ErrReactionNotInAllowlist maps to 422 VALIDATION_ERROR ("invalid").
var ErrReactionNotInAllowlist = fmt.Errorf("invalid: reaction must be one of %s", strings.Join(ReactionAllowlist, ", "))

// ValidateReaction normalises transport noise (case, whitespace) and refuses
// anything outside the allowlist.
func ValidateReaction(raw string) (string, error) {
	r := strings.ToLower(strings.TrimSpace(raw))
	for _, allowed := range ReactionAllowlist {
		if r == allowed {
			return r, nil
		}
	}
	return "", ErrReactionNotInAllowlist
}

// ReactionState is the authoritative answer after a write.
type ReactionState struct {
	PostID         uuid.UUID      `json:"post_id"`
	Reaction       *string        `json:"reaction"` // null when the viewer has none
	SparkCount     int            `json:"spark_count"`
	ReactionCounts map[string]int `json:"reaction_counts"`
	ViewerSparked  bool           `json:"viewer_sparked"`
}

/*
	engagementGate is what every engagement write must pass before it touches
	a row: the group exists and is readable by the actor (private groups are
	members-only), the actor is not banned from it, and the post is a
	published post of THAT group. It returns the post so callers do not read
	it twice.

	Spark and unspark used to check only the last of these, so a non-member
	who knew a post id could spark inside a private group. They go through
	this gate now too.
*/
func (s *Service) engagementGate(ctx context.Context, actorID, groupID, postID uuid.UUID) (*store.GroupPostV2, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("not found: group not found")
	}
	if err := s.checkGroupAccess(ctx, g, actorID); err != nil {
		return nil, err
	}
	banned, err := s.store.CheckBanned(ctx, groupID, actorID)
	if err != nil {
		return nil, err
	}
	if banned {
		return nil, fmt.Errorf("forbidden: you cannot react in this group")
	}
	p, err := s.store.GetGroupPostV2(ctx, postID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.GroupID != groupID || p.Status != "published" {
		return nil, fmt.Errorf("not found: post not found in this group")
	}
	return p, nil
}

// SetGroupPostReaction sets (or replaces) the viewer's reaction and returns
// the authoritative state.
func (s *Service) SetGroupPostReaction(ctx context.Context, actorID, groupID, postID uuid.UUID, raw string) (*ReactionState, error) {
	reaction, err := ValidateReaction(raw)
	if err != nil {
		return nil, err
	}
	if _, err := s.engagementGate(ctx, actorID, groupID, postID); err != nil {
		return nil, err
	}
	change, err := s.store.SetGroupPostReaction(ctx, postID, actorID.String(), reaction)
	if err != nil {
		return nil, err
	}
	if change.Inserted {
		// A first reaction is what a spark was: the same member stat and the
		// same event, so the author's notification does not change shape.
		_ = s.store.IncrementMemberSparks(ctx, groupID, actorID, 1)
		s.publishEvent(func() error {
			return s.producer.PublishGroupPostSparked(ctx, groupID, postID, actorID)
		})
	}
	return s.reactionState(ctx, postID, actorID)
}

// RemoveGroupPostReaction removes the viewer's reaction, if any, and returns
// the authoritative state. Idempotent.
func (s *Service) RemoveGroupPostReaction(ctx context.Context, actorID, groupID, postID uuid.UUID) (*ReactionState, error) {
	if _, err := s.engagementGate(ctx, actorID, groupID, postID); err != nil {
		return nil, err
	}
	if _, _, err := s.store.RemoveGroupPostReaction(ctx, postID, actorID.String()); err != nil {
		return nil, err
	}
	return s.reactionState(ctx, postID, actorID)
}

// reactionState reads the state back from the rows after a write.
func (s *Service) reactionState(ctx context.Context, postID, actorID uuid.UUID) (*ReactionState, error) {
	sparkCount, err := s.store.GetGroupPostSparkCount(ctx, postID)
	if err != nil {
		return nil, err
	}
	counts, err := s.store.GetGroupPostReactionCounts(ctx, []uuid.UUID{postID})
	if err != nil {
		return nil, err
	}
	mine, err := s.store.GetViewerReaction(ctx, postID, actorID.String())
	if err != nil {
		return nil, err
	}
	st := &ReactionState{
		PostID:         postID,
		SparkCount:     sparkCount,
		ReactionCounts: counts[postID],
		ViewerSparked:  mine != "",
	}
	if st.ReactionCounts == nil {
		st.ReactionCounts = map[string]int{}
	}
	if mine != "" {
		st.Reaction = &mine
	}
	return st, nil
}
