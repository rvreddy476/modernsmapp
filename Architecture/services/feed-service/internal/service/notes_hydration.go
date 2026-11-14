package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
)

// Echo note hydration (docs/designs/echo-v2-contract.md).
//
// The Following and For You pages are hydrated through post-service's
// GET /internal/notes/batch?ids=a,b,… (internal key, X-User-Id = the
// viewer), which answers {"data":{"items":[Note]}} in request order with
// every v2 field (view_count, bookmark_count, viewer_has_bookmarked,
// quote_count, status, scheduled_at, poll, publish_error) already set for
// that viewer, and never a scheduled note. Each Note is passed to the
// client byte for byte, so the feed answers exactly post-service's Note
// JSON.
//
// The feed's own hydration-time rules still run on a decoded copy of each
// row (processing, listing visibility, keyword filter, author privacy,
// "not interested"), fail closed as in HydratePosts. Blocks, mutes and the
// own-note rule run before this, on the candidates (notes.go). A note the
// batch did not answer is dropped, as a hidden or deleted one always was.
// Notes are not written to the per-viewer hydration cache: every field is
// viewer-specific and the counts move fast.

// notesBatchMax is post-service's per-call id limit on /internal/notes/batch.
const notesBatchMax = 50

// noteRow is one Note: the decoded fields the filters read, and the bytes
// post-service sent.
type noteRow struct {
	post        HydratedPost
	status      string
	scheduledAt json.RawMessage
	raw         json.RawMessage
}

// scheduled is the read-side backstop for the batch's promise: a note
// whose status is not "published", or that carries a schedule, is never on
// a feed.
func (n noteRow) scheduled() bool {
	if n.post.IsScheduled || (n.status != "" && n.status != "published") {
		return true
	}
	s := strings.TrimSpace(string(n.scheduledAt))
	return s != "" && s != "null"
}

// HydrateNotes turns note candidates into post-service's Note JSON for
// viewerID, in candidate order. Any failure is an error: the caller answers
// 503, never the unfiltered candidates.
func (s *Service) HydrateNotes(ctx context.Context, items []FeedItem, viewerID uuid.UUID) (_ []json.RawMessage, err error) {
	defer func() {
		if err != nil {
			hydrationFailures.Inc()
		}
	}()
	if len(items) == 0 {
		return []json.RawMessage{}, nil
	}
	seen := make(map[uuid.UUID]bool, len(items))
	ids := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		if !seen[it.PostID] {
			seen[it.PostID] = true
			ids = append(ids, it.PostID)
		}
	}
	rows := make(map[uuid.UUID]noteRow, len(ids))
	for start := 0; start < len(ids); start += notesBatchMax {
		end := start + notesBatchMax
		if end > len(ids) {
			end = len(ids)
		}
		chunk, err := s.fetchNotesBatch(ctx, ids[start:end], viewerID)
		if err != nil {
			return nil, err
		}
		for _, n := range chunk {
			rows[n.post.ID] = n
		}
	}

	// Candidate order; a note the batch did not answer is dropped.
	posts := make([]HydratedPost, 0, len(ids))
	for _, id := range ids {
		n, ok := rows[id]
		if !ok {
			continue // hidden, deleted or not visible to this viewer
		}
		if n.post.ContentType != NoteContentType || n.scheduled() {
			continue
		}
		posts = append(posts, n.post)
	}
	posts = applyProcessingFilter(viewerID, posts)
	posts = applyListingVisibilityFilter(viewerID, posts)
	if posts, err = s.applyKeywordHideFilter(ctx, viewerID, posts); err != nil {
		return nil, err
	}
	if posts, err = s.applyAuthorPrivacyFilter(ctx, viewerID, posts); err != nil {
		return nil, err
	}
	if posts, err = s.applyFeedbackFilter(ctx, viewerID, posts); err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, 0, len(posts))
	for _, p := range posts {
		out = append(out, rows[p.ID].raw)
	}
	return out, nil
}

// fetchNotesBatch is one call to /internal/notes/batch (at most
// notesBatchMax ids) on behalf of viewerID.
func (s *Service) fetchNotesBatch(ctx context.Context, ids []uuid.UUID, viewerID uuid.UUID) ([]noteRow, error) {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = id.String()
	}
	endpoint := strings.TrimRight(s.postServiceURL, "/") + "/internal/notes/batch?ids=" + url.QueryEscape(strings.Join(parts, ","))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create notes batch request: %w", err)
	}
	// The viewer-specific fields (viewer_has_*, viewer_can_reply, the poll's
	// viewer_vote) and the visibility verdict are this viewer's.
	req.Header.Set("X-User-Id", viewerID.String())
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	client := s.postFor(ctx)
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post-service notes batch request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read notes batch response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if len(body) > 2048 {
			body = body[:2048]
		}
		return nil, fmt.Errorf("post-service notes batch returned %d: %s", resp.StatusCode, string(body))
	}
	var envelope struct {
		Data *struct {
			Items []json.RawMessage `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal notes batch response: %w", err)
	}
	if envelope.Data == nil {
		return nil, fmt.Errorf("notes batch response has no data")
	}
	out := make([]noteRow, 0, len(envelope.Data.Items))
	for _, raw := range envelope.Data.Items {
		var n noteRow
		if err := json.Unmarshal(raw, &n.post); err != nil {
			return nil, fmt.Errorf("decode note: %w", err)
		}
		var v2 struct {
			Status      string          `json:"status"`
			ScheduledAt json.RawMessage `json:"scheduled_at"`
		}
		if err := json.Unmarshal(raw, &v2); err != nil {
			return nil, fmt.Errorf("decode note status: %w", err)
		}
		n.status, n.scheduledAt, n.raw = v2.Status, v2.ScheduledAt, raw
		out = append(out, n)
	}
	return out, nil
}
