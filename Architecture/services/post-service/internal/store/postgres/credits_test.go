package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// Video credits (2026-10-23): the in-memory half of the replace and the
// consent transitions, pinned without a database. The transaction around
// them is credits_integration_test.go.

func uid() *uuid.UUID { id := uuid.New(); return &id }

func TestPlanCreditReplace_NewTags(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	uploader, tagged := uid(), uid()
	rows, pending, skipped := PlanCreditReplace(nil, []CreditCandidate{
		{Name: "Asha Rao", Role: "Director"},                       // plain text
		{Name: "Me", Role: "Writer", UserID: uploader, Self: true}, // self-credit
		{Name: "Ravi", Role: "Actor", UserID: tagged},              // a tag
		{Name: "Ravi again", Role: "actor ", UserID: tagged},       // the same (user, role): text
	}, 5, now)
	if skipped != 0 || len(rows) != 4 || len(pending) != 1 {
		t.Fatalf("rows=%d pending=%d skipped=%d", len(rows), len(pending), skipped)
	}
	for i, r := range rows {
		if r.Position != i {
			t.Fatalf("row %d at position %d: order not kept", i, r.Position)
		}
	}
	if rows[0].Status != CreditUnlinked || rows[0].UserID != nil {
		t.Fatalf("plain credit = %+v", rows[0])
	}
	if rows[1].Status != CreditAccepted || rows[1].RespondedAt == nil || *rows[1].UserID != *uploader {
		t.Fatalf("self-credit = %+v, want accepted at once", rows[1])
	}
	if rows[2].Status != CreditPending || *rows[2].UserID != *tagged || pending[0].ID != rows[2].ID {
		t.Fatalf("tag = %+v, want pending and announced", rows[2])
	}
	if rows[3].Status != CreditUnlinked || rows[3].UserID != nil {
		t.Fatalf("repeat of (user, role) = %+v, want plain text", rows[3])
	}
}

func TestPlanCreditReplace_DailyLimitStoresOverflowAsText(t *testing.T) {
	var cands []CreditCandidate
	for i := 0; i < 5; i++ {
		cands = append(cands, CreditCandidate{Name: "n", Role: "Actor", UserID: uid()})
	}
	cands = append(cands, CreditCandidate{Name: "me", Role: "Host", UserID: uid(), Self: true})
	rows, pending, skipped := PlanCreditReplace(nil, cands, 2, time.Now())
	if len(pending) != 2 || skipped != 3 {
		t.Fatalf("pending=%d skipped=%d, want 2 and 3", len(pending), skipped)
	}
	for _, r := range rows[2:5] {
		if r.Status != CreditUnlinked || r.UserID != nil {
			t.Fatalf("over-limit tag = %+v, want plain text", r)
		}
	}
	if rows[5].Status != CreditAccepted {
		t.Fatalf("a self-credit is not limited: %+v", rows[5])
	}
	if _, _, skipped := PlanCreditReplace(nil, cands[:1], 0, time.Now()); skipped != 1 {
		t.Fatal("quota 0 must skip every new tag")
	}
}

func TestPlanCreditReplace_KeepsStatusForSameUserAndRole(t *testing.T) {
	declinedBy, acceptedBy, pendingBy := uid(), uid(), uid()
	answered := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	existing := []PostCredit{
		{ID: uuid.New(), Name: "D", Role: "Director", UserID: declinedBy, Status: CreditDeclined, RespondedAt: &answered},
		{ID: uuid.New(), Name: "A", Role: "Actor", UserID: acceptedBy, Status: CreditAccepted, RespondedAt: &answered},
		{ID: uuid.New(), Name: "P", Role: "Writer", UserID: pendingBy, Status: CreditPending},
	}
	rows, pending, skipped := PlanCreditReplace(existing, []CreditCandidate{
		{Name: "A renamed", Role: "ACTOR", UserID: acceptedBy},
		{Name: "D", Role: "Director", UserID: declinedBy},
		{Name: "P", Role: "Producer", UserID: pendingBy}, // new role: a new tag
	}, 10, time.Now())
	if rows[0].ID != existing[1].ID || rows[0].Status != CreditAccepted || rows[0].Name != "A renamed" {
		t.Fatalf("accepted tag not kept: %+v", rows[0])
	}
	if rows[1].ID != existing[0].ID || rows[1].Status != CreditDeclined || rows[1].RespondedAt == nil {
		t.Fatalf("declined tag not kept (would re-ask): %+v", rows[1])
	}
	if rows[2].ID == existing[2].ID || rows[2].Status != CreditPending || len(pending) != 1 || skipped != 0 {
		t.Fatalf("changed role = %+v pending=%d, want a fresh pending tag", rows[2], len(pending))
	}
	if sameCreditText(existing, rows) {
		t.Fatal("a renamed list reported unchanged")
	}
	if !sameCreditText(rows, rows) {
		t.Fatal("identical lists reported changed")
	}
}

