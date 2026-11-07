package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// Bulk request bodies and document rewrites (audit 2026-11-07, S-H1 / S-M1).
//
// A _bulk body is newline-delimited JSON: an action line naming the index
// and the document id, then a source line. Whoever controls one byte of an
// action line can end it and start another, and OpenSearch runs every line
// with the service's master credentials. users_v1 used to build its action
// line with Sprintf around a raw user_id, so one crafted id could delete
// erasure fences or plant "approved" posts in any index.
//
// Two rules, enforced here for every bulk writer in the service:
//
//  1. An action line is json.Marshal of bulkAction. Never string
//     formatting: the encoder escapes quotes and control characters, so no
//     id can close the object or add a line.
//  2. Every id is a canonical UUID before it reaches a body. Every
//     bulk-written index (users, products, Tube channels, playlists) is
//     keyed by a Postgres UUID, so anything else is a bug or an attack.
//
// The second half of this file is the one full-document rewrite for the
// indices that carry account-lifecycle suppression (users_v1 and the Tube
// indices): see keepHiddenUpsertScript.

// ErrInvalidBulkID is returned when a bulk document id is not a canonical
// UUID. Nothing is sent for the batch that contained it.
var ErrInvalidBulkID = errors.New("search: bulk document id is not a canonical UUID")

// bulkTarget is the body of one action line.
type bulkTarget struct {
	Index           string `json:"_index"`
	ID              string `json:"_id"`
	RetryOnConflict int    `json:"retry_on_conflict,omitempty"`
}

// bulkAction is one action line. Exactly one field is set.
type bulkAction struct {
	Index  *bulkTarget `json:"index,omitempty"`
	Update *bulkTarget `json:"update,omitempty"`
}

// bulkRetries bounds OpenSearch's internal re-run of a scripted upsert
// when a concurrent writer holds the document.
const bulkRetries = 3

// validateBulkID refuses anything but the canonical lowercase form of a
// UUID. uuid.Parse alone also accepts braces and the urn: prefix, so the
// round trip is what makes the check strict.
func validateBulkID(id string) error {
	u, err := uuid.Parse(id)
	if err != nil || u.String() != id {
		if len(id) > 64 {
			id = id[:64] + "…"
		}
		return fmt.Errorf("%w: %q", ErrInvalidBulkID, id)
	}
	return nil
}

// encodeBulkAction is the only producer of action lines.
func encodeBulkAction(a bulkAction) ([]byte, error) {
	return json.Marshal(a)
}

// bulkBodyBuilder turns one chunk of ids and documents into a _bulk body.
type bulkBodyBuilder func(index string, ids []string, docs []any) ([]byte, error)

// buildBulkIndexBody writes plain `index` actions: a full replace. Only for
// indices with no lifecycle suppression to preserve (products).
func buildBulkIndexBody(index string, ids []string, docs []any) ([]byte, error) {
	return buildBulkBody(ids, docs, func(id string, doc any) (bulkAction, any, error) {
		return bulkAction{Index: &bulkTarget{Index: index, ID: id}}, doc, nil
	})
}

// buildBulkKeepHiddenBody writes scripted upserts that keep a stored
// is_hidden=true (keepHiddenUpsertScript).
func buildBulkKeepHiddenBody(index string, ids []string, docs []any) ([]byte, error) {
	return buildBulkBody(ids, docs, func(id string, doc any) (bulkAction, any, error) {
		body, err := keepHiddenUpsertBody(doc)
		if err != nil {
			return bulkAction{}, nil, err
		}
		return bulkAction{Update: &bulkTarget{Index: index, ID: id, RetryOnConflict: bulkRetries}}, body, nil
	})
}

