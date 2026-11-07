package search

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Snapshot order for communities and channels (entity_state.go). The rules
// are Go; the script classifies and executes. These tests pin the rules,
// the request bodies, and the script's vocabulary to the Go constants. The
// script itself runs against a live OpenSearch in CI
// (internal/events/entity_order_integration_test.go).

func TestDecideEntityWrite_Rules(t *testing.T) {
	type row struct {
		op    entityOp
		kind  storedKind
		order versionOrder
		want  entityAction
	}
	rows := []row{
		// upsert
		{entityUpsert, kindAbsent, orderBothUnversioned, actDoc},
		{entityUpsert, kindAbsent, orderStoredUnversioned, actDoc},
		{entityUpsert, kindLive, orderNewer, actDoc},
		{entityUpsert, kindLive, orderSame, actDoc}, // redelivery
		{entityUpsert, kindLive, orderOlder, actNone},
		{entityUpsert, kindLive, orderStoredUnversioned, actDoc},    // first versioned write over a legacy doc
		{entityUpsert, kindLive, orderIncomingUnversioned, actNone}, // legacy event never overwrites a versioned doc
		{entityUpsert, kindLive, orderBothUnversioned, actDoc},      // today's behaviour for legacy pairs
		{entityUpsert, kindTombstone, orderNewer, actDoc},           // unsuspended, made public again
		{entityUpsert, kindTombstone, orderSame, actNone},           // removal wins ties
		{entityUpsert, kindTombstone, orderOlder, actNone},          // THE bug: stale active after a newer removal
		{entityUpsert, kindTombstone, orderIncomingUnversioned, actNone},
		{entityUpsert, kindTombstone, orderStoredUnversioned, actDoc},
		{entityUpsert, kindTombstone, orderBothUnversioned, actDoc},
		{entityUpsert, kindDeleted, orderNewer, actNone}, // deleted is terminal
		{entityUpsert, kindDeleted, orderStoredUnversioned, actNone},
		// remove
		{entityRemove, kindAbsent, orderStoredUnversioned, actTombstone}, // remembered against a later stale upsert
		{entityRemove, kindAbsent, orderBothUnversioned, actNone},
		{entityRemove, kindLive, orderNewer, actTombstone},
		{entityRemove, kindLive, orderSame, actTombstone},                // removal wins ties
		{entityRemove, kindLive, orderOlder, actNone},                    // a stale removal never takes down a newer doc
		{entityRemove, kindLive, orderIncomingUnversioned, actTombstone}, // fail closed
		{entityRemove, kindLive, orderStoredUnversioned, actTombstone},
		{entityRemove, kindLive, orderBothUnversioned, actTombstone},
		{entityRemove, kindTombstone, orderNewer, actTombstone}, // raise the version
		{entityRemove, kindTombstone, orderStoredUnversioned, actTombstone},
		{entityRemove, kindTombstone, orderSame, actNone},
		{entityRemove, kindTombstone, orderOlder, actNone},
		{entityRemove, kindTombstone, orderIncomingUnversioned, actNone},
		{entityRemove, kindDeleted, orderNewer, actNone},
		// delete
		{entityDelete, kindAbsent, orderBothUnversioned, actDeleted},
		{entityDelete, kindLive, orderIncomingUnversioned, actDeleted},
		{entityDelete, kindLive, orderBothUnversioned, actDeleted},
		{entityDelete, kindTombstone, orderIncomingUnversioned, actDeleted},
		{entityDelete, kindDeleted, orderIncomingUnversioned, actNone},
		// patch (legacy partial update)
		{entityPatch, kindLive, orderBothUnversioned, actPatch},
		{entityPatch, kindLive, orderIncomingUnversioned, actNone}, // never over a versioned doc
		{entityPatch, kindAbsent, orderBothUnversioned, actNone},   // never creates
		{entityPatch, kindTombstone, orderBothUnversioned, actNone},
		{entityPatch, kindDeleted, orderBothUnversioned, actNone},
	}
	for _, r := range rows {
		if got := decideEntityWrite(r.op, r.kind, r.order); got != r.want {
			t.Errorf("%s on %s/%s = %s, want %s", r.op, r.kind, r.order, got, r.want)
		}
	}
}

