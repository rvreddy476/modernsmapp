package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Profile status machine (Dating plan lane D2) ────────────────────────────
//
// dating_profiles carries three columns that together are the lifecycle:
//
//   - profile_status: what every gate reads (discovery shows only 'active').
//   - prior_status:   while the row is paused or held (pending_review,
//     restricted, suspended) or deleted, the onboarding step underneath —
//     draft | pending_photo | pending_selfie | active. Lifting the pause or
//     the hold restores exactly this step, so an unpause can never lift a
//     suspension and a reinstatement can never skip onboarding.
//   - paused:         the user's (or account-lifecycle's) hide flag. It
//     survives a moderation hold, so reinstating a paused user returns them
//     to 'paused', not to discovery.
//
// TransitionProfileStatus is the only code allowed to write any of the three
// (profile_status_scan_test.go enforces it). Callers name an event and an
// actor; NextProfileStatus decides the edge; the writer re-checks the row it
// locked and, for onboarding steps, the evidence in the database.

// ProfileEvent is one thing that can happen to a profile's lifecycle.
type ProfileEvent string

const (
	// ProfileEventBasicsComplete: draft → pending_photo (intent, gender,
	// interested_in, 18+ birth date, first name, location).
	ProfileEventBasicsComplete ProfileEvent = "basics_complete"
	// ProfileEventPhotoApproved: pending_photo → pending_selfie.
	ProfileEventPhotoApproved ProfileEvent = "photo_approved"
	// ProfileEventSelfiePassed: pending_selfie → active.
	ProfileEventSelfiePassed ProfileEvent = "selfie_passed"
	// ProfileEventPhotoRevoked (lane D6): the approved primary photo is gone
	// (a later rejection, a deletion, a primary change to a photo that is
	// not approved) — pending_selfie | active → pending_photo, or the
	// remembered step under a pause/hold. A no-op before the photo step.
	// The writer refuses it while the database still holds an approved
	// primary photo (ErrPhotoStillApproved).
	ProfileEventPhotoRevoked ProfileEvent = "photo_revoked"
	// ProfileEventPause hides the profile; ProfileEventUnpause restores
	// the remembered step.
	ProfileEventPause   ProfileEvent = "pause"
	ProfileEventUnpause ProfileEvent = "unpause"
	// Moderation holds and their release.
	ProfileEventReview    ProfileEvent = "review"
	ProfileEventRestrict  ProfileEvent = "restrict"
	ProfileEventSuspend   ProfileEvent = "suspend"
	ProfileEventReinstate ProfileEvent = "reinstate"
	// ProfileEventDelete is the soft delete (terminal).
	ProfileEventDelete ProfileEvent = "delete"
)

// ProfileActor is who is asking for a transition.
type ProfileActor string

const (
	ProfileActorUser      ProfileActor = "user"
	ProfileActorSystem    ProfileActor = "system"
	ProfileActorAdmin     ProfileActor = "admin"
	ProfileActorLifecycle ProfileActor = "lifecycle"
)

var (
	// ErrProfileTransitionNotAllowed: the edge does not exist from the
	// current state.
	ErrProfileTransitionNotAllowed = errors.New("invalid: profile status transition not allowed")
	// ErrProfileTransitionActor: the edge exists but not for this actor.
	ErrProfileTransitionActor = errors.New("forbidden: this actor may not perform that profile status transition")
	// ErrOnboardingIncomplete: an onboarding step was requested but the
	// database does not hold the evidence for it yet.
	ErrOnboardingIncomplete = errors.New("invalid: onboarding requirements for this step are not met")
	// ErrPhotoStillApproved: photo_revoked was requested but the database
	// still holds an approved primary photo. Callers treat it as a no-op.
	ErrPhotoStillApproved = errors.New("invalid: an approved primary photo still exists")
	// ErrProfileStatusConflict: the row changed between the lock and the
	// guarded UPDATE (should be unreachable under FOR UPDATE).
	ErrProfileStatusConflict = errors.New("conflict: profile status changed concurrently")
)

