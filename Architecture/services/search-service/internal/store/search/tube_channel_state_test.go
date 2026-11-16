package search

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Tube channel events (tube_channel_state.go) and the version guard on the
// Tube reindex rewrite (keepHiddenUpsertScript). The Painless halves run
// live in internal/events/tube_channel_order_integration_test.go.

// channel_id (2026-11-16) targets the document by its _id; an event
// without one falls back to the owner, as before.
func TestTubeChannelEventBody_PrefersChannelIDThenOwner(t *testing.T) {
	byID := tubeChannelEventBody(TubeChannelUpdate{ChannelID: "ch-1", OwnerID: "u-1"})
	q, _ := byID["query"].(map[string]any)
	ids, _ := q["ids"].(map[string]any)
	if v, _ := ids["values"].([]string); len(v) != 1 || v[0] != "ch-1" {
		t.Fatalf("query = %v, want ids [ch-1]", q)
	}
	byOwner := tubeChannelEventBody(TubeChannelUpdate{OwnerID: "u-1"})
	q, _ = byOwner["query"].(map[string]any)
	term, _ := q["term"].(map[string]any)
	if term["owner_id"] != "u-1" {
		t.Fatalf("query = %v, want term owner_id u-1", q)
	}
}

func TestTubeEventApplies_Rules(t *testing.T) {
	want := map[versionOrder]bool{
		orderNewer:               true,
		orderSame:                true, // redelivery: the same fields again
		orderBothUnversioned:     true,
		orderStoredUnversioned:   true, // a document from before versions
		orderOlder:               false,
		orderIncomingUnversioned: false, // never over a versioned document
	}
	for _, o := range allVersionOrders {
		w, ok := want[o]
		if !ok {
			t.Fatalf("no expectation for %s", o)
		}
		if got := tubeEventApplies(o); got != w {
			t.Errorf("tubeEventApplies(%s) = %v, want %v", o, got, w)
		}
	}
	if len(tubeEventApplyTable()) != len(allVersionOrders) {
		t.Fatal("params.apply is not total")
	}
}

// The script orders versions as classifyVersions does, clause for clause,
// and writes only on Go's answer.
func TestTubeChannelEventScript_ClassifiesLikeGo(t *testing.T) {
	clauses := []string{
		"stored = ((Number) ctx._source.source_updated_us).longValue();",
		"if (stored <= 0 && incoming <= 0) {\n  order = 'both_unversioned';",
		"} else if (stored <= 0) {\n  order = 'stored_unversioned';",
		"} else if (incoming <= 0) {\n  order = 'incoming_unversioned';",
		"} else if (incoming > stored) {\n  order = 'newer';",
		"} else if (incoming == stored) {\n  order = 'same';",
		"} else {\n  order = 'older';",
		"if (params.apply.get(order) == true) {\n  ctx._source.putAll(params.fields);",
		"} else {\n  ctx.op = 'noop';",
	}
	at := 0
	for _, c := range clauses {
		i := strings.Index(tubeChannelEventScript[at:], c)
		if i < 0 {
			t.Fatalf("the script no longer has, in order: %q", c)
		}
		at += i + len(c)
	}
}

// simulateTubeEventScript runs one body the way tubeChannelEventScript
// does on a stored _source; it returns the new source and whether it wrote.
func simulateTubeEventScript(stored map[string]any, body map[string]any) (map[string]any, bool) {
	params := body["script"].(map[string]any)["params"].(map[string]any)
	var st int64
	if v, ok := stored["source_updated_us"].(int64); ok {
		st = v
	}
	in := params["version"].(int64)
	if !params["apply"].(map[string]bool)[string(classifyVersions(st, in))] {
		return stored, false
	}
	out := map[string]any{}
	for k, v := range stored {
		out[k] = v
	}
	for k, v := range params["fields"].(map[string]any) {
		out[k] = v
	}
	if in > st {
		out["source_updated_us"] = in
	}
	return out, true
}

func TestTubeChannelEvent_AnyArrivalOrderEndsAtTheNewest(t *testing.T) {
	base := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	edits := []TubeChannelUpdate{
		{OwnerID: userA, Name: "first", Handle: "a", UpdatedAt: base},
		{OwnerID: userA, Name: "second", Handle: "b", UpdatedAt: base.Add(time.Second)},
		{OwnerID: userA, Name: "third", Handle: "c", UpdatedAt: base.Add(2 * time.Second)},
	}
	orders := [][]int{{0, 1, 2}, {2, 1, 0}, {1, 2, 0}, {2, 0, 1}, {0, 2, 1, 2}}
	for _, ord := range orders {
		// A document the reindex wrote before versions existed.
		doc := map[string]any{"channel_id": userB, "owner_id": userA, "name": "old", "follower_count": 7, "is_hidden": true}
		for _, i := range ord {
			doc, _ = simulateTubeEventScript(doc, tubeChannelEventBody(edits[i]))
		}
		if doc["name"] != "third" || doc["handle"] != "c" {
			t.Fatalf("order %v ended at %v, want the newest edit", ord, doc)
		}
		if doc["follower_count"] != 7 || doc["is_hidden"] != true || doc["channel_id"] != userB {
			t.Fatalf("order %v lost a field the event does not carry: %v", ord, doc)
		}
	}
	// An unversioned event never overwrites a versioned document.
	doc := map[string]any{"name": "kept", "source_updated_us": SourceVersion(base)}
	if _, wrote := simulateTubeEventScript(doc, tubeChannelEventBody(TubeChannelUpdate{OwnerID: userA, Name: "x"})); wrote {
		t.Fatal("an unversioned event overwrote a versioned document")
	}
}

