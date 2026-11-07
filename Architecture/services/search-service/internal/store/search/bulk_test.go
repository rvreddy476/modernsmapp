package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Audit 2026-11-07: S-H1 (bulk-action injection), S-M1 (rewrites that drop
// is_hidden / author_is_private), S-M2 (community filters) and S-M3 (owner
// lifecycle). Wire-level tests against a recording server; the Painless
// halves are also covered live by owner_visibility_integration_test.go.

const (
	userA = "0f3c6a52-7d1e-4c5b-9a8f-2b6e4d1c0a11"
	userB = "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
)

// craftedID is the S-H1 payload: it closes the action line and adds a
// delete of an erasure fence and a planted "approved" post.
const craftedID = "x\"}}\n{\"delete\":{\"_index\":\"author_fences_v1\",\"_id\":\"victim\"}}\n" +
	"{\"index\":{\"_index\":\"posts_v1\",\"_id\":\"p\"}}\n{\"visibility\":\"public\",\"review_status\":\"approved\"}\n" +
	"{\"index\":{\"_index\":\"users_v1\",\"_id\":\"y"

type osRequest struct {
	method, path string
	raw          []byte
}

func (r osRequest) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.raw, &m); err != nil {
		t.Fatalf("%s %s: body is not JSON: %s", r.method, r.path, r.raw)
	}
	return m
}

type recordingServer struct {
	mu    sync.Mutex
	reqs  []osRequest
	reply func(r *http.Request) (int, string)
}