func TestClassifyVersions(t *testing.T) {
	for _, c := range []struct {
		stored, incoming int64
		want             versionOrder
	}{
		{0, 0, orderBothUnversioned},
		{0, 5, orderStoredUnversioned},
		{5, 0, orderIncomingUnversioned},
		{5, 6, orderNewer},
		{5, 5, orderSame},
		{6, 5, orderOlder},
		{-1, -1, orderBothUnversioned},
	} {
		if got := classifyVersions(c.stored, c.incoming); got != c.want {
			t.Errorf("classifyVersions(%d, %d) = %s, want %s", c.stored, c.incoming, got, c.want)
		}
	}
}

// params.actions answers every class the script can compute, with an
// action the script knows, and agrees with decideEntityWrite.
func TestEntityActionTable_IsTotal(t *testing.T) {
	known := map[string]bool{string(actNone): true, string(actDoc): true, string(actPatch): true,
		string(actTombstone): true, string(actDeleted): true}
	for _, op := range []entityOp{entityUpsert, entityRemove, entityDelete, entityPatch} {
		table := entityActionTable(op)
		if len(table) != len(allStoredKinds)*len(allVersionOrders) {
			t.Fatalf("%s: %d entries", op, len(table))
		}
		for _, k := range allStoredKinds {
			for _, o := range allVersionOrders {
				got, ok := table[actionKey(k, o)]
				if !ok || !known[got] || got != string(decideEntityWrite(op, k, o)) {
					t.Fatalf("%s %s/%s: table says %q", op, k, o, got)
				}
			}
		}
	}
}

// The script spells every class and action exactly as Go does, or a lookup
// would miss and fall back.
func TestEntityStateScript_UsesTheGoVocabulary(t *testing.T) {
	for _, k := range allStoredKinds {
		if !strings.Contains(entityStateScript, "'"+string(k)+"'") {
			t.Errorf("script has no kind %q", k)
		}
	}
	for _, o := range allVersionOrders {
		if !strings.Contains(entityStateScript, "order = '"+string(o)+"'") {
			t.Errorf("script never computes order %q", o)
		}
	}
	for _, a := range []entityAction{actDoc, actPatch, actTombstone, actDeleted} {
		if !strings.Contains(entityStateScript, "action == '"+string(a)+"'") {
			t.Errorf("script never executes action %q", a)
		}
	}
	for _, field := range []string{"source_updated_us", "removed", "deleted"} {
		if !strings.Contains(entityStateScript, "ctx._source."+field) {
			t.Errorf("script never reads or writes %s", field)
		}
	}
}

// The script's classification is classifyVersions and the kind checks,
// clause for clause and in the same order (the first match wins in both).
// A changed comparison in either one fails here without an OpenSearch.
func TestEntityStateScript_ClassifiesLikeGo(t *testing.T) {
	clauses := []string{
		"if (ctx.op != 'create') {\n  kind = 'live';",
		"if (ctx._source.deleted == true) {\n    kind = 'deleted';",
		"} else if (ctx._source.removed == true) {\n    kind = 'tombstone';",
		"stored = ((Number) ctx._source.source_updated_us).longValue();",
		"if (stored <= 0 && incoming <= 0) {\n  order = 'both_unversioned';",
		"} else if (stored <= 0) {\n  order = 'stored_unversioned';",
		"} else if (incoming <= 0) {\n  order = 'incoming_unversioned';",
		"} else if (incoming > stored) {\n  order = 'newer';",
		"} else if (incoming == stored) {\n  order = 'same';",
		"} else {\n  order = 'older';",
		"String action = params.actions.get(kind + '/' + order);",
		"long version = (long) Math.max(stored, incoming);",
		"if (kind == 'live') {\n    ctx._source.putAll(params.fields);",
		"ctx._source.clear();\n  ctx._source.putAll(params.tombstone);",
	}
	at := 0
	for _, c := range clauses {
		i := strings.Index(entityStateScript[at:], c)
		if i < 0 {
			t.Fatalf("the script no longer has, in order: %q", c)
		}
		at += i + len(c)
	}
	// And classifyVersions answers the same on the boundaries each clause
	// draws.
	for _, c := range []struct {
		stored, incoming int64
		want             versionOrder
	}{{0, 0, orderBothUnversioned}, {0, 1, orderStoredUnversioned}, {1, 0, orderIncomingUnversioned},
		{1, 2, orderNewer}, {2, 2, orderSame}, {2, 1, orderOlder}} {
		if classifyVersions(c.stored, c.incoming) != c.want {
			t.Fatalf("classifyVersions(%d, %d) drifted from the script", c.stored, c.incoming)
		}
	}
}

