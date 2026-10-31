// Trusted contacts (Dating plan lane D8): the people a user's safety alerts
// and live location may go to. Eligibility (an accepted connection or a
// current match) is the service's check; this file holds the rows, the cap
// and the blocked-pair filter.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MaxTrustedContacts is the per-user cap.
const MaxTrustedContacts = 3

// ErrTrustedContactLimit is returned when adding a contact beyond the cap.
var ErrTrustedContactLimit = errors.New("trusted contact limit reached")

// ErrTrustedContactNotFound is returned when removing a contact that is not set.
var ErrTrustedContactNotFound = errors.New("not_found: trusted contact not found")

// PanicLocationConsentVersion is the version of the consent copy a member
// must have been shown before a trusted contact may receive their exact
// location when they press panic (audit 2026-10-30 M4, founder decision
// 31 Oct). Bump it, with new copy, whenever the wording changes; a member
// who agreed to an older version is then asked again.
const PanicLocationConsentVersion = "panic_location_v1"

// PanicLocationConsentCopy is the wording of PanicLocationConsentVersion. It
// is served to the app with the contacts list and stored on the row when the
// member agrees, so the record says exactly what they agreed to.
const PanicLocationConsentCopy = "If I press panic, send this person my exact location along with the alert. I can turn this off at any time."

// PanicLocationConsentAccepted reports whether consent recorded under
// version still counts at panic time. Only the current copy does.
func PanicLocationConsentAccepted(version string) bool {
	return version == PanicLocationConsentVersion
}

