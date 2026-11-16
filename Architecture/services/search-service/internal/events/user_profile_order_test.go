package events

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
)

// The profile events carry their version to users_v1
// (store/search/user_profile_state.go): UserProfileUpdated its updated_at,
// UserRegistered its created_at. Whether an older one is skipped is decided
// by the script (unit-tested in store/search, live in
// user_profile_order_integration_test.go).

const profileUser = "4b0e6c1a-2d3f-4a5b-8c7d-9e0f1a2b3c4d"

func userUpdate(t *testing.T, r *recordingOS) recordedReq {
	t.Helper()
	var got []recordedReq
	for _, q := range r.writes(search.IndexUsers) {
		if q.method == http.MethodPost && strings.Contains(q.path, "/_update/"+profileUser) {
			got = append(got, q)
		}
	}
	if len(got) != 1 {
		t.Fatalf("users_v1 updates = %d, want 1", len(got))
	}
	return got[0]
}

func TestUserProfileUpdated_CarriesTheEventVersion(t *testing.T) {
	r := newRecordingOS(t)
	c := r.consumer(t)
	at := time.Date(2026, 11, 16, 9, 0, 0, 123456000, time.UTC)
	if err := c.processMessage(context.Background(), msg(t, events.UserProfileUpdated, events.UserProfileUpdatedPayload{
		UserID: profileUser, Username: "asha", DisplayName: "Asha", UpdatedAt: at,
	})); err != nil {
		t.Fatal(err)
	}
	w := userUpdate(t, r)
	if versionOf(w) != at.UnixMicro() || docOf(w)["display_name"] != "Asha" {
		t.Fatalf("version = %d doc = %v, want the payload's updated_at %d", versionOf(w), docOf(w), at.UnixMicro())
	}
}

func TestUserRegistered_CarriesTheCreationVersion(t *testing.T) {
	r := newRecordingOS(t)
	c := r.consumer(t)
	at := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	if err := c.processMessage(context.Background(), msg(t, events.UserRegistered, events.UserRegisteredPayload{
		UserID: profileUser, FirstName: "Asha", CreatedAt: at,
	})); err != nil {
		t.Fatal(err)
	}
	if w := userUpdate(t, r); versionOf(w) != at.UnixMicro() {
		t.Fatalf("version = %d, want the payload's created_at %d", versionOf(w), at.UnixMicro())
	}
}

// An older producer's event without a timestamp is unversioned: it still
// creates or rewrites an unversioned document, never a versioned one.
func TestUserProfileUpdated_WithoutATimestampIsUnversioned(t *testing.T) {
	r := newRecordingOS(t)
	c := r.consumer(t)
	if err := c.processMessage(context.Background(), msg(t, events.UserProfileUpdated, events.UserProfileUpdatedPayload{
		UserID: profileUser, DisplayName: "Asha",
	})); err != nil {
		t.Fatal(err)
	}
	if w := userUpdate(t, r); versionOf(w) != 0 {
		t.Fatalf("version = %d, want 0", versionOf(w))
	}
}

// The partial writers carry their own event time to their own group
// version (handle_updated_us, privacy_updated_us).
func TestHandleChanged_CarriesTheChangeVersion(t *testing.T) {
	r := newRecordingOS(t)
	c := r.consumer(t)
	at := time.Date(2026, 11, 16, 9, 30, 0, 654321000, time.UTC)
	if err := c.processMessage(context.Background(), msg(t, events.HandleChanged, events.HandleChangedPayload{
		UserID: profileUser, OldUsername: "old", NewUsername: "new", ChangedAt: at,
	})); err != nil {
		t.Fatal(err)
	}
	w := userUpdate(t, r)
	if versionOf(w) != at.UnixMicro() || paramsOf(w)["version_field"] != "handle_updated_us" || docOf(w)["username"] != "new" {
		t.Fatalf("params = %v, want username at the change's %d", paramsOf(w), at.UnixMicro())
	}
}

func TestUserSettingsChanged_CarriesTheSettingsVersion(t *testing.T) {
	r := newRecordingOS(t)
	c := r.consumer(t)
	at := time.Date(2026, 11, 16, 9, 45, 0, 111111000, time.UTC)
	if err := c.processMessage(context.Background(), msg(t, events.UserSettingsChanged, map[string]any{
		"user_id": profileUser, "account_visibility": "private", "occurred_at": at,
	})); err != nil {
		t.Fatal(err)
	}
	w := userUpdate(t, r)
	if versionOf(w) != at.UnixMicro() || paramsOf(w)["version_field"] != "privacy_updated_us" || docOf(w)["is_private"] != true {
		t.Fatalf("params = %v, want is_private at the event's %d", paramsOf(w), at.UnixMicro())
	}
}
