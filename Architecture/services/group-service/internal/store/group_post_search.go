package store

import (
	"context"

	"github.com/google/uuid"
)

/*
In-group post search.

Two things in here are load-bearing and both have already gone wrong once in
this package:

  - THE PROJECTION IS NOT RE-DERIVED. This selects groupPostV2ColumnsP plus
    viewerEngagementColumns, in that order, and scans them with
    scanGroupPostV2WithViewer. A hand-rolled SELECT that happens to list the
    same columns compiles, passes review, and then 500s the first time anyone
    adds a column to the shared projection — which is exactly how the viewer
    scanner broke when the anonymity columns landed. There is an arity guard
    (projection_scanner_test.go) protecting that pair; reusing the pair is how
    this query gets to sit behind it.

  - THE TSQUERY FUNCTION IS websearch_to_tsquery, NOT to_tsquery.
    to_tsquery parses an OPERATOR expression: `book & club` is valid, `book
    club` is a syntax error raised by Postgres, which arrives here as a plain
    error and leaves the handler with nothing to do but 500. Users type
    `book club`. SearchGroups had this bug for its whole life and has been
    fixed in the same change.

    websearch_to_tsquery (Postgres 11+; this service targets 16) takes free
    text the way a search box produces it — bare words, "quoted phrases",
    or, -negation — and is documented never to raise a syntax error on any
    input. plainto_tsquery would also be safe but throws away quoted phrases,
    which a search box visibly offers.

There is deliberately NO author filter and no author-name search. Searching by
author is how an anonymous post gets de-anonymised: the row still carries the
real author_id (migration 013 keeps it, on purpose), so a query that could
select on it would let a member find "posts by X" and read the masked ones
straight out of the result. The only searchable text is the post's own title
and body.
*/

/*
groupPostSearchVector is the indexed document, and the ONE definition of it.

It is a constant because the GIN index in migration 015 must contain the same
expression byte for byte: an expression index that does not match the query is
not a wrong answer, it is a silent sequential scan that no test would notice.
TestSearchVectorExpressionMatchesTheMigration asserts the two agree.

coalesce on both columns. title and body are nullable and `NULL || ' '` is
NULL, so without it a post with no title produces a NULL vector, matches
nothing, and is simply absent from search results for ever.
*/
const groupPostSearchVector = `to_tsvector('english', coalesce(p.title, '') || ' ' || coalesce(p.body, ''))`

// groupPostSearchQuery is the parsed user input. Named alongside the vector so
// the pair is read together and the choice of function is visible at the point
// of use rather than buried in a query string.
const groupPostSearchQuery = `websearch_to_tsquery('english', $2)`

/*
SearchGroupPostsV2 returns one group's published posts matching q, newest and
most relevant first.

Callers must have already decided the viewer may read this group's posts — this
function applies the post-level rules only. service.SearchGroupPostsV2 is the
only caller and carries the group-level rule.

viewerID is the viewer's user id as TEXT ("" for no viewer), exactly as
ListGroupPostsV2 takes it, and drives the viewer_* flags.

The status filter is `= 'published'`, character for character what the feed's
ListGroupPostsV2 uses. Deleted posts are status = 'deleted' (DeleteGroupPostV2
is a soft delete) and posts awaiting a moderator are 'pending_approval'; both
are excluded here for the same reason they are excluded from the feed. Search
must not be a way to read a post the feed refuses to show.
*/
func (s *Store) SearchGroupPostsV2(ctx context.Context, groupID uuid.UUID, q string, viewerID string, limit, offset int) ([]GroupPostV2, error) {
	// The same cap, and the same silent clamp, as ListGroupPostsV2. A caller
	// that asks for 10000 gets 20 rather than an error, because that is what
	// every other list in this store does.
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := s.db.Query(ctx,
		`SELECT `+groupPostV2ColumnsP+`, `+viewerEngagementColumns+`
		FROM group_posts p
		`+viewerEngagementJoins("$5")+`
		WHERE p.group_id = $1 AND p.status = 'published'
		  AND `+groupPostSearchVector+` @@ `+groupPostSearchQuery+`
		ORDER BY ts_rank(`+groupPostSearchVector+`, `+groupPostSearchQuery+`) DESC, p.created_at DESC
		LIMIT $3 OFFSET $4`,
		groupID, q, limit, offset, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Non-nil: a search with no hits is a normal, frequent answer and the
	// client should read `[]`, not `null`.
	posts := []GroupPostV2{}
	for rows.Next() {
		p, err := scanGroupPostV2WithViewer(rows)
		if err != nil {
			return nil, err
		}
		posts = append(posts, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.attachReactionCounts(ctx, posts); err != nil {
		return nil, err
	}
	return posts, nil
}
