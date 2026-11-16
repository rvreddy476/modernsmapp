package search

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

// The create-by-id path of a Tube channel event (tube_channel_state.go,
// CREATE BY ID / HIDDEN ON CREATE). The Painless half runs live in
// internal/events/tube_channel_order_integration_test.go.

const testTubeChannel = "3f2a1b0c-9d8e-4f7a-8b6c-5d4e3f2a1b01"

// tubeOwnerState scripts the three answers the id path reads and writes:
// the author fence, the owner's users_v1 document (hidden per call, in
// order; the last answer repeats) and the _update result.
type tubeOwnerState struct {
	fenced      []bool
	hidden      []bool // nil: no users_v1 document (a confirmed miss)
	usersErr    bool
	result      string
	fenceCalls  int
	usersCalls  int
	missingTube bool
}

func pick(seq []bool, i int) bool {
	if len(seq) == 0 {
		return false
	}
	if i >= len(seq) {
		i = len(seq) - 1
	}
	return seq[i]
}

func (st *tubeOwnerState) reply(r *http.Request) (int, string) {
	switch {
	case r.Method == http.MethodHead && strings.Contains(r.URL.Path, IndexTubeChannels):
		if st.missingTube {
			return 404, ``
		}
		return 200, ``
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/"+IndexAuthorFences+"/_doc/"):
		f := pick(st.fenced, st.fenceCalls)
		st.fenceCalls++
		if f {
			return 200, `{"_index":"author_fences_v1","found":true,"_source":{"author_id":"x"}}`
		}
		return 404, `{"_index":"author_fences_v1","found":false}`
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/"+IndexUsers+"/_doc/"):
		i := st.usersCalls
		st.usersCalls++
		if st.usersErr {
			return 404, `{"error":{"type":"index_not_found_exception"},"status":404}`
		}
		if st.hidden == nil {
			return 404, `{"_index":"users_v1","found":false}`
		}
		if pick(st.hidden, i) {
			return 200, `{"_index":"users_v1","found":true,"_source":{"is_hidden":true}}`
		}
		return 200, `{"_index":"users_v1","found":true,"_source":{}}`
	case strings.Contains(r.URL.Path, "/"+IndexTubeChannels+"/_update/"):
		res := st.result
		if res == "" {
			res = "created"
		}
		return 200, `{"result":"` + res + `"}`
	case strings.Contains(r.URL.Path, "/"+IndexTubeChannels+"/_doc/") && r.Method == http.MethodDelete:
		return 200, `{"result":"deleted"}`
	}
	return 200, `{}`
}

func newTubeOwnerStore(t *testing.T, st *tubeOwnerState) (*Store, *recordingServer) {
	t.Helper()
	store, rec := newRecordingStore(t)
	rec.reply = st.reply
	return store, rec
}

func tubeEvent(at time.Time) TubeChannelUpdate {
	return TubeChannelUpdate{
		ChannelID: testTubeChannel, OwnerID: userA, Name: "Riders", Handle: "riders",
		About: "bikes", AvatarMediaID: userB,
		CreatedAt: at.Add(-time.Hour), UpdatedAt: at,
	}
}

func tubeUpdates(rec *recordingServer) []osRequest {
	return rec.matching(http.MethodPost, "/"+IndexTubeChannels+"/_update/"+testTubeChannel)
}

func TestApplyTubeChannelEvent_CreatesTheChannelWhenAbsent(t *testing.T) {
	st := &tubeOwnerState{}
	store, rec := newTubeOwnerStore(t, st)
	at := time.Date(2026, 11, 16, 9, 0, 0, 123456000, time.UTC)
	if err := store.ApplyTubeChannelEvent(context.Background(), tubeEvent(at)); err != nil {
		t.Fatal(err)
	}
	if n := len(rec.matching(http.MethodPost, "_update_by_query")); n != 0 {
		t.Fatalf("update_by_query requests = %d, want the id path only", n)
	}
	got := tubeUpdates(rec)
	if len(got) != 1 {
		t.Fatalf("_update requests = %d, want 1 (the upsert; no correction when the owner state held)", len(got))
	}
	body := got[0].json(t)
	if _, scripted := body["scripted_upsert"]; scripted {
		t.Fatal("scripted_upsert: the create must be the document as is, not run through the merge")
	}
	if body["script"].(map[string]any)["source"] != tubeChannelEventScript {
		t.Fatal("an existing document is not merged by the ordered script")
	}
	doc := body["upsert"].(map[string]any)

	// The shape the Tube reindex writes (TubeChannelDoc with reindexed_at)
	// plus is_hidden.
	ref, _ := json.Marshal(TubeChannelDoc{About: "x", AvatarMediaID: "x", SourceUpdatedUs: 1, ReindexedAt: &at})
	var refKeys map[string]any
	_ = json.Unmarshal(ref, &refKeys)
	refKeys["is_hidden"] = false
	if a, b := sortedKeys(doc), sortedKeys(refKeys); strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("upsert keys = %v, want the reindex document's %v", a, b)
	}
	want := map[string]any{
		"channel_id": testTubeChannel, "owner_id": userA, "name": "Riders", "handle": "riders",
		"about": "bikes", "avatar_media_id": userB, "follower_count": float64(0), "is_hidden": false,
		"source_updated_us": float64(at.UnixMicro()),
	}
	for k, v := range want {
		if doc[k] != v {
			t.Errorf("upsert %s = %v, want %v", k, doc[k], v)
		}
	}
	if doc["created_at"] != at.Add(-time.Hour).Format(time.RFC3339Nano) {
		t.Errorf("created_at = %v", doc["created_at"])
	}
	if _, err := time.Parse(time.RFC3339Nano, doc["reindexed_at"].(string)); err != nil {
		t.Errorf("reindexed_at = %v: a running reindex's sweep would delete the new channel", doc["reindexed_at"])
	}
	// Created: the owner state is read again (fence and users_v1 twice).
	if st.fenceCalls != 2 || st.usersCalls != 2 {
		t.Fatalf("fence reads = %d, users reads = %d; want 2 and 2 (before and after the create)", st.fenceCalls, st.usersCalls)
	}
}

