package postgres

// Momentum Premium (migration 027).
//
// A premium_periods row is one pass. Creating it never grants anything: the
// service opens a payments intent for it, and the pass is applied ONLY by
// ApplyPremiumPaymentEvent, from the signed payments event, in one
// transaction with its inbox row (paymentevents.ApplyOnce). The rules are
// internal/premium's Decide; this file serialises per buyer, locks the rows,
// asks it, and writes what it says.
//
// Concurrency: every write that touches a buyer's entitlement takes a
// transaction-scoped advisory lock on the buyer first, so two passes paid at
// the same moment stack one after the other instead of both starting at the
// same end.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/monetization-service/internal/premium"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrPremiumIntentMismatch: the pass is already bound to another intent.
	ErrPremiumIntentMismatch = errors.New("PREMIUM_INTENT_MISMATCH")
)

const premiumPeriodColumns = `id, user_id, plan_code, duration_days, amount_paise, currency, status,
	COALESCE(intent_id::text, ''), period_start, period_end, refunded_paise, created_at,
	taxable_paise, cgst_paise, sgst_paise, igst_paise, total_paise, gst_rate_bp, sac, tax_provisional`

func scanPremiumPeriod(row pgx.Row) (*premium.Period, error) {
	var p premium.Period
	var taxable, cgst, sgst, igst, total *int64
	var rate *int
	var sac *string
	var provisional *bool
	if err := row.Scan(&p.ID, &p.UserID, &p.PlanCode, &p.DurationDays, &p.AmountPaise, &p.Currency, &p.Status,
		&p.IntentID, &p.PeriodStart, &p.PeriodEnd, &p.RefundedPaise, &p.CreatedAt,
		&taxable, &cgst, &sgst, &igst, &total, &rate, &sac, &provisional); err != nil {
		return nil, err
	}
	// Migration 028's CHECK keeps the split all-NULL (a pass from before
	// prices excluded GST) or complete.
	if taxable != nil && cgst != nil && sgst != nil && igst != nil && total != nil && rate != nil && sac != nil && provisional != nil {
		p.Tax = &premium.Breakdown{TaxablePaise: *taxable, CGSTPaise: *cgst, SGSTPaise: *sgst, IGSTPaise: *igst,
			TaxPaise: *cgst + *sgst + *igst, TotalPaise: *total, RateBP: *rate, SAC: *sac, Provisional: *provisional}
	}
	return &p, nil
}

const premiumEntitlementColumns = `user_id, ends_at, access_until, role_synced_until, role_attempts, ended_notified_until`

func scanPremiumEntitlement(row pgx.Row) (*premium.Entitlement, error) {
	var e premium.Entitlement
	if err := row.Scan(&e.UserID, &e.EndsAt, &e.AccessUntil, &e.RoleSyncedUntil, &e.RoleAttempts, &e.EndedNotifiedUntil); err != nil {
		return nil, err
	}
	return &e, nil
}

// lockPremiumBuyerTx serialises every entitlement write for one buyer.
func lockPremiumBuyerTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, "premium:"+userID.String())
	return err
}

