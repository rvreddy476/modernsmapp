package events

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
	"github.com/segmentio/kafka-go"
)

// tube.channel.created / tube.channel.updated (post-service outbox): the
// row as committed, applied in version order (store/search/
// tube_channel_state.go): upserted by channel_id when the event has one,
// else an update of the owner's channel document.

const (
	testTubeOwnerID   = "9b1e4c2a-7d3f-4e5a-8c6b-1a2b3c4d5e02"
	testTubeChannelID = "3f2a1b0c-9d8e-4f7a-8b6c-5d4e3f2a1b01"
)

// The fixture is the shared events.ChannelPayload post-service marshals,
// field for field; it must decode and update the owner's channel at the
// row's version.
func TestTubeChannelUpdated_ContractFixtureUpdatesTheOwnersChannel(t *testing.T) {
	raw, err := os.ReadFile("testdata/contracts/tube_channel_updated.json")
	if err != nil {
		t.Fatal(err)
	}
	var p events.ChannelPayload
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		t.Fatalf("the contract fixture is not a ChannelPayload: %v", err)
	}
	again, _ := json.Marshal(p)
	var a, b map[string]any
	_ = json.Unmarshal(raw, &a)
	_ = json.Unmarshal(again, &b)
	if len(a) != len(b) {
		t.Fatalf("the fixture and ChannelPayload disagree: %s vs %s", raw, again)
	}

	rec := newTubeChannelOS(t, false)
	c := rec.consumer(t)
	env, _ := json.Marshal(events.EventEnvelope{EventType: events.TubeChannelUpdated, Payload: raw})
	if err := c.processMessage(context.Background(), kafka.Message{Value: env}); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	// The fixture carries channel_id, so the document is upserted by id.
	w := wantTubeChannelUpsert(t, rec.writes(search.IndexTubeChannels), testTubeChannelID)
	if want := time.Date(2026, 11, 15, 9, 0, 0, 123456000, time.UTC).UnixMicro(); versionOf(w) != want {
		t.Fatalf("version = %d, want the row's updated_at %d", versionOf(w), want)
	}
	fields, _ := paramsOf(w)["fields"].(map[string]any)
	if fields["name"] != "Riders" || fields["handle"] != "riders" || fields["avatar_media_id"] != "7c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e04" {
		t.Fatalf("fields = %v", fields)
	}
	// Absent from search: created whole, as the Tube reindex writes it.
	doc, _ := w.body["upsert"].(map[string]any)
	want := map[string]any{
		"channel_id": testTubeChannelID, "owner_id": testTubeOwnerID, "name": "Riders", "handle": "riders",
		"avatar_media_id": "7c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e04", "follower_count": float64(0),
		"created_at": "2026-10-01T08:00:00Z", "updated_at": "2026-11-15T09:00:00.123456Z",
		"source_updated_us": float64(time.Date(2026, 11, 15, 9, 0, 0, 123456000, time.UTC).UnixMicro()),
		"is_hidden":         false,
	}
	for k, v := range want {
		if doc[k] != v {
			t.Errorf("upsert %s = %v, want %v", k, doc[k], v)
		}
	}
	if _, ok := doc["about"]; ok {
		t.Errorf("upsert carries an empty about; the reindex omits it")
	}
	if _, ok := doc["reindexed_at"]; !ok {
		t.Errorf("upsert lacks reindexed_at; a running reindex would sweep it")
	}
}

// A brand-new channel of a hidden owner is created hidden.
func TestTubeChannelCreated_HiddenOwnerCreatesAHiddenChannel(t *testing.T) {
	rec := newTubeChannelOS(t, true)
	c := rec.consumer(t)
	if err := c.processMessage(context.Background(), msg(t, events.TubeChannelCreated, events.ChannelPayload{
		ChannelID: testTubeChannelID, UserID: testTubeOwnerID, Name: "New", Handle: "new", UpdatedAt: time.Now(),
	})); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	w := wantTubeChannelUpsert(t, rec.writes(search.IndexTubeChannels), testTubeChannelID)
	if doc, _ := w.body["upsert"].(map[string]any); doc["is_hidden"] != true {
		t.Fatalf("upsert is_hidden = %v, want true", doc["is_hidden"])
	}
}

