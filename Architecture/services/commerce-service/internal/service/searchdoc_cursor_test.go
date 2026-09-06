package service

// The reindex cursor.
//
// Small, but the failure it prevents is not: a walk that silently restarts
// on a bad cursor re-indexes the front of the catalogue forever and looks
// like progress the whole time.

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSearchDocCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 6, 11, 22, 33, 456789000, time.UTC)
	id := uuid.MustParse("6f9619ff-8b86-d011-b42d-00c04fc964ff")

	gotAt, gotID, err := parseSearchDocCursor(formatSearchDocCursor(at, id))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if !gotAt.Equal(at) {
		t.Fatalf("timestamp: got %s, want %s — sub-second precision has to survive, or two "+
			"products created in the same second page against each other", gotAt, at)
	}
	if *gotID != id {
		t.Fatalf("id: got %s, want %s", gotID, id)
	}
}

func TestEmptyCursorIsTheFirstPage(t *testing.T) {
	at, id, err := parseSearchDocCursor("")
	if err != nil || at != nil || id != nil {
		t.Fatalf("empty cursor: got (%v, %v, %v), want (nil, nil, nil)", at, id, err)
	}
}

func TestMalformedCursorIsRefused(t *testing.T) {
	for _, bad := range []string{
		"garbage",
		"2026-09-06T11:22:33Z", // no separator, no id
		"not-a-time|6f9619ff-8b86-d011-b42d-00c04fc964ff", // bad timestamp
		"2026-09-06T11:22:33Z|not-a-uuid",                 // bad id
		"|",                                               // both halves empty
	} {
		if _, _, err := parseSearchDocCursor(bad); !errors.Is(err, ErrInvalidSearchDocCursor) {
			t.Fatalf("cursor %q: got %v, want ErrInvalidSearchDocCursor.\n"+
				"Restarting the walk from the beginning would look like progress and would "+
				"never finish.", bad, err)
		}
	}
}
