package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The invite preview used to assign `ch.AvatarMediaID.String()` to a field
// named `avatar_url`. A client that believed the field name rendered
// `<img src="e13c1582-…">`, which the browser resolves against its OWN origin
// and draws as a broken image.
func TestInvitePreviewAvatarIsAURLNotAUUID(t *testing.T) {
	id := uuid.MustParse("e13c1582-7950-46e9-8519-f0709e982cd9")

	got := channelAvatarURL(&id)
	if got == id.String() {
		t.Fatalf("avatar_url is a bare UUID: %q", got)
	}
	if !strings.HasPrefix(got, "/v1/media/") {
		t.Errorf("avatar_url does not go through the media route: %q", got)
	}
	if want := "/v1/media/" + id.String() + "/serve/avatar"; got != want {
		t.Errorf("channelAvatarURL = %q, want %q", got, want)
	}
	// Unsigned and gateway-relative: it must not go stale in an open tab, and
	// this service has no origin configured to prefix it with.
	for _, forbidden := range []string{"http://", "https://", "X-Amz-", "Signature", "Expires"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("avatar_url contains %q; it must be a stable relative path: %q", forbidden, got)
		}
	}
}

// The other branch: no avatar means no field. An absent field is honest — the
// preview card draws its own placeholder — whereas an empty or garbage string
// makes every client special-case it.
func TestInvitePreviewOmitsAvatarWhenThereIsNone(t *testing.T) {
	if got := channelAvatarURL(nil); got != "" {
		t.Fatalf("channelAvatarURL(nil) = %q, want empty", got)
	}
	// The zero UUID is a nil id that survived a round trip through the
	// database; it is not an asset and must not be published as one.
	var zero uuid.UUID
	if got := channelAvatarURL(&zero); got != "" {
		t.Fatalf("channelAvatarURL(uuid.Nil) = %q, want empty", got)
	}

	var out map[string]any
	raw, err := json.Marshal(&InvitePreview{Code: "abc123", Name: "Riders"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, ok := out["avatar_url"]; ok {
		t.Errorf("avatar_url present with no avatar: %v", v)
	}
}

// And with one, the key is there and carries the route.
func TestInvitePreviewSerialisesTheAvatarURL(t *testing.T) {
	id := uuid.MustParse("e13c1582-7950-46e9-8519-f0709e982cd9")
	preview := &InvitePreview{Code: "abc123", Name: "Riders", AvatarURL: channelAvatarURL(&id)}

	var out map[string]any
	raw, err := json.Marshal(preview)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if want := "/v1/media/" + id.String() + "/serve/avatar"; out["avatar_url"] != want {
		t.Errorf("avatar_url = %v, want %q", out["avatar_url"], want)
	}
	// The preview is the only thing a private community shows an outsider.
	// Adding a media URL must not have widened it.
	for _, leak := range []string{"handle", "owner_id", "channel_type", "avatar_media_id"} {
		if _, ok := out[leak]; ok {
			t.Errorf("invite preview now discloses %q to a non-member", leak)
		}
	}
}
