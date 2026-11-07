package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// Ordering the lifecycle snapshots of communities and broadcast channels
// (2026-11-08).
//
// community-service and channel-service announce every change as the whole
// row (a snapshot), and search-service applies each one as "write it" or
// "take it out". The producers key by entity id, but each publish runs in
// its own goroutine and the DLQ replays old messages, so snapshots do arrive
// out of order. Applied blindly, a stale "active" snapshot landing after a
// newer "suspended" one re-lists a suspended, archived or private entity:
// its name, handle, description and owner come back to every caller.
//
// So every write carries the snapshot's version (the row's updated_at, in
// microseconds: source_updated_us), and OpenSearch decides inside one
// scripted update, under the document lock, whether it wins. The rules are
// a Go function (decideEntityWrite); the Painless script only classifies
// the stored document and executes the action Go chose for that class
// (params.actions), so the rules are unit-tested and mutation-tested here.
//
// WHY A TOMBSTONE, NOT A DELETE
//
// A removal has to be remembered. If a suspension deleted the document, a
// stale "active" snapshot arriving afterwards would find nothing to compare
// against and create the document again — exactly the bug. The removal
// therefore leaves a tombstone: the id, removed=true, status "removed" and
// the version. It carries no name, handle, description, owner or counts, so
// it discloses nothing even if some future query forgot its filter. Every
// query over the two indices requires status=active and a public type
// (publicCommunityFilterAny, publicChannelFilterAny + activeStatusFilter),
// and a tombstone has neither, so no query returns it. It carries no
// owner_id either, so owner erasure (DeleteOwnerEntityDocs) leaves it in
// place and the ordering survives the erasure.
//
// A delete (community.deleted / channel.deleted, or a snapshot with status
// deleted) is terminal: neither service can restore a deleted row, so the
// tombstone is marked deleted=true and nothing writes over it again. The
// delete events carry no row version (their deleted_at is the producer's
// clock, not the database's), which is why terminal is the only rule that
// is safe for them.
//
// RULES (decideEntityWrite)
//
//	stored kind   live doc | tombstone | deleted tombstone | absent
//	version order newer | same | older than the stored version, or one
//	              side unversioned (events from before 2026-11-08 and
//	              documents written before this change carry none)
//
//	upsert  absent → write; deleted → never; live or tombstone → write when
//	        newer or when the stored doc is unversioned; same → write over a
//	        live doc (redelivery), not over a tombstone (removal wins ties);
//	        an unversioned upsert never overwrites a versioned doc.
//	remove  live → tombstone unless older (an unversioned removal still
//	        wins: fail closed); tombstone → raise its version when newer;
//	        absent → tombstone when versioned, so a later stale upsert finds
//	        it; deleted → nothing.
//	delete  everything but deleted → deleted tombstone.
//	patch   (community.updated before the snapshot: a few fields) → only a
//	        live, unversioned doc; it never creates one.

// StatusDeleted is the status of a deleted community or channel. A snapshot
// with it is applied as a terminal delete.
const StatusDeleted = "deleted"

// StatusRemoved is the status written on a tombstone. No producer uses it,
// and it is not active, so no query matches a tombstone.
const StatusRemoved = "removed"

// SourceVersion is the version a snapshot carries: its updated_at in
// microseconds (Postgres precision, so two committed rows never round to
// one value). Zero means unversioned.
func SourceVersion(updatedAt time.Time) int64 {
	if updatedAt.IsZero() {
		return 0
	}
	if v := updatedAt.UnixMicro(); v > 0 {
		return v
	}
	return 0
}

type entityOp string

const (
	entityUpsert entityOp = "upsert" // searchable snapshot: write the document
	entityRemove entityOp = "remove" // not searchable: tombstone
	entityDelete entityOp = "delete" // deleted: terminal tombstone
	entityPatch  entityOp = "patch"  // legacy partial update of a live doc
)

type storedKind string

const (
	kindAbsent    storedKind = "absent"
	kindLive      storedKind = "live"
	kindTombstone storedKind = "tombstone"
	kindDeleted   storedKind = "deleted"
)

var allStoredKinds = []storedKind{kindAbsent, kindLive, kindTombstone, kindDeleted}

type versionOrder string

const (
	orderBothUnversioned     versionOrder = "both_unversioned"
	orderStoredUnversioned   versionOrder = "stored_unversioned"
	orderIncomingUnversioned versionOrder = "incoming_unversioned"
	orderNewer               versionOrder = "newer"
	orderSame                versionOrder = "same"
	orderOlder               versionOrder = "older"
)

var allVersionOrders = []versionOrder{
	orderBothUnversioned, orderStoredUnversioned, orderIncomingUnversioned,
	orderNewer, orderSame, orderOlder,
}

type entityAction string

