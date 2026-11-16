package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// Ordering the users_v1 profile writes (2026-11-16).
//
// Every full writer of a user document rewrites it whole: the
// UserRegistered and UserProfileUpdated events (IndexUser), the users
// reindex and auto-heal, and cmd/backfill (BulkIndexUsers). They used to
// apply in arrival order, so a redelivered or out-of-order profile event,
// or a reindex page read before an edit and written after it, put an older
// profile back over a newer one.
//
// Every full writer now carries a version (microseconds, as for
// communities and channels in entity_state.go and Tube channels in
// tube_channel_state.go):
//
//   - UserProfileUpdated: the payload's updated_at (profile-service sends
//     the committed row's updated_at);
//   - UserRegistered: the payload's created_at (the account's first state,
//     so any later edit or reindex outranks it);
//   - the users reindex: the time the page was requested from
//     profile-service, which serves no updated_at (reindex.go);
//   - cmd/backfill: the time the SELECT started (its snapshot).
//
// A snapshot taken at time T holds every edit committed before T, and an
// edit's event is stamped with its commit, so "the read time" orders the
// reindex against the events the same way the row's updated_at would.
//
// FIELD GROUPS (2026-11-16, second pass)
//
// Two partial writers carry user-editable state of their own, with their
// own event time: HandleChanged (the username, profile-service, the row's
// updated_at) and UserSettingsChanged (is_private, user-service, its
// occurred_at). A single document version could not order them: a late
// UserProfileUpdated newer than the stored version put back the username
// a newer HandleChanged had replaced. So each group has its own version:
//
//	group    fields       version field         partial writer
//	profile  everything   source_updated_us     (none: full rewrites only)
//	handle   username     handle_updated_us     UpdateUserUsername
//	privacy  is_private   privacy_updated_us    UpdateUserPrivacy
//
// A group's stored version is its own field, or source_updated_us when the
// document has none (written before this pass, or never touched by a
// partial of that group). Every write that changes a group stamps its
// version, so after any versioned write a group version is never below
// source_updated_us.
//
// RULES
//
// A full rewrite at version V decides EACH group on its own, by
// userWriteApplies(classifyVersions(stored group version, V)):
//
//	newer than the stored version              → write the group
//	stored unversioned (written before this)   → write the group
//	both unversioned (legacy writer and doc)   → write the group, as before
//	same version                               → keep the group
//	older                                      → keep the group
//	unversioned over a versioned group         → keep the group
//
// When the profile group is written, the document is rebuilt from the
// rewrite, then every kept group's fields and version are put back as they
// were. When no group is written the update answers "noop" and the event is
// acked. So a late full rewrite can no longer restore an old username after
// a newer HandleChanged, or an old is_private after a newer settings event.
//
// A partial write at version V (userPartialApplies) writes its own fields
// and stamps its own version when V is newer than the group's stored
// version (or either side is unversioned on a document without one); an
// equal or older one is a no-op. An unversioned partial (an older producer
// sending no timestamp) still writes, as it did before versions, and
// stamps nothing: for is_private that keeps a "go private" from being
// dropped. A partial never creates a document (404 is not an error, as
// before): the full rewrite that creates it carries the username and the
// current is_private itself.
//
// is_hidden keeps its one authority (the account-lifecycle events,
// UpdateUserHidden, unversioned): a stored is_hidden=true survives every
// rewrite, exactly as keepHiddenUpsertScript does. A hidden stub written
// before the first profile is unversioned, so the first profile write
// always lands on it, still hidden. Engagement counters
// (AddToEngagementScore and the recounts) are increments, not state, and
// stay unversioned.
//
// The full rewrite's is_private is the consumer's current lookup
// (authorIsPrivate), not a value as of V, so writing it under V never
// writes a value older than V; stamping V (not the lookup time) only lets a
// settings event between V and the lookup apply again, with the same value.

type userFieldGroup string

const (
	userGroupProfile userFieldGroup = "profile"
	userGroupHandle  userFieldGroup = "handle"
	userGroupPrivacy userFieldGroup = "privacy"
)

// userGroupVersionFields is each group's version field (params.groups).
var userGroupVersionFields = map[userFieldGroup]string{
	userGroupProfile: "source_updated_us",
	userGroupHandle:  "handle_updated_us",
	userGroupPrivacy: "privacy_updated_us",
}

