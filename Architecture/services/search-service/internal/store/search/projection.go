package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// Module 2 P0-1 / P0-2 / P0-7 — the atomic post projection primitive.
//
// WHY THIS EXISTS
//
// The previous design read search_rev with a GET, compared it in Go, then
// issued an unconditional write. That is a time-of-check/time-of-use race.
// Codex's sequence is real and does not need anything exotic to trigger:
//
//	1. approval rev 2 and rejection rev 3 are handled concurrently
//	2. both GET the stored rev 1
//	3. rejection writes its tombstone at rev 3
//	4. approval writes the full public document at rev 2, last
//	5. rejected content is publicly searchable, indefinitely
//
// Two consumer replicas, or one replica with the DLQ replayer running
// alongside the main loop, is enough. Keeping a revision on the tombstone
// closes only the sequential replay case; it cannot close an interleaving
// unless OpenSearch itself decides whether the write applies.
//
// So the comparison moves INTO OpenSearch. Every projection write — index,
// removal, takedown, legacy delete, reconciler repair, author fence — is a
// scripted _update that reads the stored revision and applies the change
// only if it should win. OpenSearch takes a document-level lock for the
// duration of an update script, and retry_on_conflict re-runs the whole
// read-modify-write against fresh state, so concurrent writers serialize
// instead of racing.
//
// CONFLICT RULES
//
//	incoming < stored   → drop (stale)
//	incoming == stored  → drop, UNLESS it is a removal landing on a
//	                      non-removed document, because removal wins ties
//	incoming > stored   → apply
//
// Ties go to removal deliberately. If two writers ever derive the same
// revision, the failure we can afford is content being hidden that should
// have been visible; the one we cannot afford is content being visible
// that should have been hidden.

// REVISION DOMAIN (re-review P0-3)
//
// The revision space is split into two ranges, and the split is ENFORCED
// rather than assumed:
//
//	[1, fenceRev)        ordinary transitions from Postgres search_rev
//	[fenceRev, MaxInt64] reserved for permanent author erasure
//
// The previous version documented the boundary in a comment and validated
// nothing. That left three real holes: a malformed event carrying a
// revision above fenceRev could overwrite an erasure tombstone; AutoRev
// on a document near MaxInt64 overflowed `stored + 1` into a negative
// number, turning a safety removal into a no-op; and nothing stopped
// future code from using the reserved range by accident.
//
// Enforcement now happens in both places it can: Go rejects out-of-domain
// input before the request is sent, and Painless refuses non-removal
// writes to an erased document regardless of the number attached to them.
const fenceRev int64 = 9_000_000_000_000_000_000

// maxOrdinaryRev is the highest revision an ordinary transition may carry.
const maxOrdinaryRev int64 = fenceRev - 1

// ErrRevisionOutOfDomain is returned when an eligible projection carries a
// revision outside [1, fenceRev). Callers must treat it as fail-closed:
// the content is NOT indexed.
var ErrRevisionOutOfDomain = errors.New("search: projection revision outside the ordinary domain")

// projectionRetries bounds OpenSearch's internal re-run of the script
// when a concurrent writer wins the document lock first. Each retry
// re-reads the current revision, so a retry cannot apply a stale write.
const projectionRetries = 5

// PostProjection is one write to the public post index.
type PostProjection struct {
	PostID string
	// Rev is the canonical revision from Postgres. Ignored when AutoRev.
	Rev int64
	// AutoRev asks OpenSearch to stamp storedRev+1. Used by events that
	// carry no canonical revision (takedown, legacy deletes) so they can
	// still raise the barrier rather than erasing it.
	AutoRev bool
	// Removed writes a tombstone instead of a document.
	Removed bool
	// Doc is the document to index. Ignored when Removed.
	Doc PostDoc
	// AuthorID is retained on tombstones so author-scoped erasure can
	// still find them. Falls back to Doc.AuthorID.
	AuthorID string
	// AuthorErased marks this removal as permanent account erasure. The
	// marker is sticky in the index and makes every later non-removal
	// write fail regardless of its revision.
	AuthorErased bool
}

// validate enforces the revision domain before anything is sent.
//
// Removals are deliberately permissive: a removal is always the safe
// direction, so an out-of-domain removal is clamped rather than refused.
// Eligible writes are strict and fail closed — refusing to index is
// recoverable, indexing something that should have been hidden is not.
func (p *PostProjection) validate() error {
	if p.AutoRev {
		// The revision is computed inside OpenSearch; nothing to check.
		return nil
	}
	if p.Removed {
		if p.Rev < 0 {
			return fmt.Errorf("%w: removal revision %d is negative", ErrRevisionOutOfDomain, p.Rev)
		}
		return nil
	}
	if p.Rev <= 0 || p.Rev > maxOrdinaryRev {
		return fmt.Errorf("%w: eligible revision %d not in [1, %d]",
			ErrRevisionOutOfDomain, p.Rev, maxOrdinaryRev)
	}
	return nil
}

