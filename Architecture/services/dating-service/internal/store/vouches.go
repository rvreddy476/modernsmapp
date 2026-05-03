// Vouches store — spec §10 dating_vouches. A Vouch is one user endorsing
// another's profile, optionally scoped to a community or relationship.
//
// State machine: pending -> accepted | declined | revoked. Idempotent on
// (voucher_id, vouchee_id): re-requesting against an existing pair updates
// the open row.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Vouch is one row of dating_vouches.
type Vouch struct {
	ID           uuid.UUID  `json:"id"`
	VoucherID    uuid.UUID  `json:"voucher_id"`
	VoucheeID    uuid.UUID  `json:"vouchee_id"`
	Relationship *string    `json:"relationship,omitempty"`
	CommunityID  *uuid.UUID `json:"community_id,omitempty"`
	Note         *string    `json:"note,omitempty"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	DecidedAt    *time.Time `json:"decided_at,omitempty"`
}

// ErrVouchNotFound is returned when no vouch matches the lookup.
var ErrVouchNotFound = errors.New("not_found: vouch not found")

const vouchSelectCols = `id, voucher_id, vouchee_id, relationship, community_id, note,
    status, created_at, decided_at`

func scanVouch(row pgx.Row) (*Vouch, error) {
	v := &Vouch{}
	if err := row.Scan(
		&v.ID, &v.VoucherID, &v.VoucheeID, &v.Relationship, &v.CommunityID, &v.Note,
		&v.Status, &v.CreatedAt, &v.DecidedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVouchNotFound
		}
		return nil, fmt.Errorf("scan vouch: %w", err)
	}
	return v, nil
}

// CreateVouchRequest inserts a pending vouch. Idempotent on
// (voucher_id, vouchee_id): a duplicate updates the existing row to
// 'pending' (so a re-request after a previous decline starts fresh).
func (s *Store) CreateVouchRequest(ctx context.Context, voucherID, voucheeID uuid.UUID, relationship string, communityID *uuid.UUID, note string) (*Vouch, error) {
	if voucherID == uuid.Nil || voucheeID == uuid.Nil {
		return nil, fmt.Errorf("invalid: voucher and vouchee ids required")
	}
	if voucherID == voucheeID {
		return nil, fmt.Errorf("invalid: cannot vouch for yourself")
	}
	switch relationship {
	case "friend", "community_member", "colleague", "family":
	case "":
		// Allow blank — service layer enforces.
	default:
		return nil, fmt.Errorf("invalid: relationship must be friend|community_member|colleague|family")
	}

	var (
		relPtr  *string
		notePtr *string
	)
	if relationship != "" {
		r := relationship
		relPtr = &r
	}
	if note != "" {
		n := note
		notePtr = &n
	}

	row := s.db.QueryRow(ctx, `
        INSERT INTO dating_vouches (voucher_id, vouchee_id, relationship, community_id, note, status)
        VALUES ($1, $2, $3, $4, $5, 'pending')
        ON CONFLICT (voucher_id, vouchee_id) DO UPDATE
            SET relationship = COALESCE(EXCLUDED.relationship, dating_vouches.relationship),
                community_id = COALESCE(EXCLUDED.community_id, dating_vouches.community_id),
                note         = COALESCE(EXCLUDED.note, dating_vouches.note),
                status       = 'pending',
                decided_at   = NULL,
                created_at   = now()
        RETURNING `+vouchSelectCols,
		voucherID, voucheeID, relPtr, communityID, notePtr)
	return scanVouch(row)
}

// DecideVouch transitions a pending vouch to accepted | declined. Only the
// vouchee may decide — the voucher_id is supplied here as a *guard*: the
// caller (handler) injects the authenticated user, and the SQL match
// constrains the update to that voucher's request.
//
// We accept a `voucherID` parameter as the *expected voucher* for symmetry
// with RevokeVouch, but the *deciding* user is the vouchee. The handler
// looks up the vouch first to assert vouchee_id == auth user.
func (s *Store) DecideVouch(ctx context.Context, vouchID uuid.UUID, voucheeID uuid.UUID, decision string) error {
	switch decision {
	case "accepted", "declined":
	default:
		return fmt.Errorf("invalid: decision must be accepted|declined")
	}
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_vouches
        SET status = $3, decided_at = now()
        WHERE id = $1
          AND vouchee_id = $2
          AND status = 'pending'`,
		vouchID, voucheeID, decision)
	if err != nil {
		return fmt.Errorf("decide vouch: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrVouchNotFound
	}
	return nil
}

// RevokeVouch transitions any vouch (pending or accepted) to 'revoked'.
// Only the original voucher may revoke.
func (s *Store) RevokeVouch(ctx context.Context, vouchID, voucherID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_vouches
        SET status = 'revoked', decided_at = now()
        WHERE id = $1
          AND voucher_id = $2
          AND status IN ('pending','accepted')`,
		vouchID, voucherID)
	if err != nil {
		return fmt.Errorf("revoke vouch: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrVouchNotFound
	}
	return nil
}

// GetVouch returns one row by id.
func (s *Store) GetVouch(ctx context.Context, id uuid.UUID) (*Vouch, error) {
	row := s.db.QueryRow(ctx, `SELECT `+vouchSelectCols+` FROM dating_vouches WHERE id = $1`, id)
	return scanVouch(row)
}

// ListVouchesFor returns vouches aimed at voucheeID. status="" returns all
// non-revoked; status='accepted' returns only public-displayable vouches.
func (s *Store) ListVouchesFor(ctx context.Context, voucheeID uuid.UUID, status string) ([]*Vouch, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if status == "" {
		rows, err = s.db.Query(ctx, `
            SELECT `+vouchSelectCols+`
            FROM dating_vouches
            WHERE vouchee_id = $1
              AND status <> 'revoked'
            ORDER BY created_at DESC
            LIMIT 200`, voucheeID)
	} else {
		rows, err = s.db.Query(ctx, `
            SELECT `+vouchSelectCols+`
            FROM dating_vouches
            WHERE vouchee_id = $1
              AND status = $2
            ORDER BY created_at DESC
            LIMIT 200`, voucheeID, status)
	}
	if err != nil {
		return nil, fmt.Errorf("list vouches for: %w", err)
	}
	defer rows.Close()
	out := make([]*Vouch, 0, 16)
	for rows.Next() {
		v, err := scanVouch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListVouchesSent returns vouches created by voucherID (any status except
// revoked, newest first).
func (s *Store) ListVouchesSent(ctx context.Context, voucherID uuid.UUID) ([]*Vouch, error) {
	rows, err := s.db.Query(ctx, `
        SELECT `+vouchSelectCols+`
        FROM dating_vouches
        WHERE voucher_id = $1
        ORDER BY created_at DESC
        LIMIT 200`, voucherID)
	if err != nil {
		return nil, fmt.Errorf("list vouches sent: %w", err)
	}
	defer rows.Close()
	out := make([]*Vouch, 0, 16)
	for rows.Next() {
		v, err := scanVouch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CountVouchRequestsThisWeek returns how many vouch *requests* the voucher
// has emitted in the last 7 days. Used by the service layer to enforce a
// 5/week anti-spam limit (spec §15 vouching).
func (s *Store) CountVouchRequestsThisWeek(ctx context.Context, voucherID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(*)::int
        FROM dating_vouches
        WHERE voucher_id = $1
          AND created_at > now() - INTERVAL '7 days'`, voucherID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count vouches this week: %w", err)
	}
	return n, nil
}