// userGroupFields is the fields each partial group owns
// (params.group_fields). The profile group owns everything else.
var userGroupFields = map[userFieldGroup][]string{
	userGroupHandle:  {"username"},
	userGroupPrivacy: {"is_private"},
}

// userVersionFields lists every version field, for the result strip.
func userVersionFields() []string {
	out := make([]string, 0, len(userGroupVersionFields))
	for _, f := range userGroupVersionFields {
		out = append(out, f)
	}
	return out
}

// StripUserVersions removes the write versions from a users_v1 _source map
// before it goes on a result's wire: they are edit times, activity timing
// that profile-service does not publish either.
func StripUserVersions(src map[string]any) {
	for _, f := range userVersionFields() {
		delete(src, f)
	}
}

// userWriteApplies is the ordering rule for one group of a full rewrite.
func userWriteApplies(order versionOrder) bool {
	switch order {
	case orderNewer, orderStoredUnversioned, orderBothUnversioned:
		return true
	}
	return false
}

// userPartialApplies is the ordering rule for a partial (handle, privacy)
// write. It differs from userWriteApplies only for an unversioned partial,
// which writes as before versions and stamps nothing.
func userPartialApplies(order versionOrder) bool {
	switch order {
	case orderNewer, orderStoredUnversioned, orderBothUnversioned, orderIncomingUnversioned:
		return true
	}
	return false
}

func applyTable(rule func(versionOrder) bool) map[string]bool {
	out := make(map[string]bool, len(allVersionOrders))
	for _, o := range allVersionOrders {
		out[string(o)] = rule(o)
	}
	return out
}

// userWriteApplyTable is params.apply of a full rewrite.
func userWriteApplyTable() map[string]bool { return applyTable(userWriteApplies) }

// userPartialApplyTable is params.apply of a partial write.
func userPartialApplyTable() map[string]bool { return applyTable(userPartialApplies) }

// storedUserGroupVersion is a group's stored version on a _source map (nil:
// no document): its own field, else source_updated_us, else 0. The Go
// statement of the script's `st` lookup.
func storedUserGroupVersion(src map[string]any, g userFieldGroup) int64 {
	if v, ok := numberAsInt64(src[userGroupVersionFields[g]]); ok {
		return v
	}
	if v, ok := numberAsInt64(src[userGroupVersionFields[userGroupProfile]]); ok {
		return v
	}
	return 0
}

func numberAsInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}

// decideUserRewrite is the whole rule for a full rewrite at version
// incoming over a stored _source: which groups it writes.
func decideUserRewrite(src map[string]any, incoming int64) map[userFieldGroup]bool {
	out := make(map[userFieldGroup]bool, len(userGroupVersionFields))
	for g := range userGroupVersionFields {
		out[g] = userWriteApplies(classifyVersions(storedUserGroupVersion(src, g), incoming))
	}
	return out
}

// userProfileUpsertScript decides every group as decideUserRewrite does
// (the order block is classifyVersions clause for clause) and rebuilds the
// document on the profile group's answer, putting kept groups back. A
// missing document has every stored version 0, so a create always writes.
const userProfileUpsertScript = `
boolean hidden = ctx._source.is_hidden == true;
long incoming = params.version;
long base = 0;
if (ctx._source.source_updated_us != null) {
  base = ((Number) ctx._source.source_updated_us).longValue();
}
Map applies = new HashMap();
Map stored = new HashMap();
boolean any = false;
for (def g : params.groups.keySet()) {
  String vf = params.groups.get(g);
  long st = base;
  if (ctx._source.get(vf) != null) {
    st = ((Number) ctx._source.get(vf)).longValue();
  }
  String order;
  if (st <= 0 && incoming <= 0) {
    order = 'both_unversioned';
  } else if (st <= 0) {
    order = 'stored_unversioned';
  } else if (incoming <= 0) {
    order = 'incoming_unversioned';
  } else if (incoming > st) {
    order = 'newer';
  } else if (incoming == st) {
    order = 'same';
  } else {
    order = 'older';
  }
  boolean a = params.apply.get(order) == true;
  applies.put(g, a);
  stored.put(g, st);
  if (a) {
    any = true;
  }
}
if (!any) {
  ctx.op = 'none';
} else if (applies.get('profile') == true) {
  Map kept = new HashMap();
  for (def g : params.group_fields.keySet()) {
    if (applies.get(g) != true) {
      for (def f : params.group_fields.get(g)) {
        if (ctx._source.containsKey(f)) {
          kept.put(f, ctx._source.get(f));
        }
      }
    }
  }
  ctx._source.clear();
  ctx._source.putAll(params.doc);
  for (def g : params.groups.keySet()) {
    String vf = params.groups.get(g);
    if (applies.get(g) == true) {
      if (incoming > 0) {
        ctx._source.put(vf, incoming);
      } else {
        ctx._source.remove(vf);
      }
    } else {
      if (params.group_fields.containsKey(g)) {
        for (def f : params.group_fields.get(g)) {
          ctx._source.remove(f);
        }
      }
      ctx._source.put(vf, stored.get(g));
    }
  }
  ctx._source.putAll(kept);
  if (hidden) {
    ctx._source.is_hidden = true;
  }
} else {
  for (def g : params.group_fields.keySet()) {
    if (applies.get(g) == true) {
      for (def f : params.group_fields.get(g)) {
        if (params.doc.containsKey(f)) {
          ctx._source.put(f, params.doc.get(f));
        }
      }
      if (incoming > 0) {
        ctx._source.put(params.groups.get(g), incoming);
      }
    }
  }
}
`

