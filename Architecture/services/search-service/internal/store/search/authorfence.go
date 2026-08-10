package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// Module 2 P0-7 — author-level deletion fence.
//
// Account deletion previously hard-deleted every post by the author with
// a delete-by-query. That erases every revision marker at once, which is
// the worst possible state: any stale PostCreated or approval still in
// Kafka, in the DLQ, or mid-replay recreates the deleted account's
// content, and there is nothing left in the index to say it should not.
//
// The fence replaces erasure-by-deletion with erasure-plus-barrier:
//
//	1. record the author in author_fences_v1;
//	2. convert every indexed post by that author into a tombstone stamped
//	   at fenceRev, which no ordinary revision can outrank;
//	3. reject later post writes for fenced authors on the way in.
//
// Steps 2 and 3 overlap deliberately. Step 3 alone has a window: an event
// already past its fence check when the fence lands would still be
// written. Step 2 runs after the fence is recorded, so it sweeps up
// anything that slipped through that window. Step 3 alone would also miss
// posts already indexed; step 2 alone would miss posts not yet indexed.
//
// Neither the fence document nor the tombstones retain post text, so this
// is compatible with a genuine erasure obligation — what survives is the
// fact that an identifier is permanently unusable, not the content.

const IndexAuthorFences = "author_fences_v1"

// authorFenceDoc is what we keep about an erased author. Deliberately
// minimal: an id and a timestamp, no profile data, no content.
type authorFenceDoc struct {
	AuthorID string    `json:"author_id"`
	FencedAt time.Time `json:"fenced_at"`
}

func (s *Store) ensureAuthorFenceIndex(ctx context.Context) {
	s.createIndexIfNotExists(ctx, IndexAuthorFences, `{
		"settings": `+opensearchSettingsJSON()+`,
		"mappings": {
			"properties": {
				"author_id": { "type": "keyword" },
				"fenced_at": { "type": "date" }
			}
		}
	}`)
}