// Only a patch may meet a missing document; any other write answered 404
// (the index is missing, say) did not land and must be retried.
func TestWriteEntity_A404IsAnErrorExceptForAPatch(t *testing.T) {
	store, rec := newRecordingStore(t)
	rec.reply = func(*http.Request) (int, string) { return 404, `{"error":"index_not_found_exception"}` }
	ctx := context.Background()
	if err := store.ApplyChannelState(ctx, channelSnap("suspended", 5)); err == nil {
		t.Fatal("a removal that did not land reported success")
	}
	if err := store.DeleteCommunity(ctx, userA); err == nil {
		t.Fatal("a delete that did not land reported success")
	}
	if err := store.UpdateCommunityFields(ctx, userA, map[string]any{"name": "n"}); err != nil {
		t.Fatalf("a patch of a missing document is not an error: %v", err)
	}
}

func TestFallbackAction_RemovalsStillRemove(t *testing.T) {
	if fallbackAction(entityRemove) != actTombstone || fallbackAction(entityDelete) != actDeleted ||
		fallbackAction(entityUpsert) != actNone || fallbackAction(entityPatch) != actNone {
		t.Fatal("an unknown class must fail closed")
	}
}

func TestSourceVersion(t *testing.T) {
	at := time.Date(2026, 11, 8, 10, 30, 0, 123456000, time.UTC)
	if got := SourceVersion(at); got != at.UnixMicro() || got%1_000_000 != 123456 {
		t.Fatalf("SourceVersion = %d, want microseconds %d", got, at.UnixMicro())
	}
	if SourceVersion(at.Add(time.Microsecond)) <= SourceVersion(at) {
		t.Fatal("one microsecond later is not a newer version")
	}
	if SourceVersion(time.Time{}) != 0 || SourceVersion(time.Unix(-5, 0)) != 0 {
		t.Fatal("a zero or pre-1970 time must be unversioned")
	}
}

// ─── the script, simulated ──────────────────────────────────────────────
//
// simulateEntityScript executes one request body the way entityStateScript
// does, line for line, on a stored _source (nil = no document). The live
// suite runs the same scenarios through the real script.
func simulateEntityScript(t *testing.T, stored map[string]any, body map[string]any) map[string]any {
	t.Helper()
	params := body["script"].(map[string]any)["params"].(map[string]any)
	_, scripted := body["scripted_upsert"]
	if stored == nil && !scripted {
		return nil // a plain update of a missing document: 404, nothing created
	}
	var st int64
	kind := kindAbsent
	if stored != nil {
		kind = kindLive
		if stored["deleted"] == true {
			kind = kindDeleted
		} else if stored["removed"] == true {
			kind = kindTombstone
		}
		if v, ok := stored["source_updated_us"].(int64); ok {
			st = v
		}
	}
	incoming := params["version"].(int64)
	action, ok := params["actions"].(map[string]string)[actionKey(kind, classifyVersions(st, incoming))]
	if !ok {
		action = params["fallback"].(string)
	}
	version := st
	if incoming > version {
		version = incoming
	}
	next := map[string]any{}
	for k, v := range stored {
		next[k] = v
	}
	switch entityAction(action) {
	case actDoc:
		if kind == kindLive {
			for k, v := range params["fields"].(map[string]any) {
				next[k] = v
			}
			if next["engagement_score"] == nil {
				next["engagement_score"] = params["engagement_score"]
			}
		} else {
			next = map[string]any{}
			for k, v := range params["doc"].(map[string]any) {
				next[k] = v
			}
		}
		if version > 0 {
			next["source_updated_us"] = version
		}
	case actPatch:
		for k, v := range params["fields"].(map[string]any) {
			next[k] = v
		}
	case actTombstone, actDeleted:
		next = map[string]any{}
		for k, v := range params["tombstone"].(map[string]any) {
			next[k] = v
		}
		if entityAction(action) == actDeleted {
			next["deleted"] = true
		}
		if version > 0 {
			next["source_updated_us"] = version
		}
	default:
		return stored
	}
	return next
}

