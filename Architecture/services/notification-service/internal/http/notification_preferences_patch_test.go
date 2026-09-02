package http

import (
	"encoding/json"
	"testing"

	"github.com/atpost/notification-service/internal/store/postgres"
)

// androidPreferenceKeys is the exact key set the Android settings screen
// binds to. Guarded here so a rename on either side fails loudly.
var androidPreferenceKeys = []string{
	"push_enabled", "email_enabled", "quiet_hours_enabled", "email_digest",
	"push_likes", "inapp_likes",
	"push_comments", "inapp_comments",
	"push_follows", "inapp_follows",
	"push_mentions", "inapp_mentions",
	"push_reposts", "inapp_reposts",
	"push_live", "inapp_live",
	"push_messages", "inapp_messages",
	"push_super_likes", "inapp_super_likes",
	"push_replies", "inapp_replies",
	"push_friend_requests", "inapp_friend_requests",
	"push_group_posts", "inapp_group_posts",
	"push_group_mentions", "inapp_group_mentions",
	"push_channel_updates", "inapp_channel_updates",
	"push_channel_urgent", "inapp_channel_urgent",
	"push_community_posts", "inapp_community_posts",
	"push_community_mentions", "inapp_community_mentions",
	"push_event_reminders", "inapp_event_reminders",
	"push_system", "inapp_system",
}

func TestNotifPreferences_PatchRoundTrip(t *testing.T) {
	var req UpdateNotifPreferencesRequest
	body := `{"inapp_likes":false,"push_reposts":false,"push_live":false,"inapp_messages":false}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	current := postgres.DefaultNotificationPreferences("u1")
	applyNotifPreferencesPatch(current, &req)

	if current.InappLikes || current.PushReposts || current.PushLive || current.InappMessages {
		t.Fatalf("patch not applied: %+v", current)
	}
	// Untouched keys keep their defaults.
	if !current.PushLikes || !current.InappReposts || !current.InappLive || !current.PushMessages || !current.PushEnabled {
		t.Fatalf("patch clobbered untouched fields: %+v", current)
	}

	out, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range androidPreferenceKeys {
		if _, ok := got[k]; !ok {
			t.Fatalf("GET detailed response missing key %q", k)
		}
	}
	if got["inapp_likes"] != false || got["push_reposts"] != false {
		t.Fatalf("round-trip lost patched values: %v", got)
	}
}

func TestNotifPreferences_EmptyPatchIsNoop(t *testing.T) {
	before := *postgres.DefaultNotificationPreferences("u1")
	after := before
	applyNotifPreferencesPatch(&after, &UpdateNotifPreferencesRequest{})
	before.UpdatedAt = after.UpdatedAt
	if before != after {
		t.Fatalf("empty patch changed preferences:\n%+v\n%+v", before, after)
	}
}
