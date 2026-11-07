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
// channel. It must decode and hide the document at the row's version.
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
	w := wantOneRemoval(t, os.writes(search.IndexChannels), testChannelID, "remove")
	if want := time.Date(2026, 11, 7, 10, 30, 0, 0, time.UTC).UnixMicro(); versionOf(w) != want {
		t.Fatalf("version = %d, want the row's updated_at %d", versionOf(w), want)
	}
}

// A channel that stops being searchable leaves search: suspended, archived,
// or a type outside the public set (a tombstone), or deleted (for good).
func TestChannelUpdated_NotSearchableRemovesTheDocument(t *testing.T) {
	cases := map[string]struct {
		mutate func(s *channelSnapshot)
		op     string
	}{
		"suspended": {func(s *channelSnapshot) { s.Status = "suspended" }, "remove"},
		"archived":  {func(s *channelSnapshot) { s.Status = "archived" }, "remove"},
		"deleted":   {func(s *channelSnapshot) { s.Status = "deleted" }, "delete"},
		"private":   {func(s *channelSnapshot) { s.ChannelType, s.Visibility = "private", "private" }, "remove"},
		"paid":      {func(s *channelSnapshot) { s.ChannelType, s.Visibility = "paid", "private" }, "remove"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			snap := activeSnapshot()
			tc.mutate(snap)
			os := newRecordingOS(t)
			c := os.consumer(t)
			if err := c.processMessage(context.Background(), channelUpdated(t, snap)); err != nil {
				t.Fatalf("processMessage: %v", err)
			}
			w := wantOneRemoval(t, os.writes(search.IndexChannels), testChannelID, tc.op)
			if tc.op == "remove" && versionOf(w) != snap.UpdatedAt.UnixMicro() {
				t.Fatalf("version = %d, want the snapshot's %d", versionOf(w), snap.UpdatedAt.UnixMicro())
			}
		})
	}
}

// A searchable channel is an ordered field update with an upsert: the edit
// lands, engagement_score (kept by subscribe events) is untouched, a
// channel with no document (just unsuspended, just made public) is created,
// and the version is the snapshot's own updated_at — not the envelope's,
// which channelUpdated stamps with the test's clock.
func TestChannelUpdated_SearchableIsAnUpsertOfEveryField(t *testing.T) {
	os := newRecordingOS(t)
	c := os.consumer(t)
	snap := activeSnapshot()
	if err := c.processMessage(context.Background(), channelUpdated(t, snap)); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	got := os.writes(search.IndexChannels)
	if len(got) != 1 || got[0].method != http.MethodPost || !strings.Contains(got[0].path, "/_update/"+testChannelID) || opOf(got[0]) != "upsert" {
		t.Fatalf("writes = %+v, want one ordered upsert", got)
	}
	if versionOf(got[0]) != snap.UpdatedAt.UnixMicro() {
		t.Fatalf("version = %d, want the snapshot's updated_at %d", versionOf(got[0]), snap.UpdatedAt.UnixMicro())
	}
	doc := fieldsOf(got[0])
	for field, want := range map[string]any{
		"name": "Riders club", "handle": "riders", "channel_type": "public", "status": search.StatusActive,
		"owner_id": "9b1e4c2a-7d3f-4e5a-8c6b-1a2b3c4d5e02", "category": "sports", "subscriber_count": float64(12),
		"description": "", // cleared, and the clear must be written
	} {
		if doc[field] != want {
			t.Fatalf("fields[%q] = %v, want %v (fields %v)", field, doc[field], want, doc)
		}
	}
	if _, ok := doc["engagement_score"]; ok {
		t.Fatalf("an edit must not reset engagement_score: %v", doc)
	}
	upsert := docOf(got[0])
	if upsert["status"] != search.StatusActive || upsert["handle"] != "riders" || upsert["engagement_score"] == nil {
		t.Fatalf("doc = %v, want the whole document", upsert)
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
	if len(got) != 1 || opOf(got[0]) != "upsert" {
		t.Fatalf("writes = %+v, want one ordered upsert", got)
	}
	if doc := fieldsOf(got[0]); doc["handle"] != "riders" || versionOf(got[0]) != snap.UpdatedAt.UnixMicro() {
		t.Fatalf("body = %v", got[0].body)
	}

	// Created private: never written, only remembered.
	os = newRecordingOS(t)
	c = os.consumer(t)
	snap.ChannelType = "private"
	if err := c.processMessage(context.Background(), msg(t, events.EventChannelCreated, channelCreatedPayload{
		ChannelID: testChannelID, Channel: snap,
	})); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	wantOneRemoval(t, os.writes(search.IndexChannels), testChannelID, "remove")
}

// An older channel-service sends channel.created with four fields; it still
// indexes, active, and unversioned (so it never overwrites a versioned
// document). channel.deleted removes the channel for good.
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
	if len(got) != 1 || opOf(got[0]) != "upsert" || docOf(got[0])["status"] != search.StatusActive || docOf(got[0])["name"] != "Old" {
		t.Fatalf("writes = %+v", got)
	}
	if versionOf(got[0]) != 0 {
		t.Fatalf("a legacy event carried version %d", versionOf(got[0]))
	}

	os = newRecordingOS(t)
	c = os.consumer(t)
	if err := c.processMessage(context.Background(), msg(t, events.EventChannelDeleted, map[string]any{
		"channel_id": testChannelID, "actor_id": "9b1e4c2a-7d3f-4e5a-8c6b-1a2b3c4d5e02", "deleted_at": time.Now(),
	})); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	wantOneRemoval(t, os.writes(search.IndexChannels), testChannelID, "delete")
}
