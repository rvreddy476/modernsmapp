package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLikedCursorRoundTrips(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+1800)
	at := time.Date(2026, 10, 15, 9, 30, 1, 123456000, ist)
	id := uuid.New()

	raw := EncodeLikedCursor(LikedCursor{LikedAt: at, PostID: id})
	if strings.ContainsAny(raw, "+ ") {
		t.Fatalf("cursor %q carries a '+' or space a query string would mangle", raw)
	}
	got, err := ParseLikedCursor(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	if got == nil || !got.LikedAt.Equal(at) || got.PostID != id {
		t.Fatalf("round trip = %+v, want liked_at %s id %s", got, at, id)
	}
}

func TestParseLikedCursorEmptyIsFirstPage(t *testing.T) {
	got, err := ParseLikedCursor("")
	if err != nil || got != nil {
		t.Fatalf("empty cursor = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestParseLikedCursorRefusesGarbage(t *testing.T) {
	for _, raw := range []string{
		"x",
		"not-a-time_" + uuid.NewString(),
		time.Now().UTC().Format(time.RFC3339Nano) + "_not-a-uuid-at-all-but-36-chars-long!",
		time.Now().UTC().Format(time.RFC3339Nano) + uuid.NewString(), // no separator
	} {
		if got, err := ParseLikedCursor(raw); err == nil {
			t.Errorf("ParseLikedCursor(%q) = %+v, want an error", raw, got)
		}
	}
}

func TestIndexReactionTypeKeepsTheCheckVocabulary(t *testing.T) {
	for in, want := range map[string]string{
		"like": "like", "love": "love", "spark": "spark", "angry": "angry",
		"": "like", "heart": "like", "LIKE": "like", "'; drop": "like",
	} {
		if got := IndexReactionType(in); got != want {
			t.Errorf("IndexReactionType(%q) = %q, want %q", in, got, want)
		}
	}
}