// ProfileStatusState is the lifecycle triple. Prior "" means NULL.
type ProfileStatusState struct {
	Status string
	Prior  string
	Paused bool
}

// String renders "status<prior+p" for test failures and logs.
func (st ProfileStatusState) String() string {
	out := st.Status
	if st.Prior != "" {
		out += "<" + st.Prior
	}
	if st.Paused {
		out += "+p"
	}
	return out
}

// IsOnboardingStatus reports whether s is a step of the onboarding ladder
// (draft, pending_photo, pending_selfie, active).
func IsOnboardingStatus(s string) bool { return onboardingRank(s) >= 0 }

func isHoldStatus(s string) bool {
	switch s {
	case ProfileStatusPendingReview, ProfileStatusRestricted, ProfileStatusSuspended:
		return true
	}
	return false
}

func onboardingRank(s string) int {
	switch s {
	case ProfileStatusDraft:
		return 0
	case ProfileStatusPendingPhoto:
		return 1
	case ProfileStatusPendingSelfie:
		return 2
	case ProfileStatusActive:
		return 3
	}
	return -1
}

// Base is the onboarding step underneath the current status. A paused/held
// row with no remembered step is treated as draft: never skip a step.
func (st ProfileStatusState) Base() string {
	if IsOnboardingStatus(st.Status) {
		return st.Status
	}
	if IsOnboardingStatus(st.Prior) {
		return st.Prior
	}
	return ProfileStatusDraft
}

var profileEventActors = map[ProfileEvent][]ProfileActor{
	ProfileEventBasicsComplete: {ProfileActorSystem},
	ProfileEventPhotoApproved:  {ProfileActorSystem},
	ProfileEventSelfiePassed:   {ProfileActorSystem},
	ProfileEventPhotoRevoked:   {ProfileActorSystem, ProfileActorAdmin},
	ProfileEventPause:          {ProfileActorUser, ProfileActorLifecycle},
	ProfileEventUnpause:        {ProfileActorUser, ProfileActorLifecycle},
	ProfileEventReview:         {ProfileActorAdmin, ProfileActorSystem}, // system: an underage report (service.Report, lane D8)
	ProfileEventRestrict:       {ProfileActorAdmin, ProfileActorSystem}, // system: under-18 identity birth date (service.UpsertProfile)
	ProfileEventSuspend:        {ProfileActorAdmin},
	ProfileEventReinstate:      {ProfileActorAdmin},
	ProfileEventDelete:         {ProfileActorUser, ProfileActorSystem, ProfileActorLifecycle},
}

// onboardingEdges maps each onboarding event to its {from, to} step.
var onboardingEdges = map[ProfileEvent][2]string{
	ProfileEventBasicsComplete: {ProfileStatusDraft, ProfileStatusPendingPhoto},
	ProfileEventPhotoApproved:  {ProfileStatusPendingPhoto, ProfileStatusPendingSelfie},
	ProfileEventSelfiePassed:   {ProfileStatusPendingSelfie, ProfileStatusActive},
}

var holdForEvent = map[ProfileEvent]string{
	ProfileEventReview:   ProfileStatusPendingReview,
	ProfileEventRestrict: ProfileStatusRestricted,
	ProfileEventSuspend:  ProfileStatusSuspended,
}

// OnboardingEventFrom returns the onboarding event that leaves step, if any.
func OnboardingEventFrom(step string) (ProfileEvent, bool) {
	for ev, edge := range onboardingEdges {
		if edge[0] == step {
			return ev, true
		}
	}
	return "", false
}

