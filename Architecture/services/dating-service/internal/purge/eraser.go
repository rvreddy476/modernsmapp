package purge

import (
	"context"

	"github.com/google/uuid"
)

// ProfilePurger is the existing DPDP erase (store.PurgeUserData via
// service.PurgeProfile, which also emits dating.profile.purged) plus the
// auxiliary tables it leaves behind, and the pause flag for hide.
type ProfilePurger interface {
	PurgeProfile(ctx context.Context, userID uuid.UUID) error
}

// AuxStore covers what PurgeUserData does not: account risk and device
// fingerprints, and the pause flag used for hide/unhide.
type AuxStore interface {
	PurgeUserAuxiliary(ctx context.Context, userID uuid.UUID) error
	SetProfilePaused(ctx context.Context, userID uuid.UUID, paused bool) error
}

// Eraser composes them. Satisfies purge.Eraser and purge.Hider.
type StoreEraser struct {
	svc ProfilePurger
	aux AuxStore
}

// NewEraser builds the adapter.
func NewEraser(svc ProfilePurger, aux AuxStore) *StoreEraser { return &StoreEraser{svc: svc, aux: aux} }

// PurgeUser runs the library purge then the auxiliary deletes. Both are
// idempotent (0 rows affected on a redelivery).
func (e *StoreEraser) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	if err := e.svc.PurgeProfile(ctx, userID); err != nil {
		return err
	}
	return e.aux.PurgeUserAuxiliary(ctx, userID)
}

// SetUserHidden pauses (hidden=true) or resumes (hidden=false) the dating
// profile without touching deleted_at.
func (e *StoreEraser) SetUserHidden(ctx context.Context, userID uuid.UUID, hidden bool, _ string) error {
	return e.aux.SetProfilePaused(ctx, userID, hidden)
}
