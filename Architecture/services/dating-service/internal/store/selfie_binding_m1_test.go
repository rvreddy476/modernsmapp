package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// Audit M1 follow-up (7 Nov), no database.

// The in-photo-transaction half writes only a withdrawal; restoring or
// adopting a binding is left to the full check after the commit.
func TestSelfieBindingWrite_InPhotoTransactionOnlyWithdraws(t *testing.T) {
	for _, out := range []SelfieBinding{SelfieBindingNone, SelfieBindingAdopted, SelfieBindingRestored} {
		if selfieBindingWrite(out, true) {
			t.Fatalf("withdraw-only wrote %q", out)
		}
		if !selfieBindingWrite(out, false) {
			t.Fatalf("full check skipped %q", out)
		}
	}
	if !selfieBindingWrite(SelfieBindingWithdrawn, true) || !selfieBindingWrite(SelfieBindingWithdrawn, false) {
		t.Fatal("a withdrawal was not written")
	}
}

func TestSelfieReverifySuggested_OnlyPhotosAddedAfterTheMatch(t *testing.T) {
	ref, other := uuid.New(), uuid.New()
	matched := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	before, after := matched.Add(-time.Hour), matched.Add(time.Hour)
	p := func(id uuid.UUID, at time.Time) SelfiePhotoFact { return SelfiePhotoFact{MediaID: id, CreatedAt: at} }

	if !selfieReverifySuggested(SelfieStatusPassed, "", &ref, &matched, []SelfiePhotoFact{p(ref, before), p(other, after)}) {
		t.Fatal("a photo added after the match is not marked")
	}
	for name, photos := range map[string][]SelfiePhotoFact{
		"only the matched photo":        {p(ref, before)},
		"matched photo re-dated":        {p(ref, after)},
		"other photo from before match": {p(ref, before), p(other, before)},
		"no photos":                     nil,
	} {
		if selfieReverifySuggested(SelfieStatusPassed, "", &ref, &matched, photos) {
			t.Fatalf("%s: marked", name)
		}
	}
	added := []SelfiePhotoFact{p(other, after)}
	for name, c := range map[string]struct {
		status, rebind string
		ref            *uuid.UUID
		at             *time.Time
	}{
		"not passed":           {SelfieStatusPendingReview, "", &ref, &matched},
		"re-review open":       {SelfieStatusPassed, SelfieRebindPending, &ref, &matched},
		"badge already off":    {SelfieStatusPassed, SelfieRebindAwaitingPhoto, &ref, &matched},
		"never bound":          {SelfieStatusPassed, "", nil, &matched},
		"no match time stored": {SelfieStatusPassed, "", &ref, nil},
	} {
		if selfieReverifySuggested(c.status, c.rebind, c.ref, c.at, added) {
			t.Fatalf("%s: marked", name)
		}
	}
}