// ListPremiumPlans answers every catalogue row.
func (s *Store) ListPremiumPlans(ctx context.Context) ([]premium.PlanRow, error) {
	rows, err := s.db.Query(ctx, `SELECT code, name, duration_days, active, sort_order FROM premium_plans ORDER BY sort_order, code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []premium.PlanRow
	for rows.Next() {
		var r premium.PlanRow
		if err := rows.Scan(&r.Code, &r.Name, &r.DurationDays, &r.Active, &r.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// OpenPremiumCheckout returns the buyer's open checkout for the plan when it
// is younger than premium.CheckoutTTL and still at this price and tax split
// (a retry or a double tap: one intent), and otherwise abandons it and opens
// a new one. The row's amount is the GST-inclusive total.
func (s *Store) OpenPremiumCheckout(ctx context.Context, userID uuid.UUID, plan premium.PlanRow, tax premium.Breakdown, now time.Time) (*premium.Period, error) {
	if !tax.Valid() {
		return nil, fmt.Errorf("open premium checkout: %w", premium.ErrTaxUnavailable)
	}
	now = premium.Truncate(now)
	for attempt := 0; attempt < 2; attempt++ {
		p, err := s.openPremiumCheckoutOnce(ctx, userID, plan, tax, now)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			continue // a concurrent checkout opened one first: read it next time round
		}
		return p, err
	}
	return nil, fmt.Errorf("open premium checkout: concurrent checkouts did not settle")
}

func (s *Store) openPremiumCheckoutOnce(ctx context.Context, userID uuid.UUID, plan premium.PlanRow, tax premium.Breakdown, now time.Time) (*premium.Period, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	open, err := scanPremiumPeriod(tx.QueryRow(ctx, `SELECT `+premiumPeriodColumns+` FROM premium_periods
		WHERE user_id = $1 AND plan_code = $2 AND status = 'created' FOR UPDATE`, userID, plan.Code))
	switch {
	case err == nil:
		if open.AmountPaise == tax.TotalPaise && open.Tax != nil && *open.Tax == tax && open.CreatedAt.After(now.Add(-premium.CheckoutTTL)) {
			return open, tx.Commit(ctx)
		}
		if _, err := tx.Exec(ctx, `UPDATE premium_periods SET status = 'abandoned', updated_at = NOW() WHERE id = $1`, open.ID); err != nil {
			return nil, err
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	id := uuid.New()
	p, err := scanPremiumPeriod(tx.QueryRow(ctx, `
		INSERT INTO premium_periods (id, user_id, plan_code, duration_days, amount_paise, currency, idempotency_key, created_at, updated_at,
			taxable_paise, cgst_paise, sgst_paise, igst_paise, total_paise, gst_rate_bp, sac, tax_provisional)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, $9, $10, $11, $12, $5, $13, $14, $15)
		RETURNING `+premiumPeriodColumns,
		id, userID, plan.Code, plan.DurationDays, tax.TotalPaise, premium.Currency, premium.IntentKey(id), now,
		tax.TaxablePaise, tax.CGSTPaise, tax.SGSTPaise, tax.IGSTPaise, tax.RateBP, tax.SAC, tax.Provisional))
	if err != nil {
		return nil, err
	}
	return p, tx.Commit(ctx)
}

// SetPremiumPeriodIntent binds the payments intent (and the provider order)
// to a pass. Binding the same intent again is a no-op; another one is refused.
func (s *Store) SetPremiumPeriodIntent(ctx context.Context, periodID, intentID uuid.UUID, orderID string) error {
	tag, err := s.db.Exec(ctx, `UPDATE premium_periods
		SET intent_id = $2, provider_order_id = COALESCE(NULLIF($3, ''), provider_order_id), updated_at = NOW()
		WHERE id = $1 AND (intent_id IS NULL OR intent_id = $2)`, periodID, intentID, orderID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPremiumIntentMismatch
	}
	return nil
}

// GetPremiumPeriod answers one pass, or nil.
func (s *Store) GetPremiumPeriod(ctx context.Context, periodID uuid.UUID) (*premium.Period, error) {
	p, err := scanPremiumPeriod(s.db.QueryRow(ctx, `SELECT `+premiumPeriodColumns+` FROM premium_periods WHERE id = $1`, periodID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// GetPremiumEntitlement answers the buyer's entitlement, or nil.
func (s *Store) GetPremiumEntitlement(ctx context.Context, userID uuid.UUID) (*premium.Entitlement, error) {
	e, err := scanPremiumEntitlement(s.db.QueryRow(ctx, `SELECT `+premiumEntitlementColumns+` FROM premium_entitlements WHERE user_id = $1`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return e, err
}

type premiumInbox struct{}

func (premiumInbox) Claim(ctx context.Context, tx pgx.Tx, c paymentevents.Claim) (bool, error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO premium_payment_inbox (event_id, event_type, intent_id, period_id, amount_minor, currency)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, NULLIF($6, ''))
		ON CONFLICT (event_id) DO NOTHING`,
		c.EventID, c.EventType, c.IntentID, c.ReferenceID, c.AmountMinor, c.Currency)
	if err != nil {
		return false, fmt.Errorf("record premium payment event: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ApplyPremiumPaymentEvent applies one payments event exactly once: the inbox
// row, the decision and every write it implies commit together or not at
// all. A duplicate event id is OutcomeDuplicate with nothing written; an
// unknown pass is recorded as OutcomePeriodNotFound and committed.
func (s *Store) ApplyPremiumPaymentEvent(ctx context.Context, ev premium.Event, now time.Time, sup premium.Supplier) (premium.Applied, error) {
	var applied premium.Applied
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return applied, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	claim := paymentevents.Claim{EventID: ev.EventID, EventType: ev.EventType, IntentID: ev.IntentID,
		ReferenceID: ev.PeriodID, AmountMinor: ev.AmountMinor, Currency: ev.Currency}
	err = paymentevents.ApplyOnce(ctx, tx, premiumInbox{}, claim, func(ctx context.Context, tx pgx.Tx) error {
		return applyPremiumEffectTx(ctx, tx, ev, now, sup, &applied)
	})
	switch {
	case errors.Is(err, paymentevents.ErrDuplicate):
		applied.Decision = premium.Decision{Outcome: premium.OutcomeDuplicate}
		return applied, nil
	case err != nil:
		return applied, err
	}
	return applied, tx.Commit(ctx)
}

func recordPremiumInboxOutcomeTx(ctx context.Context, tx pgx.Tx, eventID string, d premium.Decision) error {
	_, err := tx.Exec(ctx, `UPDATE premium_payment_inbox SET outcome = $2, detail = NULLIF($3, '') WHERE event_id = $1`,
		eventID, string(d.Outcome), d.Detail)
	return err
}

func applyPremiumEffectTx(ctx context.Context, tx pgx.Tx, ev premium.Event, now time.Time, sup premium.Supplier, applied *premium.Applied) error {
	now = premium.Truncate(now)
	var userID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT user_id FROM premium_periods WHERE id = $1`, ev.PeriodID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		applied.Decision = premium.Decision{Outcome: premium.OutcomePeriodNotFound, Detail: "no such premium pass"}
		return recordPremiumInboxOutcomeTx(ctx, tx, ev.EventID, applied.Decision)
	}
	if err != nil {
		return err
	}
	if err := lockPremiumBuyerTx(ctx, tx, userID); err != nil {
		return err
	}
	ent, err := scanPremiumEntitlement(tx.QueryRow(ctx, `SELECT `+premiumEntitlementColumns+` FROM premium_entitlements WHERE user_id = $1 FOR UPDATE`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		ent, err = nil, nil
	}
	if err != nil {
		return err
	}
	period, err := scanPremiumPeriod(tx.QueryRow(ctx, `SELECT `+premiumPeriodColumns+` FROM premium_periods WHERE id = $1 FOR UPDATE`, ev.PeriodID))
	if err != nil {
		return fmt.Errorf("lock premium pass %s: %w", ev.PeriodID, err)
	}
	rows, err := tx.Query(ctx, `SELECT `+premiumPeriodColumns+` FROM premium_periods
		WHERE user_id = $1 AND status = 'paid' AND id <> $2 ORDER BY period_start FOR UPDATE`, userID, ev.PeriodID)
	if err != nil {
		return err
	}
	var chain []premium.Period
	for rows.Next() {
		c, err := scanPremiumPeriod(rows)
		if err != nil {
			rows.Close()
			return err
		}
		chain = append(chain, *c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	d := premium.Decide(premium.Input{Period: *period, Entitlement: ent, Chain: chain, Event: ev, Now: now})
	applied.Decision, applied.Period, applied.Entitlement = d, *period, ent
	if d.Period != nil {
		if err := writePremiumPeriodTx(ctx, tx, d.Period, now); err != nil {
			return err
		}
		applied.Period = *d.Period
	}
	for i := range d.Shifted {
		if _, err := tx.Exec(ctx, `UPDATE premium_periods SET period_start = $2, period_end = $3, updated_at = NOW() WHERE id = $1`,
			d.Shifted[i].ID, d.Shifted[i].PeriodStart, d.Shifted[i].PeriodEnd); err != nil {
			return err
		}
	}
	if d.Entitlement != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO premium_entitlements (user_id, ends_at, access_until, role_next_attempt_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (user_id) DO UPDATE
			SET ends_at = EXCLUDED.ends_at, access_until = EXCLUDED.access_until,
			    role_next_attempt_at = EXCLUDED.role_next_attempt_at, role_attempts = 0, role_last_error = NULL,
			    updated_at = NOW()`,
			userID, d.Entitlement.EndsAt, d.Entitlement.AccessUntil, now); err != nil {
			return err
		}
		applied.Entitlement = d.Entitlement
	}
	// The tax invoice (activation) or credit note (full refund), in this
	// transaction: the pass and its document commit together or not at all.
	if err := issuePremiumDocumentTx(ctx, tx, d, applied.Period, sup, now, applied); err != nil {
		return err
	}
	return recordPremiumInboxOutcomeTx(ctx, tx, ev.EventID, d)
}

