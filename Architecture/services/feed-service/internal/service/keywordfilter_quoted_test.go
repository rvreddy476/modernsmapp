package service

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// Echo quotes (2026-11-13): a quote whose quoted_note hits one of the
// viewer's keywords is dropped whole, as Home drops a repost whose original
// hits one (keywordfilter.go quotedNoteMatchesAnyKeyword).

func quoteOf(t *testing.T, quoted any) HydratedPost {
	t.Helper()
	p := HydratedPost{ID: uuid.New(), ContentType: NoteContentType, Text: "look at this"}
	switch q := quoted.(type) {
	case nil:
	case string:
		p.QuotedNote = json.RawMessage(q)
	default:
		raw, err := json.Marshal(q)
		if err != nil {
			t.Fatal(err)
		}
		p.QuotedNote = raw
	}
	return p
}

func TestKeywordFilter_QuotedNoteHitDropsTheQuote(t *testing.T) {
	kws := []string{"spoilers"}
	for name, quoted := range map[string]any{
		"quoted text":      map[string]any{"text": "huge SPOILERS inside", "content_type": "note"},
		"quoted rich text": map[string]any{"text": "", "rich_text": map[string]any{"blocks": []any{"spoilers here"}}},
		"quoted alt text":  map[string]any{"text": "", "media": []any{map[string]any{"alt_text": "spoilers"}}},
		"malformed":        `{"text": "spoilers", `,
	} {
		if got := filterPostsByKeywords([]HydratedPost{quoteOf(t, quoted)}, kws); len(got) != 0 {
			t.Errorf("%s: quote kept although its quoted note hits a keyword", name)
		}
	}
}

func TestKeywordFilter_QuotedNoteMissOrNullKeepsTheQuote(t *testing.T) {
	kws := []string{"spoilers"}
	for name, quoted := range map[string]any{
		"no quoted_note":   nil,
		"null quoted_note": "null",
		"clean quoted":     map[string]any{"text": "a calm note", "content_type": "note"},
		"substring only":   map[string]any{"text": "nonspoilersy"},
	} {
		if got := filterPostsByKeywords([]HydratedPost{quoteOf(t, quoted)}, kws); len(got) != 1 {
			t.Errorf("%s: quote dropped without a keyword hit", name)
		}
	}
	if got := filterPostsByKeywords([]HydratedPost{quoteOf(t, map[string]any{"text": "spoilers"})}, nil); len(got) != 1 {
		t.Error("no keywords: quote dropped")
	}
}
