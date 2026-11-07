package events

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
)

// Communities and broadcast channels (audit 2026-11-07, S-M2 / S-M4): what
// the consumer writes for each event, as OpenSearch sees it.

type recordedReq struct {
	method, path string
	body         map[string]any
}

type recordingOS struct {
	mu   sync.Mutex
	reqs []recordedReq
	srv  *httptest.Server
}

func newRecordingOS(t *testing.T) *recordingOS {
	t.Helper()
	r := &recordingOS{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		raw, _ := io.ReadAll(req.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		r.mu.Lock()
		if req.Method != http.MethodHead {
			r.reqs = append(r.reqs, recordedReq{req.Method, req.URL.Path, body})
		}
		r.mu.Unlock()
		_, _ = w.Write([]byte(`{"result":"ok"}`))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *recordingOS) consumer(t *testing.T) *Consumer {
	t.Helper()
	store, err := search.New(r.srv.URL)
	if err != nil {
		t.Fatalf("search.New: %v", err)
	}
	r.mu.Lock()
	r.reqs = nil // drop index bootstrap traffic
	r.mu.Unlock()
	p := defaultRetryPolicy()
	p.BaseDelay, p.MaxDelay = time.Millisecond, 2*time.Millisecond
	return &Consumer{store: store, retry: p, topic: "test", groupID: "test"}
}

func (r *recordingOS) writes(index string) []recordedReq {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []recordedReq
	for _, q := range r.reqs {
		if strings.HasPrefix(q.path, "/"+index+"/") {
			out = append(out, q)
		}
	}
	return out
}

// Every write to communities_v1 / channels_v1 is an ordered scripted
// update (store/search/entity_state.go). paramsOf is its params; opOf its
// op: upsert (write), remove (tombstone), delete (terminal tombstone) or
// patch (legacy partial update); "" for any other request.
func paramsOf(r recordedReq) map[string]any {
	script, _ := r.body["script"].(map[string]any)
	params, _ := script["params"].(map[string]any)
	return params
}

func opOf(r recordedReq) string {
	if r.method != http.MethodPost || !strings.Contains(r.path, "/_update/") {
		return ""
	}
	op, _ := paramsOf(r)["op"].(string)
	return op
}

// fieldsOf / docOf: what an upsert merges into a live document, and the
// whole document it creates.
func fieldsOf(r recordedReq) map[string]any {
	m, _ := paramsOf(r)["fields"].(map[string]any)
	return m
}

func docOf(r recordedReq) map[string]any {
	m, _ := paramsOf(r)["doc"].(map[string]any)
	return m
}

// versionOf is the snapshot version the write carries (0: unversioned).
func versionOf(r recordedReq) int64 {
	v, _ := paramsOf(r)["version"].(float64)
	return int64(v)
}

// wantOneRemoval: exactly one write, an ordered removal of id with the op
// given (remove or delete), carrying nothing but the id in its tombstone.
func wantOneRemoval(t *testing.T, got []recordedReq, id, op string) recordedReq {
	t.Helper()
	if len(got) != 1 || opOf(got[0]) != op || !strings.HasSuffix(got[0].path, "/_update/"+id) {
		t.Fatalf("writes = %+v, want one %s of %s", got, op, id)
	}
	tomb, _ := paramsOf(got[0])["tombstone"].(map[string]any)
	if len(tomb) != 3 || tomb["removed"] != true || tomb["status"] != search.StatusRemoved {
		t.Fatalf("tombstone = %v", tomb)
	}
	if len(fieldsOf(got[0])) != 0 || len(docOf(got[0])) != 0 {
		t.Fatalf("a removal carries the entity's fields: %v", got[0].body)
	}
	return got[0]
}

const testCommunityID = "6f1c7c8e-4b8b-4e57-9a0e-0d5f2b8a1c11"

// S-M4: community.updated is a partial update. A full replace dropped
// owner_id, so someone who blocked the owner saw the community again.
func TestCommunityUpdated_IsAPartialUpdate(t *testing.T) {
	os := newRecordingOS(t)
	c := os.consumer(t)
	if err := c.processMessage(context.Background(), msg(t, events.EventCommunityUpdated, communityUpdatedPayload{
		CommunityID: testCommunityID, Name: "Riders club", CommunityType: "public", UpdatedAt: time.Now(),
	})); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	got := os.writes(search.IndexCommunities)
	if len(got) != 1 {
		t.Fatalf("writes to communities_v1 = %+v, want one partial update", got)
	}
	w := got[0]
	if w.method != http.MethodPost || !strings.Contains(w.path, "/_update/") || opOf(w) != "patch" {
		t.Fatalf("community.updated sent %s %s %v; a full replace drops owner_id, the handle and the counts", w.method, w.path, w.body)
	}
	if _, upsert := w.body["upsert"]; upsert {
		t.Fatal("a partial update must never create a document")
	}
	if versionOf(w) != 0 {
		t.Fatalf("an event without the row has no version; it carried %d", versionOf(w))
	}
	doc := fieldsOf(w)
	if doc["name"] != "Riders club" || doc["community_type"] != "public" {
		t.Fatalf("partial doc = %v", doc)
	}
	for _, kept := range []string{"owner_id", "handle", "member_count", "status", "created_at"} {
		if _, ok := doc[kept]; ok {
			t.Fatalf("partial update overwrites %q: %v", kept, doc)
		}
	}
}

// S-M2: a community that becomes private or invite-only leaves search.
func TestCommunityUpdated_ToNonPublicTypeRemovesTheDocument(t *testing.T) {
	for _, typ := range []string{"private", "invite"} {
		os := newRecordingOS(t)
		c := os.consumer(t)
		if err := c.processMessage(context.Background(), msg(t, events.EventCommunityUpdated, communityUpdatedPayload{
			CommunityID: testCommunityID, CommunityType: typ, UpdatedAt: time.Now(),
		})); err != nil {
			t.Fatalf("processMessage: %v", err)
		}
		w := wantOneRemoval(t, os.writes(search.IndexCommunities), testCommunityID, "remove")
		if versionOf(w) != 0 {
			t.Fatalf("community turned %q: the envelope's updated_at is not a row version: %d", typ, versionOf(w))
		}
	}
}

// S-M2: community.created for a private community is never written.
func TestCommunityCreated_PrivateIsNotIndexed(t *testing.T) {
	os := newRecordingOS(t)
	c := os.consumer(t)
	if err := c.processMessage(context.Background(), msg(t, events.EventCommunityCreated, communityCreatedPayload{
		CommunityID: testCommunityID, OwnerID: "11111111-1111-1111-1111-111111111111",
		Name: "Secret", CommunityType: "private", CreatedAt: time.Now(),
	})); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	wantOneRemoval(t, os.writes(search.IndexCommunities), testCommunityID, "remove")

	os = newRecordingOS(t)
	c = os.consumer(t)
	if err := c.processMessage(context.Background(), msg(t, events.EventCommunityCreated, communityCreatedPayload{
		CommunityID: testCommunityID, OwnerID: "11111111-1111-1111-1111-111111111111",
		Name: "Open", CommunityType: "public", CreatedAt: time.Now(),
	})); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	got := os.writes(search.IndexCommunities)
	if len(got) != 1 || opOf(got[0]) != "upsert" || docOf(got[0])["status"] != search.StatusActive || docOf(got[0])["owner_id"] == nil {
		t.Fatalf("a public community must be written active with its owner: %+v", got)
	}
	if versionOf(got[0]) != 0 {
		t.Fatalf("an older created event has no version; it carried %d", versionOf(got[0]))
	}
}

// S-M4: an older, id-only channel.updated (before the snapshot, 2026-11-07)
// must not touch the document (a full replace blanked channel_type and the
// channel vanished).
func TestChannelUpdated_LeavesTheDocumentAlone(t *testing.T) {
	os := newRecordingOS(t)
	c := os.consumer(t)
	if err := c.processMessage(context.Background(), msg(t, events.EventChannelUpdated, map[string]any{
		"channel_id": testCommunityID, "actor_id": "11111111-1111-1111-1111-111111111111", "updated_at": time.Now(),
	})); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	if got := os.writes(search.IndexChannels); len(got) != 0 {
		t.Fatalf("channel.updated wrote %+v; the payload has no fields to write", got)
	}
}
