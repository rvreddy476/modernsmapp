package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/atpost/wallet-service/internal/store"
	"github.com/google/uuid"
)

// indianPhoneRE matches a 10-digit Indian mobile number, optionally with
// +91 / 0 prefix. Loose enough for v1 — the service normalises via
// normalisePhone before persistence.
var indianPhoneRE = regexp.MustCompile(`^(?:\+?91|0)?[6-9][0-9]{9}$`)

// SendRequest is the structured input for Send. Either RecipientUserID or
// RecipientPhone (or both) must be set; both nil = invalid.
type SendRequest struct {
	RecipientUserID *uuid.UUID
	RecipientPhone  string
	AmountPaise     int64
	Label           string
	IdempotencyKey  string
}

// SendResult is what handlers return after a Send call.
type SendResult struct {
	TransactionID uuid.UUID `json:"transaction_id"`
	Status        string    `json:"status"`
	AmountPaise   int64     `json:"amount_paise"`
}

const sendOperation = "send"

// Send executes a peer-to-peer wallet send. Implements the saga from spec §4:
//
//  1. Idempotency check.
//  2. Validate recipient + KYC tier limit + sender funds.
//  3. Begin pgx tx → debit sender mirror → insert sender's pending tx row → commit.
//  4. Call partner bank Transfer().
//  5. On success: mark sender's tx succeeded; if recipient is in-AtPost,
//     credit recipient mirror + insert recipient's receive row.
//  6. On bank failure: reverse sender debit + mark sender tx failed.
//  7. Emit wallet.send.* events.
//
// Returns the sender-side transaction id + final status.
func (s *Service) Send(ctx context.Context, senderID uuid.UUID, req SendRequest) (*SendResult, error) {
	if req.IdempotencyKey == "" {
		return nil, fmt.Errorf("invalid: idempotency_key required")
	}
	if req.AmountPaise <= 0 {
		return nil, fmt.Errorf("invalid: amount must be positive")
	}
	phone := normalisePhone(req.RecipientPhone)
	if req.RecipientUserID == nil && phone == "" {
		return nil, fmt.Errorf("invalid: recipient_user_id or recipient_phone required")
	}
	if phone != "" && !indianPhoneRE.MatchString(phone) {
		return nil, fmt.Errorf("invalid: recipient_phone must be a 10-digit Indian mobile")
	}
	if req.RecipientUserID != nil && *req.RecipientUserID == senderID {
		return nil, fmt.Errorf("invalid: cannot send to self")
	}

	// Idempotency replay.
	if existing, err := s.store.FindIdempotency(ctx, req.IdempotencyKey, senderID, sendOperation); err == nil {
		if existing.ResponseBody != nil {
			var cached SendResult
			if err := json.Unmarshal(existing.ResponseBody, &cached); err != nil {
				return nil, fmt.Errorf("decode cached idempotency body: %w", err)
			}
			return &cached, nil
		}
		if existing.TransactionID != nil {
			tx, err := s.store.GetTransaction(ctx, senderID, *existing.TransactionID)
			if err != nil {
				return nil, err
			}
			return &SendResult{TransactionID: tx.ID, Status: tx.Status, AmountPaise: tx.AmountPaise}, nil
		}
	} else if !errors.Is(err, store.ErrIdempotencyKeyNotFound) {
		return nil, err
	}

	// Validate KYC tier limit + freeze.
	if err := s.AssertWithinMonthlyLimit(ctx, senderID, req.AmountPaise); err != nil {
		return nil, err
	}

	senderBalance, err := s.GetBalance(ctx, senderID)
	if err != nil {
		return nil, err
	}
	if senderBalance.AvailablePaise < req.AmountPaise {
		return nil, fmt.Errorf("invalid: insufficient balance")
	}

	// Resolve recipient bank ref. In-AtPost users get a mirror row + a PPI
	// sub-account on the fly. External phone-only sends are stubbed for v1
	// (see "pending_invite" branch below).
	var recipientBalance *store.Balance
	if req.RecipientUserID != nil {
		recipientBalance, err = s.GetBalance(ctx, *req.RecipientUserID)
		if err != nil {
			return nil, fmt.Errorf("resolve recipient: %w", err)
		}
	}

	// External-phone case: hold the funds in escrow at the partner bank for
	// 7 days. v1 stubs this — we record a 'pending_invite' row that the
	// invite-redemption job will close once the recipient signs up.
	if recipientBalance == nil {
		return s.sendToExternalPhone(ctx, senderID, phone, req)
	}

	// In-AtPost saga.
	dbPool := s.store.DB()
	pgtx, err := dbPool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = pgtx.Rollback(ctx) }()

	if err := s.store.DebitAvailableTx(ctx, pgtx, senderID, req.AmountPaise); err != nil {
		return nil, fmt.Errorf("debit sender: %w", err)
	}
	idem := req.IdempotencyKey
	label := req.Label
	var labelPtr *string
	if label != "" {
		labelPtr = &label
	}
	senderTx, err := s.store.InsertTransaction(ctx, pgtx, store.CreateTransactionInput{
		UserID:             senderID,
		Type:               "send",
		Direction:          "debit",
		AmountPaise:        req.AmountPaise,
		CounterpartyUserID: req.RecipientUserID,
		CounterpartyLabel:  labelPtr,
		Status:             "pending",
		IdempotencyKey:     &idem,
	})
	if err != nil {
		return nil, fmt.Errorf("insert sender tx: %w", err)
	}
	if err := pgtx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit sender debit: %w", err)
	}

	// Emit started event after the durable debit is committed.
	if err := s.producer.PublishSendStarted(ctx, senderID, senderTx.ID, req.AmountPaise, req.RecipientUserID, phone); err != nil {
		slog.Warn("wallet: publish send started failed", "tx", senderTx.ID, "error", err)
	}

	// Step 4: bank transfer.
	bankErr := s.bank.Transfer(ctx, senderBalance.BankAccountRef, recipientBalance.BankAccountRef, req.AmountPaise, senderTx.ID.String())
	if bankErr != nil {
		// Compensate: reverse the sender debit and mark failed.
		s.compensateSendFailure(ctx, senderID, senderTx.ID, req.AmountPaise, bankErr.Error())
		if err := s.producer.PublishSendFailed(ctx, senderID, senderTx.ID, req.AmountPaise, bankErr.Error()); err != nil {
			slog.Warn("wallet: publish send failed failed", "tx", senderTx.ID, "error", err)
		}
		return nil, fmt.Errorf("bank transfer: %w", bankErr)
	}

	// Step 5: settle sender + credit recipient (atomic).
	settleTx, err := dbPool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin settle tx: %w", err)
	}
	defer func() { _ = settleTx.Rollback(ctx) }()

	bankRef := senderTx.ID.String()
	if err := s.store.MarkSettled(ctx, settleTx, senderTx.ID, "succeeded", &bankRef, nil); err != nil {
		return nil, fmt.Errorf("mark sender succeeded: %w", err)
	}
	if err := s.store.SettleDebitTx(ctx, settleTx, senderID, req.AmountPaise); err != nil {
		return nil, fmt.Errorf("settle sender debit: %w", err)
	}
	if err := s.store.CreditAvailableTx(ctx, settleTx, *req.RecipientUserID, req.AmountPaise); err != nil {
		return nil, fmt.Errorf("credit recipient: %w", err)
	}
	receiveTx, err := s.store.InsertTransaction(ctx, settleTx, store.CreateTransactionInput{
		UserID:             *req.RecipientUserID,
		Type:               "receive",
		Direction:          "credit",
		AmountPaise:        req.AmountPaise,
		CounterpartyUserID: &senderID,
		CounterpartyLabel:  labelPtr,
		Status:             "succeeded",
		BankTxnRef:         &bankRef,
	})
	if err != nil {
		return nil, fmt.Errorf("insert recipient tx: %w", err)
	}
	if err := settleTx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit settle tx: %w", err)
	}

	// Frequent-recipients bookkeeping (best effort).
	if err := s.store.BumpRecipient(ctx, senderID, req.RecipientUserID, nil, labelPtr); err != nil {
		slog.Warn("wallet: bump recipient failed", "error", err)
	}

	// Events post-commit.
	if err := s.producer.PublishSendSucceeded(ctx, senderID, senderTx.ID, req.AmountPaise, req.RecipientUserID, phone, bankRef); err != nil {
		slog.Warn("wallet: publish send succeeded failed", "tx", senderTx.ID, "error", err)
	}
	if err := s.producer.PublishReceiveCredited(ctx, *req.RecipientUserID, senderID, receiveTx.ID, req.AmountPaise); err != nil {
		slog.Warn("wallet: publish receive credited failed", "tx", receiveTx.ID, "error", err)
	}

	res := &SendResult{TransactionID: senderTx.ID, Status: "succeeded", AmountPaise: req.AmountPaise}
	if body, err := json.Marshal(res); err == nil {
		_ = s.store.RecordIdempotency(ctx, req.IdempotencyKey, senderID, sendOperation, &senderTx.ID, body)
	}
	return res, nil
}

