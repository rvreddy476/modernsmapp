package service

import (
	"context"
	"errors"
	"strings"

	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
)

/*
In-group post search, above the store.

The store decides which POSTS match. This file decides whether the caller may
see any of them, and it is the whole security surface of the feature.

Two rules, and neither is invented here:

 1. THE READ RULE IS THE FEED'S. GetGroupFeedV2 reads the group, refuses a nil
    one, and then calls checkGroupAccess — and that is exactly what
    readableGroupForSearch does, by calling the same checkGroupAccess. Writing a
    second predicate that "does the same thing" is how search ends up looser
    than the feed it is supposed to be searching; there is one predicate and
    both paths call it.

 2. A REFUSAL AND A MISSING GROUP ARE THE SAME ANSWER. The feed distinguishes
    them (404 for missing, 403 for private-and-not-a-member) because it is
    reached by navigating to a group you were shown. Search is reached by
    typing, so distinguishing them turns the endpoint into an oracle: hit
    /v1/groups/<uuid>/posts/v2/search for any uuid and a 403 means "this
    private group exists" while a 404 means it does not. That is a probe for
    the existence and membership of private groups.

    This service already made this exact decision once, deliberately, for
    cross-posting: internal/service/cross_post.go collapses "no such group",
    "deleted", "archived" and "private and you are not a member" into the
    single OutcomeUnavailable, with the note that keeping them apart "turns
    cross-posting into a probe for the existence of private groups". Same
    reasoning, same answer, so the two features cannot be played against each
    other.

Nothing here distinguishes an existing route's behaviour: this is a new
endpoint, and GetGroupFeedV2's own 403/404 split is untouched.
*/

// ErrGroupSearchUnavailable is the ONE answer for every reason a caller may not
// search a group: it does not exist, it is deleted, or it is private and they
// are not a member.
//
// Exported so the HTTP layer's own test can prove what status it becomes,
// rather than a guard asserting that a string contains a substring.
//
// The message carries "not found" so handleServiceError maps it to 404
// NOT_FOUND — the same status a genuinely missing group gets, which is the
// point. There is deliberately no second error value for the private case: a
// distinct one would, sooner or later, be given a distinct status by someone
// tidying the error mapping, and the oracle would be back.
var ErrGroupSearchUnavailable = errors.New("not found: group not found")

// MaxGroupPostSearchQueryLen caps the query string.
//
// A tsquery is parsed and then matched against every candidate document, so an
// unbounded query is unbounded server work per request. 200 is generously past
// anything a person types into a box and well under anything that costs.
const MaxGroupPostSearchQueryLen = 200

/*
readableGroupForSearch resolves the group and decides whether actorID may read
its posts, or returns ErrGroupSearchUnavailable.

The nil check covers deleted groups too: GetGroupByID filters
`status != 'deleted'`, so a deleted group reads as nil here and takes the same
branch as one that never existed.
*/
func (s *Service) readableGroupForSearch(ctx context.Context, actorID, groupID uuid.UUID) (*store.Group, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, ErrGroupSearchUnavailable
	}

	// The feed's predicate, called rather than restated. See GetGroupFeedV2.
	if err := s.checkGroupAccess(ctx, g, actorID); err != nil {
		/*
			checkGroupAccess returns two very different things through one
			error: its policy refusal ("forbidden: not a member of this private
			group") and any failure of the membership read underneath it.

			Only the refusal is collapsed. Reporting a database outage as
			"group not found" would tell the user their group had been deleted,
			and would hide a real incident behind a plausible 404 — so a
			non-refusal error is returned as itself and becomes a 500.

			Substring matching on "forbidden" is how this package's own error
			taxonomy already works (see handleServiceError in internal/http).
		*/
		if strings.Contains(err.Error(), "forbidden") {
			return nil, ErrGroupSearchUnavailable
		}
		return nil, err
	}
	return g, nil
}

/*
SearchGroupPostsV2 searches one group's posts for the given text.

The return type is []store.GroupPostV2 — the SAME type the feed returns, and
that is a security property rather than a convenience. store.GroupPostV2 has a
MarshalJSON (internal/store/anonymous.go) that substitutes the per-post alias
for an anonymous post's author_id. The masking lives on the type precisely so
that a reader added later is masked without having to remember; a bespoke
response struct for this endpoint, however tidy, would serialise the real
author_id of every anonymous post in the results.
*/
func (s *Service) SearchGroupPostsV2(ctx context.Context, actorID, groupID uuid.UUID, q string, limit, offset int) ([]store.GroupPostV2, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("invalid: search query is required")
	}
	/*
		Truncated by RUNES, not by bytes.

		q[:200] on a byte slice can cut a multi-byte character in half, and the
		result is not valid UTF-8. pgx would hand that to Postgres, which
		rejects it, and a search in any non-Latin script would 500 at exactly
		200 bytes — a bug reachable only by users typing in their own language.
	*/
	if r := []rune(q); len(r) > MaxGroupPostSearchQueryLen {
		q = string(r[:MaxGroupPostSearchQueryLen])
	}

	// Authorisation FIRST, and before any query that touches posts: a refusal
	// must not be distinguishable by timing or by anything else from a group
	// that does not exist.
	if _, err := s.readableGroupForSearch(ctx, actorID, groupID); err != nil {
		return nil, err
	}

	return s.store.SearchGroupPostsV2(ctx, groupID, q, viewerKey(actorID), limit, offset)
}
