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
// CREATE BY ID (2026-11-16). post-service now also sends channel_id, the
// document _id. With it (and the owner) the event is one scripted _update
// on that id with an upsert: a channel already in search gets the ordered
// field merge below, and a channel search has never seen is CREATED with
// the document the Tube reindex would write for it (tubeChannelUpsertDoc),
// so a brand-new channel is findable at once instead of at the next
// reindex. Events from older producers (no channel_id) keep the owner_id
// _update_by_query, update-only as before.
//
// HIDDEN ON CREATE. The reindex never writes is_hidden; the lifecycle path
// (UpdateOwnerTubeHidden) sets it on documents that exist. A document the
// event creates did not exist when that path ran, so the event decides it:
// an erased owner (author fence) gets no document at all, and the new
// document is hidden exactly when the owner's users_v1 document is
// (UpdateUserHidden writes a hidden stub even for an owner with no profile
// yet). Both are read again after the create and the document corrected,
// because the lifecycle handler writes users_v1 and the fence BEFORE it
// touches the Tube indices: a lifecycle change that raced the create is
// seen by the second read. An unreadable fence or owner state fails the
// event (redelivered), never creates a visible guess.
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
// the partial update leaves both as stored. A created document starts at
// follower_count 0 (a channel that did not exist has no subscribers yet)
// and is stamped reindexed_at so a reindex walk already under way does not
// sweep it; the next walk rewrites it with the listed row as usual.

// TubeChannelUpdate is one tube.channel.created / tube.channel.updated: the
// row as committed. An empty AvatarMediaID means the channel has none.
type TubeChannelUpdate struct {
	// ChannelID is the channel row id (the document _id), carried since
	// 2026-11-16. When set the event targets that document by id; an older
	// producer's event has none and falls back to owner_id.
	ChannelID     string
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

// tubeChannelEventFields are the columns the event owns: what it merges
// into a stored document.
func tubeChannelEventFields(u TubeChannelUpdate) map[string]any {
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
	return fields
}

// tubeChannelEventScriptBody is the ordered merge, shared by the
// _update_by_query (owner path) and the _update (id path).
func tubeChannelEventScriptBody(u TubeChannelUpdate) map[string]any {
	return map[string]any{
		"lang":   "painless",
		"source": tubeChannelEventScript,
		"params": map[string]any{
			"version": SourceVersion(u.UpdatedAt),
			"apply":   tubeEventApplyTable(),
			"fields":  tubeChannelEventFields(u),
		},
	}
}

// tubeChannelEventBody is the _update_by_query body. Pure, for the tests.
func tubeChannelEventBody(u TubeChannelUpdate) map[string]any {
	query := map[string]any{"term": map[string]any{"owner_id": u.OwnerID}}
	if u.ChannelID != "" {
		query = map[string]any{"ids": map[string]any{"values": []string{u.ChannelID}}}
	}
	return map[string]any{
		"query":  query,
		"script": tubeChannelEventScriptBody(u),
	}
}

// tubeChannelUpsertDoc is the document the event creates when search has
// none: field for field what the Tube reindex writes (reindex.TubeChannelDocOf
// stamped with reindexed_at), with the two things the event cannot know set
// as the reindex would for a channel that did not exist yet (follower_count
// 0) and as the lifecycle path would (is_hidden from the owner). Dates the
// event lacks are left out for the reindex to fill, never written as the
// zero time. Pure.
func tubeChannelUpsertDoc(u TubeChannelUpdate, hidden bool, now time.Time) (map[string]any, error) {
	stamp := now.UTC()
	doc := TubeChannelDoc{
		ChannelID:       u.ChannelID,
		OwnerID:         u.OwnerID,
		Name:            u.Name,
		Handle:          u.Handle,
		About:           u.About,
		AvatarMediaID:   u.AvatarMediaID,
		FollowerCount:   0,
		CreatedAt:       u.CreatedAt,
		UpdatedAt:       u.UpdatedAt,
		SourceUpdatedUs: SourceVersion(u.UpdatedAt),
		ReindexedAt:     &stamp,
	}
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = u.UpdatedAt
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if doc.CreatedAt.IsZero() {
		delete(out, "created_at")
	}
	if doc.UpdatedAt.IsZero() {
		delete(out, "updated_at")
	}
	out["is_hidden"] = hidden
	return out, nil
}

// tubeChannelUpsertBody is the _update body of the id path: the ordered
// merge when the document exists, the whole document when it does not (a
// plain upsert: the script does not run on a create). Pure.
func tubeChannelUpsertBody(u TubeChannelUpdate, hidden bool, now time.Time) (map[string]any, error) {
	doc, err := tubeChannelUpsertDoc(u, hidden, now)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"script": tubeChannelEventScriptBody(u),
		"upsert": doc,
	}, nil
}