const (
	actNone      entityAction = "none"
	actDoc       entityAction = "doc"
	actPatch     entityAction = "patch"
	actTombstone entityAction = "tombstone"
	actDeleted   entityAction = "deleted"
)

// classifyVersions is the Go statement of how the script orders the stored
// version against the incoming one (entityStateScript, the `order` block).
// A missing document counts as stored 0.
func classifyVersions(stored, incoming int64) versionOrder {
	switch {
	case stored <= 0 && incoming <= 0:
		return orderBothUnversioned
	case stored <= 0:
		return orderStoredUnversioned
	case incoming <= 0:
		return orderIncomingUnversioned
	case incoming > stored:
		return orderNewer
	case incoming == stored:
		return orderSame
	default:
		return orderOlder
	}
}

// decideEntityWrite is the whole ordering rule (see RULES above). It is
// total: every op, kind and order has an answer.
func decideEntityWrite(op entityOp, kind storedKind, order versionOrder) entityAction {
	if kind == kindDeleted {
		return actNone // terminal: nothing writes over a deleted tombstone
	}
	switch op {
	case entityDelete:
		return actDeleted
	case entityUpsert:
		if kind == kindAbsent {
			return actDoc
		}
		switch order {
		case orderBothUnversioned, orderStoredUnversioned, orderNewer:
			return actDoc
		case orderSame:
			if kind == kindLive {
				return actDoc // redelivery of the snapshot already applied
			}
			return actNone // removal wins ties
		default: // older, or unversioned over a versioned doc
			return actNone
		}
	case entityRemove:
		switch kind {
		case kindAbsent:
			if order == orderBothUnversioned || order == orderIncomingUnversioned {
				return actNone // nothing to remove, and no version to remember
			}
			return actTombstone
		case kindLive:
			if order == orderOlder {
				return actNone // a stale removal never takes down a newer doc
			}
			return actTombstone
		default: // tombstone: only a newer removal raises its version
			if order == orderNewer || order == orderStoredUnversioned {
				return actTombstone
			}
			return actNone
		}
	case entityPatch:
		if kind == kindLive && order == orderBothUnversioned {
			return actPatch
		}
		return actNone
	}
	return actNone
}

// fallbackAction is what the script does for a class missing from the
// table. The table is total (TestEntityActionTable_IsTotal), so this is a
// backstop: removals still remove, writes do nothing.
func fallbackAction(op entityOp) entityAction {
	switch op {
	case entityRemove:
		return actTombstone
	case entityDelete:
		return actDeleted
	}
	return actNone
}

func actionKey(kind storedKind, order versionOrder) string {
	return string(kind) + "/" + string(order)
}

// entityActionTable is params.actions: the action for every stored class.
func entityActionTable(op entityOp) map[string]string {
	out := make(map[string]string, len(allStoredKinds)*len(allVersionOrders))
	for _, k := range allStoredKinds {
		for _, o := range allVersionOrders {
			out[actionKey(k, o)] = string(decideEntityWrite(op, k, o))
		}
	}
	return out
}

// entityStateScript classifies the stored document, looks up the action Go
// chose for that class, and executes it. Painless `==` on strings is
// equals(). A tombstone's version is max(stored, incoming): a raise never
// lowers it, and an unversioned removal keeps the stored one.
const entityStateScript = `
long stored = 0;
String kind = 'absent';
if (ctx.op != 'create') {
  kind = 'live';
  if (ctx._source.deleted == true) {
    kind = 'deleted';
  } else if (ctx._source.removed == true) {
    kind = 'tombstone';
  }
  if (ctx._source.source_updated_us != null) {
    stored = ((Number) ctx._source.source_updated_us).longValue();
  }
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
String action = params.actions.get(kind + '/' + order);
if (action == null) {
  action = params.fallback;
}
long version = (long) Math.max(stored, incoming);
if (action == 'doc') {
  if (kind == 'live') {
    ctx._source.putAll(params.fields);
    if (ctx._source.engagement_score == null) {
      ctx._source.engagement_score = params.engagement_score;
    }
  } else {
    ctx._source.clear();
    ctx._source.putAll(params.doc);
  }
  if (version > 0) {
    ctx._source.source_updated_us = version;
  }
} else if (action == 'patch') {
  ctx._source.putAll(params.fields);
} else if (action == 'tombstone' || action == 'deleted') {
  ctx._source.clear();
  ctx._source.putAll(params.tombstone);
  if (action == 'deleted') {
    ctx._source.deleted = true;
  }
  if (version > 0) {
    ctx._source.source_updated_us = version;
  }
} else {
  ctx.op = 'none';
}
`

// entityTarget is one of the two snapshot-ordered indices.
type entityTarget struct {
	index   string
	idField string
}

