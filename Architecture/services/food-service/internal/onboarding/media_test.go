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
