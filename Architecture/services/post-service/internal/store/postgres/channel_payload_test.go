package postgres

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

// tube.channel.created / tube.channel.updated carry the channel row id
// (channel_id, 2026-11-16) next to the owner, so search can key the
// tube_channels_v1 document by the id it is indexed under. Both events are
// built by channelPayload from the committed row, so one builder test covers
// create and update.
func TestChannelPayload_CarriesChannelIDFromTheRow(t *testing.T) {
	avatar := uuid.MustParse("7c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e04")
	ch := &Channel{
		ID:            uuid.MustParse("3f2a1b0c-9d8e-4f7a-8b6c-5d4e3f2a1b01"),
		UserID:        uuid.MustParse("9b1e4c2a-7d3f-4e5a-8c6b-1a2b3c4d5e02"),
		Name:          "Riders",
		Handle:        "riders",
		About:         "two wheels",
		AvatarMediaID: &avatar,
		CreatedAt:     time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC),
		UpdatedAt:     time.Date(2026, 11, 15, 9, 0, 0, 123456000, time.UTC),
	}
	p := channelPayload(ch)
	if p.ChannelID != ch.ID.String() {
		t.Fatalf("ChannelID = %q, want the row id %s", p.ChannelID, ch.ID)
	}
	if p.UserID != ch.UserID.String() {
		t.Fatalf("UserID = %q, want the owner %s", p.UserID, ch.UserID)
	}

	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"channel_id":      ch.ID.String(),
		"user_id":         ch.UserID.String(),
		"name":            "Riders",
		"handle":          "riders",
		"about":           "two wheels",
		"avatar_media_id": avatar.String(),
		"created_at":      "2026-11-01T08:00:00Z",
		"updated_at":      "2026-11-15T09:00:00.123456Z",
	}
	if len(wire) != len(want) {
		t.Fatalf("wire = %s, want exactly the keys %v", raw, want)
	}
	for k, v := range want {
		if wire[k] != v {
			t.Fatalf("wire[%q] = %v, want %v (wire %s)", k, wire[k], v, raw)
		}
	}
}

// A row without an id (never scanned) must not put the nil UUID on the
// wire: channel_id is omitted so a consumer falls back to user_id.
func TestChannelPayload_OmitsChannelIDForAnUnsetRow(t *testing.T) {
	p := channelPayload(&Channel{UserID: uuid.New()})
	if p.ChannelID != "" {
		t.Fatalf("ChannelID = %q, want empty for uuid.Nil", p.ChannelID)
	}
	raw, _ := json.Marshal(p)
	var wire map[string]any
	_ = json.Unmarshal(raw, &wire)
	if _, ok := wire["channel_id"]; ok {
		t.Fatalf("wire = %s, want no channel_id", raw)
	}
}
