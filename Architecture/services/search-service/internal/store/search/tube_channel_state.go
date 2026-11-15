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

// Tube channel events (2026-11-15).
//
// post-service announces every create and edit of a Tube channel
// (tube.channel.created / tube.channel.updated, its outbox) with the row as
// committed: name, handle, about, avatar, updated_at. Search used to ignore
// both, so a renamed channel or a changed handle stayed stale in
// tube_channels_v1 until the next Tube reindex.
//
// WHY BY OWNER, NOT BY ID. tube_channels_v1 is keyed by the channel row id
// (the reindex writes it), but the event carries only user_id: one channel
// per account, keyed by the owner. So the event is applied with an
// _update_by_query on owner_id. It never creates a document — there is no
// channel id to create it under — so a brand-new channel still enters
// search with the next Tube reindex, exactly as before. What the event
// fixes is the edit: a channel already in search follows its row.
//
// ORDER. The outbox and the DLQ replayer can deliver an older edit after a
// newer one, and the reindex can write a listing read before an edit after
// it. Every writer therefore carries the row's version (source_updated_us,
// the row's updated_at in microseconds, as for communities and channels in
// entity_state.go):
//
//   - the event applies only when it is not older than the stored version
//     (tubeEventApplies); an unversioned event never overwrites a
//     versioned document;
//   - the reindex rewrite (keepHiddenUpsertScript) keeps a stored document
//     that is newer than the listing row, and only stamps reindexed_at so
//     the sweep does not take it for a row post-service stopped listing.
//
// is_hidden (owner deactivated) and follower_count are not in the event;
// the partial update leaves both as stored.

// TubeChannelUpdate is one tube.channel.created / tube.channel.updated: the
// row as committed. An empty AvatarMediaID means the channel has none.
type TubeChannelUpdate struct {
	OwnerID       string
	Name          string
	Handle        string
	About         string
	AvatarMediaID string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// tubeEventApplies is the whole ordering rule for a Tube channel event over
// a stored document: newer, the same version (a redelivery rewrites the
// same fields) or nothing to compare (both unversioned, or a document
// written before versions) applies; older, or an unversioned event over a
// versioned document, does not.
func tubeEventApplies(order versionOrder) bool {
	switch order {
	case orderNewer, orderSame, orderBothUnversioned, orderStoredUnversioned:
		return true
	}
	return false
}

// tubeEventApplyTable is params.apply: the answer for every version order.
func tubeEventApplyTable() map[string]bool {
	out := make(map[string]bool, len(allVersionOrders))
	for _, o := range allVersionOrders {
		out[string(o)] = tubeEventApplies(o)
	}
	return out
}

// tubeChannelEventScript classifies the versions exactly as
// entityStateScript does and applies the fields only when Go said so.
const tubeChannelEventScript = `
long stored = 0;
if (ctx._source.source_updated_us != null) {
  stored = ((Number) ctx._source.source_updated_us).longValue();
}
long incoming = params.version;
String order;
if (stored <= 0 && incoming <= 0) {
  order = 'both_unversioned';
} else if (stored <= 0) {
  order = 'stored_unversioned';
} else if (incoming <= 0) {
  order = 'incoming_unversioned';
} else if (incoming > stored) {
  order = 'newer';
} else if (incoming == stored) {
  order = 'same';
} else {
  order = 'older';
}
if (params.apply.get(order) == true) {
  ctx._source.putAll(params.fields);
  if (incoming > stored) {
    ctx._source.source_updated_us = incoming;
  }
} else {
  ctx.op = 'noop';
}
`

// tubeChannelEventBody is the _update_by_query body. Pure, for the tests.
func tubeChannelEventBody(u TubeChannelUpdate) map[string]any {
	fields := map[string]any{
		"name":            u.Name,
		"handle":          u.Handle,
		"about":           u.About,
		"avatar_media_id": u.AvatarMediaID,
	}
	if !u.UpdatedAt.IsZero() {
		fields["updated_at"] = u.UpdatedAt
	}
	if !u.CreatedAt.IsZero() {
		fields["created_at"] = u.CreatedAt
	}
	return map[string]any{
		"query": map[string]any{"term": map[string]any{"owner_id": u.OwnerID}},
		"script": map[string]any{
			"lang":   "painless",
			"source": tubeChannelEventScript,
			"params": map[string]any{
				"version": SourceVersion(u.UpdatedAt),
				"apply":   tubeEventApplyTable(),
				"fields":  fields,
			},
		},
	}
}

// ApplyTubeChannelEvent writes a Tube channel event onto the owner's
// channel document, in version order. A missing document is not an error
// (the reindex adds it); a conflict or a failure is, so the event is
// redelivered.
func (s *Store) ApplyTubeChannelEvent(ctx context.Context, u TubeChannelUpdate) error {
	if u.OwnerID == "" {
		return nil
	}
	data, err := json.Marshal(tubeChannelEventBody(u))
	if err != nil {
		return fmt.Errorf("tube channel event for owner %s: %w", u.OwnerID, err)
	}
	// _update_by_query sees only refreshed documents (authorfence.go).
	if err := s.RefreshIndex(ctx, IndexTubeChannels); err != nil {
		if isIndexMissing(err) {
			return nil
		}
		return err
	}
	refresh := true
	req := opensearchapi.UpdateByQueryRequest{
		Index:     []string{IndexTubeChannels},
		Body:      bytes.NewReader(data),
		Conflicts: "proceed",
		Refresh:   &refresh,
	}
	out, missing, err := s.doLifecycleQuery(ctx, req)
	if err != nil {
		return fmt.Errorf("tube channel event for owner %s: %w", u.OwnerID, err)
	}
	if missing {
		return nil
	}
	if out.incomplete() {
		return fmt.Errorf("tube channel event for owner %s: %d conflicts, %d failures, timed out %v; will retry",
			u.OwnerID, out.VersionConflicts, len(out.Failures), out.TimedOut)
	}
	if out.Total == 0 {
		slog.Debug("search: tube channel event for a channel not yet indexed; the Tube reindex adds it", "owner_id", u.OwnerID)
	}
	return nil
}
