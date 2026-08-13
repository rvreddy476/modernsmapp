package service

import (
	"context"
	"errors"
	"fmt"
)

// Module 4 M4-P0-1 / M4-P0-2 — one authority for "may this viewer see this
// story", used by every story surface.
//
// WHY THIS IS ONE FILE AND NOT A CHECK PER HANDLER
//
// Before this, each story surface made its own decision and they disagreed:
// the feed and author queries filtered expiry, the single-story read filtered
// nothing at all, and the view-count mutation did not even take a viewer. The
// same class of split showed up in Module 3 (resolve-handle bypassing a gate
// its sibling routes enforced), so the fix here is deliberately structural:
// surfaces call Evaluate, and a new surface that forgets to is caught by the
// route inventory test rather than by a user.
//
// THE TWO FAILURE SHAPES ARE NOT THE SAME
//
// A resolved "you may not see this" is a 404 — non-enumerating, identical for
// missing, blocked, expired, rejected and out-of-audience, so the response
// cannot be used to probe which stories or which relationships exist.
//
// An UNRESOLVED state — graph timeout, malformed relationship body, database
// error — is a retryable 503. Returning 404 there would be a lie that happens
// to look safe: it hides the outage from the client, converts a transient
// dependency failure into "this content does not exist", and trains the app to
// treat an outage as deletion.

// StoryDenial is why a story is not visible. It never reaches the client — the
// wire response is uniform — but it drives logs, metrics and tests.
type StoryDenial string

const (
	DenyNone          StoryDenial = ""
	DenyMissing       StoryDenial = "missing"
	DenyDeleted       StoryDenial = "deleted"
	DenyExpired       StoryDenial = "expired"
	DenyNotApproved   StoryDenial = "not_approved"
	DenyBlocked       StoryDenial = "blocked"
	DenyMuted         StoryDenial = "muted"
	DenyNotInAudience StoryDenial = "not_in_audience"
	DenyNoViewer      StoryDenial = "no_viewer"
)

// ErrStoryPolicyUnresolved means the decision could not be made. Callers must
// map it to a retryable 503 and must not fall back to a permissive answer.
var ErrStoryPolicyUnresolved = errors.New("story policy: relationship state unresolved")

// StoryVisibility values as stored on the row.
const (
	StoryVisibilityPublic       = "public"
	StoryVisibilityFollowers    = "followers"
	StoryVisibilityCloseFriends = "close_friends"
)

// StoryModerationState values. Only Approved is publishable.
const (
	StoryModerationPending      = "pending"
	StoryModerationApproved     = "approved"
	StoryModerationRejected     = "rejected"
	StoryModerationManualReview = "manual_review"
)

// ViewerRelationship is the subset of the graph contract an audience decision
// needs. It is a value type on purpose: the evaluator cannot reach out to any
// dependency mid-decision, so a missing fact is a caller bug rather than a
// silent extra round trip.
type ViewerRelationship struct {
	Follows bool
	// Blocked / BlockedBy are both required. A block is symmetric here.
	Blocked   bool
	BlockedBy bool
	Muted     bool
	// ViewerIsCloseFriendOfTarget: the AUTHOR has the viewer on the author's
	// close friends list. The other direction is not an audience fact.
	ViewerIsCloseFriendOfTarget bool
}

// StoryFacts is what the evaluator needs about the story itself.
type StoryFacts struct {
	Exists          bool
	Deleted         bool
	Expired         bool
	IsHighlight     bool
	Visibility      string
	ModerationState string
}