// ApplyTubeChannelEvent writes a Tube channel event in version order. With
// channel_id and owner it upserts that document (applyTubeChannelByID);
// otherwise it updates the owner's (or the id's) document if search has
// one. A conflict or a failure is an error, so the event is redelivered.
func (s *Store) ApplyTubeChannelEvent(ctx context.Context, u TubeChannelUpdate) error {
	if u.OwnerID == "" && u.ChannelID == "" {
		return nil
	}
	if u.ChannelID != "" && u.OwnerID != "" {
		if err := validateBulkID(u.ChannelID); err == nil {
			return s.applyTubeChannelByID(ctx, u)
		}
		slog.Warn("search: tube channel event with a malformed channel_id; updating by owner", "owner_id", u.OwnerID)
		u.ChannelID = ""
	}
	return s.updateTubeChannelByQuery(ctx, u)
}

// applyTubeChannelByID is the create-or-update path (see CREATE BY ID).
func (s *Store) applyTubeChannelByID(ctx context.Context, u TubeChannelUpdate) error {
	// The index is created at boot; an upsert into a missing one would
	// auto-create it with dynamic mappings, so a missing index is left to
	// boot and the reindex, as on the owner path.
	exists, err := s.tubeChannelsIndexExists(ctx)
	if err != nil {
		return fmt.Errorf("tube channel event %s: %w", u.ChannelID, err)
	}
	if !exists {
		return nil
	}
	fenced, err := s.IsAuthorFenced(ctx, u.OwnerID)
	if err != nil {
		return fmt.Errorf("tube channel event %s: %w", u.ChannelID, err)
	}
	if fenced {
		slog.Debug("search: tube channel event for an erased owner; not indexed", "channel_id", u.ChannelID)
		return nil
	}
	hidden, err := s.ownerHiddenInUsers(ctx, u.OwnerID)
	if err != nil {
		return fmt.Errorf("tube channel event %s: %w", u.ChannelID, err)
	}
	body, err := tubeChannelUpsertBody(u, hidden, time.Now())
	if err != nil {
		return fmt.Errorf("tube channel event %s: %w", u.ChannelID, err)
	}
	result, err := s.sendTubeChannelUpdate(ctx, u.ChannelID, body)
	if err != nil {
		return fmt.Errorf("tube channel event %s: %w", u.ChannelID, err)
	}
	if result != "created" {
		return nil
	}
	return s.settleCreatedTubeChannel(ctx, u, hidden)
}

// settleCreatedTubeChannel re-reads the owner's state after a create and
// corrects the new document if a lifecycle change landed in between.
func (s *Store) settleCreatedTubeChannel(ctx context.Context, u TubeChannelUpdate, wrote bool) error {
	fenced, err := s.IsAuthorFenced(ctx, u.OwnerID)
	if err != nil {
		return fmt.Errorf("tube channel event %s: recheck: %w", u.ChannelID, err)
	}
	if fenced {
		return s.DeleteTubeChannel(ctx, u.ChannelID)
	}
	hidden, err := s.ownerHiddenInUsers(ctx, u.OwnerID)
	if err != nil {
		return fmt.Errorf("tube channel event %s: recheck: %w", u.ChannelID, err)
	}
	if hidden == wrote {
		return nil
	}
	_, err = s.sendTubeChannelUpdate(ctx, u.ChannelID, map[string]any{"doc": map[string]any{"is_hidden": hidden}})
	if err != nil && !errors.Is(err, errTubeChannelMissing) {
		return fmt.Errorf("tube channel event %s: recheck: %w", u.ChannelID, err)
	}
	return nil
}

