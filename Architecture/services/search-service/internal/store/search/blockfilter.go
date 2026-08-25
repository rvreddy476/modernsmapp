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

type blockFilterKey struct{}

// blockScope holds the resolved block set for one request.
type blockScope struct {
	ids []string
	// resolved is false when the caller never established a scope. It
	// distinguishes "anonymous viewer, nothing to filter" from "we forgot
	// to resolve" only for diagnostics; enforcement lives in the handler.
	resolved bool
}

// WithBlockedIDs returns a context carrying the user IDs whose documents
// must be excluded from any search executed with it. Pass the result of
// graphclient.BlockedIDs.
func WithBlockedIDs(ctx context.Context, ids []string) context.Context {
	return context.WithValue(ctx, blockFilterKey{}, blockScope{ids: ids, resolved: true})
}

// blockedFrom extracts the block set from the context.
func blockedFrom(ctx context.Context) []string {
	scope, _ := ctx.Value(blockFilterKey{}).(blockScope)
	return scope.ids
}

// BlockScopeResolved reports whether a block scope was established on the
// context. Used by tests to assert the handler actually resolved one.
func BlockScopeResolved(ctx context.Context) bool {
	scope, _ := ctx.Value(blockFilterKey{}).(blockScope)
	return scope.resolved
}

// BlockedIDsForTest exposes the resolved block set so the HTTP package can
// assert the middleware attached what graph-service returned.
func BlockedIDsForTest(ctx context.Context) []string { return blockedFrom(ctx) }

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
		if blocked := blockedFrom(ctx); len(blocked) > 0 {
			applyBlockMustNot(q, idField, blocked)
		}
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(q); err != nil {
		return nil, fmt.Errorf("encode search query: %w", err)
	}
	return &buf, nil
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
	terms := map[string]interface{}{
		"terms": map[string]interface{}{idField: ids},
	}

	inner, ok := q["query"].(map[string]interface{})
	if !ok {
		// No query clause at all (match_all by omission): add one that is
		// nothing but the exclusion.
		q["query"] = map[string]interface{}{
			"bool": map[string]interface{}{"must_not": []interface{}{terms}},
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
				"must_not": []interface{}{terms},
			},
		}
		return
	}

	switch existing := boolClause["must_not"].(type) {
	case nil:
		boolClause["must_not"] = []interface{}{terms}
	case []interface{}:
		boolClause["must_not"] = append(existing, terms)
	case []map[string]interface{}:
		merged := make([]interface{}, 0, len(existing)+1)
		for _, e := range existing {
			merged = append(merged, e)
		}
		boolClause["must_not"] = append(merged, terms)
	default:
		boolClause["must_not"] = []interface{}{existing, terms}
	}
}