func newRecordingStore(t *testing.T) (*Store, *recordingServer) {
	t.Helper()
	rec := &recordingServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		if r.Method != http.MethodHead {
			rec.reqs = append(rec.reqs, osRequest{r.Method, r.URL.Path, raw})
		}
		reply := rec.reply
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		status, body := http.StatusOK, `{"errors":false,"items":[],"result":"ok"}`
		if reply != nil {
			status, body = reply(r)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	store, err := New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec.mu.Lock()
	rec.reqs = nil // drop index bootstrap traffic
	rec.mu.Unlock()
	return store, rec
}

func (rec *recordingServer) matching(method, pathPart string) []osRequest {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var out []osRequest
	for _, r := range rec.reqs {
		if r.method == method && strings.Contains(r.path, pathPart) {
			out = append(out, r)
		}
	}
	return out
}

// ─── S-H1 ──────────────────────────────────────────────────────────────

// The action line is marshalled, so whatever the id holds it is one line
// and decodes back to exactly that id: it cannot end the object or add an
// action.
func TestEncodeBulkAction_CraftedIDCannotAddALine(t *testing.T) {
	line, err := encodeBulkAction(bulkAction{Index: &bulkTarget{Index: IndexUsers, ID: craftedID}})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.ContainsAny(line, "\n\r") {
		t.Fatalf("action line contains a line break: %s", line)
	}
	var back map[string]map[string]string
	if err := json.Unmarshal(line, &back); err != nil {
		t.Fatalf("action line is not one JSON object: %s", line)
	}
	if len(back) != 1 || back["index"]["_id"] != craftedID || back["index"]["_index"] != IndexUsers {
		t.Fatalf("action line decodes to %v", back)
	}
}

func TestBulkBodies_RefuseIDsThatAreNotCanonicalUUIDs(t *testing.T) {
	bad := []string{
		craftedID,
		"",
		"not-a-uuid",
		strings.ToUpper(userA),
		"{" + userA + "}",
		"urn:uuid:" + userA,
		userA + "\n",
	}
	builders := map[string]bulkBodyBuilder{"index": buildBulkIndexBody, "keep-hidden": buildBulkKeepHiddenBody}
	for name, build := range builders {
		for _, id := range bad {
			body, err := build(IndexUsers, []string{userA, id}, []any{UserDoc{UserID: userA}, UserDoc{UserID: id}})
			if !errors.Is(err, ErrInvalidBulkID) {
				t.Fatalf("%s builder accepted id %q (err %v)", name, id, err)
			}
			if body != nil {
				t.Fatalf("%s builder returned a partial body for id %q", name, id)
			}
		}
	}
}

// Two lines per document, every line one JSON object, no matter what the
// document's own fields hold.
func TestBulkBodies_OneActionPerDocument(t *testing.T) {
	docs := []any{
		UserDoc{UserID: userA, Bio: "line one\n{\"delete\":{\"_index\":\"posts_v1\",\"_id\":\"p\"}}"},
		UserDoc{UserID: userB, DisplayName: "\"}}\n"},
	}
	for name, build := range map[string]bulkBodyBuilder{"index": buildBulkIndexBody, "keep-hidden": buildBulkKeepHiddenBody} {
		body, err := build(IndexUsers, []string{userA, userB}, docs)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
		if len(lines) != 4 {
			t.Fatalf("%s: %d lines for 2 documents:\n%s", name, len(lines), body)
		}
		for i := 0; i < len(lines); i += 2 {
			var action map[string]map[string]any
			if err := json.Unmarshal([]byte(lines[i]), &action); err != nil || len(action) != 1 {
				t.Fatalf("%s: line %d is not one action: %s", name, i, lines[i])
			}
			var src map[string]any
			if err := json.Unmarshal([]byte(lines[i+1]), &src); err != nil {
				t.Fatalf("%s: line %d is not a source object: %s", name, i+1, lines[i+1])
			}
		}
	}
}

func TestBulkIndexUsers_CraftedIDSendsNothing(t *testing.T) {
	store, rec := newRecordingStore(t)
	_, err := store.BulkIndexUsers(context.Background(), []UserDoc{{UserID: userA}, {UserID: craftedID}})
	if !errors.Is(err, ErrInvalidBulkID) {
		t.Fatalf("BulkIndexUsers accepted a crafted id: %v", err)
	}
	if got := rec.matching(http.MethodPost, "_bulk"); len(got) != 0 {
		t.Fatalf("a bulk request was sent for a batch with a crafted id: %s", got[0].raw)
	}
}

// ─── S-M1: users keep is_hidden ─────────────────────────────────────────

func TestKeepHiddenScript_KeepsAStoredHide(t *testing.T) {
	for _, want := range []string{
		"boolean hidden = ctx._source.is_hidden == true;",
		"ctx._source.putAll(params.doc);",
		"if (hidden) {\n  ctx._source.is_hidden = true;\n}",
	} {
		if !strings.Contains(keepHiddenUpsertScript, want) {
			t.Fatalf("keepHiddenUpsertScript lost %q:\n%s", want, keepHiddenUpsertScript)
		}
	}
	// The stored flag is read before the source is cleared.
	if strings.Index(keepHiddenUpsertScript, "boolean hidden") > strings.Index(keepHiddenUpsertScript, "ctx._source.clear()") {
		t.Fatal("the stored is_hidden must be read before the document is cleared")
	}
}

// Every user write (the bulk reindex and the single profile event) is the
// keep-hidden scripted upsert, never a plain index replace.
func TestUserWrites_AreKeepHiddenUpserts(t *testing.T) {
	store, rec := newRecordingStore(t)
	ctx := context.Background()
	if _, err := store.BulkIndexUsers(ctx, []UserDoc{{UserID: userA, Username: "asha"}}); err != nil {
		t.Fatal(err)
	}
	bulk := rec.matching(http.MethodPost, "_bulk")
	if len(bulk) != 1 {
		t.Fatalf("bulk requests = %d, want 1", len(bulk))
	}
	lines := strings.Split(strings.TrimSpace(string(bulk[0].raw)), "\n")
	var action map[string]map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &action)
	if _, ok := action["update"]; !ok || len(action) != 1 {
		t.Fatalf("users bulk action = %s; a plain index replace resets is_hidden", lines[0])
	}
	assertKeepHiddenBody(t, []byte(lines[1]), userA)

	if err := store.IndexUser(ctx, UserDoc{UserID: userA, Username: "asha"}); err != nil {
		t.Fatal(err)
	}
	single := rec.matching(http.MethodPost, "/"+IndexUsers+"/_update/"+userA)
	if len(single) != 1 {
		t.Fatalf("IndexUser did not send a scripted update: %+v", rec.reqs)
	}
	assertKeepHiddenBody(t, single[0].raw, userA)
}

