package service

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/atpost/graph-service/internal/permission"
	"github.com/atpost/graph-service/internal/ratelimit"
	"github.com/atpost/graph-service/internal/store"
	"github.com/google/uuid"
)

// Private accounts — follow requests. Mirrors the connection-request shape
// (SendConnectionRequest / Accept / Decline / Cancel) but for the follow
// edge: a private target's follow is held as a pending request until the
// target approves it, and the approval is what materialises the follow.

// ErrNoPendingFollowRequest is re-exported so handlers map it to 404 without
// importing the store package's sentinel.
var ErrNoPendingFollowRequest = store.ErrNoPendingFollowRequest

// ErrCannotFollowSelf is returned when requester == target.
var ErrCannotFollowSelf = errors.New("cannot follow yourself")

// RequestFollowStatusFollowing is RequestFollow's outcome when the target is
// public (or already followed): the edge exists now, no request was created.
const RequestFollowStatusFollowing = "following"

// RequestFollow is the explicit follow-request entry point
// (POST /v1/graph/follow-requests). A public target behaves exactly like
// Follow and reports "following"; a private target the requester does not
// already follow gets a pending request and reports "requested".
func (s *Service) RequestFollow(ctx context.Context, requesterID, targetID uuid.UUID) (string, error) {
	if requesterID == targetID {
		return "", ErrCannotFollowSelf
	}
	et, err := s.store.LookupEntityType(ctx, targetID)
	if err != nil {
		return "", fmt.Errorf("lookup target entity type: %w", err)
	}
	if et != store.EntityTypePage && et != store.EntityTypeUser {
		return "", ErrWrongEntityType
	}
	// Shares the follow quota: a request is a follow that has not landed yet.
	if allowed, _ := s.rateLimit.Allow(ctx, ratelimit.ActionFollow, requesterID); !allowed {
		return "", ErrRateLimited
	}
	already, err := s.store.CheckFollow(ctx, requesterID, targetID)
	if err != nil {
		return "", err
	}
	if already {
		return RequestFollowStatusFollowing, nil
	}
	if et == store.EntityTypeUser && s.fetchPrivacy(ctx, targetID).AccountVisibility == "private" {
		if err := s.createFollowRequest(ctx, requesterID, targetID); err != nil {
			return "", err
		}
		return FollowStatusRequested, nil
	}
	if err := s.followDirect(ctx, requesterID, targetID); err != nil {
		return "", err
	}
	return RequestFollowStatusFollowing, nil
}

// createFollowRequest upserts the pending row (outbox graph.follow_requested
// inside the same transaction) and drops both relationship caches so
// follow_request_status flips immediately. Idempotent: an already-pending
// request neither errors nor announces twice.
func (s *Service) createFollowRequest(ctx context.Context, requesterID, targetID uuid.UUID) error {
	if _, err := s.store.UpsertFollowRequestPending(ctx, requesterID, targetID); err != nil {
		if errors.Is(err, store.ErrBlockedPair) {
			return fmt.Errorf("cannot follow: blocked")
		}
		return err
	}
	s.invalidateRel(ctx, requesterID, targetID)
	s.invalidateRel(ctx, targetID, requesterID)
	return nil
}

// AcceptFollowRequest is the TARGET approving requesterID's pending request.
// One transaction in the store: status → accepted, the follow edge, the
// UserFollowed outbox event and the graph.follow_request_accepted outbox
// event. Counters follow the committed truth, outside the transaction, as
// in Follow.
func (s *Service) AcceptFollowRequest(ctx context.Context, targetID, requesterID uuid.UUID) error {
	inserted, err := s.store.AcceptFollowRequestAtomic(ctx, requesterID, targetID)
	if err != nil {
		if errors.Is(err, store.ErrBlockedPair) {
			return fmt.Errorf("cannot accept: blocked")
		}
		return err
	}
	if inserted {
		s.adjustCount(ctx, s.followingCounter, requesterID, "following_count", 1)
		s.adjustCount(ctx, s.followerCounter, targetID, "follower_count", 1)
	}
	s.invalidateRel(ctx, requesterID, targetID)
	s.invalidateRel(ctx, targetID, requesterID)
	s.invalidateCounts(ctx, requesterID, targetID)
	return nil
}

// DeclineFollowRequest is the TARGET refusing requesterID's pending request.
// The requester may ask again later (the row is revived on re-request).
func (s *Service) DeclineFollowRequest(ctx context.Context, targetID, requesterID uuid.UUID) error {
	if err := s.store.DeclineFollowRequest(ctx, requesterID, targetID); err != nil {
		return err
	}
	s.invalidateRel(ctx, requesterID, targetID)
	s.invalidateRel(ctx, targetID, requesterID)
	return nil
}

