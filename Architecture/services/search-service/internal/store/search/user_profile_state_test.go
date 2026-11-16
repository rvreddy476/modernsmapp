package search

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Ordered users_v1 rewrites (user_profile_state.go). The Painless half runs
// live in internal/events/user_profile_order_integration_test.go; here the
// Go rule, the script's text and a line-for-line model of it.

func TestUserWriteApplies_Rules(t *testing.T) {
	want := map[versionOrder]bool{
		orderNewer:               true,
		orderStoredUnversioned:   true,  // a document written before versions
		orderBothUnversioned:     true,  // legacy writer over legacy doc, as before
		orderSame:                false, // redelivery: nothing to do
		orderOlder:               false,
		orderIncomingUnversioned: false, // never over a versioned document
	}
	for _, o := range allVersionOrders {
		w, ok := want[o]
		if !ok {
			t.Fatalf("no expectation for %s", o)
		}
		if got := userWriteApplies(o); got != w {
			t.Errorf("userWriteApplies(%s) = %v, want %v", o, got, w)
		}
	}
	if len(userWriteApplyTable()) != len(allVersionOrders) {
		t.Fatal("params.apply is not total")
	}
}

// The script orders versions as classifyVersions does, clause for clause,
// keeps a stored hide, and writes only on Go's answer.
func TestUserProfileUpsertScript_ClassifiesLikeGoAndKeepsAHide(t *testing.T) {
	clauses := []string{
		"boolean hidden = ctx._source.is_hidden == true;",
		"stored = ((Number) ctx._source.source_updated_us).longValue();",
		"long incoming = params.version;",
		"if (stored <= 0 && incoming <= 0) {\n  order = 'both_unversioned';",
		"} else if (stored <= 0) {\n  order = 'stored_unversioned';",
		"} else if (incoming <= 0) {\n  order = 'incoming_unversioned';",
		"} else if (incoming > stored) {\n  order = 'newer';",
		"} else if (incoming == stored) {\n  order = 'same';",
		"} else {\n  order = 'older';",
		"if (params.apply.get(order) == true) {\n  ctx._source.clear();\n  ctx._source.putAll(params.doc);",
		"if (hidden) {\n    ctx._source.is_hidden = true;\n  }",
		"if (incoming > 0) {\n    ctx._source.source_updated_us = incoming;",
		"} else {\n  ctx.op = 'none';",
	}
	at := 0
	for _, c := range clauses {
		i := strings.Index(userProfileUpsertScript[at:], c)
		if i < 0 {
			t.Fatalf("userProfileUpsertScript no longer has, in order: %q", c)
		}
		at += i + len(c)
	}
}

// simulateUserUpsertScript runs one body the way userProfileUpsertScript
// does on a stored _source (nil = no document); it returns the new source
// and whether it wrote.
func simulateUserUpsertScript(t *testing.T, stored map[string]any, body map[string]any) (map[string]any, bool) {
	t.Helper()
	params := body["script"].(map[string]any)["params"].(map[string]any)
	hidden := stored["is_hidden"] == true
	var st int64
	if v, ok := stored["source_updated_us"].(int64); ok {
		st = v
	}
	in := params["version"].(int64)
	if !params["apply"].(map[string]bool)[string(classifyVersions(st, in))] {
		return stored, false
	}
	out := map[string]any{}
	for k, v := range params["doc"].(map[string]any) {
		out[k] = v
	}
	if hidden {
		out["is_hidden"] = true
	}
	if in > 0 {
		out["source_updated_us"] = in
	}
	return out, true
}