func writePremiumPeriodTx(ctx context.Context, tx pgx.Tx, p *premium.Period, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE premium_periods
		SET status = $2, period_start = $3, period_end = $4, refunded_paise = $5,
		    paid_at     = CASE WHEN $2 = 'paid'     AND paid_at     IS NULL THEN $6 ELSE paid_at END,
		    failed_at   = CASE WHEN $2 = 'failed'   AND failed_at   IS NULL THEN $6 ELSE failed_at END,
		    refunded_at = CASE WHEN $2 = 'refunded' AND refunded_at IS NULL THEN $6 ELSE refunded_at END,
		    updated_at = NOW()
		WHERE id = $1`, p.ID, p.Status, p.PeriodStart, p.PeriodEnd, p.RefundedPaise, now)
	return err
}

// premiumRoleDue is the predicate for "identity does not yet agree".
const premiumRoleDue = `((access_until > $1 AND (role_synced_until IS NULL OR role_synced_until <> access_until))
	 OR (access_until <= $1 AND role_synced_until IS NOT NULL))`

// DuePremiumRoleSyncs answers the entitlements whose role must be granted,
// re-granted with a new expiry, or revoked, and whose next attempt is due.
func (s *Store) DuePremiumRoleSyncs(ctx context.Context, now time.Time, limit int) ([]premium.Entitlement, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.queryPremiumEntitlements(ctx, `SELECT `+premiumEntitlementColumns+` FROM premium_entitlements
		WHERE role_next_attempt_at <= $1 AND `+premiumRoleDue+`
		ORDER BY role_next_attempt_at LIMIT $2`, now, limit)
}

func (s *Store) queryPremiumEntitlements(ctx context.Context, sql string, args ...any) ([]premium.Entitlement, error) {
	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []premium.Entitlement
	for rows.Next() {
		e, err := scanPremiumEntitlement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// MarkPremiumRoleSynced records what identity accepted (synced nil = revoked).
// It writes only while access_until is still the value that was synced: an
// entitlement that changed meanwhile stays due and is synced again.
func (s *Store) MarkPremiumRoleSynced(ctx context.Context, userID uuid.UUID, accessUntil time.Time, synced *time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE premium_entitlements
		SET role_synced_until = $3, role_attempts = 0, role_last_error = NULL, updated_at = NOW()
		WHERE user_id = $1 AND access_until = $2`, userID, accessUntil, synced)
	return err
}

