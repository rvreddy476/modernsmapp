package search

import (
	"context"
	"encoding/json"
	"fmt"
)

// Ordering the users_v1 profile writes (2026-11-16).
//
// Every writer of a user document rewrites it whole: the UserRegistered and
// UserProfileUpdated events (IndexUser), the users reindex and auto-heal,
// and cmd/backfill (BulkIndexUsers). They used to apply in arrival order,
// so a redelivered or out-of-order profile event, or a reindex page read
// before an edit and written after it, put an older profile back over a
// newer one.
//
// Every writer now carries a version (source_updated_us, microseconds, as
// for communities and channels in entity_state.go and Tube channels in
// tube_channel_state.go), and one scripted upsert (userProfileUpsertScript)
// decides under the document lock:
//
//   - UserProfileUpdated: the payload's updated_at;
//   - UserRegistered: the payload's created_at (the account's first state,
//     so any later edit or reindex outranks it);
//   - the users reindex: the time the page was requested from
//     profile-service, which serves no updated_at (reindex.go);
//   - cmd/backfill: the time the SELECT started (its snapshot).
//
// A snapshot taken at time T holds every edit committed before T, and an
// edit's event is stamped after its commit, so "the read time" orders the
// reindex against the events the same way the row's updated_at would.
//
// RULES (userWriteApplies)
//
//	newer than the stored version              → rewrite
//	stored unversioned (written before this)   → rewrite
//	both unversioned (legacy writer and doc)   → rewrite, as before
//	same version                               → no-op (redelivery; the
//	                                             message is still acked)
//	older                                      → no-op
//	unversioned over a versioned document      → no-op
//
// A no-op is not an error: the update answers "noop" and the event is acked.
//
// is_hidden keeps its one authority (the account-lifecycle events,
// UpdateUserHidden): a stored is_hidden=true survives every rewrite, exactly
// as keepHiddenUpsertScript does. A hidden stub written before the first
// profile is unversioned, so the first profile write always lands on it,
// still hidden. The partial writers (UpdateUserHidden, UpdateUserPrivacy,
// UpdateUserUsername, engagement) do not touch the version.

// userWriteApplies is the whole ordering rule for a full user rewrite.
func userWriteApplies(order versionOrder) bool {
	switch order {
	case orderNewer, orderStoredUnversioned, orderBothUnversioned:
		return true
	}
	return false
}

// userWriteApplyTable is params.apply: the answer for every version order.
func userWriteApplyTable() map[string]bool {
	out := make(map[string]bool, len(allVersionOrders))
	for _, o := range allVersionOrders {
		out[string(o)] = userWriteApplies(o)
	}
	return out
}

// userProfileUpsertScript classifies the versions exactly as
// classifyVersions does and rewrites the document only on Go's answer. A
// missing document is stored version 0, so a create always writes.
const userProfileUpsertScript = `
boolean hidden = ctx._source.is_hidden == true;
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
  ctx._source.clear();
  ctx._source.putAll(params.doc);
  if (hidden) {
    ctx._source.is_hidden = true;
  }
  if (incoming > 0) {
    ctx._source.source_updated_us = incoming;
  }
} else {
  ctx.op = 'none';
}
`

// userProfileUpsertBody is the update body for one ordered user rewrite.
// Pure, for the tests.
func userProfileUpsertBody(doc UserDoc) (map[string]any, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var docMap map[string]any
	if err := json.Unmarshal(raw, &docMap); err != nil {
		return nil, err
	}
	version := doc.SourceVersion
	if version < 0 {
		version = 0
	}
	if version > 0 {
		docMap["source_updated_us"] = version
	}
	return map[string]any{
		"scripted_upsert": true,
		"upsert":          map[string]any{},
		"script": map[string]any{
			"lang":   "painless",
			"source": userProfileUpsertScript,
			"params": map[string]any{
				"version": version,
				"apply":   userWriteApplyTable(),
				"doc":     docMap,
			},
		},
	}, nil
}

// upsertUserProfile is the single-document form (IndexUser).
func (s *Store) upsertUserProfile(ctx context.Context, doc UserDoc) error {
	if doc.UserID == "" {
		return fmt.Errorf("upsert %s: empty document id", IndexUsers)
	}
	body, err := userProfileUpsertBody(doc)
	if err != nil {
		return err
	}
	return s.sendScriptedUpdate(ctx, IndexUsers, doc.UserID, body)
}

// buildBulkUserProfileBody writes ordered user upserts (bulk.go rules: a
// marshalled action line, a canonical UUID id).
func buildBulkUserProfileBody(index string, ids []string, docs []any) ([]byte, error) {
	return buildBulkBody(ids, docs, func(id string, doc any) (bulkAction, any, error) {
		u, ok := doc.(UserDoc)
		if !ok {
			return bulkAction{}, nil, fmt.Errorf("search: users bulk body: %T is not a UserDoc", doc)
		}
		body, err := userProfileUpsertBody(u)
		if err != nil {
			return bulkAction{}, nil, err
		}
		return bulkAction{Update: &bulkTarget{Index: index, ID: id, RetryOnConflict: bulkRetries}}, body, nil
	})
}
