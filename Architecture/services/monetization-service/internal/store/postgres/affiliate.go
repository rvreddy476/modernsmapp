package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// Models
// ---------------------------------------------------------------------------

// AffiliateLink represents a creator's affiliate link to a marketplace listing.
type AffiliateLink struct {
	ID              uuid.UUID `json:"id"`
	CreatorID       uuid.UUID `json:"creator_id"`
	ListingID       uuid.UUID `json:"listing_id"`
	CommissionPct   float32   `json:"commission_pct"`
	CommissionFlat  *float64  `json:"commission_flat,omitempty"`
	LinkCode        string    `json:"link_code"`
	ClickCount      int64     `json:"click_count"`
	ConversionCount int64     `json:"conversion_count"`
	TotalEarned     float64   `json:"total_earned"`
	IsActive        bool      `json:"is_active"`
	CreatedAt       time.Time `json:"created_at"`
}

// AffiliateConversion records a purchase made through an affiliate link.
type AffiliateConversion struct {
	ID            uuid.UUID `json:"id"`
	AffiliateID   uuid.UUID `json:"affiliate_id"`
	OrderID       uuid.UUID `json:"order_id"`
	BuyerID       uuid.UUID `json:"buyer_id"`
	CommissionAmt float64   `json:"commission_amt"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

// ---------------------------------------------------------------------------
// Affiliate Links
// ---------------------------------------------------------------------------

// CreateAffiliateLink inserts a new affiliate link and returns the created record.
// link_code is auto-generated as the first 8 characters of a new UUID.
func (s *Store) CreateAffiliateLink(ctx context.Context, l *AffiliateLink) (*AffiliateLink, error) {
	l.LinkCode = uuid.New().String()[:8]

	err := s.db.QueryRow(ctx, `
		INSERT INTO affiliate_links
			(creator_id, listing_id, commission_pct, commission_flat, link_code, is_active, created_at)
		VALUES ($1, $2, $3, $4, $5, TRUE, NOW())
		RETURNING id, creator_id, listing_id, commission_pct, commission_flat,
		          link_code, click_count, conversion_count, total_earned, is_active, created_at
	`, l.CreatorID, l.ListingID, l.CommissionPct, l.CommissionFlat, l.LinkCode).Scan(
		&l.ID, &l.CreatorID, &l.ListingID, &l.CommissionPct, &l.CommissionFlat,
		&l.LinkCode, &l.ClickCount, &l.ConversionCount, &l.TotalEarned, &l.IsActive, &l.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return l, nil
}

// GetAffiliateLinkByCode returns the affiliate link with the given link_code.
func (s *Store) GetAffiliateLinkByCode(ctx context.Context, code string) (*AffiliateLink, error) {
	var l AffiliateLink
	err := s.db.QueryRow(ctx, `
		SELECT id, creator_id, listing_id, commission_pct, commission_flat,
		       link_code, click_count, conversion_count, total_earned, is_active, created_at
		FROM affiliate_links
		WHERE link_code = $1
	`, code).Scan(
		&l.ID, &l.CreatorID, &l.ListingID, &l.CommissionPct, &l.CommissionFlat,
		&l.LinkCode, &l.ClickCount, &l.ConversionCount, &l.TotalEarned, &l.IsActive, &l.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

// GetAffiliateLinkByID returns the affiliate link with the given UUID.
// post-service's product-tag handler uses this to validate ownership
// before persisting the tag — the tag stores the link by UUID, not by
// link_code (UUIDs are stable; link_code is human-friendly and could
// be regenerated).
func (s *Store) GetAffiliateLinkByID(ctx context.Context, id uuid.UUID) (*AffiliateLink, error) {
	var l AffiliateLink
	err := s.db.QueryRow(ctx, `
		SELECT id, creator_id, listing_id, commission_pct, commission_flat,
		       link_code, click_count, conversion_count, total_earned, is_active, created_at
		FROM affiliate_links
		WHERE id = $1
	`, id).Scan(
		&l.ID, &l.CreatorID, &l.ListingID, &l.CommissionPct, &l.CommissionFlat,
		&l.LinkCode, &l.ClickCount, &l.ConversionCount, &l.TotalEarned, &l.IsActive, &l.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

// GetAffiliateLinksByCreator returns active affiliate links for a creator, ordered by created_at DESC.
func (s *Store) GetAffiliateLinksByCreator(ctx context.Context, creatorID uuid.UUID, limit, offset int) ([]AffiliateLink, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, creator_id, listing_id, commission_pct, commission_flat,
		       link_code, click_count, conversion_count, total_earned, is_active, created_at
		FROM affiliate_links
		WHERE creator_id = $1 AND is_active = TRUE
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, creatorID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []AffiliateLink
	for rows.Next() {
		var l AffiliateLink
		if err := rows.Scan(
			&l.ID, &l.CreatorID, &l.ListingID, &l.CommissionPct, &l.CommissionFlat,
			&l.LinkCode, &l.ClickCount, &l.ConversionCount, &l.TotalEarned, &l.IsActive, &l.CreatedAt,
		); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

// IncrementAffiliateLinkClick increments the click_count for the given link_code by 1.
func (s *Store) IncrementAffiliateLinkClick(ctx context.Context, linkCode string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE affiliate_links SET click_count = click_count + 1 WHERE link_code = $1
	`, linkCode)
	return err
}

// RecordAffiliateConversion inserts a conversion record and atomically updates
// conversion_count and total_earned on the affiliate link.
func (s *Store) RecordAffiliateConversion(ctx context.Context, c *AffiliateConversion) (*AffiliateConversion, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
		INSERT INTO affiliate_conversions (affiliate_id, order_id, buyer_id, commission_amt, status, created_at)
		VALUES ($1, $2, $3, $4, 'pending', NOW())
		RETURNING id, affiliate_id, order_id, buyer_id, commission_amt, status, created_at
	`, c.AffiliateID, c.OrderID, c.BuyerID, c.CommissionAmt).Scan(
		&c.ID, &c.AffiliateID, &c.OrderID, &c.BuyerID, &c.CommissionAmt, &c.Status, &c.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx, `
		UPDATE affiliate_links
		SET conversion_count = conversion_count + 1,
		    total_earned = total_earned + $2
		WHERE id = $1
	`, c.AffiliateID, c.CommissionAmt)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// GetAffiliateConversions returns conversions for an affiliate link, ordered by created_at DESC.
func (s *Store) GetAffiliateConversions(ctx context.Context, affiliateID uuid.UUID, limit, offset int) ([]AffiliateConversion, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, affiliate_id, order_id, buyer_id, commission_amt, status, created_at
		FROM affiliate_conversions
		WHERE affiliate_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, affiliateID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var convs []AffiliateConversion
	for rows.Next() {
		var c AffiliateConversion
		if err := rows.Scan(
			&c.ID, &c.AffiliateID, &c.OrderID, &c.BuyerID, &c.CommissionAmt, &c.Status, &c.CreatedAt,
		); err != nil {
			return nil, err
		}
		convs = append(convs, c)
	}
	return convs, rows.Err()
}