func channelSnap(status string, version int64) ChannelDoc {
	return ChannelDoc{
		ChannelID: userA, OwnerID: userB, Handle: "riders", Name: "Riders club",
		ChannelType: "public", Status: status, SubscriberCount: 4, SourceVersion: version,
	}
}

// channelBody is the body ApplyChannelState / DeleteChannel would send.
func channelBody(t *testing.T, d ChannelDoc, deleteEvent bool) map[string]any {
	t.Helper()
	var w entityWrite
	switch {
	case deleteEvent || d.Status == StatusDeleted:
		w = entityWrite{op: entityDelete, id: d.ChannelID}
	case !ChannelSearchable(d):
		w = entityWrite{op: entityRemove, id: d.ChannelID, version: d.SourceVersion}
	default:
		w = entityWrite{op: entityUpsert, id: d.ChannelID, version: d.SourceVersion,
			fields: map[string]any{"channel_id": d.ChannelID, "name": d.Name, "status": d.Status, "channel_type": d.ChannelType},
			doc:    d, engagement: 4}
	}
	body, err := entityStateBody(channelTarget, w)
	if err != nil {
		t.Fatal(err)
	}
	// The simulator reads int64 versions, as the script reads longs.
	return body
}

// visible is what the ranked query admits: status active and a public type.
func visible(doc map[string]any) bool {
	if doc == nil {
		return false
	}
	return doc["status"] == StatusActive && inSet(fmt.Sprint(doc["channel_type"]), publicChannelTypes)
}

// The bug, end to end through the simulator: a stale "active" snapshot
// arriving after a newer "suspended" one must not re-list the channel.
func TestEntityOrder_StaleActiveAfterNewerSuspensionStaysHidden(t *testing.T) {
	var doc map[string]any
	doc = simulateEntityScript(t, doc, channelBody(t, channelSnap(StatusActive, 100), false))
	doc = simulateEntityScript(t, doc, channelBody(t, channelSnap("suspended", 300), false))
	doc = simulateEntityScript(t, doc, channelBody(t, channelSnap(StatusActive, 200), false)) // stale
	if visible(doc) {
		t.Fatalf("a stale active snapshot re-listed a suspended channel: %v", doc)
	}
	if doc["source_updated_us"] != int64(300) || doc["removed"] != true {
		t.Fatalf("tombstone = %v, want removed at version 300", doc)
	}
	for _, leak := range []string{"name", "handle", "owner_id", "description", "subscriber_count"} {
		if _, ok := doc[leak]; ok {
			t.Fatalf("the tombstone carries %q: %v", leak, doc)
		}
	}

	// A suspension that lands FIRST (the document never existed) is still
	// remembered, so the stale active cannot create the document.
	doc = nil
	doc = simulateEntityScript(t, doc, channelBody(t, channelSnap("suspended", 300), false))
	doc = simulateEntityScript(t, doc, channelBody(t, channelSnap(StatusActive, 200), false))
	if visible(doc) {
		t.Fatalf("a stale active snapshot created a suspended channel: %v", doc)
	}

	// And the unsuspension (newer) brings it back.
	doc = simulateEntityScript(t, doc, channelBody(t, channelSnap(StatusActive, 400), false))
	if !visible(doc) || doc["name"] != "Riders club" || doc["source_updated_us"] != int64(400) {
		t.Fatalf("a newer active snapshot did not restore the channel: %v", doc)
	}
}

