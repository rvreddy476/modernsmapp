package postgres

// Sell scope (migration 045): what a shop may list, and the writes that set it.
//
// all_categories is the shop the platform has always had, now with a GSTIN.
// books_only is the founder's 24 Oct 2026 path for a seller with a PAN and a
// proof of address and no GSTIN, who may list only in book categories and
// only on a zero-rated tax class. The rules live in the service
// (internal/service/sellscope.go); this file holds the SQL.

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrSellScopeLocked refuses a scope change once the application has left
// the seller's hands (submitted, under review, approved, ...). The way an
// approved books-only shop widens is the upgrade, never the onboarding step.
var ErrSellScopeLocked = errors.New("commerce: the sell scope can only be chosen while the application is a draft or has changes requested")

// ErrSellScopeNotBooksOnly refuses an upgrade for a shop that is not
// books-only: there is nothing to upgrade from.
var ErrSellScopeNotBooksOnly = errors.New("commerce: only a books-only shop can be upgraded to all categories")

// SellScopeWrite is one save of the "What will you sell?" step.
//
// GSTIN is written as given, NULL included: a books-only shop holds no GSTIN
// (it would print on its invoices), and switching a draft to books_only
// clears one saved earlier. LegalBusinessName nil leaves the stored name
// alone. PAN follows SealedIdentifierWrite's Supplied rule.
type SellScopeWrite struct {
	Scope             string
	GSTIN             *string
	LegalBusinessName *string
	PAN               SealedIdentifierWrite
}

// SaveOnboardingSellScope writes the scope step for the user's own draft.
//
// Guarded on the status in the same statement, so a scope cannot change
// under a reviewer: ErrSellScopeLocked when the row exists but has left
// draft/changes_required, ErrNoSellerRow when there is no row at all.
func (s *Store) SaveOnboardingSellScope(ctx context.Context, userID uuid.UUID, w SellScopeWrite) error {
	if !ValidSellScope(w.Scope) {
		return errors.New("commerce: refusing to store an unknown sell scope")
	}
	if err := w.PAN.validate(); err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE sellers SET
		  sell_scope          = $2,
		  gst_number          = $3,
		  legal_business_name = COALESCE($4, legal_business_name),
		  pan_number          = CASE WHEN $5 THEN $6 ELSE pan_number END,
		  pan_enc             = CASE WHEN $5 THEN $7::bytea ELSE pan_enc END,
		  pan_masked          = CASE WHEN $5 THEN NULLIF($8,'') ELSE pan_masked END,
		  pan_hash            = CASE WHEN $5 THEN NULLIF($9,'') ELSE pan_hash END,
		  pan_key_version     = CASE WHEN $5 THEN NULLIF($10::int,0) ELSE pan_key_version END,
		  onboarding_step     = GREATEST(onboarding_step, 2),
		  updated_at          = NOW()
		WHERE user_id = $1 AND status IN ('draft','changes_required')`,
		userID, w.Scope, w.GSTIN, w.LegalBusinessName,
		w.PAN.Supplied, w.PAN.plaintext(), w.PAN.Enc, w.PAN.Masked, w.PAN.Hash, w.PAN.KeyVersion,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var exists bool
	if err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM sellers WHERE user_id = $1)`, userID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNoSellerRow
	}
	return ErrSellScopeLocked
}

// UpgradeSellScopeToAllCategories moves a books-only shop to all categories
// with the GSTIN that makes that lawful.
//
// Guarded on the CURRENT scope in the statement, so two racing upgrades
// write once and the second is told there is nothing to upgrade.
func (s *Store) UpgradeSellScopeToAllCategories(ctx context.Context, sellerID uuid.UUID, gstin string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE sellers SET sell_scope = 'all_categories', gst_number = $2, updated_at = NOW()
		 WHERE id = $1 AND sell_scope = 'books_only'`, sellerID, gstin)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSellScopeNotBooksOnly
	}
	return nil
}

// CategoryIsBookCategory reports whether a category, or any category above
// it, is flagged is_book_category. False for an id that names nothing.
//
// Inherited downwards so a child added under Textbooks is a book category
// without anybody remembering to flag it; never upwards, and "Books &
// Stationery" itself is not flagged, so a stationery child added there later
// does not become a place a GSTIN-less seller may list taxable goods.
// The walk is capped at the taxonomy's depth limit, like CategoryPath.
func (s *Store) CategoryIsBookCategory(ctx context.Context, id uuid.UUID) (bool, error) {
	var isBook bool
	err := s.db.QueryRow(ctx, `
		WITH RECURSIVE up AS (
		    SELECT c.id, c.parent_id, c.is_book_category, 0 AS depth
		      FROM product_categories c WHERE c.id = $1
		    UNION ALL
		    SELECT p.id, p.parent_id, p.is_book_category, up.depth + 1
		      FROM product_categories p JOIN up ON p.id = up.parent_id
		     WHERE up.depth < 16
		)
		SELECT COALESCE(bool_or(is_book_category), FALSE) FROM up`, id).Scan(&isBook)
	return isBook, err
}

// BookCategoryIDs lists every active category a books-only shop may list in:
// the flagged ones and everything beneath them. Sorted by id so the answer is
// byte-stable for a client to cache and for a golden to pin.
func (s *Store) BookCategoryIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		WITH RECURSIVE down AS (
		    SELECT c.id, 0 AS depth FROM product_categories c WHERE c.is_book_category
		    UNION
		    SELECT ch.id, down.depth + 1
		      FROM product_categories ch JOIN down ON ch.parent_id = down.id
		     WHERE down.depth < 16
		)
		SELECT DISTINCT d.id FROM down d
		  JOIN product_categories c ON c.id = d.id AND c.is_active
		 ORDER BY d.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// TaxClassIsZeroRated reports whether a tax class charges no GST at all —
// every component zero. False for an id that names nothing.
func (s *Store) TaxClassIsZeroRated(ctx context.Context, id uuid.UUID) (bool, error) {
	var zero bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM tax_classes
		     WHERE id = $1
		       AND cgst_percentage = 0 AND sgst_percentage = 0
		       AND igst_percentage = 0 AND cess_percentage = 0)`, id).Scan(&zero)
	return zero, err
}

// sellScopeOrDefault is the scope a new row is created with: the one asked
// for, or the column's own default when none was.
func sellScopeOrDefault(s string) string {
	if s == "" {
		return SellScopeAllCategories
	}
	return s
}
