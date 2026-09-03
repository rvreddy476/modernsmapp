package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// Module 2 M2-P0-4 — two-way block enforcement on every search surface.
//
// Search previously applied no block filtering whatsoever. Blocking
// someone removed them from your feed but left their posts, their
// profile, and their autocomplete entry fully reachable by searching —
// and, worse, left YOUR content reachable by the person who blocked you.
// The protection a block is supposed to provide simply did not extend
// past the feed.
//
// Design decisions:
//
//   - Applied as an OpenSearch `must_not`, not as post-filtering of the
//     result page. Post-filtering silently shrinks pages, leaks the
//     existence of hidden results through gaps in the count, and breaks
//     pagination — page 2 would be computed from an offset that includes
//     the very documents being hidden.
//   - Carried on the context and applied at the single point where every
//     query is serialized. Threading a parameter through each of the
//     surfaces would work until someone adds the sixth surface and
//     forgets. This way a new query is protected by default.
//   - FAIL CLOSED at the handler: if the block set cannot be resolved for
//     an authenticated viewer, the request fails rather than returning
//     unfiltered results.
//
// PRIVATE ACCOUNTS ride the same scope. A posts query additionally
// excludes `author_is_private == true` unless the author is the viewer or
// someone the viewer follows. Users stay searchable — a private profile is
// findable, its posts are not. The exclusion applies to EVERY resolved
// scope, anonymous included: an anonymous viewer follows nobody, so every
// private author's posts are hidden from them.

type blockFilterKey struct{}

// viewerScope holds everything resolved for one request that the store
// layer must apply to every query it runs.
type viewerScope struct {
	// viewerID is empty for an anonymous viewer.
	viewerID string
	ids      []string
	// following is the viewer's follow set (best-effort; see blockscope).
	// Absent entries only ever HIDE more, never less.
	following []string
	// resolved is false when the caller never established a scope. It
	// distinguishes "anonymous viewer, nothing to filter" from "we forgot
	// to resolve" only for diagnostics; enforcement lives in the handler.
	resolved bool
}

// WithBlockedIDs returns a context carrying the user IDs whose documents
// must be excluded from any search executed with it. Pass the result of
// graphclient.BlockedIDs. Equivalent to WithViewerScope with no viewer and
// no follow set — every private author's posts are hidden.
func WithBlockedIDs(ctx context.Context, ids []string) context.Context {
	return WithViewerScope(ctx, "", ids, nil)
}

// WithViewerScope returns a context carrying the viewer's identity, block
// set and follow set. viewerID may be empty (anonymous).
func WithViewerScope(ctx context.Context, viewerID string, blocked, following []string) context.Context {
	return context.WithValue(ctx, blockFilterKey{}, viewerScope{
		viewerID:  viewerID,
		ids:       blocked,
		following: following,
		resolved:  true,
	})
}

func scopeFrom(ctx context.Context) viewerScope {
	scope, _ := ctx.Value(blockFilterKey{}).(viewerScope)
	return scope
}

// blockedFrom extracts the block set from the context.
func blockedFrom(ctx context.Context) []string {
	return scopeFrom(ctx).ids
}

// BlockScopeResolved reports whether a block scope was established on the
// context. Used by tests to assert the handler actually resolved one.
func BlockScopeResolved(ctx context.Context) bool {
	return scopeFrom(ctx).resolved
}

// BlockedIDsForTest exposes the resolved block set so the HTTP package can
// assert the middleware attached what graph-service returned.
func BlockedIDsForTest(ctx context.Context) []string { return blockedFrom(ctx) }

// FollowingIDsForTest exposes the resolved follow set for the HTTP tests.
func FollowingIDsForTest(ctx context.Context) []string { return scopeFrom(ctx).following }

// ViewerIDForTest exposes the resolved viewer id for the HTTP tests.
func ViewerIDForTest(ctx context.Context) string { return scopeFrom(ctx).viewerID }

// privateAuthorField is the posts_v1 owner field; the private-author
// exclusion applies exactly when a query is keyed on it.
const privateAuthorField = "author_id"