// The other direction: a stale removal must not take down a newer doc.
func TestEntityOrder_StaleSuspensionAfterNewerActiveStaysVisible(t *testing.T) {
	var doc map[string]any
	doc = simulateEntityScript(t, doc, channelBody(t, channelSnap(StatusActive, 300), false))
	doc = simulateEntityScript(t, doc, channelBody(t, channelSnap("suspended", 200), false))
	if !visible(doc) {
		t.Fatalf("a stale suspension took down a newer active channel: %v", doc)
	}
}

// Deleted is terminal: nothing after it, however new, writes the channel.
func TestEntityOrder_DeleteIsTerminal(t *testing.T) {
	var doc map[string]any
	doc = simulateEntityScript(t, doc, channelBody(t, channelSnap(StatusActive, 100), false))
	doc = simulateEntityScript(t, doc, channelBody(t, ChannelDoc{ChannelID: userA}, true))
	for _, v := range []int64{50, 100, 1 << 60, 0} {
		doc = simulateEntityScript(t, doc, channelBody(t, channelSnap(StatusActive, v), false))
	}
	if visible(doc) || doc["deleted"] != true {
		t.Fatalf("a deleted channel came back: %v", doc)
	}
	// Deleted before it was ever indexed: still remembered.
	doc = simulateEntityScript(t, nil, channelBody(t, ChannelDoc{ChannelID: userA}, true))
	doc = simulateEntityScript(t, doc, channelBody(t, channelSnap(StatusActive, 100), false))
	if visible(doc) {
		t.Fatalf("a delete that arrived first was forgotten: %v", doc)
	}
}

// Old events (no version) keep today's behaviour among themselves but never
// overwrite a versioned document; an unversioned removal still removes.
func TestEntityOrder_UnversionedEvents(t *testing.T) {
	doc := simulateEntityScript(t, nil, channelBody(t, channelSnap(StatusActive, 0), false))
	if !visible(doc) {
		t.Fatalf("a legacy created event no longer indexes: %v", doc)
	}
	doc = simulateEntityScript(t, doc, channelBody(t, channelSnap("suspended", 0), false))
	doc = simulateEntityScript(t, doc, channelBody(t, channelSnap(StatusActive, 0), false))
	if !visible(doc) {
		t.Fatalf("legacy remove-then-write no longer behaves as before: %v", doc)
	}

	doc = simulateEntityScript(t, doc, channelBody(t, channelSnap("suspended", 500), false))
	doc = simulateEntityScript(t, doc, channelBody(t, channelSnap(StatusActive, 0), false))
	if visible(doc) {
		t.Fatalf("an unversioned event overwrote a versioned tombstone: %v", doc)
	}

	live := simulateEntityScript(t, nil, channelBody(t, channelSnap(StatusActive, 500), false))
	renamed := channelSnap(StatusActive, 0)
	renamed.Name = "Stale name"
	if got := simulateEntityScript(t, live, channelBody(t, renamed, false)); got["name"] != "Riders club" {
		t.Fatalf("an unversioned event overwrote a versioned document: %v", got)
	}
	if got := simulateEntityScript(t, live, channelBody(t, channelSnap("suspended", 0), false)); visible(got) {
		t.Fatalf("an unversioned removal must still remove (fail closed): %v", got)
	} else if got["source_updated_us"] != int64(500) {
		t.Fatalf("an unversioned removal must keep the stored version: %v", got)
	}
}