// applyPostProjectionScript is the compare-and-apply executed inside
// OpenSearch. Painless has no early `return` from the top level of an
// update script in every version, so the flow is expressed with nested
// conditionals rather than guard clauses.
const applyPostProjectionScript = `
long stored = 0;
if (ctx._source.containsKey('search_rev') && ctx._source.search_rev != null) {
  stored = ((Number)ctx._source.search_rev).longValue();
}
boolean storedRemoved = ctx._source.containsKey('removed') && ctx._source.removed == true;
boolean storedErased = ctx._source.containsKey('author_erased') && ctx._source.author_erased == true;

long incoming = params.rev;
if (params.auto_rev) {
  // Overflow-safe: never advance past the reserved boundary. A document
  // already at or above it is erased or corrupt, and a removal there is
  // already in force, so pinning rather than incrementing is correct and
  // cannot wrap into a negative revision.
  if (stored >= params.max_ordinary_rev) {
    incoming = stored;
  } else {
    incoming = stored + 1;
  }
}

boolean apply = false;
if (incoming > stored) {
  apply = true;
} else if (incoming == stored && params.removed && !storedRemoved) {
  apply = true;
}

// Permanent erasure is absolute and is NOT expressible as a number. Once
// a document carries author_erased, no amount of revision can bring the
// content back — only another removal may touch it. This is the backstop
// for a malformed or hostile event carrying a revision above the fence.
if (apply && storedErased && !params.removed) {
  apply = false;
}

if (apply) {
  Map next = new HashMap();
  if (params.removed) {
    next.putAll(params.tombstone);
  } else {
    next.putAll(params.doc);
  }
  next.put('search_rev', incoming);
  // author_erased is sticky: it survives every later write to this
  // document, including a subsequent ordinary removal.
  if (storedErased || (params.removed && params.author_erased)) {
    next.put('author_erased', true);
  }
  ctx._source.clear();
  ctx._source.putAll(next);
} else {
  ctx.op = 'none';
}
`

// ApplyPostProjection atomically applies one projection write.
//
// It is the ONLY way a post document may be written or removed. Anything
// that bypasses it — an unconditional IndexRequest, a DeleteRequest —
// reopens the resurrection window, because a plain write has no opinion
// about what it is overwriting.
func (s *Store) ApplyPostProjection(ctx context.Context, p PostProjection) error {
	if p.PostID == "" {
		return nil
	}
	if err := p.validate(); err != nil {
		return err
	}

	authorID := p.AuthorID
	if authorID == "" {
		authorID = p.Doc.AuthorID
	}

	// The tombstone keeps the revision and the author, and nothing that
	// could disclose the content: no text, no hashtags, no captions.
	tombstone := map[string]any{
		"post_id":       p.PostID,
		"review_status": "removed",
		"visibility":    "removed",
		"removed":       true,
	}
	if authorID != "" {
		tombstone["author_id"] = authorID
	}
	if p.AuthorErased {
		tombstone["author_erased"] = true
	}

	docMap := map[string]any{}
	if !p.Removed {
		raw, err := json.Marshal(p.Doc)
		if err != nil {
			return fmt.Errorf("marshal post projection %s: %w", p.PostID, err)
		}
		if err := json.Unmarshal(raw, &docMap); err != nil {
			return fmt.Errorf("normalize post projection %s: %w", p.PostID, err)
		}
		docMap["removed"] = false
	}

	body := map[string]any{
		"scripted_upsert": true,
		"upsert":          map[string]any{},
		"script": map[string]any{
			"lang":   "painless",
			"source": applyPostProjectionScript,
			"params": map[string]any{
				"rev":              p.Rev,
				"auto_rev":         p.AutoRev,
				"removed":          p.Removed,
				"author_erased":    p.AuthorErased,
				"max_ordinary_rev": maxOrdinaryRev,
				"doc":              docMap,
				"tombstone":        tombstone,
			},
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode projection request %s: %w", p.PostID, err)
	}

	retries := projectionRetries
	req := opensearchapi.UpdateRequest{
		Index:           IndexPosts,
		DocumentID:      p.PostID,
		Body:            bytes.NewReader(data),
		RetryOnConflict: &retries,
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return fmt.Errorf("apply projection %s: %w", p.PostID, err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("apply projection %s: %s", p.PostID, res.String())
	}
	return nil
}

// FencePostForErasedAuthor writes an author-deletion fence over a post.
//
// The fence is a tombstone at a revision no ordinary event can reach, so
// every later stale PostCreated or approval for that post is rejected by
// the same comparison that handles normal ordering — no separate "is this
// author deleted?" lookup on the hot path.
//
// It retains no post text, which is what makes it usable as GDPR erasure
// rather than merely as a hiding mechanism.
func (s *Store) FencePostForErasedAuthor(ctx context.Context, postID, authorID string) error {
	return s.ApplyPostProjection(ctx, PostProjection{
		PostID:       postID,
		Rev:          fenceRev,
		Removed:      true,
		AuthorID:     authorID,
		AuthorErased: true,
	})
}

// FenceRevision exposes the fence revision for tests.
func FenceRevision() int64 { return fenceRev }