var (
	communityTarget = entityTarget{index: IndexCommunities, idField: "community_id"}
	channelTarget   = entityTarget{index: IndexChannels, idField: "channel_id"}
)

// entityWrite is one ordered write.
type entityWrite struct {
	op      entityOp
	id      string
	version int64
	// fields are merged into a live document (upsert, patch); they never
	// include engagement_score, which the member / subscriber events keep.
	fields map[string]any
	// doc replaces a tombstone or creates the document (upsert only).
	doc        any
	engagement float64
}

// entityStateBody builds the _update body. Pure, for the tests.
func entityStateBody(t entityTarget, w entityWrite) (map[string]any, error) {
	docMap := map[string]any{}
	if w.op == entityUpsert && w.doc != nil {
		raw, err := json.Marshal(w.doc)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &docMap); err != nil {
			return nil, err
		}
	}
	fields := w.fields
	if fields == nil {
		fields = map[string]any{}
	}
	version := w.version
	if version < 0 {
		version = 0
	}
	body := map[string]any{
		"script": map[string]any{
			"lang":   "painless",
			"source": entityStateScript,
			"params": map[string]any{
				"op":               string(w.op),
				"version":          version,
				"actions":          entityActionTable(w.op),
				"fallback":         string(fallbackAction(w.op)),
				"fields":           fields,
				"doc":              docMap,
				"engagement_score": w.engagement,
				"tombstone": map[string]any{
					t.idField: w.id,
					"removed": true,
					"status":  StatusRemoved,
				},
			},
		},
	}
	// A patch never creates a document: without an upsert a missing one is
	// a 404, which writeEntity accepts. Everything else may need to create
	// (a document or a tombstone), so the script runs on a missing one too.
	if w.op != entityPatch {
		body["scripted_upsert"] = true
		body["upsert"] = map[string]any{}
	}
	return body, nil
}

// writeEntity sends one ordered write.
func (s *Store) writeEntity(ctx context.Context, t entityTarget, w entityWrite) error {
	if w.id == "" {
		return nil
	}
	body, err := entityStateBody(t, w)
	if err != nil {
		return fmt.Errorf("%s %s %s: %w", w.op, t.index, w.id, err)
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("%s %s %s: %w", w.op, t.index, w.id, err)
	}
	retries := projectionRetries
	req := opensearchapi.UpdateRequest{
		Index:           t.index,
		DocumentID:      w.id,
		Body:            bytes.NewReader(data),
		RetryOnConflict: &retries,
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return fmt.Errorf("%s %s %s: %w", w.op, t.index, w.id, err)
	}
	defer res.Body.Close()
	if res.IsError() && !(w.op == entityPatch && res.StatusCode == 404) {
		return fmt.Errorf("%s %s %s: %s", w.op, t.index, w.id, res.String())
	}
	return nil
}

// EngagementStubIndices are the indices AddToEngagementScore writes, with
// the id field every real document (and every tombstone) carries.
var EngagementStubIndices = map[string]string{
	IndexPosts:       "post_id",
	IndexUsers:       "user_id",
	IndexCommunities: "community_id",
	IndexChannels:    "channel_id",
}

// engagementStubQuery matches the documents AddToEngagementScore used to
// create for an entity with no document: no id field at all.
func engagementStubQuery(idField string) map[string]any {
	return map[string]any{
		"query": map[string]any{"bool": map[string]any{
			"must_not": []any{map[string]any{"exists": map[string]any{"field": idField}}},
		}},
	}
}

// PurgeEngagementStubs deletes the engagement-only stub documents from the
// four indices and returns how many each lost. Safe to re-run: once the
// stubs are gone it finds nothing, and nothing creates them any more.
func (s *Store) PurgeEngagementStubs(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	for _, index := range []string{IndexPosts, IndexUsers, IndexCommunities, IndexChannels} {
		idField := EngagementStubIndices[index]
		if err := s.RefreshIndex(ctx, index); err != nil && !isIndexMissing(err) {
			return out, err
		}
		body, _ := json.Marshal(engagementStubQuery(idField))
		refresh := true
		req := opensearchapi.DeleteByQueryRequest{
			Index:     []string{index},
			Body:      bytes.NewReader(body),
			Conflicts: "proceed",
			Refresh:   &refresh,
		}
		res, missing, err := s.doLifecycleQuery(ctx, req)
		if err != nil {
			return out, fmt.Errorf("purge engagement stubs on %s: %w", index, err)
		}
		if missing {
			continue
		}
		out[index] = res.Deleted
		if res.incomplete() {
			return out, fmt.Errorf("purge engagement stubs on %s: %d conflicts, %d failures, timed out %v; re-run",
				index, res.VersionConflicts, len(res.Failures), res.TimedOut)
		}
	}
	return out, nil
}