func assertKeepHiddenBody(t *testing.T, raw []byte, userID string) {
	t.Helper()
	var body struct {
		ScriptedUpsert bool `json:"scripted_upsert"`
		Script         struct {
			Source string `json:"source"`
			Params struct {
				Doc map[string]any `json:"doc"`
			} `json:"params"`
		} `json:"script"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("update body: %v: %s", err, raw)
	}
	if !body.ScriptedUpsert || body.Script.Source != keepHiddenUpsertScript || body.Script.Params.Doc["user_id"] != userID {
		t.Fatalf("not the keep-hidden upsert: %s", raw)
	}
}

// Hiding a user with no document yet writes a hidden stub, so a later
// profile event cannot create them visible. Unhiding never creates one.
func TestUpdateUserHidden_HideUpsertsAStub(t *testing.T) {
	store, rec := newRecordingStore(t)
	ctx := context.Background()
	if err := store.UpdateUserHidden(ctx, userA, true); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateUserHidden(ctx, userB, false); err != nil {
		t.Fatal(err)
	}
	hide := rec.matching(http.MethodPost, "/_update/"+userA)
	if len(hide) != 1 {
		t.Fatal("hide sent no update")
	}
	up, _ := hide[0].json(t)["upsert"].(map[string]any)
	if up["is_hidden"] != true || up["user_id"] != userA {
		t.Fatalf("hide must upsert a hidden stub, got %s", hide[0].raw)
	}
	unhide := rec.matching(http.MethodPost, "/_update/"+userB)
	if len(unhide) != 1 {
		t.Fatal("unhide sent no update")
	}
	if _, ok := unhide[0].json(t)["upsert"]; ok {
		t.Fatalf("unhide must not create a document: %s", unhide[0].raw)
	}
}

// ─── S-M1: posts carry the author's visibility ──────────────────────────

func TestProjectionValidate_EligibleWriteNeedsAuthorVisibility(t *testing.T) {
	p := PostProjection{PostID: "p1", Rev: 1, Doc: PostDoc{PostID: "p1"}}
	if err := p.validate(); !errors.Is(err, ErrAuthorVisibilityUnresolved) {
		t.Fatalf("an eligible write without the author's visibility was accepted: %v", err)
	}
	for _, removal := range []PostProjection{
		{PostID: "p1", Rev: 2, Removed: true},
		{PostID: "p1", AutoRev: true, Removed: true},
	} {
		if err := removal.validate(); err != nil {
			t.Fatalf("a removal must never need the author's visibility: %v", err)
		}
	}
}

// The author's flags are written over whatever the document says, and the
// script keeps a stored hide on every rewrite, a tombstone included.
func TestApplyPostProjection_WritesTheAuthorVisibility(t *testing.T) {
	store, rec := newRecordingStore(t)
	err := store.ApplyPostProjection(context.Background(), PostProjection{
		PostID: "p1", Rev: 1,
		Doc:    PostDoc{PostID: "p1", Visibility: "public", ReviewStatus: "approved", AuthorIsPrivate: false, IsHidden: false},
		Author: &AuthorVisibility{IsPrivate: true, IsHidden: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	sent := rec.matching(http.MethodPost, "/"+IndexPosts+"/_update/p1")
	if len(sent) != 1 {
		t.Fatal("no projection request")
	}
	var body struct {
		Script struct {
			Params struct {
				Doc map[string]any `json:"doc"`
			} `json:"params"`
		} `json:"script"`
	}
	_ = json.Unmarshal(sent[0].raw, &body)
	if body.Script.Params.Doc["author_is_private"] != true || body.Script.Params.Doc["is_hidden"] != true {
		t.Fatalf("projection doc = %v, want the author's private and hidden flags", body.Script.Params.Doc)
	}
}

func TestPostProjectionScript_KeepsAStoredHide(t *testing.T) {
	read := "boolean storedHidden = ctx._source.containsKey('is_hidden') && ctx._source.is_hidden == true;"
	keep := "if (storedHidden) {\n    next.put('is_hidden', true);\n  }"
	if !strings.Contains(applyPostProjectionScript, read) || !strings.Contains(applyPostProjectionScript, keep) {
		t.Fatalf("the projection script no longer keeps a stored is_hidden:\n%s", applyPostProjectionScript)
	}
	// Outside the removed/doc branch, so a tombstone keeps it too.
	if strings.Index(applyPostProjectionScript, keep) < strings.Index(applyPostProjectionScript, "next.putAll(params.doc);") {
		t.Fatal("the hide is applied before the document is copied in, so the copy would overwrite it")
	}
}

// ─── S-M2: communities ──────────────────────────────────────────────────

func TestAutocompleteCommunities_OnlyPublicActive(t *testing.T) {
	store, rec := newRecordingStore(t)
	rec.reply = func(*http.Request) (int, string) { return 200, `{"hits":{"hits":[]}}` }
	if _, err := store.autocompleteCommunities(context.Background(), "rid", 5); err != nil {
		t.Fatal(err)
	}
	sent := rec.matching(http.MethodPost, "/"+IndexCommunities+"/_search")
	if len(sent) != 1 {
		t.Fatalf("no community autocomplete query: %+v", rec.reqs)
	}
	// Read the filter where OpenSearch reads it: query.bool.filter. A
	// substring check passed with the filter under a key nothing reads.
	var body struct {
		Query struct {
			Bool struct {
				Filter json.RawMessage `json:"filter"`
			} `json:"bool"`
		} `json:"query"`
	}
	if err := json.Unmarshal(sent[0].raw, &body); err != nil {
		t.Fatalf("query is not JSON: %v", err)
	}
	f := string(body.Query.Bool.Filter)
	if !strings.Contains(f, `"community_type":["public","education","professional"]`) || !strings.Contains(f, `"status":"active"`) {
		t.Fatalf("community autocomplete has no type/status filter in query.bool.filter: %s", sent[0].raw)
	}
}

func TestIndexCommunityAndChannel_DeleteWhatIsNotSearchable(t *testing.T) {
	store, rec := newRecordingStore(t)
	ctx := context.Background()
	if err := store.IndexCommunity(ctx, CommunityDoc{CommunityID: userA, CommunityType: "public", Status: "suspended"}); err != nil {
		t.Fatal(err)
	}
	if err := store.IndexChannel(ctx, ChannelDoc{ChannelID: userB, ChannelType: "public", Status: "suspended"}); err != nil {
		t.Fatal(err)
	}
	if got := rec.matching(http.MethodPut, "/_doc/"); len(got) != 0 {
		t.Fatalf("a suspended community or channel was written: %s", got[0].path)
	}
	if len(rec.matching(http.MethodDelete, "/"+IndexCommunities+"/_doc/"+userA)) != 1 ||
		len(rec.matching(http.MethodDelete, "/"+IndexChannels+"/_doc/"+userB)) != 1 {
		t.Fatalf("not deleted: %+v", rec.reqs)
	}
}

// ─── S-M3: what the person owns ─────────────────────────────────────────

func TestUpdateOwnerTubeHidden_BothTubeIndicesAndConflictsRetry(t *testing.T) {
	store, rec := newRecordingStore(t)
	rec.reply = func(*http.Request) (int, string) { return 200, `{"updated":1}` }
	if err := store.UpdateOwnerTubeHidden(context.Background(), userA, true); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{IndexTubeChannels, IndexTubeCollections} {
		got := rec.matching(http.MethodPost, "/"+index+"/_update_by_query")
		if len(got) != 1 || !strings.Contains(string(got[0].raw), `"owner_id":"`+userA+`"`) ||
			!strings.Contains(string(got[0].raw), `"v":true`) {
			t.Fatalf("%s: hide by owner not sent: %+v", index, got)
		}
	}

	rec.reply = func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Path, "_update_by_query") {
			return 200, `{"updated":0,"version_conflicts":1}`
		}
		return 200, `{}`
	}
	if err := store.UpdateOwnerTubeHidden(context.Background(), userA, true); err == nil {
		t.Fatal("a conflicted hide reported success; the conflicted document may still be visible")
	}
}

func TestDeleteOwnerEntityDocs_EveryOwnedIndex(t *testing.T) {
	store, rec := newRecordingStore(t)
	rec.reply = func(*http.Request) (int, string) { return 200, `{"deleted":1}` }
	if err := store.DeleteOwnerEntityDocs(context.Background(), userA); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{IndexTubeChannels, IndexTubeCollections, IndexChannels, IndexCommunities} {
		got := rec.matching(http.MethodPost, "/"+index+"/_delete_by_query")
		if len(got) != 1 || !strings.Contains(string(got[0].raw), `"owner_id":"`+userA+`"`) {
			t.Fatalf("%s: owner's documents not deleted: %+v", index, got)
		}
	}
	rec.reply = func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Path, "_delete_by_query") {
			return 200, `{"deleted":0,"failures":[{"cause":"x"}]}`
		}
		return 200, `{}`
	}
	if err := store.DeleteOwnerEntityDocs(context.Background(), userA); err == nil {
		t.Fatal("a failed erasure reported success; the purge would be acked with the channel still searchable")
	}
}

func TestSweepTubeIndex_RefusesOtherIndices(t *testing.T) {
	store, rec := newRecordingStore(t)
	for _, index := range []string{IndexUsers, IndexPosts, IndexChannels, IndexCommunities, IndexProducts} {
		if _, err := store.SweepTubeIndex(context.Background(), index, time.Now()); err == nil {
			t.Fatalf("sweep accepted %s; an index with an event feed must never be emptied by a listing walk", index)
		}
	}
	if _, err := store.SweepTubeIndex(context.Background(), IndexTubeChannels, time.Time{}); err == nil {
		t.Fatal("sweep accepted a zero cut-off, which deletes every document")
	}
	if got := rec.matching(http.MethodPost, "_delete_by_query"); len(got) != 0 {
		t.Fatalf("a refused sweep still sent %s", got[0].path)
	}
}

// A post written inside the refresh interval is invisible to
// _update_by_query, so the hide refreshes first; a conflict means a
// concurrent rewrite read the old flag, so the hide must be retried.
func TestUpdatePostsAuthorHidden_RefreshesFirstAndConflictsRetry(t *testing.T) {
	store, rec := newRecordingStore(t)
	rec.reply = func(*http.Request) (int, string) { return 200, `{"updated":2}` }
	if err := store.UpdatePostsAuthorHidden(context.Background(), userA, true); err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	order := []string{}
	for _, r := range rec.reqs {
		order = append(order, r.path)
	}
	rec.mu.Unlock()
	if len(order) != 2 || !strings.HasSuffix(order[0], "/_refresh") || !strings.HasSuffix(order[1], "/_update_by_query") {
		t.Fatalf("requests = %v, want a refresh of posts_v1 before the update_by_query", order)
	}

	rec.reply = func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Path, "_update_by_query") {
			return 200, `{"updated":1,"version_conflicts":1}`
		}
		return 200, `{}`
	}
	if err := store.UpdatePostsAuthorHidden(context.Background(), userA, true); err == nil {
		t.Fatal("a conflicted hide reported success; the conflicted post may still be visible")
	}
}

// The Tube reindex rewrites through the same keep-hidden upsert, so a
// deactivated person's channel and playlists stay hidden (S-M3).
func TestTubeWrites_AreKeepHiddenUpserts(t *testing.T) {
	store, rec := newRecordingStore(t)
	ctx := context.Background()
	if _, err := store.BulkIndexTubeChannels(ctx, []TubeChannelDoc{{ChannelID: userA, OwnerID: userB}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BulkIndexTubeCollections(ctx, []TubeCollectionDoc{{PlaylistID: userB, OwnerID: userA, Visibility: "public"}}); err != nil {
		t.Fatal(err)
	}
	bulk := rec.matching(http.MethodPost, "_bulk")
	if len(bulk) != 2 {
		t.Fatalf("bulk requests = %d, want 2", len(bulk))
	}
	for _, b := range bulk {
		lines := strings.Split(strings.TrimSpace(string(b.raw)), "\n")
		var action map[string]map[string]any
		_ = json.Unmarshal([]byte(lines[0]), &action)
		if _, ok := action["update"]; !ok {
			t.Fatalf("Tube bulk action = %s; a plain index replace resets is_hidden", lines[0])
		}
		var body struct {
			Script struct {
				Source string `json:"source"`
			} `json:"script"`
		}
		_ = json.Unmarshal([]byte(lines[1]), &body)
		if body.Script.Source != keepHiddenUpsertScript {
			t.Fatalf("Tube rewrite is not the keep-hidden upsert: %s", lines[1])
		}
	}
}