// EvaluateStoryVisibility decides whether viewer may see a story.
//
// It is pure. Every input is supplied by the caller, which is what makes it
// testable against the full matrix of relationship and moderation states
// without a database or a graph service.
func EvaluateStoryVisibility(viewerID, authorID string, f StoryFacts, rel ViewerRelationship) StoryDenial {
	if viewerID == "" {
		return DenyNoViewer
	}
	if !f.Exists {
		return DenyMissing
	}
	if f.Deleted {
		return DenyDeleted
	}

	// The owner sees their own story in every state. This is what makes a
	// truthful pending/rejected surface possible without a second code path
	// that could drift from this one.
	if viewerID == authorID {
		return DenyNone
	}

	// Moderation before audience: an unapproved story is invisible to
	// everyone but its author regardless of who is asking.
	if f.ModerationState != StoryModerationApproved {
		return DenyNotApproved
	}

	// Blocks outrank everything below, in both directions.
	if rel.Blocked || rel.BlockedBy {
		return DenyBlocked
	}
	if rel.Muted {
		return DenyMuted
	}

	// Expiry. A highlight is the one thing allowed to outlive its 24 hours —
	// and only a highlight that is still approved and still passes the
	// audience rules below, which is why this check sits here and not earlier.
	if f.Expired && !f.IsHighlight {
		return DenyExpired
	}

	switch f.Visibility {
	case StoryVisibilityPublic:
		return DenyNone
	case StoryVisibilityFollowers:
		if !rel.Follows {
			return DenyNotInAudience
		}
		return DenyNone
	case StoryVisibilityCloseFriends:
		if !rel.ViewerIsCloseFriendOfTarget {
			return DenyNotInAudience
		}
		return DenyNone
	default:
		// An unrecognised visibility is not a reason to show something. New
		// audience types must be added here deliberately.
		return DenyNotInAudience
	}
}

// StoryAudience resolves, server-side, which authors a viewer may see stories
// from. A caller never supplies this set.
type StoryAudience struct {
	graph GraphRelationships
}

// GraphRelationships is the graph-service contract this needs. An interface so
// the evaluator's tests can drive timeout/partial/error paths without a live
// dependency, and so post-service cannot accidentally grow a second way of
// asking the same question.
type GraphRelationships interface {
	// Following returns the authors this viewer follows. An error here is
	// unresolved state, never an empty audience.
	Following(ctx context.Context, viewerID string) ([]string, error)
	// RelationshipBatch returns one entry per requested target. A target
	// missing from the result is an error, not "no relationship".
	RelationshipBatch(ctx context.Context, viewerID string, targetIDs []string) (map[string]ViewerRelationship, error)
}

func NewStoryAudience(g GraphRelationships) *StoryAudience { return &StoryAudience{graph: g} }

// CandidateAuthors returns the viewer plus every author they follow.
//
// The viewer's own id is always included: a creator seeing their own story in
// their own feed is expected, and leaving it to the follow graph would mean it
// appeared only if they happened to follow themselves.
func (a *StoryAudience) CandidateAuthors(ctx context.Context, viewerID string) ([]string, error) {
	if a == nil || a.graph == nil {
		// No graph dependency configured is unresolved state, not an empty
		// audience. Fail closed and retryable.
		return nil, fmt.Errorf("%w: no graph client configured", ErrStoryPolicyUnresolved)
	}
	following, err := a.graph.Following(ctx, viewerID)
	if err != nil {
		return nil, fmt.Errorf("%w: following: %v", ErrStoryPolicyUnresolved, err)
	}
	out := make([]string, 0, len(following)+1)
	out = append(out, viewerID)
	seen := map[string]bool{viewerID: true}
	for _, id := range following {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// Relationships fetches the viewer's relationship to each author in bounded
// batches.
//
// Chunking is the caller's job because the graph store now rejects an
// oversized batch rather than truncating it — silently answering for the first
// 100 authors and reporting "no relationship" for the rest was how a blocked
// author could survive the filter.
func (a *StoryAudience) Relationships(ctx context.Context, viewerID string, authorIDs []string) (map[string]ViewerRelationship, error) {
	if a == nil || a.graph == nil {
		return nil, fmt.Errorf("%w: no graph client configured", ErrStoryPolicyUnresolved)
	}
	const chunk = 100
	out := make(map[string]ViewerRelationship, len(authorIDs))
	for start := 0; start < len(authorIDs); start += chunk {
		end := start + chunk
		if end > len(authorIDs) {
			end = len(authorIDs)
		}
		batch := authorIDs[start:end]
		got, err := a.graph.RelationshipBatch(ctx, viewerID, batch)
		if err != nil {
			return nil, fmt.Errorf("%w: relationship batch: %v", ErrStoryPolicyUnresolved, err)
		}
		// A target the graph did not answer for is unresolved. Treating an
		// absent entry as the zero value would read as "not blocked".
		for _, id := range batch {
			rel, ok := got[id]
			if !ok {
				return nil, fmt.Errorf("%w: no relationship returned for author %s", ErrStoryPolicyUnresolved, id)
			}
			out[id] = rel
		}
	}
	return out, nil
}
