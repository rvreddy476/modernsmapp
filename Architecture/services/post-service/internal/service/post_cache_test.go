package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// TestBuildPostBodyCacheKey — keys are namespaced under "post:body:"
// so they're easy to scan + bulk-invalidate from ops tooling and
// don't collide with other Redis state in this service (engagement
// counters, dedup keys, etc).
func TestBuildPostBodyCacheKey(t *testing.T) {
	id := uuid.New()
	got := BuildPostBodyCacheKey(id)
	if !strings.HasPrefix(got, "post:body:") {
		t.Errorf("expected 'post:body:' prefix, got %q", got)
	}
	if !strings.HasSuffix(got, id.String()) {
		t.Errorf("expected key to end with the UUID, got %q", got)
	}
	// Different UUIDs must yield different keys.
	if BuildPostBodyCacheKey(uuid.New()) == got {
		t.Errorf("two distinct UUIDs produced the same cache key")
	}
}

// TestPostJSONRoundTrip — what we put into Redis must come back
// equal. The cached payload IS the post struct as JSON, so any
// future field added with a non-default zero value needs to round-
// trip correctly. This test catches the foot-gun where a custom
// MarshalJSON / UnmarshalJSON drifts apart and silently corrupts
// cached entries.
func TestPostJSONRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	tierID := uuid.New()
	coverID := uuid.New()
	postID := uuid.New()
	authorID := uuid.New()
	p := postgres.Post{
		ID:             postID,
		AuthorID:       authorID,
		Text:           "hello world #cache @viewer",
		Visibility:     "public",
		ContentType:    "long_video",
		Title:          "Cached title",
		CreatedAt:      now,
		UpdatedAt:      now,
		CoverMediaID:   &coverID,
		TierRequiredID: &tierID,
		Hashtags:       []string{"cache"},
		Media: []postgres.PostMedia{
			{MediaID: uuid.New(), Kind: "image"},
		},
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got postgres.Post
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != p.ID {
		t.Errorf("ID round-trip: got %v, want %v", got.ID, p.ID)
	}
	if got.AuthorID != p.AuthorID {
		t.Errorf("AuthorID round-trip lost")
	}
	if got.Text != p.Text {
		t.Errorf("Text round-trip lost: %q vs %q", got.Text, p.Text)
	}
	if got.TierRequiredID == nil || *got.TierRequiredID != tierID {
		t.Errorf("TierRequiredID lost in round-trip")
	}
	if got.CoverMediaID == nil || *got.CoverMediaID != coverID {
		t.Errorf("CoverMediaID lost in round-trip")
	}
	if len(got.Media) != 1 || got.Media[0].MediaID != p.Media[0].MediaID {
		t.Errorf("Media slice lost in round-trip")
	}
	if !got.CreatedAt.Equal(p.CreatedAt) {
		t.Errorf("CreatedAt drift: got %v, want %v", got.CreatedAt, p.CreatedAt)
	}
}
