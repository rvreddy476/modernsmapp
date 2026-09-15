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

// TrustedContact is one of the user's trusted contacts.
type TrustedContact struct {
	ContactID            uuid.UUID `json:"contact_id"`
	ShareLocationOnPanic bool      `json:"share_location_on_panic"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// UpsertTrustedContact sets contactID as a trusted contact of userID (or
// updates its location opt-in). A new contact beyond MaxTrustedContacts is
// refused with ErrTrustedContactLimit. The cap is checked under a per-user
// advisory lock so two concurrent adds cannot both pass it.
func (s *Store) UpsertTrustedContact(ctx context.Context, userID, contactID uuid.UUID, shareLocation bool) (*TrustedContact, bool, error) {
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
	err = tx.QueryRow(ctx, `
        UPDATE dating_trusted_contacts
        SET share_location_on_panic = $3, updated_at = now()
        WHERE user_id = $1 AND contact_id = $2
        RETURNING share_location_on_panic, created_at, updated_at`,
		userID, contactID, shareLocation).Scan(&tc.ShareLocationOnPanic, &tc.CreatedAt, &tc.UpdatedAt)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("commit trusted contact: %w", err)
		}
		return tc, false, nil
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
        VALUES ($1, $2, $3)
        RETURNING share_location_on_panic, created_at, updated_at`,
		userID, contactID, shareLocation).Scan(&tc.ShareLocationOnPanic, &tc.CreatedAt, &tc.UpdatedAt); err != nil {
		return nil, false, fmt.Errorf("insert trusted contact: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit trusted contact: %w", err)
	}
	return tc, true, nil
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
// leaving out anyone blocked either way.
func (s *Store) ListTrustedContacts(ctx context.Context, userID uuid.UUID) ([]*TrustedContact, error) {
	rows, err := s.db.Query(ctx, `
        SELECT tc.contact_id, tc.share_location_on_panic, tc.created_at, tc.updated_at
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
		if err := rows.Scan(&tc.ContactID, &tc.ShareLocationOnPanic, &tc.CreatedAt, &tc.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan trusted contact: %w", err)
		}
		out = append(out, tc)
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
