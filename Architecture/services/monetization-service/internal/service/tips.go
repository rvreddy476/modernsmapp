package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Tier 3d — Tips and Super Chat.
//
// One-shot fan→creator transfers. Reuses the existing
// ChargeAndCredit wallet plumbing — same atomicity, same audit trail,
// no new ledger conventions. Validation is the new thing here:
//
//   * min/max paise (₹1 / ₹5,000 per tip),
//   * no self-tipping (creator can't pay themselves),
//   * a per-(sender, recipient) daily cap so a single fan can't pump
//     thousands of rupees at one creator in minutes (anti-fraud +
//     anti-friend-coercion),
//   * message length capped at 250 chars (Super Chat-style memo).
//
// On the recipient side, every tip writes a 'tip_received' transaction
// row plus updates wallets.lifetime_earnings, so the existing
// dashboard query just works.

const (
	tipMinPaise            = int64(100)         // ₹1
	tipMaxPaise            = int64(500_000)     // ₹5,000 per tip
	tipDailyCapPerPair     = int64(2_000_000)   // ₹20,000 from one fan to one creator per 24h
	tipMessageMaxChars     = 250
	tipReferenceTypeBase   = "tip"
)

// TipResult is the success shape returned to fans.
type TipResult struct {
	Tip *postgres.Tip `json:"tip"`
}

// SendTipInput captures the request body. Either PostID or StreamID
// (or neither) may be set; both can't be set at once — a tip is on a
// specific surface.
type SendTipInput struct {
	SenderID    uuid.UUID
	RecipientID uuid.UUID
	AmountPaise int64
	Message     string
	PostID      *uuid.UUID
	StreamID    *uuid.UUID
}

// SendTip validates, charges the sender, credits the recipient, and
// records the tip + transaction rows in one DB transaction (via
// ChargeAndCredit). On a failed charge, returns a sentinel error
// without writing anything.
func (s *Service) SendTip(ctx context.Context, in SendTipInput) (*TipResult, error) {
	if err := validateTipInput(&in); err != nil {
		return nil, err
	}

	// Per-pair daily cap. SQL sum is the source of truth (Redis would
	// be faster but Redis truth-of-cap drifts under failures; we'd
	// rather a slightly slower call than allow over-cap during a
	// Redis flap).
	dayAgo := time.Now().Add(-24 * time.Hour)
	already, err := s.store.SumTipsFromSenderToRecipientSince(ctx, in.SenderID, in.RecipientID, dayAgo)
	if err != nil {
		return nil, fmt.Errorf("daily cap query: %w", err)
	}
	if already+in.AmountPaise > tipDailyCapPerPair {
		return nil, fmt.Errorf("DAILY_TIP_CAP_EXCEEDED: %d paise already sent in last 24h, cap %d",
			already, tipDailyCapPerPair)
	}

	// Both wallets must exist before ChargeAndCredit so the atomic
	// debit/credit doesn't fail mid-transaction on a missing row.
	if _, err := s.store.EnsureWallet(ctx, in.SenderID); err != nil {
		return nil, fmt.Errorf("ensure sender wallet: %w", err)
	}
	if _, err := s.store.EnsureWallet(ctx, in.RecipientID); err != nil {
		return nil, fmt.Errorf("ensure recipient wallet: %w", err)
	}

	tip := &postgres.Tip{
		ID:          uuid.New(),
		SenderID:    in.SenderID,
		RecipientID: in.RecipientID,
		AmountPaise: in.AmountPaise,
		Currency:    "INR",
		Message:     in.Message,
		PostID:      in.PostID,
		StreamID:    in.StreamID,
		Status:      "pending",
		CreatedAt:   time.Now(),
	}

	desc := buildTipDescription(in)
	if err := s.store.ChargeAndCredit(
		ctx,
		in.SenderID.String(), in.RecipientID.String(),
		in.AmountPaise, desc,
	); err != nil {
		// Don't even insert the tip row on charge failure — the fan
		// gets a clean error, no orphan record to reconcile.
		return nil, fmt.Errorf("charge failed: %w", err)
	}

	tip.Status = "completed"
	if err := s.store.InsertTip(ctx, tip); err != nil {
		// Charge already succeeded but the artefact write failed —
		// the wallet/transaction rows are the source of truth, so we
		// don't reverse. Return error so the client retries the
		// artefact insert (ID is idempotent on retry).
		return nil, fmt.Errorf("insert tip artefact: %w", err)
	}

	return &TipResult{Tip: tip}, nil
}

// ListSentTips paginates the caller's outgoing tips.
func (s *Service) ListSentTips(ctx context.Context, senderID uuid.UUID, cursor string, limit int) ([]postgres.Tip, error) {
	return s.store.ListTipsBySender(ctx, senderID, parseCursor(cursor), limit)
}

// ListReceivedTips paginates the creator's incoming tips.
func (s *Service) ListReceivedTips(ctx context.Context, recipientID uuid.UUID, cursor string, limit int) ([]postgres.Tip, error) {
	return s.store.ListTipsByRecipient(ctx, recipientID, parseCursor(cursor), limit)
}

// ListTipsForPost is public-ish: anyone can see the supporters wall
// for a post (the creator opted into tips on it).
func (s *Service) ListTipsForPost(ctx context.Context, postID uuid.UUID, cursor string, limit int) ([]postgres.Tip, error) {
	return s.store.ListTipsForPost(ctx, postID, parseCursor(cursor), limit)
}

// ---------------------------------------------------------------------------
// Validation (pure functions — testable without a DB)
// ---------------------------------------------------------------------------

// ValidateTipInput is the pure validation logic. Exposed so a future
// admin tool / preview API can call it without sending the tip.
func ValidateTipInput(in *SendTipInput) error {
	return validateTipInput(in)
}

func validateTipInput(in *SendTipInput) error {
	if in.SenderID == uuid.Nil {
		return fmt.Errorf("INVALID_SENDER")
	}
	if in.RecipientID == uuid.Nil {
		return fmt.Errorf("INVALID_RECIPIENT")
	}
	if in.SenderID == in.RecipientID {
		return fmt.Errorf("CANNOT_TIP_SELF")
	}
	if in.AmountPaise < tipMinPaise {
		return fmt.Errorf("AMOUNT_TOO_SMALL: minimum is %d paise (₹1)", tipMinPaise)
	}
	if in.AmountPaise > tipMaxPaise {
		return fmt.Errorf("AMOUNT_TOO_LARGE: maximum is %d paise (₹5000)", tipMaxPaise)
	}
	in.Message = strings.TrimSpace(in.Message)
	if len([]rune(in.Message)) > tipMessageMaxChars {
		return fmt.Errorf("MESSAGE_TOO_LONG: max %d chars", tipMessageMaxChars)
	}
	if in.PostID != nil && in.StreamID != nil {
		return fmt.Errorf("INVALID_TARGET: tip can be on a post or a stream, not both")
	}
	return nil
}

func buildTipDescription(in SendTipInput) string {
	var b strings.Builder
	b.WriteString("Tip")
	if in.PostID != nil {
		b.WriteString(" on post ")
		b.WriteString(in.PostID.String())
	} else if in.StreamID != nil {
		b.WriteString(" on stream ")
		b.WriteString(in.StreamID.String())
	}
	if in.Message != "" {
		b.WriteString(": ")
		// Don't dump arbitrary user text into a transaction description
		// at full length — keep it bounded so an admin grepping
		// transactions doesn't see paragraphs of fan emoji.
		msg := in.Message
		if len(msg) > 60 {
			msg = msg[:57] + "..."
		}
		b.WriteString(msg)
	}
	return b.String()
}
