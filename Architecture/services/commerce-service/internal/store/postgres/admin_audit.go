package postgres

// Audited admin writes — migration 036.
//
// Each function here performs one human admin action AND appends its row to
// commerce_admin_audit_log in the same transaction. Either both happen or
// neither does: a settled remittance with no author, or an audit row for a
// banner that was never saved, are both wrong answers to "who did this".
//
// The actor is required. uuid.Nil is refused here as well as at the HTTP
// layer and by the table's CHECK, so no path can write a nil author.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrActorRequired is returned when an audited admin write is attempted
// without a real actor.
var ErrActorRequired = errors.New("a human actor is required for this admin action")

// Audit actions, mirroring migration 036's CHECK.
const (
	AuditActionCODRemittanceSettle = "cod_remittance_settle"
	AuditActionSellerKYCVerify     = "seller_kyc_verify"
	AuditActionBannerCreate        = "banner_create"
	AuditActionBannerUpdate        = "banner_update"
	AuditActionBannerDelete        = "banner_delete"
)

type adminAuditEntry struct {
	Actor      uuid.UUID
	Action     string
	TargetType string
	TargetID   uuid.UUID
	Before     any
	After      any
	Reason     *string
}

func jsonOrNil(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

func insertAdminAudit(ctx context.Context, tx pgx.Tx, e adminAuditEntry) error {
	if e.Actor == uuid.Nil {
		return ErrActorRequired
	}
	before, err := jsonOrNil(e.Before)
	if err != nil {
		return fmt.Errorf("audit before: %w", err)
	}
	after, err := jsonOrNil(e.After)
	if err != nil {
		return fmt.Errorf("audit after: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO commerce_admin_audit_log
		  (actor_user_id, action, target_type, target_id, before_state, after_state, reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		e.Actor, e.Action, e.TargetType, e.TargetID, before, after, e.Reason)
	if err != nil {
		return fmt.Errorf("write admin audit: %w", err)
	}
	return nil
}

// ─── COD remittance settle ──────────────────────────────────────────────

type codRemittanceAuditState struct {
	Status        string     `json:"status"`
	SellerID      uuid.UUID  `json:"seller_id"`
	NetAmount     string     `json:"net_amount"`
	CurrencyCode  string     `json:"currency_code"`
	PayoutBatchID *uuid.UUID `json:"payout_batch_id"`
}

// SettleCODRemittanceAudited flips a pending remittance to settled and
// records who did it. It reports whether a row transitioned; settling a
// remittance that is not pending (already settled, on hold, or absent)
// changes nothing and writes no audit row, exactly as the old unaudited
// MarkCODRemittanceSettled behaves.
func (s *Store) SettleCODRemittanceAudited(ctx context.Context, remittanceID, payoutBatchID, actor uuid.UUID) (bool, error) {
	if actor == uuid.Nil {
		return false, ErrActorRequired
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var before codRemittanceAuditState
	err = tx.QueryRow(ctx, `
		SELECT status, seller_id, net_amount::text, currency_code, payout_batch_id
		FROM cod_remittances WHERE id = $1 FOR UPDATE`, remittanceID,
	).Scan(&before.Status, &before.SellerID, &before.NetAmount, &before.CurrencyCode, &before.PayoutBatchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if before.Status != "pending" {
		return false, nil
	}

	var batchArg *uuid.UUID
	if payoutBatchID != uuid.Nil {
		batchArg = &payoutBatchID
	}
	if _, err := tx.Exec(ctx, `
		UPDATE cod_remittances
		SET status = 'settled', settled_at = NOW(), payout_batch_id = $2
		WHERE id = $1 AND status = 'pending'`, remittanceID, batchArg); err != nil {
		return false, err
	}
	after := before
	after.Status = "settled"
	after.PayoutBatchID = batchArg
	if err := insertAdminAudit(ctx, tx, adminAuditEntry{
		Actor: actor, Action: AuditActionCODRemittanceSettle,
		TargetType: "cod_remittance", TargetID: remittanceID,
		Before: before, After: after,
	}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// ─── Seller KYC verify ──────────────────────────────────────────────────

// KYCAuditCheck is one field's verdict as the audit stores it: which field,
// what the adapter said, and which adapter said it. Deliberately no value and
// no free-text message — the message is adapter-authored and could echo the
// document number.
type KYCAuditCheck struct {
	Field  string `json:"field"`
	Status string `json:"status"`
	Source string `json:"source"`
}

// KYCAuditVerdict is the non-identifying summary of one verification.
type KYCAuditVerdict struct {
	Adapter  string          `json:"adapter"`
	AllValid bool            `json:"all_valid"`
	Checks   []KYCAuditCheck `json:"checks"`
}

// RecordSellerKYCVerificationAudited writes the verdict's status onto the
// seller row and appends the audit row, in one transaction.
func (s *Store) RecordSellerKYCVerificationAudited(ctx context.Context, sellerID, actor uuid.UUID, status string, verdict KYCAuditVerdict) error {
	if actor == uuid.Nil {
		return ErrActorRequired
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var prev *string
	if err := tx.QueryRow(ctx,
		`SELECT verification_status FROM sellers WHERE id=$1 FOR UPDATE`, sellerID,
	).Scan(&prev); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE sellers SET verification_status=$2, updated_at=NOW() WHERE id=$1`,
		sellerID, status); err != nil {
		return err
	}
	type kycAfter struct {
		VerificationStatus string `json:"verification_status"`
		KYCAuditVerdict
	}
	if err := insertAdminAudit(ctx, tx, adminAuditEntry{
		Actor: actor, Action: AuditActionSellerKYCVerify,
		TargetType: "seller", TargetID: sellerID,
		Before: map[string]any{"verification_status": prev},
		After:  kycAfter{VerificationStatus: status, KYCAuditVerdict: verdict},
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ─── Banners ────────────────────────────────────────────────────────────

func bannerForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Banner, error) {
	var b Banner
	err := tx.QueryRow(ctx, `
		SELECT id, title, subtitle, image_media_id, target_type, target_id,
		       position, active, starts_at, ends_at, created_at, updated_at
		FROM commerce_banners WHERE id=$1 FOR UPDATE`, id,
	).Scan(&b.ID, &b.Title, &b.Subtitle, &b.ImageMediaID, &b.TargetType,
		&b.TargetID, &b.Position, &b.Active, &b.StartsAt, &b.EndsAt,
		&b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// UpsertBannerAudited is UpsertBanner plus its audit row: banner_create when
// no row with that id existed, banner_update (with the previous row) when one
// did.
func (s *Store) UpsertBannerAudited(ctx context.Context, b *Banner, actor uuid.UUID) error {
	if actor == uuid.Nil {
		return ErrActorRequired
	}
	if b.ID == uuid.Nil {
		b.ID = uuid.New()
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	before, err := bannerForUpdate(ctx, tx, b.ID)
	if err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO commerce_banners
		  (id, title, subtitle, image_media_id, target_type, target_id, position, active, starts_at, ends_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (id) DO UPDATE SET
		  title=EXCLUDED.title, subtitle=EXCLUDED.subtitle,
		  image_media_id=EXCLUDED.image_media_id,
		  target_type=EXCLUDED.target_type, target_id=EXCLUDED.target_id,
		  position=EXCLUDED.position, active=EXCLUDED.active,
		  starts_at=EXCLUDED.starts_at, ends_at=EXCLUDED.ends_at,
		  updated_at=NOW()
		RETURNING created_at, updated_at`,
		b.ID, b.Title, b.Subtitle, b.ImageMediaID, b.TargetType, b.TargetID,
		b.Position, b.Active, b.StartsAt, b.EndsAt,
	).Scan(&b.CreatedAt, &b.UpdatedAt); err != nil {
		return err
	}
	action := AuditActionBannerCreate
	var beforeState any
	if before != nil {
		action = AuditActionBannerUpdate
		beforeState = before
	}
	after := *b
	after.ImageURL = ""
	if err := insertAdminAudit(ctx, tx, adminAuditEntry{
		Actor: actor, Action: action, TargetType: "banner", TargetID: b.ID,
		Before: beforeState, After: after,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteBannerAudited is DeleteBanner plus its audit row, which keeps the
// deleted banner as before_state. Reports whether the row existed; a missing
// banner writes no audit row.
func (s *Store) DeleteBannerAudited(ctx context.Context, id, actor uuid.UUID) (bool, error) {
	if actor == uuid.Nil {
		return false, ErrActorRequired
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	before, err := bannerForUpdate(ctx, tx, id)
	if err != nil {
		return false, err
	}
	if before == nil {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM commerce_banners WHERE id=$1`, id); err != nil {
		return false, err
	}
	if err := insertAdminAudit(ctx, tx, adminAuditEntry{
		Actor: actor, Action: AuditActionBannerDelete, TargetType: "banner", TargetID: id,
		Before: before,
	}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
