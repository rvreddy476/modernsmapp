package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A suspension is announced as community.updated with status "suspended" at
// the top level and in the snapshot: search-service's communityFieldsHide
// hides any status but "active", and orders snapshots by updated_at, so an
// unsuspend (status "active", newer updated_at) brings a public community
// back. Neither event names the admin's suspension columns.
func TestSuspensionIsAnnouncedWithTheStatusSearchHides(t *testing.T) {
	actor := uuid.New()
	row := fixtureCommunity()
	row.CommunityType = "public"
	row.Status = "suspended"
	row.UpdatedAt = time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	suspended := NewCommunityUpdatedPayload(row, actor, time.Now())
	if suspended.Status != "suspended" || suspended.Community == nil || suspended.Community.Status != "suspended" {
		t.Fatalf("suspension payload = %+v, want status suspended at the top and in the snapshot", suspended)
	}

	lifted := *row
	lifted.Status = "active"
	lifted.UpdatedAt = row.UpdatedAt.Add(time.Microsecond)
	back := NewCommunityUpdatedPayload(&lifted, actor, time.Now())
	if back.Status != "active" || back.Community.Status != "active" || !back.Community.UpdatedAt.After(suspended.Community.UpdatedAt) {
		t.Fatalf("unsuspend payload = %+v, want active with a newer version", back)
	}

	raw, err := json.Marshal(suspended)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	_ = json.Unmarshal(raw, &generic)
	snap, _ := generic["community"].(map[string]any)
	for _, leaked := range []string{"suspended_by", "suspension_reason_code", "suspended_at"} {
		if _, ok := generic[leaked]; ok {
			t.Errorf("community.updated carries %s", leaked)
		}
		if _, ok := snap[leaked]; ok {
			t.Errorf("the snapshot carries %s", leaked)
		}
	}
}