func TestNextCreditStatus(t *testing.T) {
	cases := []struct {
		from, action, to string
		announce, ok     bool
	}{
		{CreditPending, CreditActionAccept, CreditAccepted, true, true},
		{CreditAccepted, CreditActionAccept, CreditAccepted, false, true}, // idempotent, silent
		{CreditDeclined, CreditActionAccept, CreditAccepted, true, true},
		{CreditPending, CreditActionDecline, CreditDeclined, false, true},
		{CreditDeclined, CreditActionDecline, CreditDeclined, false, true},
		{CreditAccepted, CreditActionRemove, CreditUnlinked, false, true},
		{CreditUnlinked, CreditActionAccept, "", false, false}, // plain text: nobody to answer
		{CreditPending, "bogus", "", false, false},
	}
	for _, c := range cases {
		to, announce, ok := NextCreditStatus(c.from, c.action)
		if to != c.to || announce != c.announce || ok != c.ok {
			t.Errorf("%s/%s = (%q,%v,%v), want (%q,%v,%v)", c.from, c.action, to, announce, ok, c.to, c.announce, c.ok)
		}
	}
}

func TestCreditCursorRoundTrip(t *testing.T) {
	in := CreditCursor{At: time.Date(2026, 10, 23, 9, 30, 0, 123, time.UTC), ID: uuid.New()}
	out, err := DecodeCreditCursor(EncodeCreditCursor(in))
	if err != nil || out == nil || !out.At.Equal(in.At) || out.ID != in.ID {
		t.Fatalf("round trip = %+v, %v", out, err)
	}
	if c, err := DecodeCreditCursor(""); c != nil || err != nil {
		t.Fatal("empty cursor is the first page")
	}
	for _, bad := range []string{"!!!", "bm8tcGlwZQ", EncodeCreditCursor(in)[:5]} {
		if _, err := DecodeCreditCursor(bad); err != ErrInvalidCreditCursor {
			t.Errorf("cursor %q accepted", bad)
		}
	}
}

func TestPostEditChanges_CreditFields(t *testing.T) {
	year, house := 2024, "Blue Hour"
	before := &Post{Genres: []string{"drama"}, ReleaseYear: &year, ProductionHouse: &house}
	same := []string{"drama"}
	if got := postEditChanges(before, PostEditPatch{Genres: &same, ReleaseYear: &year, ProductionHouse: &house}); len(got) != 0 {
		t.Fatalf("unchanged credit fields audited: %v", got)
	}
	got := postEditChanges(before, PostEditPatch{Genres: &[]string{}, ClearReleaseYear: true, ClearProductionHouse: true})
	for _, k := range []string{"genres", "release_year", "production_house"} {
		if _, ok := got[k]; !ok {
			t.Errorf("clearing %s not audited", k)
		}
	}
	if !creditSearchChanged(got) || creditSearchChanged(map[string]PostEditChange{"title": {}}) {
		t.Fatal("creditSearchChanged mis-reports")
	}
}

func TestCreditAnswerAllowed_OnlyTheTaggedPerson(t *testing.T) {
	tagged, other := uuid.New(), uuid.New()
	if !CreditAnswerAllowed(&tagged, tagged, false) {
		t.Fatal("the tagged person may answer")
	}
	for label, ok := range map[string]bool{
		"someone else": CreditAnswerAllowed(&tagged, other, false),
		"plain text":   CreditAnswerAllowed(nil, tagged, false),
		"deleted post": CreditAnswerAllowed(&tagged, tagged, true),
		"no identity":  CreditAnswerAllowed(&uuid.UUID{}, uuid.Nil, false),
	} {
		if ok {
			t.Errorf("%s may answer", label)
		}
	}
}