// NextProfileStatus is the transition table. It returns the state after ev,
// the unchanged state for an idempotent no-op, or an error wrapping
// ErrProfileTransitionActor / ErrProfileTransitionNotAllowed.
//
//	draft → pending_photo → pending_selfie → active   system, evidence-checked;
//	                                                   advances the remembered
//	                                                   step while paused/held
//	onboarding step → paused (remembers step)          user | lifecycle
//	paused → remembered step                           user | lifecycle
//	held + pause/unpause → same hold, flag only        user | lifecycle
//	any non-deleted → pending_review|restricted|suspended  admin
//	any non-deleted → restricted                       system (under-18 identity
//	                                                   birth date); a no-op on a
//	                                                   suspended or restricted row
//	any non-deleted → pending_review                   system (underage report);
//	                                                   same no-op rule
//	held → remembered step (or paused if flagged)      admin
//	any → deleted                                      user | system | lifecycle
func NextProfileStatus(cur ProfileStatusState, ev ProfileEvent, actor ProfileActor) (ProfileStatusState, error) {
	actors, known := profileEventActors[ev]
	if !known {
		return cur, fmt.Errorf("%w: unknown event %q", ErrProfileTransitionNotAllowed, ev)
	}
	actorOK := false
	for _, a := range actors {
		if a == actor {
			actorOK = true
			break
		}
	}
	if !actorOK {
		return cur, fmt.Errorf("%w: %s may not %s", ErrProfileTransitionActor, actor, ev)
	}
	refuse := func() (ProfileStatusState, error) {
		return cur, fmt.Errorf("%w: %s from %s", ErrProfileTransitionNotAllowed, ev, cur)
	}

	if cur.Status == ProfileStatusDeleted {
		switch {
		case ev == ProfileEventDelete:
			return cur, nil
		case actor == ProfileActorLifecycle && (ev == ProfileEventPause || ev == ProfileEventUnpause):
			// Account lifecycle redelivers hide/unhide; a deleted
			// profile stays deleted and hidden.
			return cur, nil
		}
		return refuse()
	}

	base := cur.Base()
	switch ev {
	case ProfileEventBasicsComplete, ProfileEventPhotoApproved, ProfileEventSelfiePassed:
		edge := onboardingEdges[ev]
		if base != edge[0] {
			return refuse()
		}
		if IsOnboardingStatus(cur.Status) {
			return ProfileStatusState{Status: edge[1]}, nil
		}
		return ProfileStatusState{Status: cur.Status, Prior: edge[1], Paused: cur.Paused}, nil

	case ProfileEventPhotoRevoked:
		if onboardingRank(base) <= onboardingRank(ProfileStatusPendingPhoto) {
			// Nothing past the photo step to take back.
			return cur, nil
		}
		if IsOnboardingStatus(cur.Status) {
			return ProfileStatusState{Status: ProfileStatusPendingPhoto}, nil
		}
		// Paused or held: the hold stays, the remembered step drops.
		return ProfileStatusState{Status: cur.Status, Prior: ProfileStatusPendingPhoto, Paused: cur.Paused}, nil

	case ProfileEventPause:
		if IsOnboardingStatus(cur.Status) {
			return ProfileStatusState{Status: ProfileStatusPaused, Prior: cur.Status, Paused: true}, nil
		}
		if cur.Status == ProfileStatusPaused || isHoldStatus(cur.Status) {
			return ProfileStatusState{Status: cur.Status, Prior: base, Paused: true}, nil
		}
		return refuse()

	case ProfileEventUnpause:
		if cur.Status == ProfileStatusPaused {
			return ProfileStatusState{Status: base}, nil
		}
		if isHoldStatus(cur.Status) {
			// Never lifts a hold: only the flag clears.
			return ProfileStatusState{Status: cur.Status, Prior: base}, nil
		}
		if IsOnboardingStatus(cur.Status) {
			return ProfileStatusState{Status: cur.Status}, nil
		}
		return refuse()

	case ProfileEventReview, ProfileEventRestrict, ProfileEventSuspend:
		if actor == ProfileActorSystem &&
			(cur.Status == ProfileStatusSuspended || cur.Status == ProfileStatusRestricted) {
			// The system's age restriction never softens a suspension a
			// moderator imposed, and is idempotent on a restricted row.
			return cur, nil
		}
		if IsOnboardingStatus(cur.Status) || cur.Status == ProfileStatusPaused || isHoldStatus(cur.Status) {
			return ProfileStatusState{Status: holdForEvent[ev], Prior: base, Paused: cur.Paused}, nil
		}
		return refuse()

	case ProfileEventReinstate:
		if !isHoldStatus(cur.Status) {
			return refuse()
		}
		if cur.Paused {
			return ProfileStatusState{Status: ProfileStatusPaused, Prior: base, Paused: true}, nil
		}
		return ProfileStatusState{Status: base}, nil

	case ProfileEventDelete:
		return ProfileStatusState{Status: ProfileStatusDeleted, Prior: base, Paused: true}, nil
	}
	return refuse()
}