func buildBulkBody(ids []string, docs []any, line func(id string, doc any) (bulkAction, any, error)) ([]byte, error) {
	if len(ids) != len(docs) {
		return nil, fmt.Errorf("search: bulk body: %d ids for %d documents", len(ids), len(docs))
	}
	var buf bytes.Buffer
	for i, doc := range docs {
		if err := validateBulkID(ids[i]); err != nil {
			return nil, fmt.Errorf("bulk position %d: %w", i, err)
		}
		action, source, err := line(ids[i], doc)
		if err != nil {
			return nil, err
		}
		meta, err := encodeBulkAction(action)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(source)
		if err != nil {
			return nil, err
		}
		buf.Write(meta)
		buf.WriteByte('\n')
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// bulkWrite chunks at bulkChunkSize, builds each chunk with build, sends it
// and counts the items OpenSearch accepted. A chunk failure stops the run;
// the count covers what landed before it.
func (s *Store) bulkWrite(ctx context.Context, index string, ids []string, docs []any, build bulkBodyBuilder) (int, error) {
	total := 0
	for start := 0; start < len(docs); start += bulkChunkSize {
		end := start + bulkChunkSize
		if end > len(docs) {
			end = len(docs)
		}
		body, err := build(index, ids[start:end], docs[start:end])
		if err != nil {
			return total, err
		}
		n, err := s.sendBulk(ctx, index, body, end-start)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// sendBulk sends one body and counts the per-item successes. OpenSearch
// answers 200 even when some items fail, so the count is taken item by
// item rather than from the top-level `errors` flag alone.
func (s *Store) sendBulk(ctx context.Context, index string, body []byte, n int) (int, error) {
	req := opensearchapi.BulkRequest{Body: bytes.NewReader(body)}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.IsError() {
		return 0, fmt.Errorf("bulk %s error: %s", index, res.String())
	}
	var parsed struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int    `json:"status"`
			Error  any    `json:"error,omitempty"`
			ID     string `json:"_id"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		slog.Warn("search: bulk response unparseable, assuming success", "index", index, "error", err, "count", n)
		return n, nil
	}
	if !parsed.Errors {
		return n, nil
	}
	ok := 0
	for _, item := range parsed.Items {
		for op, st := range item {
			if st.Status >= 200 && st.Status < 300 {
				ok++
			} else {
				slog.Warn("search: bulk doc failed", "index", index, "op", op, "id", st.ID, "status", st.Status, "error", st.Error)
			}
		}
	}
	return ok, nil
}

// keepHiddenUpsertScript is the full-document rewrite for users_v1 and the
// Tube indices (S-M1 / S-M3).
//
// is_hidden is set by exactly one authority, the account-lifecycle events
// (UpdateUserHidden, UpdateOwnerTubeHidden). Every other writer (profile
// events, the users reindex and auto-heal, the backfill, the Tube reindex)
// rewrites the whole document and cannot know the lifecycle state. A plain
// index replace therefore reset is_hidden to false and brought deactivated
// and deletion-scheduled accounts back into search. Here a stored true
// survives the rewrite; only the lifecycle path clears it.
const keepHiddenUpsertScript = `
boolean hidden = ctx._source.is_hidden == true;
ctx._source.clear();
ctx._source.putAll(params.doc);
if (hidden) {
  ctx._source.is_hidden = true;
}
`

// keepHiddenUpsertBody is the update body for one keep-hidden rewrite.
func keepHiddenUpsertBody(doc any) (map[string]any, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var docMap map[string]any
	if err := json.Unmarshal(raw, &docMap); err != nil {
		return nil, err
	}
	return map[string]any{
		"scripted_upsert": true,
		"upsert":          map[string]any{},
		"script": map[string]any{
			"lang":   "painless",
			"source": keepHiddenUpsertScript,
			"params": map[string]any{"doc": docMap},
		},
	}, nil
}

// upsertKeepingHidden is the single-document form of the keep-hidden
// rewrite.
func (s *Store) upsertKeepingHidden(ctx context.Context, index, id string, doc any) error {
	if id == "" {
		return fmt.Errorf("upsert %s: empty document id", index)
	}
	body, err := keepHiddenUpsertBody(doc)
	if err != nil {
		return err
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	retries := bulkRetries
	req := opensearchapi.UpdateRequest{
		Index:           index,
		DocumentID:      id,
		Body:            bytes.NewReader(data),
		RetryOnConflict: &retries,
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("upsert %s error: %s", index, res.String())
	}
	return nil
}

// toAnySlice adapts a typed document slice for bulkWrite.
func toAnySlice[T any](docs []T, id func(T) string) ([]string, []any) {
	ids := make([]string, len(docs))
	out := make([]any, len(docs))
	for i, d := range docs {
		ids[i], out[i] = id(d), d
	}
	return ids, out
}
