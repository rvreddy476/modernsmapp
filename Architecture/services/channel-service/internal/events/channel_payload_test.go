package events

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// channel.updated carries the whole row (2026-11-07). The fixture is the
// contract search-service decodes; it is copied byte for byte into
// search-service/internal/events/testdata/contracts/.
func fixtureSnapshot() ChannelSnapshot {
	return ChannelSnapshot{
		ChannelID:       "3f0c2a8e-6b1d-4c7a-9e2f-5a8b7c6d1e01",
		OwnerID:         "9b1e4c2a-7d3f-4e5a-8c6b-1a2b3c4d5e02",
		Handle:          "riders",
		Name:            "Riders club",
		Description:     "",
		ChannelType:     "private",
		Visibility:      "private",
		Category:        "sports",
		SubscriberCount: 12,
		Status:          "suspended",
		CreatedAt:       time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2026, 11, 7, 10, 30, 0, 0, time.UTC),
	}
}

func TestChannelUpdatedPayloadMatchesTheContract(t *testing.T) {
	actor := uuid.MustParse("9b1e4c2a-7d3f-4e5a-8c6b-1a2b3c4d5e02")
	got, err := json.MarshalIndent(NewChannelUpdatedPayload(fixtureSnapshot(), &actor, time.Now()), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/contracts/channel_updated.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")))) {
		t.Fatalf("channel.updated drifted from the contract:\n%s\nwant:\n%s", got, want)
	}
}

// A system change (internal key, account deletion) has no actor, and the
// event time falls back to now only when the row has no updated_at.
func TestChannelUpdatedPayloadWithoutActor(t *testing.T) {
	snap := fixtureSnapshot()
	snap.UpdatedAt = time.Time{}
	now := time.Date(2026, 11, 7, 11, 0, 0, 0, time.UTC)
	p := NewChannelUpdatedPayload(snap, nil, now)
	if p.ActorID != "" || !p.UpdatedAt.Equal(now) || p.Channel == nil || p.Channel.Status != "suspended" {
		t.Fatalf("payload = %+v", p)
	}
}

// channel.created keeps its original top-level fields for older consumers
// and adds the snapshot.
func TestChannelCreatedPayloadKeepsTheOldFields(t *testing.T) {
	snap := fixtureSnapshot()
	p := NewChannelCreatedPayload(snap, time.Now())
	if p.ChannelID != snap.ChannelID || p.OwnerID != snap.OwnerID || p.Name != snap.Name ||
		p.ChannelType != snap.ChannelType || !p.CreatedAt.Equal(snap.CreatedAt) {
		t.Fatalf("old fields = %+v", p)
	}
	if p.Channel == nil || p.Channel.Handle != "riders" || p.Channel.Status != "suspended" {
		t.Fatalf("snapshot = %+v", p.Channel)
	}
}