// TransitionProfileStatus applies ev to the user's profile in one
// transaction: lock the row, compute the edge, check onboarding evidence
// (dating_onboarding_step in setup.sql), and write guarded on the exact
// state that was read. Returns the profile as written (including a
// soft-deleted one), or ErrProfileNotFound when no row exists.
func (s *Store) TransitionProfileStatus(ctx context.Context, userID uuid.UUID, ev ProfileEvent, actor ProfileActor) (*Profile, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("profile transition: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		status   string
		prior    *string
		paused   bool
		evidence string
	)
	err = tx.QueryRow(ctx, `
        SELECT profile_status, prior_status, paused, dating_onboarding_step(user_id)
        FROM dating_profiles
        WHERE user_id = $1
        FOR UPDATE`, userID).Scan(&status, &prior, &paused, &evidence)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProfileNotFound
		}
		return nil, fmt.Errorf("profile transition: lock: %w", err)
	}
	cur := ProfileStatusState{Status: status, Paused: paused}
	if prior != nil {
		cur.Prior = *prior
	}
	decideFrom := cur
	if decideFrom.Prior == "" && !IsOnboardingStatus(decideFrom.Status) && decideFrom.Status != ProfileStatusDeleted {
		// Legacy paused/held row with no remembered step: use what the
		// evidence supports rather than guessing 'active'.
		decideFrom.Prior = evidence
	}
	next, err := NextProfileStatus(decideFrom, ev, actor)
	if err != nil {
		return nil, err
	}
	if edge, ok := onboardingEdges[ev]; ok && onboardingRank(evidence) < onboardingRank(edge[1]) {
		return nil, fmt.Errorf("%w: %s needs %s, evidence supports %s", ErrOnboardingIncomplete, ev, edge[1], evidence)
	}
	if ev == ProfileEventPhotoRevoked && onboardingRank(evidence) > onboardingRank(ProfileStatusPendingPhoto) {
		return nil, fmt.Errorf("%w: evidence supports %s", ErrPhotoStillApproved, evidence)
	}

	if next != cur {
		var nextPrior any
		if next.Prior != "" {
			nextPrior = next.Prior
		}
		tag, err := tx.Exec(ctx, `
            UPDATE dating_profiles
            SET profile_status = $2,
                prior_status   = $3,
                paused         = $4,
                deleted_at     = CASE WHEN $2::text = 'deleted' THEN COALESCE(deleted_at, now()) ELSE deleted_at END,
                updated_at     = now()
            WHERE user_id = $1
              AND profile_status = $5
              AND prior_status IS NOT DISTINCT FROM $6::text
              AND paused = $7`,
			userID, next.Status, nextPrior, next.Paused, cur.Status, prior, cur.Paused)
		if err != nil {
			return nil, fmt.Errorf("profile transition %s: write: %w", ev, err)
		}
		if tag.RowsAffected() != 1 {
			return nil, ErrProfileStatusConflict
		}
	}

	out, err := scanProfile(tx.QueryRow(ctx, `
        SELECT `+profileSelectCols+`
        FROM dating_profiles
        WHERE user_id = $1`, userID))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("profile transition: commit: %w", err)
	}
	return out, nil
}

// SoftDeleteProfile moves the profile to 'deleted' and stamps deleted_at
// (kept if already set). The 30-day grace begins then; cmd/data-purger
// sweeps rows where deleted_at < now() - 30d. Idempotent.
//
// DPDP §15.8 — soft-delete is the user-visible "delete account" action; the
// real purge runs after the grace window so accidental deletes can be
// reversed.
func (s *Store) SoftDeleteProfile(ctx context.Context, userID uuid.UUID) error {
	_, err := s.TransitionProfileStatus(ctx, userID, ProfileEventDelete, ProfileActorSystem)
	return err
}
