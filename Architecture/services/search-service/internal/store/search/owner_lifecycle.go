package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// Account lifecycle beyond users and posts (audit 2026-11-07, S-M3).
//
// Every account has a Tube channel named after the person, with their
// username as its handle, and may own playlists, broadcast channels and
// communities. Erasure and deactivation used to touch only users_v1 and
// posts_v1, so a deleted person's name, handle, bio and avatar stayed
// findable through their channel. These are the owner-scoped writes the
// purge adapter runs alongside the user and post ones.

// tubeOwnedIndices carry is_hidden, set while the owner is deactivated or
// scheduled for deletion. The query-time must_not on is_hidden
// (blockfilter.go) then keeps them out of every search.
var tubeOwnedIndices = []string{IndexTubeChannels, IndexTubeCollections}

// ownerErasedIndices are cleared of the owner's documents on erasure.
var ownerErasedIndices = []string{IndexTubeChannels, IndexTubeCollections, IndexChannels, IndexCommunities}

// lifecycleQueryResult is the part of an _update_by_query /
// _delete_by_query answer that says whether every document was handled.
type lifecycleQueryResult struct {
	Total            int64 `json:"total"`
	Deleted          int64 `json:"deleted"`
	Updated          int64 `json:"updated"`
	VersionConflicts int64 `json:"version_conflicts"`
	Failures         []any `json:"failures"`
	TimedOut         bool  `json:"timed_out"`
}

func (r lifecycleQueryResult) incomplete() bool {
	return r.VersionConflicts > 0 || len(r.Failures) > 0 || r.TimedOut
}

// UpdateOwnerTubeHidden flips is_hidden on the owner's Tube channel and
// playlists. A conflict means a concurrent rewrite took the document
// between the snapshot and the update, so that document may still show
// the old flag: the call fails and the lifecycle event is redelivered.
func (s *Store) UpdateOwnerTubeHidden(ctx context.Context, ownerID string, hidden bool) error {
	if ownerID == "" {
		return fmt.Errorf("update owner tube hidden: empty owner id")
	}
	body, _ := json.Marshal(map[string]any{
		"query": map[string]any{"term": map[string]any{"owner_id": ownerID}},
		"script": map[string]any{
			"source": "ctx._source.is_hidden = params.v",
			"lang":   "painless",
			"params": map[string]any{"v": hidden},
		},
	})
	for _, index := range tubeOwnedIndices {
		// _update_by_query sees only refreshed documents (authorfence.go).
		if err := s.RefreshIndex(ctx, index); err != nil && !isIndexMissing(err) {
			return err
		}
		refresh := true
		req := opensearchapi.UpdateByQueryRequest{
			Index:     []string{index},
			Body:      bytes.NewReader(body),
			Conflicts: "proceed",
			Refresh:   &refresh,
		}
		out, missing, err := s.doLifecycleQuery(ctx, req)
		if err != nil {
			return fmt.Errorf("owner %s hidden=%v on %s: %w", ownerID, hidden, index, err)
		}
		if missing {
			continue
		}
		if out.incomplete() {
			return fmt.Errorf("owner %s hidden=%v on %s: %d conflicts, %d failures, timed out %v; will retry",
				ownerID, hidden, index, out.VersionConflicts, len(out.Failures), out.TimedOut)
		}
	}
	return nil
}

