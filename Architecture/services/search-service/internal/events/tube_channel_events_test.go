package events

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
	"github.com/segmentio/kafka-go"
)

// tube.channel.created / tube.channel.updated (post-service outbox): the
// row as committed, applied in version order onto the owner's channel
// document (store/search/tube_channel_state.go).

const testTubeOwnerID = "9b1e4c2a-7d3f-4e5a-8c6b-1a2b3c4d5e02"

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

	rec := newRecordingOS(t)
	c := rec.consumer(t)
	env, _ := json.Marshal(events.EventEnvelope{EventType: events.TubeChannelUpdated, Payload: raw})
	if err := c.processMessage(context.Background(), kafka.Message{Value: env}); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	w := wantTubeChannelUpdate(t, rec.writes(search.IndexTubeChannels))
	if want := time.Date(2026, 11, 15, 9, 0, 0, 123456000, time.UTC).UnixMicro(); versionOf(w) != want {
		t.Fatalf("version = %d, want the row's updated_at %d", versionOf(w), want)
	}
	fields, _ := paramsOf(w)["fields"].(map[string]any)
	if fields["name"] != "Riders" || fields["handle"] != "riders" || fields["avatar_media_id"] != "7c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e04" {
		t.Fatalf("fields = %v", fields)
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
	w := wantTubeChannelUpdate(t, rec.writes(search.IndexTubeChannels))
	fields, _ := paramsOf(w)["fields"].(map[string]any)
	if v, ok := fields["avatar_media_id"]; !ok || v != "" {
		t.Fatalf("avatar_media_id = %v (present %v), want cleared", v, ok)
	}
}

func wantTubeChannelUpdate(t *testing.T, got []recordedReq) recordedReq {
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
	term, _ := q["term"].(map[string]any)
	if term["owner_id"] != testTubeOwnerID {
		t.Fatalf("query = %v, want the owner's channel", q)
	}
	return ubq[0]
}
