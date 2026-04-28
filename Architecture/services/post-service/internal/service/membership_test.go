package service

import (
	"encoding/json"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// TestRedactGatedPost asserts that the redaction strips the heavy
// body fields (text, rich_text, media, poll) but leaves the metadata
// the frontend needs to render a "subscribe to view" preview card —
// title, author, content_type, cover, counts, and the
// tier_required_id itself.
func TestRedactGatedPost(t *testing.T) {
	tierID := uuid.New()
	authorID := uuid.New()
	coverID := uuid.New()

	p := &postgres.Post{
		ID:             uuid.New(),
		AuthorID:       authorID,
		Title:          "Behind the scenes",
		Text:           "Long-form members-only content the public must not see",
		ContentType:    "long_video",
		RichText:       json.RawMessage(`{"blocks":[{"type":"p","text":"secret"}]}`),
		Media:          []postgres.PostMedia{{MediaID: uuid.New(), Kind: "video"}},
		Poll:           &postgres.PollData{Question: "secret poll"},
		CoverMediaID:   &coverID,
		TierRequiredID: &tierID,
	}

	RedactGatedPost(p)

	if p.Text != "" {
		t.Errorf("Text should be redacted, got %q", p.Text)
	}
	if p.RichText != nil {
		t.Errorf("RichText should be nil, got %s", string(p.RichText))
	}
	if p.Media != nil {
		t.Errorf("Media should be nil, got %v", p.Media)
	}
	if p.Poll != nil {
		t.Errorf("Poll should be nil, got %v", p.Poll)
	}
	if !p.BodyRedacted {
		t.Errorf("BodyRedacted should be true")
	}
	// Preview metadata must survive redaction.
	if p.Title == "" {
		t.Errorf("Title should survive redaction")
	}
	if p.AuthorID != authorID {
		t.Errorf("AuthorID should survive redaction")
	}
	if p.CoverMediaID == nil || *p.CoverMediaID != coverID {
		t.Errorf("CoverMediaID should survive redaction")
	}
	if p.ContentType != "long_video" {
		t.Errorf("ContentType should survive redaction")
	}
	if p.TierRequiredID == nil || *p.TierRequiredID != tierID {
		t.Errorf("TierRequiredID should survive redaction so frontend can show the gate")
	}
}