// FenceAuthor records the author as permanently erased.
func (s *Store) FenceAuthor(ctx context.Context, authorID string) error {
	if authorID == "" {
		return nil
	}
	doc := authorFenceDoc{AuthorID: authorID, FencedAt: time.Now().UTC()}
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	req := opensearchapi.IndexRequest{
		Index:      IndexAuthorFences,
		DocumentID: authorID,
		Body:       bytes.NewReader(data),
		Refresh:    "true", // the fence must be visible to the check below
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return fmt.Errorf("fence author %s: %w", authorID, err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("fence author %s: %s", authorID, res.String())
	}
	return nil
}

// ErrFenceStateUnknown means erasure state could not be established.
// Callers must fail closed: no eligible content may be indexed.
var ErrFenceStateUnknown = errors.New("search: author erasure state is unknown")

// IsAuthorFenced reports whether the author has been erased.
//
// Re-review v2 P0-2: a 404 used to mean "not fenced". OpenSearch returns
// 404 for TWO states with opposite safety meanings:
//
//	{"found": false, ...}                    the index exists, this author
//	                                         has no fence → safe to index
//	{"error": {"type": "index_not_found_…"}}  the fence index is GONE → we
//	                                         know nothing → must refuse
//
// The second is not hypothetical. Index creation at startup is
// best-effort: createIndexIfNotExists only logs failures and New() returns
// success regardless, so a service can come up with no fence index at all.
// Under the old code every author then looked un-erased and every erased
// account's content was indexable again.
//
// A transport failure, an unparseable body, or a missing index all return
// an error. Only a confirmed found:false returns (false, nil).
func (s *Store) IsAuthorFenced(ctx context.Context, authorID string) (bool, error) {
	if authorID == "" {
		return false, nil
	}
	req := opensearchapi.GetRequest{
		Index:          IndexAuthorFences,
		DocumentID:     authorID,
		SourceIncludes: []string{"author_id"},
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return false, fmt.Errorf("%w: read author fence %s: %v", ErrFenceStateUnknown, authorID, err)
	}
	defer res.Body.Close()

	body, readErr := io.ReadAll(res.Body)
	if readErr != nil {
		return false, fmt.Errorf("%w: read author fence body %s: %v",
			ErrFenceStateUnknown, authorID, readErr)
	}

	// Found is a POINTER so absence is distinguishable from false.
	//
	// Re-review v3 P0-1: it used to be a plain bool, and Go decodes both
	// `{"found": false}` and a body with no `found` field at all to the
	// same zero value. A 404 carrying `{}` — from a proxy, a routing
	// error, or an unexpected response shape — therefore looked exactly
	// like "the index exists and this author has no fence", and erasure
	// enforcement was silently skipped for that lookup. Presence must be
	// observed, not inferred from a zero value.
	var parsed struct {
		Found *bool  `json:"found"`
		Index string `json:"_index"`
		Error *struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false, fmt.Errorf("%w: decode author fence response %s: %v",
			ErrFenceStateUnknown, authorID, err)
	}

	if res.StatusCode == 404 {
		// The ONLY 404 that means "safe" must satisfy every one of these:
		//   1. no error object
		//   2. `found` is PRESENT
		//   3. its value is exactly false
		//   4. `_index` identifies the fence index (when the field is
		//      present at all — OpenSearch includes it on a document miss)
		// Anything else is an unknown state and fails closed.
		safeMiss := parsed.Error == nil &&
			parsed.Found != nil &&
			!*parsed.Found &&
			(parsed.Index == "" || parsed.Index == IndexAuthorFences)
		if safeMiss {
			return false, nil
		}
		return false, fmt.Errorf("%w: author fence lookup for %s returned an "+
			"unrecognised 404 (%s); it is not a confirmed document miss, so "+
			"erasure state cannot be established",
			ErrFenceStateUnknown, authorID, describeFenceBody(parsed.Error, parsed.Found, parsed.Index))
	}

	if res.IsError() {
		return false, fmt.Errorf("%w: read author fence %s: %s",
			ErrFenceStateUnknown, authorID, string(body))
	}
	if parsed.Error != nil {
		return false, fmt.Errorf("%w: author fence lookup for %s returned an error body (%s)",
			ErrFenceStateUnknown, authorID, parsed.Error.Type)
	}
	// A 200 without an explicit found:true should not happen, but treating
	// it as "not fenced" would be guessing. Only found:true is an
	// affirmative fence; absent or false both fail closed here.
	if parsed.Found == nil || !*parsed.Found {
		return false, fmt.Errorf("%w: author fence lookup for %s returned 200 without "+
			"found:true", ErrFenceStateUnknown, authorID)
	}
	return true, nil
}

// describeFenceBody renders just enough of an unrecognised fence response
// for an operator to tell the failure modes apart in a log line.
func describeFenceBody(errObj *struct {
	Type string `json:"type"`
}, found *bool, index string) string {
	switch {
	case errObj != nil:
		return "error=" + errObj.Type
	case found == nil:
		return "no `found` field; index=" + fenceIndexOrUnset(index)
	case *found:
		return "found=true on a 404"
	default:
		return "found=false but index=" + fenceIndexOrUnset(index)
	}
}

func fenceIndexOrUnset(index string) string {
	if index == "" {
		return "(unset)"
	}
	return index
}

// EnsureSafetyIndices creates the indices that safety checks depend on and
// FAILS if they cannot be created.
//
// Re-review v2 P0-2: normal index bootstrap is log-only and best effort,
// which is fine for a search index that will refill itself. It is not fine
// for author_fences_v1, because its absence silently disables erasure
// enforcement. Wire this into startup so the service refuses to serve
// rather than serving unsafely.
func (s *Store) EnsureSafetyIndices(ctx context.Context) error {
	s.ensureAuthorFenceIndex(ctx)

	exists, err := opensearchapi.IndicesExistsRequest{
		Index: []string{IndexAuthorFences},
	}.Do(ctx, s.client)
	if err != nil {
		return fmt.Errorf("verify %s exists: %w", IndexAuthorFences, err)
	}
	defer exists.Body.Close()
	if exists.StatusCode != 200 {
		return fmt.Errorf("safety index %s is not present (status %d); refusing to "+
			"start, because without it every erased author would look un-erased",
			IndexAuthorFences, exists.StatusCode)
	}
	return nil
}

// sweepAuthorPostsScript converts an author's posts into fence tombstones
// in place. It mirrors the tombstone shape written by ApplyPostProjection
// so a swept document is indistinguishable from a normally-removed one.
const sweepAuthorPostsScript = `
Map next = new HashMap();
next.put('post_id', ctx._source.post_id);
next.put('author_id', params.author_id);
next.put('review_status', 'removed');
next.put('visibility', 'removed');
next.put('removed', true);
next.put('search_rev', params.fence_rev);
next.put('author_erased', true);
ctx._source.clear();
ctx._source.putAll(next);
`

// sweepResult is the part of the _update_by_query response we must read.
//
// Re-review P0-2: the previous version ignored the response body entirely.
// With conflicts=proceed, OpenSearch returns HTTP 200 while reporting
// version_conflicts > 0 — meaning some of the author's documents were NOT
// swept because a concurrent writer held them. A comment said "we sweep
// again"; nothing did. A conflicting public document survived erasure.
type sweepResult struct {
	Updated          int  `json:"updated"`
	VersionConflicts int  `json:"version_conflicts"`
	TimedOut         bool `json:"timed_out"`
	Failures         []struct {
		Cause struct {
			Reason string `json:"reason"`
		} `json:"cause"`
	} `json:"failures"`
}

// sweepAttempts bounds the retry of a conflicted sweep. Conflicts mean a
// concurrent writer touched the document; re-running picks it up.
const sweepAttempts = 5

// SweepAuthorPosts tombstones every indexed post by the author at the
// fence revision, erasing their content while leaving a barrier that
// stale events cannot overwrite.
//
// This replaces DeleteByQuery. Deleting was faster and strictly less
// safe: it removed the evidence that these post ids must never be
// indexed again.
func (s *Store) SweepAuthorPosts(ctx context.Context, authorID string) error {
	if authorID == "" {
		return nil
	}

	// Retry until a sweep completes with zero conflicts, zero failures and
	// no timeout. Anything less means at least one of the author's
	// documents was left untouched, and for erasure "mostly swept" is not
	// a success — it is a surviving public document belonging to a deleted
	// account.
	backoff := 100 * time.Millisecond
	for attempt := 1; attempt <= sweepAttempts; attempt++ {
		// REFRESH FIRST. _update_by_query is a search-based operation, so
		// it only sees documents that have been refreshed into a segment.
		// Without this, any post indexed within the refresh interval
		// (1s in dev, 10s in production) is INVISIBLE to the sweep and
		// survives account erasure permanently — the writer's fence
		// recheck cannot help, because at write time there was no fence.
		//
		// This was found by running the suite against a real OpenSearch;
		// no amount of static reasoning or fake-server testing exposed it,
		// because refresh semantics are the one thing a fake does not have.
		if err := s.RefreshPosts(ctx); err != nil {
			return fmt.Errorf("refresh before author sweep %s: %w", authorID, err)
		}

		result, err := s.sweepOnce(ctx, authorID)
		if err != nil {
			return err
		}
		if result.VersionConflicts == 0 && len(result.Failures) == 0 && !result.TimedOut {
			return nil
		}
		slog.Warn("search: author sweep incomplete; retrying",
			"author_id", authorID, "attempt", attempt,
			"version_conflicts", result.VersionConflicts,
			"failures", len(result.Failures), "timed_out", result.TimedOut)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Brief backoff so a contending writer can finish rather than
		// losing the same race five times in a row.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 2*time.Second {
			backoff *= 2
		}
	}
	// Returning an error matters: the caller propagates it, the consumer
	// refuses to commit, and Kafka redelivers the deletion request.
	return fmt.Errorf("sweep author posts %s: still conflicted after %d attempts",
		authorID, sweepAttempts)
}