// DeleteOwnerEntityDocs removes every document the owner has in the Tube,
// broadcast channel and community indices. Idempotent: a second run finds
// nothing, which the purge contract needs (auth-service re-emits
// user.purge_requested until it sees the ack).
func (s *Store) DeleteOwnerEntityDocs(ctx context.Context, ownerID string) error {
	if ownerID == "" {
		return fmt.Errorf("delete owner docs: empty owner id")
	}
	body, _ := json.Marshal(map[string]any{
		"query": map[string]any{"term": map[string]any{"owner_id": ownerID}},
	})
	for _, index := range ownerErasedIndices {
		if err := s.RefreshIndex(ctx, index); err != nil && !isIndexMissing(err) {
			return err
		}
		refresh := true
		req := opensearchapi.DeleteByQueryRequest{
			Index:     []string{index},
			Body:      bytes.NewReader(body),
			Conflicts: "proceed",
			Refresh:   &refresh,
		}
		out, missing, err := s.doLifecycleQuery(ctx, req)
		if err != nil {
			return fmt.Errorf("delete owner %s docs on %s: %w", ownerID, index, err)
		}
		if missing {
			continue
		}
		if out.incomplete() {
			return fmt.Errorf("delete owner %s docs on %s: %d conflicts, %d failures, timed out %v; will retry",
				ownerID, index, out.VersionConflicts, len(out.Failures), out.TimedOut)
		}
	}
	return nil
}

// SweepTubeIndex deletes the documents of one Tube index that the reindex
// did not rewrite since `before` (or ever): rows post-service no longer
// lists, such as hard-deleted playlists. Only the two Tube indices may be
// swept; every other index has an event feed and must not be emptied by a
// listing walk.
func (s *Store) SweepTubeIndex(ctx context.Context, index string, before time.Time) (int64, error) {
	if index != IndexTubeChannels && index != IndexTubeCollections {
		return 0, fmt.Errorf("sweep: %s is not a Tube index", index)
	}
	if before.IsZero() {
		return 0, fmt.Errorf("sweep %s: zero cut-off", index)
	}
	if err := s.RefreshIndex(ctx, index); err != nil {
		return 0, err
	}
	body, _ := json.Marshal(map[string]any{
		"query": map[string]any{"bool": map[string]any{
			"must_not": []any{map[string]any{"range": map[string]any{
				"reindexed_at": map[string]any{"gte": before.UTC().Format(time.RFC3339Nano)},
			}}},
		}},
	})
	refresh := true
	req := opensearchapi.DeleteByQueryRequest{
		Index:     []string{index},
		Body:      bytes.NewReader(body),
		Conflicts: "proceed",
		Refresh:   &refresh,
	}
	out, _, err := s.doLifecycleQuery(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("sweep %s: %w", index, err)
	}
	return out.Deleted, nil
}

type lifecycleRequest interface {
	Do(ctx context.Context, transport opensearchapi.Transport) (*opensearchapi.Response, error)
}

// doLifecycleQuery runs one by-query request. missing reports a 404 (the
// index does not exist, so there is nothing to change).
func (s *Store) doLifecycleQuery(ctx context.Context, req lifecycleRequest) (lifecycleQueryResult, bool, error) {
	var out lifecycleQueryResult
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return out, false, err
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return out, true, nil
	}
	if res.IsError() {
		return out, false, fmt.Errorf("%s", res.String())
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return out, false, fmt.Errorf("decode by-query response: %w", err)
	}
	return out, false, nil
}

// isIndexMissing reports whether a refresh failed only because the index
// does not exist yet.
func isIndexMissing(err error) bool {
	return err != nil && bytes.Contains([]byte(err.Error()), []byte("index_not_found_exception"))
}

// putFieldMapping idempotently adds one field of the given type to an
// existing index (put-mapping is additive; it cannot change a type).
func (s *Store) putFieldMapping(ctx context.Context, index, field, typ string) {
	body, _ := json.Marshal(map[string]any{"properties": map[string]any{field: map[string]any{"type": typ}}})
	req := opensearchapi.IndicesPutMappingRequest{
		Index: []string{index},
		Body:  bytes.NewReader(body),
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		slog.Warn("opensearch: put mapping failed", "index", index, "field", field, "err", err)
		return
	}
	defer res.Body.Close()
	if res.IsError() {
		slog.Warn("opensearch: put mapping rejected", "index", index, "field", field, "status", res.StatusCode)
	}
}
