package purge

import (
	"context"

	"github.com/google/uuid"
)

// ProfileService is the existing DPDP erase (store.PurgeUserData via
// service.PurgeProfile, which also emits dating.profile.purged) and the
// account-lifecycle hide, which pauses through the profile status machine
// and invalidates deck caches (service.SetProfileHidden).
type ProfileService interface {
	PurgeProfile(ctx context.Context, userID uuid.UUID) error
	SetProfileHidden(ctx context.Context, userID uuid.UUID, hidden bool) error
}

// AuxStore covers what PurgeUserData does not: account risk and device
// fingerprints.
type AuxStore interface {
	PurgeUserAuxiliary(ctx context.Context, userID uuid.UUID) error
}

// Eraser composes them. Satisfies purge.Eraser and purge.Hider.
type StoreEraser struct {
	svc ProfileService
	aux AuxStore
}

// NewEraser builds the adapter.
func NewEraser(svc ProfileService, aux AuxStore) *StoreEraser { return &StoreEraser{svc: svc, aux: aux} }

// PurgeUser runs the library purge then the auxiliary deletes. Both are
// idempotent (0 rows affected on a redelivery).
func (e *StoreEraser) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	if err := e.svc.PurgeProfile(ctx, userID); err != nil {
		return err
	}
	return e.aux.PurgeUserAuxiliary(ctx, userID)
}

// SetUserHidden pauses (hidden=true) or resumes (hidden=false) the dating
// profile through the status machine. Resuming restores the remembered step
// and never lifts a moderation hold.
func (e *StoreEraser) SetUserHidden(ctx context.Context, userID uuid.UUID, hidden bool, _ string) error {
	return e.svc.SetProfileHidden(ctx, userID, hidden)
}