func TestApplyTubeChannelEvent_UpdatesWhenPresent(t *testing.T) {
	st := &tubeOwnerState{result: "updated", hidden: []bool{false}}
	store, rec := newTubeOwnerStore(t, st)
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	if err := store.ApplyTubeChannelEvent(context.Background(), tubeEvent(at)); err != nil {
		t.Fatal(err)
	}
	got := tubeUpdates(rec)
	if len(got) != 1 {
		t.Fatalf("_update requests = %d, want 1", len(got))
	}
	params := got[0].json(t)["script"].(map[string]any)["params"].(map[string]any)
	fields := params["fields"].(map[string]any)
	if fields["name"] != "Riders" || fields["handle"] != "riders" {
		t.Fatalf("fields = %v", fields)
	}
	for _, k := range []string{"is_hidden", "follower_count", "channel_id", "owner_id", "reindexed_at"} {
		if _, ok := fields[k]; ok {
			t.Errorf("the merge carries %s, which only the create may set", k)
		}
	}
	if st.usersCalls != 1 || st.fenceCalls != 1 {
		t.Fatalf("an update re-read the owner state (%d fence, %d users)", st.fenceCalls, st.usersCalls)
	}
}

// An older (or unversioned-over-versioned) event is refused by the merge,
// and OpenSearch's "noop" is success.
func TestApplyTubeChannelEvent_OlderEventIsANoop(t *testing.T) {
	base := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	newer, err := tubeChannelUpsertBody(tubeEvent(base.Add(time.Second)), false, base)
	if err != nil {
		t.Fatal(err)
	}
	stored := newer["upsert"].(map[string]any)
	stored["source_updated_us"] = SourceVersion(base.Add(time.Second))
	older := tubeEvent(base)
	older.Name = "stale"
	olderBody, _ := tubeChannelUpsertBody(older, false, base)
	if _, wrote := simulateTubeEventScript(stored, map[string]any{"script": olderBody["script"]}); wrote {
		t.Fatal("an older event merged over a newer document")
	}
	same := tubeEvent(base.Add(time.Second))
	same.Name = "redelivered"
	sameBody, _ := tubeChannelUpsertBody(same, false, base)
	if out, wrote := simulateTubeEventScript(stored, map[string]any{"script": sameBody["script"]}); !wrote || out["name"] != "redelivered" {
		t.Fatal("an equal-version redelivery must apply")
	}

	st := &tubeOwnerState{result: "noop"}
	store, rec := newTubeOwnerStore(t, st)
	if err := store.ApplyTubeChannelEvent(context.Background(), older); err != nil {
		t.Fatalf("noop is success: %v", err)
	}
	if len(tubeUpdates(rec)) != 1 || st.usersCalls != 1 {
		t.Fatal("a noop was followed by a correction")
	}
}

func TestApplyTubeChannelEvent_HiddenOwnerCreatesAHiddenDocument(t *testing.T) {
	st := &tubeOwnerState{hidden: []bool{true}}
	store, rec := newTubeOwnerStore(t, st)
	if err := store.ApplyTubeChannelEvent(context.Background(), tubeEvent(time.Now())); err != nil {
		t.Fatal(err)
	}
	got := tubeUpdates(rec)
	if len(got) != 1 {
		t.Fatalf("_update requests = %d, want 1", len(got))
	}
	if doc := got[0].json(t)["upsert"].(map[string]any); doc["is_hidden"] != true {
		t.Fatalf("upsert is_hidden = %v, want true for a hidden owner", doc["is_hidden"])
	}
}

