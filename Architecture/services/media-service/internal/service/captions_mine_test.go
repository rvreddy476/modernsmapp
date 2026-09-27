package service

import (
	"errors"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The creator caption list's cursor and filter parsing, pinned to the
// fixture in internal/http/testdata/contracts/mtube/subtitles_mine.json.

func TestCaptionCursorRoundTripMatchesTheFixture(t *testing.T) {
	at, _ := time.Parse(time.RFC3339, "2026-09-26T18:00:00Z")
	pos := postgres.SubtitleListCursor{ModifiedAt: at, MediaID: uuid.MustParse("0b6a2c7d-8e9f-4a1b-8c2d-4e5f6a7b8c02")}
	token := encodeCaptionCursor(pos)
	if token != "MjAyNi0wOS0yNlQxODowMDowMFp8MGI2YTJjN2QtOGU5Zi00YTFiLThjMmQtNGU1ZjZhN2I4YzAy" {
		t.Fatalf("cursor = %s", token)
	}
	back, err := decodeCaptionCursor(token)
	if err != nil || !back.ModifiedAt.Equal(pos.ModifiedAt) || back.MediaID != pos.MediaID {
		t.Fatalf("decode: %+v %v", back, err)
	}
	if empty, err := decodeCaptionCursor(""); err != nil || !empty.ModifiedAt.IsZero() {
		t.Fatalf("empty cursor: %+v %v", empty, err)
	}
	for _, bad := range []string{"!!!", "bm90LWEtY3Vyc29y", "MjAyNi0wOS0yNlQxODowMDowMFp8bm90LWEtdXVpZA"} {
		if _, err := decodeCaptionCursor(bad); !errors.Is(err, ErrInvalidCaptionCursor) {
			t.Errorf("cursor %q: got %v, want ErrInvalidCaptionCursor", bad, err)
		}
	}
}

func TestParseCaptionStatus(t *testing.T) {
	for raw, want := range map[string]postgres.SubtitleStatusFilter{
		"": postgres.SubtitleStatusAll, "all": postgres.SubtitleStatusAll, "Draft": postgres.SubtitleStatusDraft,
		"published": postgres.SubtitleStatusPublished,
	} {
		got, err := parseCaptionStatus(raw)
		if err != nil || got != want {
			t.Errorf("parseCaptionStatus(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	if _, err := parseCaptionStatus("reviewed"); !errors.Is(err, ErrInvalidCaptionFilter) {
		t.Fatalf("unknown status: %v", err)
	}
}
