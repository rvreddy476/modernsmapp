package onboarding

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestParseOptionalMediaID(t *testing.T) {
	if id, err := ParseOptionalMediaID("image_media_id", "  "); id != nil || err != nil {
		t.Fatalf("blank = %v %v, want none", id, err)
	}
	want := uuid.New()
	if id, err := ParseOptionalMediaID("image_media_id", " "+want.String()+" "); err != nil || id == nil || *id != want {
		t.Fatalf("valid = %v %v", id, err)
	}
	for _, bad := range []string{"not-a-uuid", uuid.Nil.String(), "12345"} {
		_, err := ParseOptionalMediaID("image_media_id", bad)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Code != CodeMediaIDInvalid || fe.Field != "image_media_id" {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}

func TestMediaServeURL(t *testing.T) {
	id := uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0007")
	if got := MediaServeURL("", id); got != "/v1/media/0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0007/serve" {
		t.Fatalf("relative = %s", got)
	}
	if got := MediaServeURL("https://api.example.test/", id); got != "https://api.example.test/v1/media/0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0007/serve" {
		t.Fatalf("absolute = %s", got)
	}
}

// Audit L6: only platform media is an image reference.
func TestParsePlatformMediaRef(t *testing.T) {
	id := uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0007")
	base := "https://api.example.test"
	ok := []string{
		id.String(),
		"  " + id.String() + " ",
		"/v1/media/" + id.String() + "/serve",
		"/v1/media/" + id.String() + "/serve/thumb",
		base + "/v1/media/" + id.String() + "/serve",
	}
	for _, raw := range ok {
		got, valid := ParsePlatformMediaRef(base, raw)
		if !valid || got != id {
			t.Errorf("ParsePlatformMediaRef(%q) = %s, %v; want %s, true", raw, got, valid, id)
		}
	}
	bad := []string{
		"",
		"https://cdn.example.test/v1/media/" + id.String() + "/serve", // another host
		"https://api.example.test.evil.test/v1/media/" + id.String() + "/serve",
		"https://images.unsplash.com/photo.jpg",
		"/v1/media/" + id.String() + "/serve?track=1",
		"/v1/media/" + id.String() + "/serve/../../x",
		"/v1/media/" + id.String(),
		"/v1/media/" + uuid.Nil.String() + "/serve",
		uuid.Nil.String(),
		"javascript:alert(1)",
	}
	for _, raw := range bad {
		if got, valid := ParsePlatformMediaRef(base, raw); valid {
			t.Errorf("ParsePlatformMediaRef(%q) accepted as %s", raw, got)
		}
	}
	// With no base configured, an absolute URL is never platform media.
	if _, valid := ParsePlatformMediaRef("", base+"/v1/media/"+id.String()+"/serve"); valid {
		t.Error("absolute URL accepted with no media base configured")
	}
}
