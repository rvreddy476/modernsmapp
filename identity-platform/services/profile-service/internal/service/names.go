package service

import (
	"context"
	"strings"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/atpost/identity-shared/verification"
	"github.com/google/uuid"
)

// Impersonation holds and verification marks across renames
// (docs/designs/short-posts-design.md section 3.4).

// NameReservations is the reserved-name storage (*store.Store).
type NameReservations interface {
	// FindReservedNameHolder returns another account whose reserved names
	// meet candidate, or nil.
	FindReservedNameHolder(ctx context.Context, exclude uuid.UUID, candidate string) (*store.ReservedNameHit, error)
	// RefreshReservedNames rebuilds userID's reserved names from its row.
	RefreshReservedNames(ctx context.Context, userID uuid.UUID) error
}

// WithNameReservations replaces the reserved-name storage. For tests.
func (s *Service) WithNameReservations(n NameReservations) *Service {
	s.names = n
	return s
}

func isMarkedLevel(level string) bool {
	for _, l := range verification.MarkedLevels {
		if level == l {
			return true
		}
	}
	return false
}

// nameHeld reports whether candidate is held by an account other than
// userID. A blank candidate is never held.
func (s *Service) nameHeld(ctx context.Context, userID uuid.UUID, candidate string) (bool, error) {
	if s.names == nil || strings.TrimSpace(candidate) == "" {
		return false, nil
	}
	hit, err := s.names.FindReservedNameHolder(ctx, userID, candidate)
	if err != nil {
		return false, err
	}
	if hit != nil {
		s.log.Info("name change held as possible impersonation", "user_id", userID, "held_by", hit.UserID, "kind", hit.Kind)
		return true, nil
	}
	return false, nil
}

// checkNamesNotReserved refuses a display name or handle that another
// account holds. A value equal to the stored one is not a change and is not
// checked, so a profile saved with its current name never fails.
func (s *Service) checkNamesNotReserved(ctx context.Context, userID uuid.UUID, before *store.Profile, displayName, username *string) error {
	if displayName != nil && (before == nil || strings.TrimSpace(*displayName) != strings.TrimSpace(before.DisplayName)) {
		if held, err := s.nameHeld(ctx, userID, *displayName); err != nil {
			return err
		} else if held {
			return ErrNameReserved
		}
	}
	if username != nil {
		stored := ""
		if before != nil && before.Username != nil {
			stored = *before.Username
		}
		if *username != stored {
			if held, err := s.nameHeld(ctx, userID, *username); err != nil {
				return err
			} else if held {
				return ErrNameReserved
			}
		}
	}
	return nil
}

// renameKeepsMark reports whether a marked profile may keep its mark under
// newName. Official and Government marks keep it only for the organisation
// name (corporate suffixes and case aside); a person keeps it only for the
// DigiLocker name. False on any doubt.
func (s *Service) renameKeepsMark(ctx context.Context, userID uuid.UUID, before *store.Profile, newName string) bool {
	if before != nil && (before.VerificationLevel == verification.LevelOrganisation || before.VerificationLevel == verification.LevelGovernment) {
		return verification.OrgNamesMatch(newName, before.OrganisationName)
	}
	return s.person != nil && s.person.RenameKeepsTick(ctx, userID, newName)
}

// refreshReservedNames rebuilds the account's reserved names after a name
// change. A failure is logged: the write already happened, and the next
// change or approval rebuilds them.
func (s *Service) refreshReservedNames(ctx context.Context, userID uuid.UUID) {
	if s.names == nil {
		return
	}
	if err := s.names.RefreshReservedNames(ctx, userID); err != nil {
		s.log.Error("reserved names refresh failed", "user_id", userID, "err", err)
	}
}

// signupDisplayName is the display name a new profile starts with: the
// registration name, or a neutral "User xxxxxxxx" when there is none or when
// it is held as possible impersonation of an Official, Government or
// verified-person name. The profile is still created either way (a dropped
// UserRegistered would leave an account with no profile); the user can
// rename, and a rename runs the same hold.
func (s *Service) signupDisplayName(ctx context.Context, userID uuid.UUID, firstName, lastName string) (string, error) {
	neutral := "User " + userID.String()[:8]
	displayName := firstName + " " + lastName
	if displayName == " " {
		return neutral, nil
	}
	held, err := s.nameHeld(ctx, userID, displayName)
	if err != nil {
		return "", err
	}
	if held {
		return neutral, nil
	}
	return displayName, nil
}
