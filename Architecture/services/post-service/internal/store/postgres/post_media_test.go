package postgres

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Ordinal normalization — Creator Studio P0-A, errata E-2.1.
//
// These are pure-function tests on purpose. The ordering itself is SQL and is
// proven live; what cannot be proven live on a healthy database is what happens
// when the ordinals are wrong, because a healthy database never produces that.

func media(id string) PostMedia {
	return PostMedia{MediaID: uuid.MustParse(id), Kind: "image"}
}

func at(p int) *int { return &p }

var (
	idA = "6f3b1c58-2a41-4e0d-9c77-1b5a0d8e4f21"
	idB = "b27d9a10-88c5-4f3e-a1d6-3e9c7f204b8a"
	idC = "0c4e5f92-71ab-4d68-8f30-9a2b6c1d7e45"
)

// A fully-backfilled slice: the stored ordinals win.
func TestNormalizeAllPresentUsesStoredOrdinals(t *testing.T) {
	post := uuid.New()
	got, err := normalizePostMediaPositions(post, []scannedMedia{
		{media: media(idA), position: at(0)},
		{media: media(idB), position: at(1)},
		{media: media(idC), position: at(2)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 media, got %d", len(got))
	}
	for i, m := range got {
		if m.Position != i {
			t.Errorf("index %d: want position %d, got %d", i, i, m.Position)
		}
	}
}

// The phase-A case. An old pod inserted rows with no ordinal at all; the
// deterministic ORDER BY already fixed the order, so the index is the ordinal.
func TestNormalizeAllAbsentFallsBackToSliceIndex(t *testing.T) {
	post := uuid.New()
	got, err := normalizePostMediaPositions(post, []scannedMedia{
		{media: media(idA)},
		{media: media(idB)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0].Position != 0 || got[1].Position != 1 {
		t.Fatalf("want 0,1 got %d,%d", got[0].Position, got[1].Position)
	}
	if got[0].MediaID != uuid.MustParse(idA) {
		t.Errorf("fallback must not reorder: first item changed")
	}
}

// THE LOAD-BEARING ONE.
//
// Mixed presence is not a state any legitimate writer produces — a create
// transaction writes every ordinal or none. Normalizing it would publish a
// silently reordered carousel, so it must fail closed instead.
func TestNormalizeMixedPresenceFailsClosed(t *testing.T) {
	post := uuid.New()
	_, err := normalizePostMediaPositions(post, []scannedMedia{
		{media: media(idA), position: at(0)},
		{media: media(idB)},
	})
	if err == nil {
		t.Fatal("mixed presence must fail closed, got nil error")
	}
	if !strings.Contains(err.Error(), "mixed presence") {
		t.Errorf("error should name the condition, got: %v", err)
	}
}

// A duplicate ordinal means the create-time invariant was violated. Returning a
// best-effort carousel would hide it from the only code able to notice.
func TestNormalizeDuplicateOrdinalFailsClosed(t *testing.T) {
	post := uuid.New()
	_, err := normalizePostMediaPositions(post, []scannedMedia{
		{media: media(idA), position: at(0)},
		{media: media(idB), position: at(0)},
	})
	if err == nil {
		t.Fatal("duplicate ordinal must fail closed")
	}
	if !strings.Contains(err.Error(), "duplicate ordinal") {
		t.Errorf("error should name the condition, got: %v", err)
	}
}

// Gap-free 0..N-1 is a service invariant, so an ordinal outside that range is
// corruption even though UNIQUE(post_id, position) would happily allow it.
func TestNormalizeNonContiguousOrdinalFailsClosed(t *testing.T) {
	post := uuid.New()
	_, err := normalizePostMediaPositions(post, []scannedMedia{
		{media: media(idA), position: at(0)},
		{media: media(idB), position: at(7)},
	})
	if err == nil {
		t.Fatal("non-contiguous ordinals must fail closed")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("error should name the condition, got: %v", err)
	}
}

func TestNormalizeEmptySliceIsNotAnError(t *testing.T) {
	got, err := normalizePostMediaPositions(uuid.New(), nil)
	if err != nil || got != nil {
		t.Fatalf("a post with no media is ordinary: got %v, %v", got, err)
	}
}