// Whatever order the snapshots arrive in, the document ends as the newest
// one says.
func TestEntityOrder_AnyArrivalOrderEndsAtTheNewest(t *testing.T) {
	rng := rand.New(rand.NewSource(20261108))
	statuses := []string{StatusActive, "suspended", "archived", StatusActive}
	for trial := 0; trial < 500; trial++ {
		n := 2 + rng.Intn(5)
		snaps := make([]ChannelDoc, n)
		for i := range snaps {
			snaps[i] = channelSnap(statuses[rng.Intn(len(statuses))], int64(1000+i))
			if rng.Intn(4) == 0 {
				snaps[i].ChannelType = "private"
			}
		}
		newest := snaps[n-1]
		var doc map[string]any
		for _, i := range rng.Perm(n) {
			doc = simulateEntityScript(t, doc, channelBody(t, snaps[i], false))
			if rng.Intn(3) == 0 { // a redelivery
				doc = simulateEntityScript(t, doc, channelBody(t, snaps[i], false))
			}
		}
		if visible(doc) != ChannelSearchable(newest) {
			t.Fatalf("trial %d: visible=%v, newest %s/%s: %v", trial, visible(doc), newest.Status, newest.ChannelType, doc)
		}
		if doc["source_updated_us"] != newest.SourceVersion {
			t.Fatalf("trial %d: version %v, want %d", trial, doc["source_updated_us"], newest.SourceVersion)
		}
	}
}

// No query over the two indices admits a tombstone.
func TestTombstoneMatchesNoQuery(t *testing.T) {
	tomb := map[string]any{"channel_id": userA, "community_id": userA, "removed": true, "status": StatusRemoved, "source_updated_us": int64(9)}
	for _, entity := range []string{EntityChannels, EntityCommunities} {
		filter := boolFilterOf(t, buildFunctionScoreQuery(entity, "riders", RankedSearchOptions{}))
		if len(filter) == 0 || docPassesFilter(t, filter, tomb) {
			t.Fatalf("%s: the ranked query admits a tombstone (filter %v)", entity, filter)
		}
	}
	if docPassesFilter(t, publicCommunityFilterAny(), tomb) {
		t.Fatal("the community autocomplete filter admits a tombstone")
	}
}

// ─── request bodies ─────────────────────────────────────────────────────

func paramsOf(t *testing.T, raw []byte) (map[string]any, map[string]any) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	script, _ := body["script"].(map[string]any)
	params, _ := script["params"].(map[string]any)
	if params == nil {
		t.Fatalf("no script params: %s", raw)
	}
	return body, params
}