// compensateSendFailure rolls back a sender debit when the bank transfer
// fails. Best-effort logging on each step — we never want to silently drop a
// reversal because the second leg threw.
func (s *Service) compensateSendFailure(ctx context.Context, userID, txID uuid.UUID, amountPaise int64, reason string) {
	pgtx, err := s.store.DB().Begin(ctx)
	if err != nil {
		slog.Error("wallet: compensate begin tx failed", "tx", txID, "error", err)
		return
	}
	defer func() { _ = pgtx.Rollback(ctx) }()

	if err := s.store.ReverseDebitTx(ctx, pgtx, userID, amountPaise); err != nil {
		slog.Error("wallet: compensate reverse debit failed", "tx", txID, "error", err)
		return
	}
	if err := s.store.MarkSettled(ctx, pgtx, txID, "failed", nil, &reason); err != nil {
		slog.Error("wallet: compensate mark failed failed", "tx", txID, "error", err)
		return
	}
	if err := pgtx.Commit(ctx); err != nil {
		slog.Error("wallet: compensate commit failed", "tx", txID, "error", err)
	}
}

// sendToExternalPhone records a 'pending_invite' row that holds the funds in
// escrow at the partner bank for 7 days. v1 stub — the actual escrow + SMS
// invite + redemption flow ships in v1.1. The pending_out_paise is bumped
// so the sender's available balance reflects the held amount.
func (s *Service) sendToExternalPhone(ctx context.Context, senderID uuid.UUID, phone string, req SendRequest) (*SendResult, error) {
	pgtx, err := s.store.DB().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin invite tx: %w", err)
	}
	defer func() { _ = pgtx.Rollback(ctx) }()

	if err := s.store.DebitAvailableTx(ctx, pgtx, senderID, req.AmountPaise); err != nil {
		return nil, err
	}
	idem := req.IdempotencyKey
	phonePtr := phone
	label := req.Label
	var labelPtr *string
	if label != "" {
		labelPtr = &label
	}
	tx, err := s.store.InsertTransaction(ctx, pgtx, store.CreateTransactionInput{
		UserID:             senderID,
		Type:               "send",
		Direction:          "debit",
		AmountPaise:        req.AmountPaise,
		CounterpartyPhone:  &phonePtr,
		CounterpartyLabel:  labelPtr,
		Status:             "pending_invite",
		IdempotencyKey:     &idem,
		Metadata: map[string]any{
			"escrow_days": 7,
			"flow":        "external_phone_invite",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("insert invite tx: %w", err)
	}
	if err := pgtx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit invite tx: %w", err)
	}

	if err := s.store.BumpRecipient(ctx, senderID, nil, &phonePtr, labelPtr); err != nil {
		slog.Warn("wallet: bump recipient (phone) failed", "error", err)
	}

	if err := s.producer.PublishSendStarted(ctx, senderID, tx.ID, req.AmountPaise, nil, phone); err != nil {
		slog.Warn("wallet: publish invite started failed", "tx", tx.ID, "error", err)
	}

	res := &SendResult{TransactionID: tx.ID, Status: "pending_invite", AmountPaise: req.AmountPaise}
	if body, err := json.Marshal(res); err == nil {
		_ = s.store.RecordIdempotency(ctx, req.IdempotencyKey, senderID, sendOperation, &tx.ID, body)
	}
	return res, nil
}