// encodeQuery applies the context's block filter to an OpenSearch query
// body and serializes it.
//
// idField is the field in the target index that identifies the user a
// document belongs to — "author_id" for posts, "user_id" for users,
// "owner_id" for communities and channels, "seller_id" for products.
// Pass "" for indices with no owning user (hashtags), where blocks do not
// apply directly.
func encodeQuery(ctx context.Context, q map[string]interface{}, idField string) (*bytes.Buffer, error) {
	if idField != "" {
		scope := scopeFrom(ctx)
		if len(scope.ids) > 0 {
			applyBlockMustNot(q, idField, scope.ids)
		}
		if idField == privateAuthorField && scope.resolved {
			applyPrivateAuthorMustNot(q, scope.allowedPrivateAuthors())
		}
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(q); err != nil {
		return nil, fmt.Errorf("encode search query: %w", err)
	}
	return &buf, nil
}

// allowedPrivateAuthors is following ∪ {viewer}: the private authors whose
// posts this viewer may still see.
func (s viewerScope) allowedPrivateAuthors() []string {
	out := make([]string, 0, len(s.following)+1)
	seen := make(map[string]bool, len(s.following)+1)
	if s.viewerID != "" {
		seen[s.viewerID] = true
		out = append(out, s.viewerID)
	}
	for _, id := range s.following {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// ownerFieldForIndex maps an index to the field naming the user whose
// content it is, so the block exclusion targets the right field.
//
// Hashtags map to "" — a hashtag document belongs to no one. Blocked
// users' posts are already excluded from the aggregation that builds
// hashtag counts, so there is nothing left to filter here.
func ownerFieldForIndex(index string) string {
	switch index {
	case IndexPosts:
		return "author_id"
	case IndexUsers:
		return "user_id"
	case IndexCommunities, IndexChannels:
		return "owner_id"
	case IndexProducts:
		return "seller_id"
	default:
		return ""
	}
}

// applyBlockMustNot injects `must_not: [{terms: {<idField>: ids}}]` into
// the query's top-level bool clause, wrapping a non-bool query if needed
// so the exclusion always applies.
func applyBlockMustNot(q map[string]interface{}, idField string, ids []string) {
	appendMustNot(q, map[string]interface{}{
		"terms": map[string]interface{}{idField: ids},
	})
}

// applyPrivateAuthorMustNot excludes posts by private authors the viewer
// may not read:
//
//	must_not: [{bool: {filter: [{term: {author_is_private: true}}],
//	                   must_not: [{terms: {author_id: allowed}}]}}]
//
// With an empty allowed set the clause collapses to the bare term, so an
// anonymous viewer never sees a private author's post.
func applyPrivateAuthorMustNot(q map[string]interface{}, allowed []string) {
	private := map[string]interface{}{
		"term": map[string]interface{}{"author_is_private": true},
	}
	if len(allowed) == 0 {
		appendMustNot(q, private)
		return
	}
	appendMustNot(q, map[string]interface{}{
		"bool": map[string]interface{}{
			"filter":   []interface{}{private},
			"must_not": []interface{}{map[string]interface{}{"terms": map[string]interface{}{privateAuthorField: allowed}}},
		},
	})
}

// appendMustNot adds one clause to the query's top-level bool must_not,
// creating the bool (and wrapping a non-bool query to preserve its scoring)
// when needed.
func appendMustNot(q map[string]interface{}, clause map[string]interface{}) {
	inner, ok := q["query"].(map[string]interface{})
	if !ok {
		// No query clause at all (match_all by omission): add one that is
		// nothing but the exclusion.
		q["query"] = map[string]interface{}{
			"bool": map[string]interface{}{"must_not": []interface{}{clause}},
		}
		return
	}

	boolClause, ok := inner["bool"].(map[string]interface{})
	if !ok {
		// A non-bool query (function_score, match, term…). Wrap it so the
		// original scoring is preserved and the exclusion still applies.
		q["query"] = map[string]interface{}{
			"bool": map[string]interface{}{
				"must":     []interface{}{inner},
				"must_not": []interface{}{clause},
			},
		}
		return
	}

	switch existing := boolClause["must_not"].(type) {
	case nil:
		boolClause["must_not"] = []interface{}{clause}
	case []interface{}:
		boolClause["must_not"] = append(existing, clause)
	case []map[string]interface{}:
		merged := make([]interface{}, 0, len(existing)+1)
		for _, e := range existing {
			merged = append(merged, e)
		}
		boolClause["must_not"] = append(merged, clause)
	default:
		boolClause["must_not"] = []interface{}{existing, clause}
	}
}