// MarkPremiumRoleSyncRetry schedules the next attempt and records why.
func (s *Store) MarkPremiumRoleSyncRetry(ctx context.Context, userID uuid.UUID, next time.Time, cause string) error {
	_, err := s.db.Exec(ctx, `UPDATE premium_entitlements
		SET role_attempts = role_attempts + 1, role_next_attempt_at = $2, role_last_error = $3, updated_at = NOW()
		WHERE user_id = $1`, userID, next, cause)
	return err
}

// DuePremiumReminders answers the renewal reminders due at now: the paid end
// within the last or next three days, a grace still ahead (a refund that
// ended access has none), and this (ends_at, days_left) not yet sent.
func (s *Store) DuePremiumReminders(ctx context.Context, now time.Time) ([]premium.ReminderDue, error) {
	rows, err := s.db.Query(ctx, `
		WITH c AS (
			SELECT `+premiumEntitlementColumns+`,
			       CASE WHEN ends_at <= $1::timestamptz + $2::interval THEN 0 ELSE 3 END AS days_left
			FROM premium_entitlements
			WHERE ends_at <= $1::timestamptz + $3::interval
			  AND ends_at >  $1::timestamptz - $3::interval
			  AND access_until > ends_at
			  AND access_until > $1
		)
		SELECT c.* FROM c
		WHERE NOT EXISTS (SELECT 1 FROM premium_renewal_reminders r
		                  WHERE r.user_id = c.user_id AND r.ends_at = c.ends_at AND r.days_left = c.days_left)
		ORDER BY c.ends_at LIMIT 500`,
		now, fmt.Sprintf("%d seconds", int64(premium.ReminderDay/time.Second)),
		fmt.Sprintf("%d seconds", int64(premium.ReminderLead/time.Second)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []premium.ReminderDue
	for rows.Next() {
		var r premium.ReminderDue
		e := &r.Entitlement
		if err := rows.Scan(&e.UserID, &e.EndsAt, &e.AccessUntil, &e.RoleSyncedUntil, &e.RoleAttempts, &e.EndedNotifiedUntil, &r.DaysLeft); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ClaimPremiumReminder records a reminder as sent; false if it already was.
func (s *Store) ClaimPremiumReminder(ctx context.Context, userID uuid.UUID, endsAt time.Time, daysLeft int) (bool, error) {
	tag, err := s.db.Exec(ctx, `INSERT INTO premium_renewal_reminders (user_id, ends_at, days_left)
		VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, userID, endsAt, daysLeft)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ReleasePremiumReminder drops a claim whose publish failed.
func (s *Store) ReleasePremiumReminder(ctx context.Context, userID uuid.UUID, endsAt time.Time, daysLeft int) error {
	_, err := s.db.Exec(ctx, `DELETE FROM premium_renewal_reminders WHERE user_id = $1 AND ends_at = $2 AND days_left = $3`,
		userID, endsAt, daysLeft)
	return err
}

// ClaimPremiumEndedNotices claims every access that ended (within the last
// week) and has not been announced: ended_notified_until := access_until.
func (s *Store) ClaimPremiumEndedNotices(ctx context.Context, now time.Time) ([]premium.Entitlement, error) {
	return s.queryPremiumEntitlements(ctx, `
		UPDATE premium_entitlements SET ended_notified_until = access_until, updated_at = NOW()
		WHERE access_until <= $1 AND access_until > $1::timestamptz - INTERVAL '7 days'
		  AND (ended_notified_until IS NULL OR ended_notified_until <> access_until)
		RETURNING `+premiumEntitlementColumns, now)
}

// ReleasePremiumEndedNotice un-claims a notice whose publish failed.
func (s *Store) ReleasePremiumEndedNotice(ctx context.Context, userID uuid.UUID, accessUntil time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE premium_entitlements SET ended_notified_until = NULL
		WHERE user_id = $1 AND ended_notified_until = $2`, userID, accessUntil)
	return err
}

// AbandonStalePremiumCheckouts moves checkouts unpaid since before cutoff to
// abandoned. A capture that still arrives for one activates it.
func (s *Store) AbandonStalePremiumCheckouts(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.db.Exec(ctx, `UPDATE premium_periods SET status = 'abandoned', updated_at = NOW()
		WHERE status = 'created' AND created_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