// userScriptGroupParams is params.groups and params.group_fields.
func userScriptGroupParams() (map[string]string, map[string][]string) {
	groups := make(map[string]string, len(userGroupVersionFields))
	for g, f := range userGroupVersionFields {
		groups[string(g)] = f
	}
	fields := make(map[string][]string, len(userGroupFields))
	for g, fs := range userGroupFields {
		fields[string(g)] = append([]string(nil), fs...)
	}
	return groups, fields
}

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
		for _, f := range userGroupVersionFields {
			docMap[f] = version
		}
	}
	groups, groupFields := userScriptGroupParams()
	return map[string]any{
		"scripted_upsert": true,
		"upsert":          map[string]any{},
		"script": map[string]any{
			"lang":   "painless",
			"source": userProfileUpsertScript,
			"params": map[string]any{
				"version":      version,
				"apply":        userWriteApplyTable(),
				"groups":       groups,
				"group_fields": groupFields,
				"doc":          docMap,
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

// userPartialScript writes one group's fields at its version, by
// userPartialApplies over the group's stored version (its own field, else
// source_updated_us, as storedUserGroupVersion).
const userPartialScript = `
long incoming = params.version;
long st = 0;
if (ctx._source.source_updated_us != null) {
  st = ((Number) ctx._source.source_updated_us).longValue();
}
if (ctx._source.get(params.version_field) != null) {
  st = ((Number) ctx._source.get(params.version_field)).longValue();
}
String order;
if (st <= 0 && incoming <= 0) {
  order = 'both_unversioned';
} else if (st <= 0) {
  order = 'stored_unversioned';
} else if (incoming <= 0) {
  order = 'incoming_unversioned';
} else if (incoming > st) {
  order = 'newer';
} else if (incoming == st) {
  order = 'same';
} else {
  order = 'older';
}
if (params.apply.get(order) == true) {
  ctx._source.putAll(params.doc);
  if (incoming > 0) {
    ctx._source.put(params.version_field, incoming);
  }
} else {
  ctx.op = 'none';
}
`

// userPartialBody is the update body for one ordered partial write of
// group g. Pure, for the tests. No upsert: a partial never creates a
// document.
func userPartialBody(g userFieldGroup, values map[string]any, version int64) map[string]any {
	if version < 0 {
		version = 0
	}
	return map[string]any{
		"script": map[string]any{
			"lang":   "painless",
			"source": userPartialScript,
			"params": map[string]any{
				"version":       version,
				"version_field": userGroupVersionFields[g],
				"apply":         userPartialApplyTable(),
				"doc":           values,
			},
		},
	}
}

// updateUserPartial sends one ordered partial write. A missing document is
// not an error (the full rewrite that creates it carries the field); a
// no-op answers 200 "noop".
func (s *Store) updateUserPartial(ctx context.Context, userID string, g userFieldGroup, values map[string]any, version int64) error {
	if userID == "" {
		return nil
	}
	data, err := json.Marshal(userPartialBody(g, values, version))
	if err != nil {
		return err
	}
	retries := bulkRetries
	req := opensearchapi.UpdateRequest{
		Index:           IndexUsers,
		DocumentID:      userID,
		Body:            bytes.NewReader(data),
		RetryOnConflict: &retries,
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() && res.StatusCode != 404 {
		return fmt.Errorf("opensearch update user %s error: %s", g, res.String())
	}
	return nil
}
