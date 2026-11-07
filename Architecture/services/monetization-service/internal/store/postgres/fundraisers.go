package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/monetization-service/internal/donation"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// Models
// ---------------------------------------------------------------------------

// Fundraiser represents a fundraising campaign created by a user. Money is
// integer paise (migration 026); the NUMERIC goal_amount / raised_amount
// columns are kept in step for readers that predate it, never read here.
type Fundraiser struct {
	ID           uuid.UUID  `json:"id"`
	CreatorID    uuid.UUID  `json:"creator_id"`
	Type         string     `json:"type"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	CoverMediaID *uuid.UUID `json:"cover_media_id,omitempty"`
	GoalPaise    int64      `json:"goal_paise"`
	RaisedPaise  int64      `json:"raised_paise"`
	DonorCount   int        `json:"donor_count"`
	Currency     string     `json:"currency"`
	Status       string     `json:"status"`
	NgoID        *uuid.UUID `json:"ngo_id,omitempty"`
	GstExempt    bool       `json:"gst_exempt"`
	EndsAt       *time.Time `json:"ends_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// Donation represents a single donation made to a fundraiser. It counts
// towards the fundraiser only once payments-service's signed event says it
// was paid (internal/donation). DonorID is the donor's id as stored; what a
// viewer may see of it is decided by the service (anonymous donors are
// redacted for everyone but themselves).
type Donation struct {
	ID            uuid.UUID `json:"id"`
	FundraiserID  uuid.UUID `json:"fundraiser_id"`
	DonorID       uuid.UUID `json:"donor_id"`
	AmountPaise   int64     `json:"amount_paise"`
	RefundedPaise int64     `json:"refunded_paise"`
	Currency      string    `json:"currency"`
	Status        string    `json:"status"`
	IsAnonymous   bool      `json:"is_anonymous"`
	Message       *string   `json:"message,omitempty"`
	ReceiptURL    *string   `json:"receipt_url,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// ---------------------------------------------------------------------------
// Fundraisers
// ---------------------------------------------------------------------------

const fundraiserColumns = `id, creator_id, type, title, description, cover_media_id,
	COALESCE(goal_paise, ROUND(goal_amount * 100)::BIGINT), raised_paise, donor_count, currency, status,
	ngo_id, gst_exempt, ends_at, created_at`

func scanFundraiser(row pgx.Row, f *Fundraiser) error {
	return row.Scan(
		&f.ID, &f.CreatorID, &f.Type, &f.Title, &f.Description, &f.CoverMediaID,
		&f.GoalPaise, &f.RaisedPaise, &f.DonorCount, &f.Currency, &f.Status,
		&f.NgoID, &f.GstExempt, &f.EndsAt, &f.CreatedAt,
	)
}

// CreateFundraiser inserts a new fundraiser and returns the created record.
// A cover is refused (ErrMediaClaimRequired, media_holders.go).
func (s *Store) CreateFundraiser(ctx context.Context, f *Fundraiser) (*Fundraiser, error) {
	if f.CoverMediaID != nil {
		return nil, fmt.Errorf("%w: fundraisers.cover_media_id", ErrMediaClaimRequired)
	}
	err := scanFundraiser(s.db.QueryRow(ctx, `
		INSERT INTO fundraisers
			(creator_id, type, title, description, cover_media_id, goal_paise, goal_amount, currency,
			 ngo_id, gst_exempt, ends_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, ($6::BIGINT)::NUMERIC / 100, $7, $8, $9, $10, NOW())
		RETURNING `+fundraiserColumns,
		f.CreatorID, f.Type, f.Title, f.Description, f.CoverMediaID, f.GoalPaise,
		f.Currency, f.NgoID, f.GstExempt, f.EndsAt), f)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// GetFundraiser returns a single fundraiser by ID.
func (s *Store) GetFundraiser(ctx context.Context, id uuid.UUID) (*Fundraiser, error) {
	var f Fundraiser
	err := scanFundraiser(s.db.QueryRow(ctx, `SELECT `+fundraiserColumns+` FROM fundraisers WHERE id = $1`, id), &f)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}

func (s *Store) listFundraisers(ctx context.Context, sql string, args ...any) ([]Fundraiser, error) {
	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fundraisers []Fundraiser
	for rows.Next() {
		var f Fundraiser
		if err := scanFundraiser(rows, &f); err != nil {
			return nil, err
		}
		fundraisers = append(fundraisers, f)
	}
	return fundraisers, rows.Err()
}

// ListActiveFundraisers returns active fundraisers ordered by created_at DESC.
func (s *Store) ListActiveFundraisers(ctx context.Context, limit, offset int) ([]Fundraiser, error) {
	return s.listFundraisers(ctx, `
		SELECT `+fundraiserColumns+`
		FROM fundraisers
		WHERE status = 'active'
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2`, limit, offset)
}

// ListFundraisersByCreator returns all fundraisers for a specific creator, ordered by created_at DESC.
func (s *Store) ListFundraisersByCreator(ctx context.Context, creatorID uuid.UUID, limit, offset int) ([]Fundraiser, error) {
	return s.listFundraisers(ctx, `
		SELECT `+fundraiserColumns+`
		FROM fundraisers
		WHERE creator_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, creatorID, limit, offset)
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

// ---------------------------------------------------------------------------
// Donations
// ---------------------------------------------------------------------------

// CreatePendingDonation records a donation the donor has asked to make. It
// counts for NOTHING: the fundraiser's total, donor count and status move
// only when ApplyDonationPaymentEvent applies payments-service's signed
// payment.succeeded event for it.
func (s *Store) CreatePendingDonation(ctx context.Context, d *Donation) (*Donation, error) {
	if d.AmountPaise <= 0 {
		return nil, errors.New("INVALID_AMOUNT: donation must be greater than zero")
	}
	err := s.db.QueryRow(ctx, `
		INSERT INTO donations
			(fundraiser_id, donor_id, amount, amount_paise, currency, status, is_anonymous, message, created_at)
		VALUES ($1, $2, ($3::BIGINT)::NUMERIC / 100, $3, $4, 'pending', $5, $6, NOW())
		RETURNING id, status, created_at
	`, d.FundraiserID, d.DonorID, d.AmountPaise, d.Currency, d.IsAnonymous, d.Message).Scan(
		&d.ID, &d.Status, &d.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// GetDonationsByFundraiser returns the donations that count — paid ones,
// and pre-026 rows labelled legacy_unverified — newest first. Pending,
// failed and fully refunded donations are not listed.
func (s *Store) GetDonationsByFundraiser(ctx context.Context, fundraiserID uuid.UUID, limit, offset int) ([]Donation, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, fundraiser_id, donor_id, COALESCE(amount_paise, ROUND(amount * 100)::BIGINT), refunded_paise,
		       currency, status, is_anonymous, message, receipt_url, created_at
		FROM donations
		WHERE fundraiser_id = $1
		  AND status = ANY($4)
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, fundraiserID, limit, offset, []string{donation.StatusLegacy, donation.StatusSucceeded, donation.StatusPartiallyRefunded})
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var donations []Donation
	for rows.Next() {
		var d Donation
		if err := rows.Scan(
			&d.ID, &d.FundraiserID, &d.DonorID, &d.AmountPaise, &d.RefundedPaise,
			&d.Currency, &d.Status, &d.IsAnonymous, &d.Message, &d.ReceiptURL, &d.CreatedAt,
		); err != nil {
			return nil, err
		}
		donations = append(donations, d)
	}
	return donations, rows.Err()
}

// donationInbox is donation_payment_inbox, the dedupe half of ApplyOnce.
type donationInbox struct{}

func (donationInbox) Claim(ctx context.Context, tx pgx.Tx, c paymentevents.Claim) (bool, error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO donation_payment_inbox (event_id, event_type, intent_id, donation_id, amount_minor, currency)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, NULLIF($6, ''))
		ON CONFLICT (event_id) DO NOTHING`,
		c.EventID, c.EventType, c.IntentID, c.ReferenceID, c.AmountMinor, c.Currency)
	if err != nil {
		return false, fmt.Errorf("record donation payment event: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ApplyDonationPaymentEvent applies one payments event exactly once: the
// inbox row, the decision and every write it implies commit together or not
// at all. A duplicate event id is OutcomeDuplicate with nothing written; an
// unknown donation is recorded as OutcomeDonationNotFound and committed.
func (s *Store) ApplyDonationPaymentEvent(ctx context.Context, ev donation.Event) (donation.Applied, error) {
	var applied donation.Applied
	if strings.TrimSpace(ev.EventID) == "" {
		return applied, paymentevents.ErrNoEventID
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return applied, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	claim := paymentevents.Claim{EventID: ev.EventID, EventType: ev.EventType, IntentID: ev.IntentID,
		ReferenceID: ev.DonationID, AmountMinor: ev.AmountMinor, Currency: ev.Currency}
	err = paymentevents.ApplyOnce(ctx, tx, donationInbox{}, claim, func(ctx context.Context, tx pgx.Tx) error {
		return applyDonationEffectTx(ctx, tx, ev, &applied)
	})
	switch {
	case errors.Is(err, paymentevents.ErrDuplicate):
		applied.Decision = donation.Decision{Outcome: donation.OutcomeDuplicate}
		return applied, nil
	case err != nil:
		return applied, err
	}
	return applied, tx.Commit(ctx)
}

func recordDonationInboxOutcomeTx(ctx context.Context, tx pgx.Tx, eventID string, d donation.Decision) error {
	_, err := tx.Exec(ctx, `UPDATE donation_payment_inbox SET outcome = $2, detail = NULLIF($3, '') WHERE event_id = $1`,
		eventID, string(d.Outcome), d.Detail)
	return err
}

func applyDonationEffectTx(ctx context.Context, tx pgx.Tx, ev donation.Event, applied *donation.Applied) error {
	var d donation.Donation
	err := tx.QueryRow(ctx, `
		SELECT id, fundraiser_id, donor_id, COALESCE(amount_paise, 0), refunded_paise, currency, status, COALESCE(intent_id, '')
		FROM donations WHERE id = $1 FOR UPDATE`, ev.DonationID).Scan(
		&d.ID, &d.FundraiserID, &d.DonorID, &d.AmountPaise, &d.RefundedPaise, &d.Currency, &d.Status, &d.IntentID)
	if errors.Is(err, pgx.ErrNoRows) {
		applied.Decision = donation.Decision{Outcome: donation.OutcomeDonationNotFound, Detail: "no such donation"}
		return recordDonationInboxOutcomeTx(ctx, tx, ev.EventID, applied.Decision)
	}
	if err != nil {
		return fmt.Errorf("lock donation %s: %w", ev.DonationID, err)
	}
	var f donation.Fundraiser
	if err := tx.QueryRow(ctx, `
		SELECT id, COALESCE(goal_paise, ROUND(goal_amount * 100)::BIGINT), raised_paise, status
		FROM fundraisers WHERE id = $1 FOR UPDATE`, d.FundraiserID).Scan(
		&f.ID, &f.GoalPaise, &f.RaisedPaise, &f.Status); err != nil {
		return fmt.Errorf("lock fundraiser %s: %w", d.FundraiserID, err)
	}

	dec := donation.Decide(d, f, ev)
	applied.Decision, applied.Donation = dec, d
	if dec.Donation != nil {
		n := dec.Donation
		if _, err := tx.Exec(ctx, `
			UPDATE donations SET status = $2, refunded_paise = $3, intent_id = NULLIF($4, ''),
			       counted_at = CASE WHEN $2 = 'succeeded' AND counted_at IS NULL THEN NOW() ELSE counted_at END
			WHERE id = $1`, n.ID, n.Status, n.RefundedPaise, n.IntentID); err != nil {
			return err
		}
		applied.Donation = *n
	}
	if dec.RaisedDeltaPaise != 0 || dec.DonorDelta != 0 || dec.Complete {
		if _, err := tx.Exec(ctx, `
			UPDATE fundraisers
			SET raised_paise  = raised_paise + $2,
			    raised_amount = ((raised_paise + $2)::NUMERIC) / 100,
			    donor_count   = GREATEST(donor_count + $3, 0),
			    status        = CASE WHEN $4 THEN 'completed' ELSE status END
			WHERE id = $1`, f.ID, dec.RaisedDeltaPaise, dec.DonorDelta, dec.Complete); err != nil {
			return err
		}
	}
	return recordDonationInboxOutcomeTx(ctx, tx, ev.EventID, dec)
}
