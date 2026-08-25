package service

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// P0-5: draft payloads are validated on save (and again at publish via
// the same function + the full CreatePost gates).

func TestValidateDraft(t *testing.T) {
	intPtr := func(i int) *int { return &i }
	cases := []struct {
		name     string
		postType string
		payload  PostDraftPayload
		wantErr  bool
	}{
		{"text post ok", "post", PostDraftPayload{Text: "hello"}, false},
		{"media-only post ok", "post", PostDraftPayload{MediaIDs: []uuid.UUID{uuid.New()}}, false},
		{"empty post invalid", "post", PostDraftPayload{}, true},
		{"article ok", "article", PostDraftPayload{Text: "long form", Title: "T"}, false},
		{"poll ok", "poll", PostDraftPayload{Poll: &DraftPoll{Question: "Q?", Options: []string{"a", "b"}, DurationHours: intPtr(24)}}, false},
		{"poll without block", "poll", PostDraftPayload{Text: "x"}, true},
		{"poll one option", "poll", PostDraftPayload{Poll: &DraftPoll{Question: "Q?", Options: []string{"a"}}}, true},
		{"poll six options", "poll", PostDraftPayload{Poll: &DraftPoll{Question: "Q?", Options: []string{"a", "b", "c", "d", "e", "f"}}}, true},
		{"poll blank question", "poll", PostDraftPayload{Poll: &DraftPoll{Question: "  ", Options: []string{"a", "b"}}}, true},
		// "reel"/"video" became valid draft types in fixes-v1 (P1-5), so
		// the invalid-type case now uses a genuinely unknown value.
		{"reel is now a valid draft type", "reel", PostDraftPayload{Text: "x"}, false},
		{"bad post type", "story", PostDraftPayload{Text: "x"}, true},
		{"bad distribution rejected on save", "post", PostDraftPayload{Text: "x", Distribution: json.RawMessage(`{"version":9}`)}, true},
		{"valid distribution ok", "post", PostDraftPayload{Text: "x", Distribution: json.RawMessage(`{"version":1,"main_feed":false}`)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDraft(tc.postType, &tc.payload)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseDraftPayload_UnknownFieldsRejected(t *testing.T) {
	_, err := parseDraftPayload(json.RawMessage(`{"text":"x","surprise_field":true}`))
	if err == nil || !errors.Is(err, ErrInvalidDraft) {
		t.Fatalf("unknown fields must be rejected, got %v", err)
	}
	p, err := parseDraftPayload(json.RawMessage(`{"text":"x","alt_texts":{"id1":"a photo"}}`))
	if err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	if p.AltTexts["id1"] != "a photo" {
		t.Fatalf("alt_texts lost in parse")
	}
}
