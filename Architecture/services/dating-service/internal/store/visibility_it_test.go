// Audit 2026-10-30 access & privacy lane, database half: H1 (one visibility
// rule for Nebula, the card loader and the point check), L4 (photo audience)
// and M1 (Aadhaar bound to one account; selfie badge bound to the photo).
// Skipped when TEST_PG_DSN is unset; CI runs them on a _test database.
package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// visPair seeds an active viewer and candidate the candidate's rules admit
// (viewer female, candidate male preferring female).
func visPair(t *testing.T, s *Store) (viewer, candidate uuid.UUID) {
	t.Helper()
	viewer, candidate = uuid.New(), uuid.New()
	seedDiscoverableProfile(t, s, viewer, "female")
	seedDiscoverableProfile(t, s, candidate, "male")
	return viewer, candidate
}

// H1: every hiding change takes the candidate out of the point check, the
// card loader and the passed list (Nebula) — the pass row stays.
func TestCandidateVisibility_NebulaHidesWhatDiscoveryHides(t *testing.T) {
	s, cleanup := privacyTestStore(t)
	defer cleanup()
	ctx := context.Background()
	hide := map[string]func(viewer, candidate uuid.UUID){
		"paused": func(_, c uuid.UUID) {
			if _, err := s.TransitionProfileStatus(ctx, c, ProfileEventPause, ProfileActorUser); err != nil {
				t.Fatal(err)
			}
		},
		"hidden (visible_to_public off)": func(_, c uuid.UUID) {
			if _, err := s.db.Exec(ctx, `UPDATE dating_profiles SET visible_to_public = false WHERE user_id = $1`, c); err != nil {
				t.Fatal(err)
			}
		},
		"under review": func(_, c uuid.UUID) {
			if _, err := s.TransitionProfileStatus(ctx, c, ProfileEventReview, ProfileActorAdmin); err != nil {
				t.Fatal(err)
			}
		},
		"restricted (under-18 hold)": func(_, c uuid.UUID) {
			if _, err := s.TransitionProfileStatus(ctx, c, ProfileEventRestrict, ProfileActorSystem); err != nil {
				t.Fatal(err)
			}
		},
		"their preference excludes the viewer": func(_, c uuid.UUID) {
			if _, err := s.db.Exec(ctx, `UPDATE dating_preferences SET interested_in_gender = 'man' WHERE user_id = $1`, c); err != nil {
				t.Fatal(err)
			}
		},
		"the viewer's preference excludes them": func(v, _ uuid.UUID) {
			if _, err := s.db.Exec(ctx, `UPDATE dating_preferences SET interested_in_gender = 'nonbinary' WHERE user_id = $1`, v); err != nil {
				t.Fatal(err)
			}
		},
		"blocked by the candidate": func(v, c uuid.UUID) {
			if _, err := s.db.Exec(ctx, `INSERT INTO dating_blocks (user_id, blocked_id) VALUES ($1, $2)`, c, v); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, apply := range hide {
		t.Run(name, func(t *testing.T) {
			viewer, candidate := visPair(t, s)
			if _, err := s.RecordPass(ctx, viewer, candidate, ""); err != nil {
				t.Fatal(err)
			}
			if ok, err := s.CandidateVisibleTo(ctx, viewer, candidate); err != nil || !ok {
				t.Fatalf("before: visible=%v err=%v, want visible", ok, err)
			}
			apply(viewer, candidate)
			if ok, err := s.CandidateVisibleTo(ctx, viewer, candidate); err != nil || ok {
				t.Fatalf("after: visible=%v err=%v, want hidden", ok, err)
			}
			if c, err := s.GetCandidateForViewer(ctx, viewer, candidate); err == nil && c != nil {
				t.Fatalf("card loader returned the hidden candidate")
			}
			passes, err := s.ListPassedCandidates(ctx, viewer, 100, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range passes {
				if p.CandidateID == candidate {
					t.Fatalf("Nebula lists the hidden candidate")
				}
			}
		})
	}
}

// L4: a paused or hidden owner's photos go to their matches only; a
// restricted owner's to nobody.
func TestPhotoAudience_PausedOrHiddenOwnerMatchesOnly(t *testing.T) {
	s, cleanup := privacyTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner := uuid.New()
	seedDiscoverableProfile(t, s, owner, "male")
	stranger, match := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{stranger, match} {
		seedDiscoverableProfile(t, s, id, "female")
	}
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_matches (user_a, user_b, status)
        VALUES (LEAST($1::uuid, $2::uuid), GREATEST($1::uuid, $2::uuid), 'matched')`, owner, match); err != nil {
		t.Fatal(err)
	}
	visible := func(viewer uuid.UUID) bool {
		t.Helper()
		f, err := s.PhotoAudience(ctx, viewer, owner)
		if err != nil {
			t.Fatal(err)
		}
		return f.OwnerVisible
	}
	if !visible(stranger) || !visible(match) {
		t.Fatalf("active owner: stranger=%v match=%v, want both", visible(stranger), visible(match))
	}
	if _, err := s.db.Exec(ctx, `UPDATE dating_profiles SET visible_to_public = false WHERE user_id = $1`, owner); err != nil {
		t.Fatal(err)
	}
	if visible(stranger) || !visible(match) {
		t.Fatalf("hidden owner: stranger=%v match=%v, want match only", visible(stranger), visible(match))
	}
	if _, err := s.db.Exec(ctx, `UPDATE dating_profiles SET visible_to_public = true WHERE user_id = $1`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionProfileStatus(ctx, owner, ProfileEventPause, ProfileActorUser); err != nil {
		t.Fatal(err)
	}
	if visible(stranger) || !visible(match) {
		t.Fatalf("paused owner: stranger=%v match=%v, want match only", visible(stranger), visible(match))
	}
	if _, err := s.TransitionProfileStatus(ctx, owner, ProfileEventUnpause, ProfileActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionProfileStatus(ctx, owner, ProfileEventRestrict, ProfileActorSystem); err != nil {
		t.Fatal(err)
	}
	if visible(stranger) || visible(match) {
		t.Fatalf("restricted owner: stranger=%v match=%v, want nobody", visible(stranger), visible(match))
	}
}

// M1: one DigiLocker subject verifies one account; only a keyed hash is kept.
func TestBindAadhaarVerification_OneSubjectOneAccount(t *testing.T) {
	s, cleanup := privacyTestStore(t)
	defer cleanup()
	s.SetPII(testPII(t))
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	subject := "subject-" + uuid.NewString()
	if err := s.BindAadhaarVerification(ctx, a, "ref-1", "h", subject); err != nil {
		t.Fatalf("first account: %v", err)
	}
	// Re-verifying the same account with the same subject is fine.
	if err := s.BindAadhaarVerification(ctx, a, "ref-2", "h", subject); err != nil {
		t.Fatalf("same account again: %v", err)
	}
	if err := s.BindAadhaarVerification(ctx, b, "ref-3", "h", subject); !errors.Is(err, ErrAadhaarAlreadyLinked) {
		t.Fatalf("second account: err=%v, want ErrAadhaarAlreadyLinked", err)
	}
	if _, err := s.GetVerification(ctx, b); !errors.Is(err, ErrVerificationNotFound) {
		t.Fatalf("refused bind wrote a row (err=%v)", err)
	}
	var stored string
	if err := s.db.QueryRow(ctx, `SELECT aadhaar_subject_hash FROM dating_verifications WHERE user_id = $1`, a).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == "" || stored == subject {
		t.Fatalf("stored subject %q: want a keyed hash, never the id", stored)
	}
	s.SetPII(nil)
	if err := s.BindAadhaarVerification(ctx, uuid.New(), "r", "h", "other-"+subject); !errors.Is(err, ErrPIINotConfigured) {
		t.Fatalf("without keys: err=%v, want ErrPIINotConfigured", err)
	}
}

// M1: a new primary photo withdraws the selfie badge and queues a re-review;
// the moderator's approval binds the pass to the new photo.
func TestCheckSelfieBinding_PrimaryChangeWithdrawsBadge(t *testing.T) {
	s, cleanup := privacyTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, admin := uuid.New(), uuid.New()
	seedDiscoverableProfile(t, s, user, "male")
	if err := s.RecordSelfieAttempt(ctx, user, 99, SelfieStatusPassed); err != nil {
		t.Fatal(err)
	}
	if out, err := s.CheckSelfieBinding(ctx, user); err != nil || out != SelfieBindingAdopted {
		t.Fatalf("first check: %q %v, want adopted", out, err)
	}
	if bound, _ := s.SelfieBadgeBound(ctx, user); !bound {
		t.Fatalf("badge not bound after adoption")
	}
	// Swap the primary for another approved photo.
	if _, err := s.db.Exec(ctx, `UPDATE dating_photos SET is_primary = false WHERE user_id = $1`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_photos (user_id, media_id, sort_order, is_primary, visibility, moderation_status)
        VALUES ($1, $2, 1, true, 'public', 'approved')`, user, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if out, err := s.CheckSelfieBinding(ctx, user); err != nil || out != SelfieBindingWithdrawn {
		t.Fatalf("after swap: %q %v, want withdrawn", out, err)
	}
	p, err := s.GetProfile(ctx, user)
	if err != nil || p.TrustTier != "phone" {
		t.Fatalf("trust tier after swap = %v (%v), want phone", p, err)
	}
	if bound, _ := s.SelfieBadgeBound(ctx, user); bound {
		t.Fatalf("badge still bound after the primary changed")
	}
	queue, err := s.ListPendingSelfieReviews(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	queued := false
	for _, r := range queue {
		if r.UserID == user && r.Reason != nil && *r.Reason == SelfieReasonPrimaryPhotoChanged {
			queued = true
		}
	}
	if !queued {
		t.Fatalf("re-review not in the moderator queue")
	}
	if err := s.ResolveSelfieReview(ctx, user, admin, true); err != nil {
		t.Fatalf("approve re-review: %v", err)
	}
	if bound, _ := s.SelfieBadgeBound(ctx, user); !bound {
		t.Fatalf("approved re-review left the badge unbound")
	}
	if out, err := s.CheckSelfieBinding(ctx, user); err != nil || out != SelfieBindingNone {
		t.Fatalf("after approval: %q %v, want none (bound to the new photo)", out, err)
	}
	if v, err := s.GetVerification(ctx, user); err != nil || v.SelfieStatus == nil || *v.SelfieStatus != SelfieStatusPassed {
		t.Fatalf("selfie status = %+v (%v), want passed throughout", v, err)
	}
}

// H1: the dealt record is per viewer and pair, refreshed on re-serving,
// bounded by the window, and goes with a purged profile on either side.
func TestDeckServed_RecordWindowAndPurge(t *testing.T) {
	s, cleanup := privacyTestStore(t)
	defer cleanup()
	ctx := context.Background()
	viewer, dealt, other, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{viewer, dealt, other, stranger} {
		ensureProfileForTest(t, s, id)
	}
	was := func(v, c uuid.UUID) bool {
		t.Helper()
		ok, err := s.WasDealt(ctx, v, c)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if err := s.RecordDeckServed(ctx, viewer, []uuid.UUID{dealt, other}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordDeckServed(ctx, viewer, []uuid.UUID{dealt}); err != nil { // re-served: no duplicate
		t.Fatalf("re-serve: %v", err)
	}
	if !was(viewer, dealt) || !was(viewer, other) {
		t.Fatalf("dealt cards not recorded")
	}
	if was(viewer, stranger) || was(dealt, viewer) {
		t.Fatalf("a card never dealt (or dealt to someone else) counts as dealt")
	}
	// Past the window: not dealt any more, and pruned on the next deal.
	if _, err := s.db.Exec(ctx, `UPDATE dating_deck_served SET served_at = now() - make_interval(secs => $3)
        WHERE user_id = $1 AND candidate_id = $2`, viewer, other, DeckServedWindow.Seconds()+60); err != nil {
		t.Fatal(err)
	}
	if was(viewer, other) {
		t.Fatalf("a card dealt before the window still counts")
	}
	if err := s.RecordDeckServed(ctx, viewer, []uuid.UUID{dealt}); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM dating_deck_served WHERE user_id = $1`, viewer).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("rows after prune = %d (%v), want 1", rows, err)
	}
	// A purge removes the rows on both sides.
	if _, err := s.PurgeUserData(ctx, dealt); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if was(viewer, dealt) {
		t.Fatalf("purged profile still recorded as dealt")
	}
}
