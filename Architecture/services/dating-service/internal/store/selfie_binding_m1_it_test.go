// Audit M1 follow-up (7 Nov), database half: every photo change that takes
// away the face photo a passed selfie was matched against withdraws the
// badge in the photo change's own transaction — no CheckSelfieBinding call
// needed — and a photo added after the match marks "re-verify" without
// touching the badge. Skipped when TEST_PG_DSN is unset; CI runs them on a
// _test database.
package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// boundSelfieUser seeds an active profile with an approved primary photo, a
// passed selfie bound to it and the selfie badge on. It returns the primary
// photo's id.
func boundSelfieUser(t *testing.T, s *Store) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	user := uuid.New()
	seedDiscoverableProfile(t, s, user, "male")
	if err := s.RecordSelfieAttempt(ctx, user, 99, SelfieStatusPassed); err != nil {
		t.Fatal(err)
	}
	if out, err := s.CheckSelfieBinding(ctx, user); err != nil || out != SelfieBindingAdopted {
		t.Fatalf("bind: %q %v, want adopted", out, err)
	}
	if err := s.UpdateTrustTier(ctx, user, "selfie"); err != nil {
		t.Fatal(err)
	}
	var primary uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT id FROM dating_photos WHERE user_id = $1 AND is_primary`, user).Scan(&primary); err != nil {
		t.Fatal(err)
	}
	return user, primary
}

func requireBadge(t *testing.T, s *Store, user uuid.UUID, wantOn bool) {
	t.Helper()
	ctx := context.Background()
	p, err := s.GetProfile(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := s.SelfieBadgeBound(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if on := p.TrustTier == "selfie" && bound; on != wantOn {
		t.Fatalf("badge on = %v (tier %q, bound %v), want %v", on, p.TrustTier, bound, wantOn)
	}
}

func approvedDecision() PhotoDecision {
	return PhotoDecision{Status: PhotoStatusApproved, Source: PhotoSourceAuto}
}

func TestSelfieBadge_WithdrawnInThePhotoChangeTransaction(t *testing.T) {
	s, cleanup := privacyTestStore(t)
	defer cleanup()
	ctx := context.Background()

	t.Run("new primary attached", func(t *testing.T) {
		user, _ := boundSelfieUser(t, s)
		if _, err := s.CreateModeratedPhoto(ctx, user, CreatePhotoParams{MediaID: uuid.New(), IsPrimary: true, SortOrder: 1}, 6, approvedDecision()); err != nil {
			t.Fatal(err)
		}
		requireBadge(t, s, user, false)
	})
	t.Run("another photo made primary", func(t *testing.T) {
		user, _ := boundSelfieUser(t, s)
		other, err := s.CreateModeratedPhoto(ctx, user, CreatePhotoParams{MediaID: uuid.New(), SortOrder: 1}, 6, approvedDecision())
		if err != nil {
			t.Fatal(err)
		}
		requireBadge(t, s, user, true)
		primary := true
		if _, err := s.UpdatePhoto(ctx, user, other.ID, UpdatePhotoParams{IsPrimary: &primary}); err != nil {
			t.Fatal(err)
		}
		requireBadge(t, s, user, false)
	})
	t.Run("bound primary rejected", func(t *testing.T) {
		user, primary := boundSelfieUser(t, s)
		if _, changed, err := s.SetPhotoModerationDecision(ctx, primary, PhotoDecision{Status: PhotoStatusRejected, Reason: "NO_FACE", Source: PhotoSourceAdmin}); err != nil || !changed {
			t.Fatalf("reject: changed=%v %v", changed, err)
		}
		requireBadge(t, s, user, false)
	})
	t.Run("bound primary deleted", func(t *testing.T) {
		user, primary := boundSelfieUser(t, s)
		if err := s.DeletePhoto(ctx, user, primary); err != nil {
			t.Fatal(err)
		}
		requireBadge(t, s, user, false)
	})
}

func TestSelfieBadge_AddedSecondaryPhotoMarksReverifyAndKeepsTheBadge(t *testing.T) {
	s, cleanup := privacyTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, _ := boundSelfieUser(t, s)
	if got, err := s.SelfieReverifySuggested(ctx, user); err != nil || got {
		t.Fatalf("before any new photo: %v %v, want false", got, err)
	}
	if _, err := s.CreateModeratedPhoto(ctx, user, CreatePhotoParams{MediaID: uuid.New(), SortOrder: 1}, 6, approvedDecision()); err != nil {
		t.Fatal(err)
	}
	requireBadge(t, s, user, true)
	if got, err := s.SelfieReverifySuggested(ctx, user); err != nil || !got {
		t.Fatalf("after a new secondary photo: %v %v, want true", got, err)
	}
}