func TestApplyTubeChannelEvent_UpdatesTheOwnersChannelInOrder(t *testing.T) {
	store, rec := newRecordingStore(t)
	at := time.Date(2026, 11, 15, 9, 0, 0, 123456000, time.UTC)
	if err := store.ApplyTubeChannelEvent(context.Background(), TubeChannelUpdate{
		OwnerID: userA, Name: "Riders", Handle: "riders", About: "", UpdatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	got := rec.matching(http.MethodPost, "/"+IndexTubeChannels+"/_update_by_query")
	if len(got) != 1 {
		t.Fatalf("update_by_query requests = %d, want 1", len(got))
	}
	body := got[0].json(t)
	term := body["query"].(map[string]any)["term"].(map[string]any)
	if len(term) != 1 || term["owner_id"] != userA {
		t.Fatalf("query = %v, want the owner's channel only", body["query"])
	}
	script := body["script"].(map[string]any)
	if script["source"] != tubeChannelEventScript {
		t.Fatal("not the ordered tube channel script")
	}
	params := script["params"].(map[string]any)
	if int64(params["version"].(float64)) != at.UnixMicro() {
		t.Fatalf("version = %v, want the row's updated_at %d", params["version"], at.UnixMicro())
	}
	apply := params["apply"].(map[string]any)
	if apply[string(orderOlder)] != false || apply[string(orderNewer)] != true {
		t.Fatalf("apply = %v", apply)
	}
	fields := params["fields"].(map[string]any)
	for _, k := range []string{"name", "handle", "about", "avatar_media_id", "updated_at"} {
		if _, ok := fields[k]; !ok {
			t.Errorf("fields lack %s (the event is the whole row; empty means cleared)", k)
		}
	}
	for _, k := range []string{"is_hidden", "follower_count", "channel_id", "owner_id", "reindexed_at"} {
		if _, ok := fields[k]; ok {
			t.Errorf("fields carry %s, which the event does not own", k)
		}
	}
	if len(rec.matching(http.MethodPut, "/"+IndexTubeChannels+"/_doc/")) != 0 ||
		len(rec.matching(http.MethodPost, "/"+IndexTubeChannels+"/_doc/")) != 0 {
		t.Fatal("an event created a document: it has no channel id to create it under")
	}
}

func TestApplyTubeChannelEvent_ConflictIsRetriedAndMissingIndexIsNot(t *testing.T) {
	store, rec := newRecordingStore(t)
	rec.reply = func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Path, "_update_by_query") {
			return 200, `{"total":1,"updated":0,"version_conflicts":1,"failures":[]}`
		}
		return 200, `{}`
	}
	ctx := context.Background()
	if err := store.ApplyTubeChannelEvent(ctx, TubeChannelUpdate{OwnerID: userA, UpdatedAt: time.Now()}); err == nil {
		t.Fatal("a conflicted update reported success; the edit may not have landed")
	}
	rec.reply = func(*http.Request) (int, string) { return 404, `{"error":{"type":"index_not_found_exception"}}` }
	if err := store.ApplyTubeChannelEvent(ctx, TubeChannelUpdate{OwnerID: userA, UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("no index yet is nothing to update: %v", err)
	}
	if err := store.ApplyTubeChannelEvent(ctx, TubeChannelUpdate{}); err != nil {
		t.Fatalf("no owner: %v", err)
	}
}

// The reindex rewrite keeps a stored document newer than the listing row,
// and still stamps reindexed_at so the sweep keeps it.
func TestKeepHiddenScript_AnOlderRewriteKeepsTheNewerDocument(t *testing.T) {
	clauses := []string{
		"boolean hidden = ctx._source.is_hidden == true;",
		"if (ctx._source.source_updated_us != null && params.doc.source_updated_us != null) {",
		"stale = ((Number) ctx._source.source_updated_us).longValue() > ((Number) params.doc.source_updated_us).longValue();",
		"if (stale) {\n  if (params.doc.reindexed_at != null) {\n    ctx._source.reindexed_at = params.doc.reindexed_at;",
		"} else {\n  ctx._source.clear();\n  ctx._source.putAll(params.doc);",
	}
	at := 0
	for _, c := range clauses {
		i := strings.Index(keepHiddenUpsertScript[at:], c)
		if i < 0 {
			t.Fatalf("keepHiddenUpsertScript no longer has, in order: %q", c)
		}
		at += i + len(c)
	}
	// The Tube channel document carries the version the guard reads.
	body, err := keepHiddenUpsertBody(TubeChannelDoc{ChannelID: userA, SourceUpdatedUs: 42})
	if err != nil {
		t.Fatal(err)
	}
	doc := body["script"].(map[string]any)["params"].(map[string]any)["doc"].(map[string]any)
	if doc["source_updated_us"] != float64(42) {
		t.Fatalf("doc = %v, want source_updated_us", doc)
	}
}
