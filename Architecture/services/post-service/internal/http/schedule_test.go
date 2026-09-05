package http

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
)

// Scheduled publish + explicit tags on the wire (2026-09-05).

func TestParsePublishAt(t *testing.T) {
	str := func(s string) *string { return &s }
	t.Run("absent publishes now", func(t *testing.T) {
		got, err := parsePublishAt(nil)
		if err != nil || got != nil {
			t.Fatalf("got %v err %v", got, err)
		}
	})
	t.Run("empty publishes now", func(t *testing.T) {
		got, err := parsePublishAt(str("  "))
		if err != nil || got != nil {
			t.Fatalf("got %v err %v", got, err)
		}
	})
	t.Run("RFC3339 with offset is normalised to UTC", func(t *testing.T) {
		got, err := parsePublishAt(str("2026-09-05T17:30:00+05:30"))
		if err != nil || got == nil {
			t.Fatalf("got %v err %v", got, err)
		}
		if want := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC); !got.Equal(want) || got.Location() != time.UTC {
			t.Fatalf("got %v want %v", got, want)
		}
	})
	t.Run("garbage is INVALID_PUBLISH_AT", func(t *testing.T) {
		_, err := parsePublishAt(str("tomorrow"))
		if !errors.Is(err, service.ErrInvalidPublishAt) {
			t.Fatalf("want ErrInvalidPublishAt, got %v", err)
		}
	})
	t.Run("date only is not RFC3339", func(t *testing.T) {
		_, err := parsePublishAt(str("2026-09-06"))
		if !errors.Is(err, service.ErrInvalidPublishAt) {
			t.Fatalf("want ErrInvalidPublishAt, got %v", err)
		}
	})
}

// The create request carries the three new fields by their contract names.
func TestCreatePostRequestBindsScheduleAndTags(t *testing.T) {
	body := `{"visibility":"public","content_type":"flick","text":"caption",
		"publish_at":"2026-09-05T12:06:00Z","hashtags":["momentum","#test"],"mentions":["call.usera"]}`
	var req CreatePostRequest
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&req); err != nil {
		t.Fatal(err)
	}
	if req.PublishAt == nil || *req.PublishAt != "2026-09-05T12:06:00Z" {
		t.Fatalf("publish_at: %v", req.PublishAt)
	}
	if len(req.Hashtags) != 2 || req.Hashtags[1] != "#test" {
		t.Fatalf("hashtags: %v", req.Hashtags)
	}
	if len(req.Mentions) != 1 || req.Mentions[0] != "call.usera" {
		t.Fatalf("mentions: %v", req.Mentions)
	}
	// And the idempotency fingerprint sees them: two requests that differ
	// only in publish_at must not replay each other.
	a, _ := createFingerprint(req)
	req.PublishAt = nil
	b, _ := createFingerprint(req)
	if a == b {
		t.Fatal("fingerprint must include publish_at")
	}
}

// PATCH body shapes: absent and null both mean "publish now"; a string is
// the new time; anything else is INVALID_PUBLISH_AT.
func TestScheduleRequestShapes(t *testing.T) {
	decode := func(body string) scheduleRequest {
		var r scheduleRequest
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := decode(`{}`); len(r.PublishAt) != 0 {
		t.Fatalf("absent: %q", r.PublishAt)
	}
	if r := decode(`{"publish_at":null}`); string(r.PublishAt) != "null" {
		t.Fatalf("null: %q", r.PublishAt)
	}
	r := decode(`{"publish_at":"2026-09-05T12:06:00Z"}`)
	var s string
	if err := json.Unmarshal(r.PublishAt, &s); err != nil || s != "2026-09-05T12:06:00Z" {
		t.Fatalf("string: %q %v", r.PublishAt, err)
	}
	r = decode(`{"publish_at":12345}`)
	if err := json.Unmarshal(r.PublishAt, &s); err == nil {
		t.Fatal("a number must not decode as a time string")
	}
}