func TestApplyChannelState_SendsTheOrderedWrite(t *testing.T) {
	store, rec := newRecordingStore(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 8, 9, 0, 0, 5000, time.UTC)

	active := channelSnap(StatusActive, SourceVersion(at))
	active.Description = ""
	if err := store.ApplyChannelState(ctx, active); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyChannelState(ctx, channelSnap("suspended", SourceVersion(at)+1)); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteChannel(ctx, userA); err != nil {
		t.Fatal(err)
	}
	got := rec.matching(http.MethodPost, "/"+IndexChannels+"/_update/"+userA)
	if len(got) != 3 {
		t.Fatalf("writes = %+v, want three ordered updates", rec.reqs)
	}

	body, p := paramsOf(t, got[0].raw)
	if body["scripted_upsert"] != true || p["op"] != "upsert" || p["version"] != float64(at.UnixMicro()) {
		t.Fatalf("upsert body = %s", got[0].raw)
	}
	fields, _ := p["fields"].(map[string]any)
	if fields["owner_id"] != userB || fields["description"] != "" || fields["status"] != StatusActive {
		t.Fatalf("fields = %v (an empty description must be written)", fields)
	}
	if _, ok := fields["engagement_score"]; ok {
		t.Fatal("an edit must not reset engagement_score")
	}
	if doc, _ := p["doc"].(map[string]any); doc["owner_id"] != userB || doc["engagement_score"] != float64(4) {
		t.Fatalf("doc = %v, want the whole document", doc)
	}
	actions, _ := p["actions"].(map[string]any)
	if actions["tombstone/older"] != "none" || actions["live/newer"] != "doc" {
		t.Fatalf("actions = %v", actions)
	}

	body, p = paramsOf(t, got[1].raw)
	if body["scripted_upsert"] != true || p["op"] != "remove" || p["version"] != float64(at.UnixMicro()+1) {
		t.Fatalf("removal body = %s", got[1].raw)
	}
	tomb, _ := p["tombstone"].(map[string]any)
	if len(tomb) != 3 || tomb["channel_id"] != userA || tomb["removed"] != true || tomb["status"] != StatusRemoved {
		t.Fatalf("tombstone = %v, want the id and the two markers only", tomb)
	}
	if actions, _ := p["actions"].(map[string]any); actions["live/older"] != "none" || actions["absent/stored_unversioned"] != "tombstone" {
		t.Fatalf("removal actions = %v", actions)
	}

	_, p = paramsOf(t, got[2].raw)
	if p["op"] != "delete" || p["version"] != float64(0) {
		t.Fatalf("delete body = %s", got[2].raw)
	}

	// A deleted snapshot is the terminal delete, not a removal.
	store, rec = newRecordingStore(t)
	if err := store.ApplyChannelState(ctx, channelSnap(StatusDeleted, 7)); err != nil {
		t.Fatal(err)
	}
	if got := rec.matching(http.MethodPost, "/_update/"); len(got) != 1 {
		t.Fatalf("writes = %+v", rec.reqs)
	} else if _, p := paramsOf(t, got[0].raw); p["op"] != "delete" {
		t.Fatalf("a deleted channel snapshot sent %s", got[0].raw)
	}
}

func TestApplyCommunityState_SendsTheOrderedWrite(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		doc    CommunityDoc
		wantOp string
	}{
		"archived":  {CommunityDoc{CommunityID: userA, OwnerID: userB, CommunityType: "public", Status: "archived", SourceVersion: 9}, "remove"},
		"private":   {CommunityDoc{CommunityID: userA, OwnerID: userB, CommunityType: "private", Status: StatusActive, SourceVersion: 9}, "remove"},
		"no status": {CommunityDoc{CommunityID: userA, OwnerID: userB, CommunityType: "education"}, "remove"},
		"deleted":   {CommunityDoc{CommunityID: userA, OwnerID: userB, CommunityType: "public", Status: StatusDeleted}, "delete"},
		"active":    {CommunityDoc{CommunityID: userA, OwnerID: userB, CommunityType: "professional", Status: StatusActive, MemberCount: 3, SourceVersion: 9}, "upsert"},
	} {
		store, rec := newRecordingStore(t)
		if err := store.ApplyCommunityState(ctx, c.doc); err != nil {
			t.Fatal(err)
		}
		got := rec.matching(http.MethodPost, "/"+IndexCommunities+"/_update/"+userA)
		if len(got) != 1 {
			t.Fatalf("%s: writes = %+v", name, rec.reqs)
		}
		_, p := paramsOf(t, got[0].raw)
		if p["op"] != c.wantOp {
			t.Fatalf("%s: op %v, want %s", name, p["op"], c.wantOp)
		}
		if c.wantOp == "upsert" {
			if f, _ := p["fields"].(map[string]any); f["owner_id"] != userB || p["version"] != float64(9) {
				t.Fatalf("%s: an active public community is not written with its owner: %s", name, got[0].raw)
			}
		} else if strings.Contains(string(got[0].raw), `"tombstone":{"community_id":"`+userA+`","removed":true,"status":"removed"}`) == false {
			t.Fatalf("%s: tombstone = %s", name, got[0].raw)
		}
	}
}