// created is applied the same way, and an avatar that was removed is
// written as cleared, not left stale.
func TestTubeChannelCreated_AppliesAndClearsAnAbsentAvatar(t *testing.T) {
	rec := newRecordingOS(t)
	c := rec.consumer(t)
	if err := c.processMessage(context.Background(), msg(t, events.TubeChannelCreated, events.ChannelPayload{
		UserID: testTubeOwnerID, Name: "New", Handle: "new", UpdatedAt: time.Now(),
	})); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	// No channel_id (an older producer): falls back to the owner.
	w := wantTubeChannelUpdate(t, rec.writes(search.IndexTubeChannels), "")
	fields, _ := paramsOf(w)["fields"].(map[string]any)
	if v, ok := fields["avatar_media_id"]; !ok || v != "" {
		t.Fatalf("avatar_media_id = %v (present %v), want cleared", v, ok)
	}
}

// An event that carries channel_id is applied to that document by id.
func TestTubeChannelUpdated_PrefersChannelIDOverOwner(t *testing.T) {
	rec := newTubeChannelOS(t, false)
	c := rec.consumer(t)
	if err := c.processMessage(context.Background(), msg(t, events.TubeChannelUpdated, events.ChannelPayload{
		ChannelID: testTubeChannelID, UserID: testTubeOwnerID, Name: "Renamed", Handle: "renamed", UpdatedAt: time.Now(),
	})); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	w := wantTubeChannelUpsert(t, rec.writes(search.IndexTubeChannels), testTubeChannelID)
	fields, _ := paramsOf(w)["fields"].(map[string]any)
	if fields["name"] != "Renamed" {
		t.Fatalf("fields = %v", fields)
	}
}

// newTubeChannelOS answers the reads the id path makes: no author fence,
// the owner's users_v1 document (hidden or not), and an _update that
// reports "updated" (so no post-create re-read is made).
func newTubeChannelOS(t *testing.T, ownerHidden bool) *recordingOS {
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
		switch {
		case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/"+search.IndexAuthorFences+"/_doc/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"_index":"author_fences_v1","found":false}`))
		case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/"+search.IndexUsers+"/_doc/"):
			if ownerHidden {
				_, _ = w.Write([]byte(`{"_index":"users_v1","found":true,"_source":{"is_hidden":true}}`))
			} else {
				_, _ = w.Write([]byte(`{"_index":"users_v1","found":true,"_source":{}}`))
			}
		case strings.Contains(req.URL.Path, "/_update/"):
			_, _ = w.Write([]byte(`{"result":"updated"}`))
		default:
			_, _ = w.Write([]byte(`{"result":"ok"}`))
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// wantTubeChannelUpsert wants exactly one _update on the channel document:
// the ordered merge with the whole document as its upsert.
func wantTubeChannelUpsert(t *testing.T, got []recordedReq, channelID string) recordedReq {
	t.Helper()
	var ups []recordedReq
	for _, r := range got {
		if r.method == http.MethodPost && r.path == "/"+search.IndexTubeChannels+"/_update/"+channelID {
			ups = append(ups, r)
		} else {
			t.Fatalf("unexpected write %s %s", r.method, r.path)
		}
	}
	if len(ups) != 1 {
		t.Fatalf("writes = %+v, want one _update on %s", got, channelID)
	}
	if _, ok := ups[0].body["upsert"].(map[string]any); !ok {
		t.Fatalf("body = %v, want an upsert document", ups[0].body)
	}
	return ups[0]
}

// wantTubeChannelUpdate wants exactly one update_by_query: on the channel
// document by id when channelID is set, else on the owner's channel.
func wantTubeChannelUpdate(t *testing.T, got []recordedReq, channelID string) recordedReq {
	t.Helper()
	var ubq []recordedReq
	for _, r := range got {
		if r.method == http.MethodPost && strings.HasSuffix(r.path, "/_update_by_query") {
			ubq = append(ubq, r)
		} else if r.method != http.MethodPost || !strings.HasSuffix(r.path, "/_refresh") {
			t.Fatalf("unexpected write %s %s", r.method, r.path)
		}
	}
	if len(ubq) != 1 {
		t.Fatalf("writes = %+v, want one update_by_query", got)
	}
	q, _ := ubq[0].body["query"].(map[string]any)
	if channelID != "" {
		ids, _ := q["ids"].(map[string]any)
		values, _ := ids["values"].([]any)
		if len(values) != 1 || values[0] != channelID {
			t.Fatalf("query = %v, want the channel document %s by id", q, channelID)
		}
		return ubq[0]
	}
	term, _ := q["term"].(map[string]any)
	if term["owner_id"] != testTubeOwnerID {
		t.Fatalf("query = %v, want the owner's channel", q)
	}
	return ubq[0]
}