// TrustedContact is one of the user's trusted contacts.
//
// ShareLocationOnPanic, as served, is the EFFECTIVE opt-in: the stored flag
// AND a recorded consent under an accepted copy version. A row from before
// the consent record (or one an older pod wrote with the flag and no
// consent) reads false and gets no location.
type TrustedContact struct {
	ContactID            uuid.UUID `json:"contact_id"`
	ShareLocationOnPanic bool      `json:"share_location_on_panic"`
	// LocationConsentAt is when the member agreed to send this contact
	// their exact location on panic; nil when they have not (or revoked).
	LocationConsentAt *time.Time `json:"location_consent_at"`
	// LocationConsentVersion is the copy version they agreed to.
	LocationConsentVersion *string   `json:"location_consent_version"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

// SharesPanicLocation is the panic-time consent gate: the exact point goes
// to this contact only when the member turned sharing on AND a consent is
// recorded under a copy version that still counts.
func (tc *TrustedContact) SharesPanicLocation() bool {
	if tc == nil || !tc.ShareLocationOnPanic || tc.LocationConsentAt == nil || tc.LocationConsentVersion == nil {
		return false
	}
	return PanicLocationConsentAccepted(*tc.LocationConsentVersion)
}

// trustedContactColumns is the column list every read scans with
// scanTrustedContact (the raw flag; normalise() derives the served one).
const trustedContactColumns = `share_location_on_panic, location_consent_at, location_consent_version, created_at, updated_at`

// revokePanicLocationSet withdraws location-on-panic consent: the flag goes
// off, the consent record is cleared, and revoked_at notes when (only when
// something was on, so a no-op revoke does not invent one).
const revokePanicLocationSet = `share_location_on_panic = false,
            location_consent_revoked_at = CASE WHEN location_consent_at IS NOT NULL OR share_location_on_panic
                                               THEN now() ELSE location_consent_revoked_at END,
            location_consent_at = NULL, location_consent_version = NULL, location_consent_copy = NULL,
            updated_at = now()`

func (tc *TrustedContact) scanDest() []any {
	return []any{&tc.ShareLocationOnPanic, &tc.LocationConsentAt, &tc.LocationConsentVersion, &tc.CreatedAt, &tc.UpdatedAt}
}

// normalise replaces the raw stored flag with the effective opt-in, and
// drops a consent record that no longer counts, so the client never sees
// "on" for a contact who would not get the point.
func (tc *TrustedContact) normalise() *TrustedContact {
	on := tc.SharesPanicLocation()
	tc.ShareLocationOnPanic = on
	if !on {
		tc.LocationConsentAt, tc.LocationConsentVersion = nil, nil
	}
	return tc
}

// UpsertTrustedContact sets contactID as a trusted contact of userID. A new
// contact is always stored NOT sharing its location on panic: that needs
// the separate consent action (SetPanicLocationConsent). On an existing
// contact, revokeLocation=true withdraws any location consent; false leaves
// it as it is (an add can never grant it). A new contact beyond
// MaxTrustedContacts is refused with ErrTrustedContactLimit. The cap is
// checked under a per-user advisory lock so two concurrent adds cannot both
// pass it.
func (s *Store) UpsertTrustedContact(ctx context.Context, userID, contactID uuid.UUID, revokeLocation bool) (*TrustedContact, bool, error) {
	if userID == uuid.Nil || contactID == uuid.Nil {
		return nil, false, fmt.Errorf("invalid: user and contact ids required")
	}
	if userID == contactID {
		return nil, false, fmt.Errorf("invalid: you cannot be your own trusted contact")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin trusted contact: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "dating_trusted_contacts:"+userID.String()); err != nil {
		return nil, false, fmt.Errorf("lock trusted contacts: %w", err)
	}
	tc := &TrustedContact{ContactID: contactID}
	if revokeLocation {
		err = tx.QueryRow(ctx, `
        UPDATE dating_trusted_contacts
        SET `+revokePanicLocationSet+`
        WHERE user_id = $1 AND contact_id = $2
        RETURNING `+trustedContactColumns, userID, contactID).Scan(tc.scanDest()...)
	} else {
		err = tx.QueryRow(ctx, `
        SELECT `+trustedContactColumns+`
        FROM dating_trusted_contacts
        WHERE user_id = $1 AND contact_id = $2`, userID, contactID).Scan(tc.scanDest()...)
	}
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("commit trusted contact: %w", err)
		}
		return tc.normalise(), false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("update trusted contact: %w", err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*)::int FROM dating_trusted_contacts WHERE user_id = $1`, userID).Scan(&n); err != nil {
		return nil, false, fmt.Errorf("count trusted contacts: %w", err)
	}
	if n >= MaxTrustedContacts {
		return nil, false, ErrTrustedContactLimit
	}
	if err := tx.QueryRow(ctx, `
        INSERT INTO dating_trusted_contacts (user_id, contact_id, share_location_on_panic)
        VALUES ($1, $2, false)
        RETURNING `+trustedContactColumns, userID, contactID).Scan(tc.scanDest()...); err != nil {
		return nil, false, fmt.Errorf("insert trusted contact: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit trusted contact: %w", err)
	}
	return tc.normalise(), true, nil
}

// SetPanicLocationConsent records the member's explicit consent to send
// contactID their exact location when they press panic, under the copy
// version they were shown (which the caller has checked is current). The
// contact must already be set and not blocked either way; otherwise
// ErrTrustedContactNotFound.
func (s *Store) SetPanicLocationConsent(ctx context.Context, userID, contactID uuid.UUID, version, copyText string) (*TrustedContact, error) {
	tc := &TrustedContact{ContactID: contactID}
	err := s.db.QueryRow(ctx, `
        UPDATE dating_trusted_contacts tc
        SET share_location_on_panic = true,
            location_consent_at = now(),
            location_consent_version = $3,
            location_consent_copy = $4,
            location_consent_revoked_at = NULL,
            updated_at = now()
        WHERE tc.user_id = $1 AND tc.contact_id = $2
          AND NOT `+blockedPairPredicate("tc.user_id", "tc.contact_id")+`
        RETURNING `+trustedContactColumns, userID, contactID, version, copyText).Scan(tc.scanDest()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTrustedContactNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("record panic location consent: %w", err)
	}
	return tc.normalise(), nil
}