// MerchantDebit is the internal-only flow used by Pulse Premium / Commerce /
// Food / Bill-pay. It debits the user's wallet and records a merchant_pay
// transaction. Idempotent on idempotencyKey.
func (s *Service) MerchantDebit(ctx context.Context, userID uuid.UUID, amountPaise int64, merchantService, merchantRef, idempotencyKey string) (*SendResult, error) {
	if idempotencyKey == "" {
		return nil, fmt.Errorf("invalid: idempotency_key required")
	}
	if amountPaise <= 0 {
		return nil, fmt.Errorf("invalid: amount must be positive")
	}
	if merchantService == "" || merchantRef == "" {
		return nil, fmt.Errorf("invalid: merchant_service and merchant_ref required")
	}

	if existing, err := s.store.FindIdempotency(ctx, idempotencyKey, userID, "merchant_pay"); err == nil {
		if existing.TransactionID != nil {
			tx, err := s.store.GetTransaction(ctx, userID, *existing.TransactionID)
			if err != nil {
				return nil, err
			}
			return &SendResult{TransactionID: tx.ID, Status: tx.Status, AmountPaise: tx.AmountPaise}, nil
		}
	} else if !errors.Is(err, store.ErrIdempotencyKeyNotFound) {
		return nil, err
	}

	if err := s.AssertWithinMonthlyLimit(ctx, userID, amountPaise); err != nil {
		return nil, err
	}
	balance, err := s.GetBalance(ctx, userID)
	if err != nil {
		return nil, err
	}
	if balance.AvailablePaise < amountPaise {
		return nil, fmt.Errorf("invalid: insufficient balance")
	}

	pgtx, err := s.store.DB().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin merchant tx: %w", err)
	}
	defer func() { _ = pgtx.Rollback(ctx) }()

	if err := s.store.DebitAvailableTx(ctx, pgtx, userID, amountPaise); err != nil {
		return nil, fmt.Errorf("debit merchant: %w", err)
	}
	idem := idempotencyKey
	mService := merchantService
	mRef := merchantRef
	mLabel := titleCase(merchantService)
	tx, err := s.store.InsertTransaction(ctx, pgtx, store.CreateTransactionInput{
		UserID:            userID,
		Type:              "merchant_pay",
		Direction:         "debit",
		AmountPaise:       amountPaise,
		MerchantService:   &mService,
		MerchantRef:       &mRef,
		CounterpartyLabel: &mLabel,
		Status:            "pending",
		IdempotencyKey:    &idem,
	})
	if err != nil {
		return nil, fmt.Errorf("insert merchant tx: %w", err)
	}
	if err := pgtx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit merchant tx: %w", err)
	}

	// For merchant pay, the partner bank moves funds from the user's PPI
	// to the merchant's settlement account. We use the same pool ref we
	// configured in cfg.PoolBankRef as the destination — accounting reconciles
	// nightly via cmd/reconciler.
	bankErr := s.bank.Transfer(ctx, balance.BankAccountRef, s.cfg.PoolBankRef, amountPaise, tx.ID.String())
	if bankErr != nil {
		s.compensateSendFailure(ctx, userID, tx.ID, amountPaise, bankErr.Error())
		return nil, fmt.Errorf("bank transfer: %w", bankErr)
	}

	settleTx, err := s.store.DB().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin settle merchant tx: %w", err)
	}
	defer func() { _ = settleTx.Rollback(ctx) }()
	bankRef := tx.ID.String()
	if err := s.store.MarkSettled(ctx, settleTx, tx.ID, "succeeded", &bankRef, nil); err != nil {
		return nil, fmt.Errorf("mark merchant succeeded: %w", err)
	}
	if err := s.store.SettleDebitTx(ctx, settleTx, userID, amountPaise); err != nil {
		return nil, fmt.Errorf("settle merchant debit: %w", err)
	}
	if err := settleTx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit settle merchant tx: %w", err)
	}

	if err := s.producer.PublishMerchantDebited(ctx, userID, tx.ID, amountPaise, merchantService, merchantRef); err != nil {
		slog.Warn("wallet: publish merchant debited failed", "tx", tx.ID, "error", err)
	}

	res := &SendResult{TransactionID: tx.ID, Status: "succeeded", AmountPaise: amountPaise}
	if body, err := json.Marshal(res); err == nil {
		_ = s.store.RecordIdempotency(ctx, idempotencyKey, userID, "merchant_pay", &tx.ID, body)
	}
	return res, nil
}