func (s *Store) sweepOnce(ctx context.Context, authorID string) (sweepResult, error) {
	var out sweepResult

	body := map[string]any{
		"query": map[string]any{
			"term": map[string]any{"author_id": authorID},
		},
		"script": map[string]any{
			"lang":   "painless",
			"source": sweepAuthorPostsScript,
			"params": map[string]any{
				"author_id": authorID,
				"fence_rev": fenceRev,
			},
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return out, err
	}

	res, err := s.client.UpdateByQuery(
		[]string{IndexPosts},
		s.client.UpdateByQuery.WithContext(ctx),
		s.client.UpdateByQuery.WithBody(bytes.NewReader(data)),
		s.client.UpdateByQuery.WithConflicts("proceed"),
		s.client.UpdateByQuery.WithRefresh(true),
	)
	if err != nil {
		return out, fmt.Errorf("sweep author posts %s: %w", authorID, err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return out, fmt.Errorf("sweep author posts %s: %s", authorID, res.String())
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return out, fmt.Errorf("decode sweep result for %s: %w", authorID, err)
	}
	return out, nil
}

// EraseAuthorContent performs the full erasure protocol in the order that
// makes the two mechanisms cover each other's gaps.
func (s *Store) EraseAuthorContent(ctx context.Context, authorID string) error {
	if err := s.FenceAuthor(ctx, authorID); err != nil {
		return err
	}
	// After the fence, so it also catches anything written during the
	// check-to-fence window.
	if err := s.SweepAuthorPosts(ctx, authorID); err != nil {
		return err
	}
	return nil
}

// IndexPostUnlessAuthorErased applies an eligible projection with a
// fence recheck, closing the writer half of the erasure race
// (re-review P0-2).
//
// The check-then-write pattern alone loses this interleaving:
//
//  1. writer checks the fence   → not fenced
//  2. eraser writes the fence
//  3. eraser's _update_by_query takes its snapshot (post does not exist)
//  4. writer creates the public document
//  5. sweep finishes without ever seeing it
//
// Step 4 lands after the only sweep that will ever run, so a deleted
// account keeps a public post. Rechecking AFTER the write closes it: the
// eraser's fence is durable by step 2, so any writer that gets past step
// 1 must observe it on the recheck, and immediately fences its own post.
//
// Both orderings are now covered — fence-before-write is caught by the
// first check, fence-during-write by the second.
func (s *Store) IndexPostUnlessAuthorErased(ctx context.Context, p PostProjection) error {
	authorID := p.AuthorID
	if authorID == "" {
		authorID = p.Doc.AuthorID
	}

	fenced, err := s.IsAuthorFenced(ctx, authorID)
	if err != nil {
		return fmt.Errorf("author fence check %s: %w", authorID, err)
	}
	if fenced {
		slog.Warn("search: refusing to index content for an erased author",
			"post_id", p.PostID, "author_id", authorID)
		return nil
	}

	if err := s.ApplyPostProjection(ctx, p); err != nil {
		return err
	}

	// Recheck. If the erasure landed while we were writing, undo our own
	// write immediately rather than waiting for a sweep that has already
	// run.
	fenced, err = s.IsAuthorFenced(ctx, authorID)
	if err != nil {
		// We cannot prove the author is safe and we have just published
		// their content. Remove it and surface the error so the message
		// is retried — failing closed costs a redelivery, failing open
		// costs an erased account a public post.
		if ferr := s.FencePostForErasedAuthor(ctx, p.PostID, authorID); ferr != nil {
			slog.Error("search: fence recheck failed AND rollback failed",
				"post_id", p.PostID, "author_id", authorID, "rollback_err", ferr)
		}
		return fmt.Errorf("author fence recheck %s: %w", authorID, err)
	}
	if fenced {
		slog.Warn("search: author was erased mid-write; fencing the post we just indexed",
			"post_id", p.PostID, "author_id", authorID)
		return s.FencePostForErasedAuthor(ctx, p.PostID, authorID)
	}
	return nil
}
