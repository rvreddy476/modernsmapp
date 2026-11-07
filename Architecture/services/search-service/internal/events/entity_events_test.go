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
	if w.method != http.MethodPost || !strings.Contains(w.path, "/_update/") {
		t.Fatalf("community.updated sent %s %s; a full replace drops owner_id, the handle and the counts", w.method, w.path)
	}
	if _, upsert := w.body["upsert"]; upsert {
		t.Fatal("a partial update must never create a document")
	}
	doc, _ := w.body["doc"].(map[string]any)
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
		got := os.writes(search.IndexCommunities)
		if len(got) != 1 || got[0].method != http.MethodDelete {
			t.Fatalf("community turned %q: writes = %+v, want one delete", typ, got)
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
	for _, w := range os.writes(search.IndexCommunities) {
		if w.method == http.MethodPut || (w.method == http.MethodPost && !strings.Contains(w.path, "_update")) {
			t.Fatalf("a private community was written: %s %s %v", w.method, w.path, w.body)
		}
	}

	os = newRecordingOS(t)
	c = os.consumer(t)
	if err := c.processMessage(context.Background(), msg(t, events.EventCommunityCreated, communityCreatedPayload{
		CommunityID: testCommunityID, OwnerID: "11111111-1111-1111-1111-111111111111",
		Name: "Open", CommunityType: "public", CreatedAt: time.Now(),
	})); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	got := os.writes(search.IndexCommunities)
	if len(got) != 1 || got[0].body["status"] != search.StatusActive || got[0].body["owner_id"] == nil {
		t.Fatalf("a public community must be written active with its owner: %+v", got)
	}
}

// S-M4: channel.updated carries ids only, so it must not touch the
// document (a full replace blanked channel_type and the channel vanished).
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
