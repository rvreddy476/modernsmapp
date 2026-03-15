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

// Fundraiser represents a fundraising campaign created by a user.
type Fundraiser struct {
	ID           uuid.UUID  `json:"id"`
	CreatorID    uuid.UUID  `json:"creator_id"`
	Type         string     `json:"type"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	CoverMediaID *uuid.UUID `json:"cover_media_id,omitempty"`
	GoalAmount   float64    `json:"goal_amount"`
	RaisedAmount float64    `json:"raised_amount"`
	DonorCount   int        `json:"donor_count"`
	Currency     string     `json:"currency"`
	Status       string     `json:"status"`
	NgoID        *uuid.UUID `json:"ngo_id,omitempty"`
	GstExempt    bool       `json:"gst_exempt"`
	EndsAt       *time.Time `json:"ends_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// Donation represents a single donation made to a fundraiser.
type Donation struct {
	ID              uuid.UUID `json:"id"`
	FundraiserID    uuid.UUID `json:"fundraiser_id"`
	DonorID         uuid.UUID `json:"donor_id"`
	Amount          float64   `json:"amount"`
	Currency        string    `json:"currency"`
	PaymentIntentID uuid.UUID `json:"payment_intent_id"`
	IsAnonymous     bool      `json:"is_anonymous"`
	Message         *string   `json:"message,omitempty"`
	ReceiptURL      *string   `json:"receipt_url,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// ---------------------------------------------------------------------------
// Fundraisers
// ---------------------------------------------------------------------------

// CreateFundraiser inserts a new fundraiser and returns the created record.
func (s *Store) CreateFundraiser(ctx context.Context, f *Fundraiser) (*Fundraiser, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO fundraisers
			(creator_id, type, title, description, cover_media_id, goal_amount, currency,
			 ngo_id, gst_exempt, ends_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
		RETURNING id, creator_id, type, title, description, cover_media_id,
		          goal_amount, raised_amount, donor_count, currency, status,
		          ngo_id, gst_exempt, ends_at, created_at
	`, f.CreatorID, f.Type, f.Title, f.Description, f.CoverMediaID, f.GoalAmount,
		f.Currency, f.NgoID, f.GstExempt, f.EndsAt).Scan(
		&f.ID, &f.CreatorID, &f.Type, &f.Title, &f.Description, &f.CoverMediaID,
		&f.GoalAmount, &f.RaisedAmount, &f.DonorCount, &f.Currency, &f.Status,
		&f.NgoID, &f.GstExempt, &f.EndsAt, &f.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// GetFundraiser returns a single fundraiser by ID.
func (s *Store) GetFundraiser(ctx context.Context, id uuid.UUID) (*Fundraiser, error) {
	var f Fundraiser
	err := s.db.QueryRow(ctx, `
		SELECT id, creator_id, type, title, description, cover_media_id,
		       goal_amount, raised_amount, donor_count, currency, status,
		       ngo_id, gst_exempt, ends_at, created_at
		FROM fundraisers
		WHERE id = $1
	`, id).Scan(
		&f.ID, &f.CreatorID, &f.Type, &f.Title, &f.Description, &f.CoverMediaID,
		&f.GoalAmount, &f.RaisedAmount, &f.DonorCount, &f.Currency, &f.Status,
		&f.NgoID, &f.GstExempt, &f.EndsAt, &f.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}

// ListActiveFundraisers returns active fundraisers ordered by created_at DESC.
func (s *Store) ListActiveFundraisers(ctx context.Context, limit, offset int) ([]Fundraiser, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, creator_id, type, title, description, cover_media_id,
		       goal_amount, raised_amount, donor_count, currency, status,
		       ngo_id, gst_exempt, ends_at, created_at
		FROM fundraisers
		WHERE status = 'active'
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fundraisers []Fundraiser
	for rows.Next() {
		var f Fundraiser
		if err := rows.Scan(
			&f.ID, &f.CreatorID, &f.Type, &f.Title, &f.Description, &f.CoverMediaID,
			&f.GoalAmount, &f.RaisedAmount, &f.DonorCount, &f.Currency, &f.Status,
			&f.NgoID, &f.GstExempt, &f.EndsAt, &f.CreatedAt,
		); err != nil {
			return nil, err
		}
		fundraisers = append(fundraisers, f)
	}
	return fundraisers, rows.Err()
}

// ListFundraisersByCreator returns all fundraisers for a specific creator, ordered by created_at DESC.
func (s *Store) ListFundraisersByCreator(ctx context.Context, creatorID uuid.UUID, limit, offset int) ([]Fundraiser, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, creator_id, type, title, description, cover_media_id,
		       goal_amount, raised_amount, donor_count, currency, status,
		       ngo_id, gst_exempt, ends_at, created_at
		FROM fundraisers
		WHERE creator_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, creatorID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fundraisers []Fundraiser
	for rows.Next() {
		var f Fundraiser
		if err := rows.Scan(
			&f.ID, &f.CreatorID, &f.Type, &f.Title, &f.Description, &f.CoverMediaID,
			&f.GoalAmount, &f.RaisedAmount, &f.DonorCount, &f.Currency, &f.Status,
			&f.NgoID, &f.GstExempt, &f.EndsAt, &f.CreatedAt,
		); err != nil {
			return nil, err
		}
		fundraisers = append(fundraisers, f)
	}
	return fundraisers, rows.Err()
}

// UpdateFundraiserStatus updates the status of a fundraiser.
func (s *Store) UpdateFundraiserStatus(ctx context.Context, id uuid.UUID, status string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE fundraisers SET status = $2 WHERE id = $1
	`, id, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("FUNDRAISER_NOT_FOUND")
	}
	return nil
}

// CreateDonation inserts a donation and atomically updates the fundraiser's raised_amount and
// donor_count. If raised_amount reaches or exceeds goal_amount, the fundraiser status is set
// to 'completed'.
func (s *Store) CreateDonation(ctx context.Context, d *Donation) (*Donation, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
		INSERT INTO donations
			(fundraiser_id, donor_id, amount, currency, payment_intent_id, is_anonymous, message, receipt_url, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		RETURNING id, fundraiser_id, donor_id, amount, currency, payment_intent_id,
		          is_anonymous, message, receipt_url, created_at
	`, d.FundraiserID, d.DonorID, d.Amount, d.Currency, d.PaymentIntentID,
		d.IsAnonymous, d.Message, d.ReceiptURL).Scan(
		&d.ID, &d.FundraiserID, &d.DonorID, &d.Amount, &d.Currency, &d.PaymentIntentID,
		&d.IsAnonymous, &d.Message, &d.ReceiptURL, &d.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	// Update fundraiser totals and optionally mark as completed.
	_, err = tx.Exec(ctx, `
		UPDATE fundraisers
		SET raised_amount = raised_amount + $2,
		    donor_count   = donor_count + 1,
		    status = CASE
		        WHEN raised_amount + $2 >= goal_amount THEN 'completed'
		        ELSE status
		    END
		WHERE id = $1
	`, d.FundraiserID, d.Amount)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return d, nil
}

// GetDonationsByFundraiser returns donations for a fundraiser ordered by created_at DESC.
func (s *Store) GetDonationsByFundraiser(ctx context.Context, fundraiserID uuid.UUID, limit, offset int) ([]Donation, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, fundraiser_id, donor_id, amount, currency, payment_intent_id,
		       is_anonymous, message, receipt_url, created_at
		FROM donations
		WHERE fundraiser_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, fundraiserID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var donations []Donation
	for rows.Next() {
		var d Donation
		if err := rows.Scan(
			&d.ID, &d.FundraiserID, &d.DonorID, &d.Amount, &d.Currency, &d.PaymentIntentID,
			&d.IsAnonymous, &d.Message, &d.ReceiptURL, &d.CreatedAt,
		); err != nil {
			return nil, err
		}
		donations = append(donations, d)
	}
	return donations, rows.Err()
}