// Refund issues a refund of amountPaise against an originalTransactionID. The
// caller is the *internal* merchant service (e.g. dating-service when a
// premium subscription is cancelled). DPDP: the reason field MUST NOT carry
// PII; callers pass discriminator strings only.
func (s *Service) Refund(ctx context.Context, originalTxID uuid.UUID, amountPaise int64, reason string) (*SendResult, error) {
	if amountPaise <= 0 {
		return nil, fmt.Errorf("invalid: amount must be positive")
	}
	original, err := s.store.GetTransactionByID(ctx, originalTxID)
	if err != nil {
		return nil, err
	}
	if original.Status != "succeeded" {
		return nil, fmt.Errorf("invalid: original transaction is not succeeded (status=%s)", original.Status)
	}
	if amountPaise > original.AmountPaise {
		return nil, fmt.Errorf("invalid: refund exceeds original amount")
	}

	originalRef := ""
	if original.BankTxnRef != nil {
		originalRef = *original.BankTxnRef
	}
	if err := s.bank.Refund(ctx, originalRef, amountPaise); err != nil {
		return nil, fmt.Errorf("bank refund: %w", err)
	}

	pgtx, err := s.store.DB().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin refund tx: %w", err)
	}
	defer func() { _ = pgtx.Rollback(ctx) }()

	if err := s.store.CreditAvailableTx(ctx, pgtx, original.UserID, amountPaise); err != nil {
		return nil, fmt.Errorf("credit refund: %w", err)
	}
	reasonPtr := reason
	tx, err := s.store.InsertTransaction(ctx, pgtx, store.CreateTransactionInput{
		UserID:          original.UserID,
		Type:            "refund",
		Direction:       "credit",
		AmountPaise:     amountPaise,
		MerchantService: original.MerchantService,
		MerchantRef:     original.MerchantRef,
		Status:          "succeeded",
		Metadata: map[string]any{
			"original_transaction_id": originalTxID.String(),
			"reason":                  reasonPtr,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("insert refund tx: %w", err)
	}
	if err := pgtx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit refund tx: %w", err)
	}

	if err := s.producer.PublishRefundIssued(ctx, original.UserID, tx.ID, originalTxID, amountPaise, reason); err != nil {
		slog.Warn("wallet: publish refund issued failed", "tx", tx.ID, "error", err)
	}
	return &SendResult{TransactionID: tx.ID, Status: "succeeded", AmountPaise: amountPaise}, nil
}

// titleCase upper-cases the first ASCII letter of s. Avoids strings.Title
// (deprecated since Go 1.18) and locale awareness we do not need.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	first := s[0]
	if first >= 'a' && first <= 'z' {
		first -= 'a' - 'A'
	}
	return string(first) + s[1:]
}

// normalisePhone strips +91/0/spaces; returns "" if input is empty.
func normalisePhone(raw string) string {
	if raw == "" {
		return ""
	}
	s := strings.ReplaceAll(raw, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.TrimPrefix(s, "+91")
	s = strings.TrimPrefix(s, "0091")
	if strings.HasPrefix(s, "0") {
		s = s[1:]
	}
	return s
}