// A lifecycle change between the read and the create is caught by the
// second read: the new document is corrected.
func TestApplyTubeChannelEvent_CreateRacingAHideIsCorrected(t *testing.T) {
	st := &tubeOwnerState{hidden: []bool{false, true}}
	store, rec := newTubeOwnerStore(t, st)
	if err := store.ApplyTubeChannelEvent(context.Background(), tubeEvent(time.Now())); err != nil {
		t.Fatal(err)
	}
	got := tubeUpdates(rec)
	if len(got) != 2 {
		t.Fatalf("_update requests = %d, want the upsert and one correction", len(got))
	}
	fix := got[1].json(t)
	if d, _ := fix["doc"].(map[string]any); len(d) != 1 || d["is_hidden"] != true {
		t.Fatalf("correction = %v, want doc is_hidden true only", fix)
	}
}

func TestApplyTubeChannelEvent_ErasedOwnerGetsNoDocument(t *testing.T) {
	st := &tubeOwnerState{fenced: []bool{true}}
	store, rec := newTubeOwnerStore(t, st)
	if err := store.ApplyTubeChannelEvent(context.Background(), tubeEvent(time.Now())); err != nil {
		t.Fatal(err)
	}
	if n := len(tubeUpdates(rec)); n != 0 {
		t.Fatalf("_update requests = %d for an erased owner", n)
	}
	// Erased after the create: the new document is deleted.
	st2 := &tubeOwnerState{fenced: []bool{false, true}}
	store2, rec2 := newTubeOwnerStore(t, st2)
	if err := store2.ApplyTubeChannelEvent(context.Background(), tubeEvent(time.Now())); err != nil {
		t.Fatal(err)
	}
	if n := len(rec2.matching(http.MethodDelete, "/"+IndexTubeChannels+"/_doc/"+testTubeChannel)); n != 1 {
		t.Fatalf("deletes = %d, want the created document removed", n)
	}
}

// An unknown owner state fails the event (redelivered); nothing is written.
func TestApplyTubeChannelEvent_UnknownOwnerStateIsRetried(t *testing.T) {
	st := &tubeOwnerState{usersErr: true}
	store, rec := newTubeOwnerStore(t, st)
	if err := store.ApplyTubeChannelEvent(context.Background(), tubeEvent(time.Now())); err == nil {
		t.Fatal("an unreadable owner state reported success")
	}
	if n := len(tubeUpdates(rec)); n != 0 {
		t.Fatalf("_update requests = %d with the owner state unknown", n)
	}
	// No index yet: nothing to write, as on the owner path.
	st2 := &tubeOwnerState{missingTube: true}
	store2, rec2 := newTubeOwnerStore(t, st2)
	if err := store2.ApplyTubeChannelEvent(context.Background(), tubeEvent(time.Now())); err != nil {
		t.Fatalf("missing index: %v", err)
	}
	if len(tubeUpdates(rec2)) != 0 || st2.fenceCalls != 0 {
		t.Fatal("an upsert into a missing index would auto-create it unmapped")
	}
}

func TestApplyTubeChannelEvent_NoChannelIDStaysUpdateOnly(t *testing.T) {
	st := &tubeOwnerState{}
	store, rec := newTubeOwnerStore(t, st)
	rec.reply = func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Path, "_update_by_query") {
			return 200, `{"total":0,"updated":0,"version_conflicts":0,"failures":[]}`
		}
		return st.reply(r)
	}
	u := tubeEvent(time.Now())
	u.ChannelID = ""
	if err := store.ApplyTubeChannelEvent(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if n := len(rec.matching(http.MethodPost, "/"+IndexTubeChannels+"/_update_by_query")); n != 1 {
		t.Fatalf("update_by_query requests = %d, want 1", n)
	}
	if len(rec.matching(http.MethodPost, "/_update/")) != 0 || st.usersCalls != 0 || st.fenceCalls != 0 {
		t.Fatal("an event without channel_id took the create path")
	}
	// A malformed channel_id falls back to the owner, never into a URL.
	u.ChannelID = "../users_v1/_doc/x"
	if err := store.ApplyTubeChannelEvent(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if len(rec.matching(http.MethodPost, "/_update/")) != 0 {
		t.Fatal("a malformed channel_id reached the id path")
	}
}

func TestOwnerHiddenFromGet(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		hidden  bool
		wantErr bool
	}{
		{"hidden", 200, `{"found":true,"_source":{"is_hidden":true}}`, true, false},
		{"visible", 200, `{"found":true,"_source":{"is_hidden":false}}`, false, false},
		{"no field", 200, `{"found":true,"_source":{}}`, false, false},
		{"confirmed miss", 404, `{"_index":"users_v1","found":false}`, false, false},
		{"index missing", 404, `{"error":{"type":"index_not_found_exception"},"status":404}`, false, true},
		{"bare 404", 404, `{}`, false, true},
		{"unreadable", 502, `<html>`, false, true},
		{"200 without found", 200, `{}`, false, true},
	}
	for _, c := range cases {
		got, err := ownerHiddenFromGet(c.status, []byte(c.body))
		if (err != nil) != c.wantErr || got != c.hidden {
			t.Errorf("%s: got (%v, %v), want (%v, err %v)", c.name, got, err, c.hidden, c.wantErr)
		}
	}
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
