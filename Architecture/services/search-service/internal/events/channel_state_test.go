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

// channel.created / channel.updated carry the whole row since 2026-11-07
// (channel-service ChannelSnapshot). What the consumer writes for each, as
// OpenSearch sees it.

const testChannelID = "3f0c2a8e-6b1d-4c7a-9e2f-5a8b7c6d1e01"

func activeSnapshot() *channelSnapshot {
	return &channelSnapshot{
		ChannelID: testChannelID, OwnerID: "9b1e4c2a-7d3f-4e5a-8c6b-1a2b3c4d5e02",
		Handle: "riders", Name: "Riders club", Description: "",
		ChannelType: "public", Visibility: "public", Category: "sports",
		SubscriberCount: 12, Status: search.StatusActive,
		CreatedAt: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 11, 7, 10, 30, 0, 0, time.UTC),
	}
}

func channelUpdated(t *testing.T, snap *channelSnapshot) kafka.Message {
	t.Helper()
	return msg(t, events.EventChannelUpdated, channelUpdatedPayload{
		ChannelID: testChannelID, UpdatedAt: time.Now(), Channel: snap,
	})
}

// The fixture is channel-service's, byte for byte: a suspended private
// channel. It must decode and remove the document.
func TestChannelUpdated_ContractFixtureRemovesASuspendedChannel(t *testing.T) {
	raw, err := os.ReadFile("testdata/contracts/channel_updated.json")
	if err != nil {
		t.Fatal(err)
	}
	var p channelUpdatedPayload
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&p); err != nil {
		t.Fatalf("the contract fixture does not decode: %v", err)
	}
	if p.Channel == nil || p.Channel.Status != "suspended" || p.Channel.Handle != "riders" || p.Channel.SubscriberCount != 12 {
		t.Fatalf("decoded %+v", p.Channel)
	}
	os := newRecordingOS(t)
	c := os.consumer(t)
	env, _ := json.Marshal(events.EventEnvelope{EventType: events.EventChannelUpdated, Payload: raw})
	if err := c.processMessage(context.Background(), kafka.Message{Value: env}); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	got := os.writes(search.IndexChannels)
	if len(got) != 1 || got[0].method != http.MethodDelete {
		t.Fatalf("a suspended channel: writes = %+v, want one delete", got)
	}
}

// A channel that stops being searchable leaves search: suspended, archived,
// deleted, or a type outside the public set.
func TestChannelUpdated_NotSearchableRemovesTheDocument(t *testing.T) {
	cases := map[string]func(s *channelSnapshot){
		"suspended": func(s *channelSnapshot) { s.Status = "suspended" },
		"archived":  func(s *channelSnapshot) { s.Status = "archived" },
		"deleted":   func(s *channelSnapshot) { s.Status = "deleted" },
		"private":   func(s *channelSnapshot) { s.ChannelType, s.Visibility = "private", "private" },
		"paid":      func(s *channelSnapshot) { s.ChannelType, s.Visibility = "paid", "private" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			snap := activeSnapshot()
			mutate(snap)
			os := newRecordingOS(t)
			c := os.consumer(t)
			if err := c.processMessage(context.Background(), channelUpdated(t, snap)); err != nil {
				t.Fatalf("processMessage: %v", err)
			}
			got := os.writes(search.IndexChannels)
			if len(got) != 1 || got[0].method != http.MethodDelete {
				t.Fatalf("writes = %+v, want one delete", got)
			}
		})
	}
}

// A searchable channel is a field update with an upsert: the edit lands,
// engagement_score (kept by subscribe events) is untouched, and a channel
// with no document (just unsuspended, just made public) is created.
func TestChannelUpdated_SearchableIsAnUpsertOfEveryField(t *testing.T) {
	os := newRecordingOS(t)
	c := os.consumer(t)
	if err := c.processMessage(context.Background(), channelUpdated(t, activeSnapshot())); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	got := os.writes(search.IndexChannels)
	if len(got) != 1 || got[0].method != http.MethodPost || !strings.Contains(got[0].path, "/_update/"+testChannelID) {
		t.Fatalf("writes = %+v, want one _update", got)
	}
	doc, _ := got[0].body["doc"].(map[string]any)
	for field, want := range map[string]any{
		"name": "Riders club", "handle": "riders", "channel_type": "public", "status": search.StatusActive,
		"owner_id": "9b1e4c2a-7d3f-4e5a-8c6b-1a2b3c4d5e02", "category": "sports", "subscriber_count": float64(12),
		"description": "", // cleared, and the clear must be written
	} {
		if doc[field] != want {
			t.Fatalf("doc[%q] = %v, want %v (doc %v)", field, doc[field], want, doc)
		}
	}
	if _, ok := doc["engagement_score"]; ok {
		t.Fatalf("an edit must not reset engagement_score: %v", doc)
	}
	upsert, _ := got[0].body["upsert"].(map[string]any)
	if upsert["status"] != search.StatusActive || upsert["handle"] != "riders" || upsert["engagement_score"] == nil {
		t.Fatalf("upsert = %v, want the whole document", upsert)
	}
}

// channel.created with the snapshot indexes the handle too, through the
// same path.
func TestChannelCreated_SnapshotIsApplied(t *testing.T) {
	os := newRecordingOS(t)
	c := os.consumer(t)
	snap := activeSnapshot()
	if err := c.processMessage(context.Background(), msg(t, events.EventChannelCreated, channelCreatedPayload{
		ChannelID: testChannelID, OwnerID: snap.OwnerID, Name: snap.Name, ChannelType: snap.ChannelType,
		CreatedAt: snap.CreatedAt, Channel: snap,
	})); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	got := os.writes(search.IndexChannels)
	if len(got) != 1 || !strings.Contains(got[0].path, "/_update/") {
		t.Fatalf("writes = %+v, want one _update", got)
	}
	if doc, _ := got[0].body["doc"].(map[string]any); doc["handle"] != "riders" {
		t.Fatalf("doc = %v", got[0].body)
	}

	// Created private: never written.
	os = newRecordingOS(t)
	c = os.consumer(t)
	snap.ChannelType = "private"
	if err := c.processMessage(context.Background(), msg(t, events.EventChannelCreated, channelCreatedPayload{
		ChannelID: testChannelID, Channel: snap,
	})); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	for _, w := range os.writes(search.IndexChannels) {
		if w.method != http.MethodDelete {
			t.Fatalf("a private channel was written: %s %s %v", w.method, w.path, w.body)
		}
	}
}

// An older channel-service sends channel.created with four fields; it still
// indexes, active.
func TestChannelCreated_LegacyPayloadStillIndexes(t *testing.T) {
	os := newRecordingOS(t)
	c := os.consumer(t)
	if err := c.processMessage(context.Background(), msg(t, events.EventChannelCreated, map[string]any{
		"channel_id": testChannelID, "owner_id": "9b1e4c2a-7d3f-4e5a-8c6b-1a2b3c4d5e02",
		"name": "Old", "channel_type": "public", "created_at": time.Now(),
	})); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	got := os.writes(search.IndexChannels)
	if len(got) != 1 || got[0].body["status"] != search.StatusActive || got[0].body["name"] != "Old" {
		t.Fatalf("writes = %+v", got)
	}
}