// The legacy partial update never creates a document and is a no-op on a
// tombstone or a versioned document (decided in the script).
func TestUpdateCommunityFields_IsAnOrderedPatch(t *testing.T) {
	store, rec := newRecordingStore(t)
	rec.reply = func(*http.Request) (int, string) { return 404, `{"error":"document_missing_exception"}` }
	if err := store.UpdateCommunityFields(context.Background(), userA, map[string]any{"name": "n", "status": StatusActive}); err != nil {
		t.Fatalf("a missing document is not an error: %v", err)
	}
	got := rec.matching(http.MethodPost, "/"+IndexCommunities+"/_update/"+userA)
	if len(got) != 1 {
		t.Fatalf("writes = %+v", rec.reqs)
	}
	body, p := paramsOf(t, got[0].raw)
	if _, ok := body["upsert"]; ok || body["scripted_upsert"] != nil {
		t.Fatalf("a partial update must never create a document: %s", got[0].raw)
	}
	if p["op"] != "patch" || p["version"] != float64(0) {
		t.Fatalf("patch body = %s", got[0].raw)
	}
	if f, _ := p["fields"].(map[string]any); f["name"] != "n" || len(f) != 2 {
		t.Fatalf("fields = %v", f)
	}
	if a, _ := p["actions"].(map[string]any); a["live/both_unversioned"] != "patch" || a["tombstone/both_unversioned"] != "none" || a["live/incoming_unversioned"] != "none" {
		t.Fatalf("patch actions = %v", a)
	}
}

// ─── engagement stubs ───────────────────────────────────────────────────

func TestAddToEngagementScore_NeverCreatesADocument(t *testing.T) {
	store, rec := newRecordingStore(t)
	rec.reply = func(*http.Request) (int, string) { return 404, `{"error":"document_missing_exception"}` }
	for _, index := range []string{IndexCommunities, IndexChannels, IndexPosts, IndexUsers} {
		if err := store.AddToEngagementScore(context.Background(), index, userA, 1); err != nil {
			t.Fatalf("%s: a missing document is not an error: %v", index, err)
		}
	}
	got := rec.matching(http.MethodPost, "/_update/"+userA)
	if len(got) != 4 {
		t.Fatalf("writes = %+v", rec.reqs)
	}
	for _, w := range got {
		body, p := paramsOf(t, w.raw)
		if _, ok := body["upsert"]; ok || body["scripted_upsert"] != nil {
			t.Fatalf("%s: the engagement update creates a stub: %s", w.path, w.raw)
		}
		if p["delta"] != float64(1) {
			t.Fatalf("%s: params = %v", w.path, p)
		}
	}
	if !strings.Contains(engagementScoreScript, "ctx._source.removed == true") || !strings.Contains(engagementScoreScript, "ctx.op = 'none'") {
		t.Fatal("a tombstone must not collect a score")
	}
}

func TestPurgeEngagementStubs_DeletesDocumentsWithoutAnID(t *testing.T) {
	store, rec := newRecordingStore(t)
	rec.reply = func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Path, "_delete_by_query") {
			return 200, `{"total":2,"deleted":2}`
		}
		return 200, `{}`
	}
	removed, err := store.PurgeEngagementStubs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for index, idField := range EngagementStubIndices {
		got := rec.matching(http.MethodPost, "/"+index+"/_delete_by_query")
		if len(got) != 1 {
			t.Fatalf("%s: no purge sent: %+v", index, rec.reqs)
		}
		want := `{"query":{"bool":{"must_not":[{"exists":{"field":"` + idField + `"}}]}}}`
		if string(got[0].raw) != want {
			t.Fatalf("%s: purge query %s, want %s", index, got[0].raw, want)
		}
		if removed[index] != 2 {
			t.Fatalf("%s: removed %d", index, removed[index])
		}
	}

	// A conflict means a stub may be left: the run says so.
	store, rec = newRecordingStore(t)
	rec.reply = func(*http.Request) (int, string) { return 200, `{"deleted":1,"version_conflicts":1}` }
	if _, err := store.PurgeEngagementStubs(context.Background()); err == nil {
		t.Fatal("an incomplete purge reported success")
	}
}