func userBody(t *testing.T, d UserDoc) map[string]any {
	t.Helper()
	b, err := userProfileUpsertBody(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var userT0 = time.Date(2026, 11, 16, 9, 0, 0, 123456000, time.UTC)

func storedUser(name string, at time.Time) map[string]any {
	return map[string]any{"user_id": userA, "display_name": name, "source_updated_us": SourceVersion(at)}
}

func TestUserProfileWrite_OlderIsSkipped(t *testing.T) {
	stored := storedUser("newer", userT0.Add(time.Second))
	got, wrote := simulateUserUpsertScript(t, stored, userBody(t, UserDoc{
		UserID: userA, DisplayName: "older", SourceVersion: SourceVersion(userT0),
	}))
	if wrote || got["display_name"] != "newer" || got["source_updated_us"] != SourceVersion(userT0.Add(time.Second)) {
		t.Fatalf("an older event rewrote the document: %v (wrote %v)", got, wrote)
	}
}

func TestUserProfileWrite_NewerApplies(t *testing.T) {
	stored := storedUser("older", userT0)
	got, wrote := simulateUserUpsertScript(t, stored, userBody(t, UserDoc{
		UserID: userA, DisplayName: "newer", SourceVersion: SourceVersion(userT0.Add(time.Millisecond)),
	}))
	if !wrote || got["display_name"] != "newer" || got["source_updated_us"] != SourceVersion(userT0.Add(time.Millisecond)) {
		t.Fatalf("a newer event did not apply at its version: %v (wrote %v)", got, wrote)
	}
}

func TestUserProfileWrite_EqualIsNoop(t *testing.T) {
	stored := storedUser("applied", userT0)
	got, wrote := simulateUserUpsertScript(t, stored, userBody(t, UserDoc{
		UserID: userA, DisplayName: "redelivered", SourceVersion: SourceVersion(userT0),
	}))
	if wrote || got["display_name"] != "applied" {
		t.Fatalf("a redelivery at the same version rewrote the document: %v", got)
	}
}

// An unversioned writer never overwrites a versioned document, and still
// writes over an unversioned one (documents from before 2026-11-16).
func TestUserProfileWrite_UnversionedRules(t *testing.T) {
	if _, wrote := simulateUserUpsertScript(t, storedUser("kept", userT0), userBody(t, UserDoc{UserID: userA, DisplayName: "x"})); wrote {
		t.Fatal("an unversioned write overwrote a versioned document")
	}
	legacy := map[string]any{"user_id": userA, "display_name": "legacy"}
	got, wrote := simulateUserUpsertScript(t, legacy, userBody(t, UserDoc{UserID: userA, DisplayName: "v", SourceVersion: SourceVersion(userT0)}))
	if !wrote || got["display_name"] != "v" || got["source_updated_us"] != SourceVersion(userT0) {
		t.Fatalf("a versioned write did not stamp an unversioned document: %v", got)
	}
	got, wrote = simulateUserUpsertScript(t, nil, userBody(t, UserDoc{UserID: userA, DisplayName: "first"}))
	if !wrote || got["display_name"] != "first" {
		t.Fatalf("a first unversioned write did not create the document: %v", got)
	}
	if _, ok := got["source_updated_us"]; ok {
		t.Fatalf("an unversioned write stamped a version: %v", got)
	}
}

// The lifecycle hide survives every ordered rewrite, including the first
// versioned one over a hidden stub (UpdateUserHidden before any profile).
func TestUserProfileWrite_KeepsAStoredHide(t *testing.T) {
	stub := map[string]any{"user_id": userA, "is_hidden": true}
	got, wrote := simulateUserUpsertScript(t, stub, userBody(t, UserDoc{UserID: userA, DisplayName: "late", SourceVersion: SourceVersion(userT0)}))
	if !wrote || got["is_hidden"] != true || got["display_name"] != "late" {
		t.Fatalf("the first profile over a hidden stub = %v (wrote %v), want written and still hidden", got, wrote)
	}
	got, _ = simulateUserUpsertScript(t, got, userBody(t, UserDoc{UserID: userA, DisplayName: "edit", SourceVersion: SourceVersion(userT0.Add(time.Second))}))
	if got["is_hidden"] != true || got["display_name"] != "edit" {
		t.Fatalf("a newer edit unhid a deactivated account: %v", got)
	}
}

func TestUserProfileWrite_AnyArrivalOrderEndsAtTheNewest(t *testing.T) {
	edits := []UserDoc{
		{UserID: userA, DisplayName: "first", SourceVersion: SourceVersion(userT0)},
		{UserID: userA, DisplayName: "second", SourceVersion: SourceVersion(userT0.Add(time.Second))},
		{UserID: userA, DisplayName: "third", SourceVersion: SourceVersion(userT0.Add(2 * time.Second))},
	}
	for _, ord := range [][]int{{0, 1, 2}, {2, 1, 0}, {1, 2, 0}, {2, 0, 1}, {0, 2, 1, 2}} {
		var doc map[string]any
		for _, i := range ord {
			doc, _ = simulateUserUpsertScript(t, doc, userBody(t, edits[i]))
		}
		if doc["display_name"] != "third" || doc["source_updated_us"] != edits[2].SourceVersion {
			t.Fatalf("order %v ended at %v, want the newest edit", ord, doc)
		}
	}
}

// The users reindex stamps a page with the time it was read (reindex.go).
// Events for edits before that read lose to it; events for edits after it
// win; and a page written late, after a newer event landed, is skipped.
func TestUserProfileWrite_ReindexThenLiveEventOrdering(t *testing.T) {
	read := userT0
	page := UserDoc{UserID: userA, DisplayName: "from reindex", SourceVersion: SourceVersion(read)}
	doc, _ := simulateUserUpsertScript(t, nil, userBody(t, page))

	// An edit committed before the read, its event delivered late.
	doc, wrote := simulateUserUpsertScript(t, doc, userBody(t, UserDoc{
		UserID: userA, DisplayName: "stale edit", SourceVersion: SourceVersion(read.Add(-time.Second)),
	}))
	if wrote || doc["display_name"] != "from reindex" {
		t.Fatalf("an edit older than the reindex read rewrote it: %v", doc)
	}
	// An edit after the read.
	doc, wrote = simulateUserUpsertScript(t, doc, userBody(t, UserDoc{
		UserID: userA, DisplayName: "live edit", SourceVersion: SourceVersion(read.Add(time.Second)),
	}))
	if !wrote || doc["display_name"] != "live edit" {
		t.Fatalf("an edit after the reindex read did not apply: %v", doc)
	}
	// The same reindex page retried after the edit landed, and the next
	// reindex that reads after the edit.
	doc, wrote = simulateUserUpsertScript(t, doc, userBody(t, page))
	if wrote || doc["display_name"] != "live edit" {
		t.Fatalf("a late reindex page rewrote a newer edit: %v", doc)
	}
	next := UserDoc{UserID: userA, DisplayName: "live edit", SourceVersion: SourceVersion(read.Add(time.Minute))}
	if doc, wrote = simulateUserUpsertScript(t, doc, userBody(t, next)); !wrote || doc["source_updated_us"] != next.SourceVersion {
		t.Fatalf("the next reindex did not take over the version: %v", doc)
	}
}

// Both writers send the ordered script with the version in params and in
// the doc; the version is never on a search result's wire.
func TestUserWrites_CarryTheVersion(t *testing.T) {
	store, rec := newRecordingStore(t)
	ctx := context.Background()
	v := SourceVersion(userT0)
	if err := store.IndexUser(ctx, UserDoc{UserID: userA, Username: "asha", SourceVersion: v}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BulkIndexUsers(ctx, []UserDoc{{UserID: userB, Username: "ravi", SourceVersion: v}}); err != nil {
		t.Fatal(err)
	}
	single := rec.matching(http.MethodPost, "/"+IndexUsers+"/_update/"+userA)
	bulk := rec.matching(http.MethodPost, "_bulk")
	if len(single) != 1 || len(bulk) != 1 {
		t.Fatalf("requests: %d single, %d bulk", len(single), len(bulk))
	}
	lines := strings.Split(strings.TrimSpace(string(bulk[0].raw)), "\n")
	for name, raw := range map[string][]byte{"IndexUser": single[0].raw, "BulkIndexUsers": []byte(lines[1])} {
		var body struct {
			Script struct {
				Source string `json:"source"`
				Params struct {
					Version int64           `json:"version"`
					Apply   map[string]bool `json:"apply"`
					Doc     map[string]any  `json:"doc"`
				} `json:"params"`
			} `json:"script"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		p := body.Script.Params
		if body.Script.Source != userProfileUpsertScript || p.Version != v || p.Doc["source_updated_us"] != float64(v) {
			t.Fatalf("%s did not send the ordered write at version %d: %s", name, v, raw)
		}
		if p.Apply[string(orderOlder)] || p.Apply[string(orderSame)] || !p.Apply[string(orderNewer)] {
			t.Fatalf("%s apply = %v", name, p.Apply)
		}
	}

	wire, _ := json.Marshal(UserDoc{UserID: userA, SourceVersion: v})
	if strings.Contains(string(wire), "source_updated_us") {
		t.Fatalf("the version is on a user result's wire: %s", wire)
	}
}