// CancelFollowRequest is the REQUESTER withdrawing their own pending request.
func (s *Service) CancelFollowRequest(ctx context.Context, requesterID, targetID uuid.UUID) error {
	if err := s.store.CancelFollowRequest(ctx, requesterID, targetID); err != nil {
		return err
	}
	s.invalidateRel(ctx, requesterID, targetID)
	s.invalidateRel(ctx, targetID, requesterID)
	return nil
}

// ListIncomingFollowRequests pages the target's pending requests, newest
// first. Cursor is opaque to clients ("<unix_micros>:<uuid>").
func (s *Service) ListIncomingFollowRequests(ctx context.Context, targetID uuid.UUID, limit int, cursor string) ([]store.FollowRequest, string, error) {
	return s.store.ListIncomingFollowRequests(ctx, targetID, limit, cursor)
}

// AutoAcceptChunkSize bounds each sweep of the private→public auto-accept.
const AutoAcceptChunkSize = 100

// AutoAcceptPendingFollowRequests approves every pending request toward
// targetID, in chunks of AutoAcceptChunkSize. Run when the account flips
// private → public: a request was only ever pending because the account was
// private, so opening the account is the approval. Each accept goes through
// AcceptFollowRequest and therefore produces the UserFollowed outbox event.
//
// Returns the number accepted. Stops on a chunk that made no progress so a
// persistently failing row cannot spin the sweep forever.
func (s *Service) AutoAcceptPendingFollowRequests(ctx context.Context, targetID uuid.UUID) (int, error) {
	accepted := 0
	for {
		if err := ctx.Err(); err != nil {
			return accepted, err
		}
		ids, err := s.store.ListPendingFollowRequesterIDs(ctx, targetID, AutoAcceptChunkSize)
		if err != nil {
			return accepted, fmt.Errorf("list pending follow requests: %w", err)
		}
		if len(ids) == 0 {
			return accepted, nil
		}
		progressed := 0
		for _, requesterID := range ids {
			err := s.AcceptFollowRequest(ctx, targetID, requesterID)
			switch {
			case err == nil:
				accepted++
				progressed++
			case errors.Is(err, store.ErrNoPendingFollowRequest):
				// Raced with a manual accept/decline/cancel — already gone.
				progressed++
			default:
				log.Printf("[graph] auto-accept follow request %s→%s failed: %v", requesterID, targetID, err)
			}
		}
		if progressed == 0 {
			return accepted, fmt.Errorf("auto-accept for %s made no progress on a chunk of %d", targetID, len(ids))
		}
		if len(ids) < AutoAcceptChunkSize {
			return accepted, nil
		}
	}
}

// ErrUnsupportedCanAction is returned by CanBatch for an action outside the
// content-gating pair the internal endpoint serves.
var ErrUnsupportedCanAction = errors.New("unsupported action: expected view_posts or comment")

// CanBatch answers "may viewer do <action> to each target" for the content
// services (post/feed/comment). Facts come from one relationship batch;
// privacy comes from the per-target snapshot. A privacy-fetch failure yields
// the strict defaults, which the matrix resolves to DENY for comment and to
// follower-only for view_posts — never to a grant. The viewer always passes
// against themselves.
func (s *Service) CanBatch(ctx context.Context, viewerID uuid.UUID, action permission.Action, targetIDs []uuid.UUID) (map[string]bool, error) {
	if action != permission.ActionViewPosts && action != permission.ActionComment {
		return nil, ErrUnsupportedCanAction
	}
	out := make(map[string]bool, len(targetIDs))
	if len(targetIDs) == 0 {
		return out, nil
	}
	rels, err := s.store.GetRelationshipBatch(ctx, viewerID, targetIDs)
	if err != nil {
		return nil, err
	}
	for _, targetID := range targetIDs {
		if targetID == viewerID {
			out[targetID.String()] = true
			continue
		}
		r := rels[targetID]
		facts := permission.Facts{
			Blocked:            r.Blocked || r.BlockedBy,
			IsConnection:       r.IsConnection,
			ActorFollowsTarget: r.Follows,
			TargetFollowsActor: r.FollowedBy,
		}
		out[targetID.String()] = permission.Resolve(action, facts, s.fetchPrivacy(ctx, targetID)).Allowed
	}
	return out, nil
}