// RevokePanicLocationConsent turns location sharing off for contactID and
// clears the consent record (keeping when it was revoked). It takes effect
// for the next panic: the notify context reads the row when it pages.
// ErrTrustedContactNotFound when the contact is not set.
func (s *Store) RevokePanicLocationConsent(ctx context.Context, userID, contactID uuid.UUID) (*TrustedContact, error) {
	tc := &TrustedContact{ContactID: contactID}
	err := s.db.QueryRow(ctx, `
        UPDATE dating_trusted_contacts
        SET `+revokePanicLocationSet+`
        WHERE user_id = $1 AND contact_id = $2
        RETURNING `+trustedContactColumns, userID, contactID).Scan(tc.scanDest()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTrustedContactNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("revoke panic location consent: %w", err)
	}
	return tc.normalise(), nil
}

// RemoveTrustedContact removes one trusted contact.
func (s *Store) RemoveTrustedContact(ctx context.Context, userID, contactID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM dating_trusted_contacts WHERE user_id = $1 AND contact_id = $2`, userID, contactID)
	if err != nil {
		return fmt.Errorf("remove trusted contact: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTrustedContactNotFound
	}
	return nil
}

// ListTrustedContacts returns the user's trusted contacts, oldest first,
// leaving out anyone blocked either way. ShareLocationOnPanic is the
// effective opt-in (see TrustedContact).
func (s *Store) ListTrustedContacts(ctx context.Context, userID uuid.UUID) ([]*TrustedContact, error) {
	rows, err := s.db.Query(ctx, `
        SELECT tc.contact_id, tc.share_location_on_panic, tc.location_consent_at,
               tc.location_consent_version, tc.created_at, tc.updated_at
        FROM dating_trusted_contacts tc
        WHERE tc.user_id = $1
          AND NOT `+blockedPairPredicate("tc.user_id", "tc.contact_id")+`
        ORDER BY tc.created_at, tc.contact_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list trusted contacts: %w", err)
	}
	defer rows.Close()
	out := make([]*TrustedContact, 0, MaxTrustedContacts)
	for rows.Next() {
		tc := &TrustedContact{}
		if err := rows.Scan(append([]any{&tc.ContactID}, tc.scanDest()...)...); err != nil {
			return nil, fmt.Errorf("scan trusted contact: %w", err)
		}
		out = append(out, tc.normalise())
	}
	return out, rows.Err()
}

// IsTrustedContact reports whether contactID is a trusted contact of userID
// and the pair is not blocked either way.
func (s *Store) IsTrustedContact(ctx context.Context, userID, contactID uuid.UUID) (bool, error) {
	var ok bool
	if err := s.db.QueryRow(ctx, `
        SELECT EXISTS (SELECT 1 FROM dating_trusted_contacts
                       WHERE user_id = $1 AND contact_id = $2)
           AND NOT `+blockedPairPredicate("$1::uuid", "$2::uuid"), userID, contactID).Scan(&ok); err != nil {
		return false, fmt.Errorf("check trusted contact: %w", err)
	}
	return ok, nil
}

// HasOpenMatch reports whether the pair has an open match (matched,
// conversing or quiet) and is not blocked either way.
func (s *Store) HasOpenMatch(ctx context.Context, x, y uuid.UUID) (bool, error) {
	if x == uuid.Nil || y == uuid.Nil || x == y {
		return false, nil
	}
	a, b := canonicalPair(x, y)
	var ok bool
	if err := s.db.QueryRow(ctx, `
        SELECT EXISTS (SELECT 1 FROM dating_matches
                       WHERE user_a = $1 AND user_b = $2 AND status IN `+openMatchStatuses+`)
           AND NOT `+blockedPairPredicate("$1::uuid", "$2::uuid"), a, b).Scan(&ok); err != nil {
		return false, fmt.Errorf("check open match: %w", err)
	}
	return ok, nil
}