// errTubeChannelMissing is a 404 on a partial update: the document is gone.
var errTubeChannelMissing = errors.New("tube channel document missing")

// sendTubeChannelUpdate sends one _update on tube_channels_v1 and returns
// its result ("created", "updated", "noop").
func (s *Store) sendTubeChannelUpdate(ctx context.Context, id string, body map[string]any) (string, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	retries := bulkRetries
	req := opensearchapi.UpdateRequest{
		Index:           IndexTubeChannels,
		DocumentID:      id,
		Body:            bytes.NewReader(data),
		RetryOnConflict: &retries,
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return "", errTubeChannelMissing
	}
	if res.IsError() {
		return "", fmt.Errorf("update %s/%s: %s", IndexTubeChannels, id, res.String())
	}
	var out struct {
		Result string `json:"result"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode update %s/%s: %w", IndexTubeChannels, id, err)
	}
	return out.Result, nil
}

// tubeChannelsIndexExists is a HEAD on tube_channels_v1.
func (s *Store) tubeChannelsIndexExists(ctx context.Context) (bool, error) {
	res, err := opensearchapi.IndicesExistsRequest{Index: []string{IndexTubeChannels}}.Do(ctx, s.client)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == 404:
		return false, nil
	case res.IsError():
		return false, fmt.Errorf("check %s: %s", IndexTubeChannels, res.String())
	}
	return true, nil
}

// ownerHiddenInUsers reads is_hidden from the owner's users_v1 document,
// which the lifecycle path keeps (UpdateUserHidden: a hidden owner always
// has one, a stub if need be). Only a confirmed document miss is "not
// hidden" (an active account with no profile indexed yet); a missing
// index, an error body or an unreadable answer is an error, as for the
// author fence: the state is unknown and the event is redelivered.
func (s *Store) ownerHiddenInUsers(ctx context.Context, ownerID string) (bool, error) {
	req := opensearchapi.GetRequest{
		Index:          IndexUsers,
		DocumentID:     ownerID,
		SourceIncludes: []string{"is_hidden"},
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return false, fmt.Errorf("read owner %s hidden state: %w", ownerID, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return false, fmt.Errorf("read owner %s hidden state: %w", ownerID, err)
	}
	return ownerHiddenFromGet(res.StatusCode, raw)
}

// ownerHiddenFromGet decides ownerHiddenInUsers from the GET answer. Pure.
func ownerHiddenFromGet(status int, raw []byte) (bool, error) {
	var parsed struct {
		Found  *bool          `json:"found"`
		Error  map[string]any `json:"error"`
		Source struct {
			IsHidden bool `json:"is_hidden"`
		} `json:"_source"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return false, fmt.Errorf("owner hidden state: unreadable answer (status %d)", status)
	}
	if parsed.Error != nil {
		return false, fmt.Errorf("owner hidden state: error answer (status %d)", status)
	}
	if parsed.Found == nil {
		return false, fmt.Errorf("owner hidden state: answer without found (status %d)", status)
	}
	if !*parsed.Found {
		if status == 404 {
			return false, nil
		}
		return false, fmt.Errorf("owner hidden state: found:false with status %d", status)
	}
	if status != 200 {
		return false, fmt.Errorf("owner hidden state: status %d", status)
	}
	return parsed.Source.IsHidden, nil
}

// updateTubeChannelByQuery is the update-only path: an event with no
// channel_id (older producers) or no owner. A missing document is not an
// error (the reindex adds it).
func (s *Store) updateTubeChannelByQuery(ctx context.Context, u TubeChannelUpdate) error {
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
