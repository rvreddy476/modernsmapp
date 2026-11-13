package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/identity-shared/verification"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Impersonation holds (short-posts design 3.4). profile.reserved_names holds
// the skeleton keys of every marked account's display name, handle and
// organisation name (identity-shared/verification/names.go):
//
//   - an Official or Government account's keys with their one-deletion
//     variants, so a name one edit away is held;
//   - a verified person's keys only, so only the same name is held.
//
// The rows are rebuilt from the profile row by refreshReservedNamesTx, inside
// every transaction that grants a mark, and by RefreshReservedNames after a
// rename or handle change.

// ReservedNameHit is the account holding a name.
type ReservedNameHit struct {
	UserID uuid.UUID
	Kind   string
}

// FindReservedNameHolder returns another account whose reserved names meet
// candidate, or nil. exclude is the account making the change: its own
// names never hold it.
func (s *Store) FindReservedNameHolder(ctx context.Context, exclude uuid.UUID, candidate string) (*ReservedNameHit, error) {
	keys := verification.CandidateKeys(candidate)
	if len(keys) == 0 {
		return nil, nil
	}
	fuzzy := verification.FuzzyVariantsOf(keys)
	var h ReservedNameHit
	err := s.db.QueryRow(ctx, `
		SELECT user_id, kind FROM profile.reserved_names
		 WHERE user_id <> $1
		   AND ((kind <> 'person' AND variant = ANY($2::text[]))
		     OR (kind = 'person' AND variant = ANY($3::text[])))
		 ORDER BY kind DESC
		 LIMIT 1`, exclude, fuzzy, keys).Scan(&h.UserID, &h.Kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// RefreshReservedNames rebuilds userID's reserved names from the profile row.
func (s *Store) RefreshReservedNames(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("reserved names: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := refreshReservedNamesTx(ctx, tx, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReservedNameRows is what a profile reserves: one (variant, kind) per row.
// Exported for tests; the store writes exactly these.
func ReservedNameRows(level string, isVerified bool, names ...string) (variants []string, kind string) {
	if !isVerified {
		return nil, ""
	}
	switch level {
	case verification.LevelPerson, verification.LevelOrganisation, verification.LevelGovernment:
		kind = level
	default:
		return nil, ""
	}
	set := map[string]bool{}
	for _, n := range names {
		keys := verification.ReservedKeys(n)
		if kind != verification.LevelPerson {
			keys = verification.FuzzyVariantsOf(keys)
		}
		for _, k := range keys {
			set[k] = true
		}
	}
	for k := range set {
		variants = append(variants, k)
	}
	return variants, kind
}

func refreshReservedNamesTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	var displayName, level, orgName string
	var username *string
	var verified bool
	err := tx.QueryRow(ctx, `
		SELECT display_name, username, verification_level, is_verified, organisation_name
		  FROM profile.profiles WHERE user_id = $1 FOR UPDATE`, userID).
		Scan(&displayName, &username, &level, &verified, &orgName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reserved names: read profile: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM profile.reserved_names WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("reserved names: clear: %w", err)
	}
	names := []string{displayName}
	if username != nil {
		names = append(names, *username)
	}
	if level == verification.LevelOrganisation || level == verification.LevelGovernment {
		names = append(names, orgName)
	}
	variants, kind := ReservedNameRows(level, verified, names...)
	if len(variants) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO profile.reserved_names (variant, user_id, kind)
		SELECT v, $2, $3 FROM unnest($1::text[]) AS v
		ON CONFLICT (variant, user_id) DO UPDATE SET kind = EXCLUDED.kind`,
		variants, userID, kind); err != nil {
		return fmt.Errorf("reserved names: insert: %w", err)
	}
	return nil
}
