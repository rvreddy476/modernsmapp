package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/monetization-service/internal/client/razorpayx"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// The payout rail (plan Phase 4C): submitter, reconciler, webhook
// ---------------------------------------------------------------------------
//
// Three entry points, one convergence function, one transition table.
//
//   SubmitPayouts   every five minutes. requested rows older than the
//                   review window move to reserved (over the auto-approve
//                   limit: held plus a manual review). Each reserved row
//                   gets a fund account ensured and one CreatePayout keyed
//                   payout:<id>; success moves it to submitted with the
//                   provider reference.
//
//   ReconcilePayouts every ten minutes. FetchPayout for every submitted or
//                   processing row not reconciled in fifteen minutes,
//                   converged through the table.
//
//   HandleProviderWebhook verifies the signature, stores the event
//                   (ON CONFLICT DO NOTHING; zero rows is a replay and a
//                   no-op), finds the row by provider reference, and
//                   converges the same way. The event is marked consumed
//                   ONLY if a request row was actually updated.
//
// The ambiguous-response rule, verbatim from the plan: on timeout or
// unknown error the row stays reserved, and the next attempt first calls
// ListPayoutsByReference. One match is adopted, two matches -> held plus
// alert, none -> resubmit with the same key. Never submit twice without
// looking first.
//
// The failure that most worried the designer: a paid webhook arriving
// before the provider reference is persisted, because CreatePayout
// returned but our commit failed. The lookup misses, and had the event
// been consumed, the request would sit in reserved forever. Hence the
// two rules above — adopt by reference before resubmitting, and never
// consume an event that updated nothing.

const (
	// PayoutAutoApproveLimitPaise: a request above this waits for a human.
	// Rs 10,000.
	PayoutAutoApproveLimitPaise int64 = 1_000_000
	// payoutReviewWindow: how long a requested payout sits before the
	// submitter picks it up.
	payoutReviewWindow = 24 * time.Hour
	// payoutReconcileAfter: a submitted or processing payout not heard
	// about for this long is fetched.
	payoutReconcileAfter = 15 * time.Minute
	// payoutSubmitBatch bounds one submitter pass.
	payoutSubmitBatch = 100

	payoutProvider = "razorpayx"

	// HoldReasonManual is the review_type for holds the rail opens itself
	// (over the limit, ambiguous, unpayable method).
	HoldReasonManual = "manual"
)

var (
	// ErrPayoutRailNotConfigured: payouts are enabled but no client is
	// wired (RazorpayX credentials incomplete), so the rail is off.
	ErrPayoutRailNotConfigured = errors.New("PAYOUT_RAIL_NOT_CONFIGURED")
	// ErrWebhookSignature and ErrWebhookEventID are the client's,
	// re-exported so the HTTP layer maps them without importing it.
	ErrWebhookSignature = razorpayx.ErrSignatureInvalid
	ErrWebhookEventID   = razorpayx.ErrMissingEventID
)

// WithPayoutRail wires the provider client and the webhook secret. Both
// are required for the rail to be on; main constructs the client only
// when payouts are enabled and all four credentials are set.
func (s *Service) WithPayoutRail(client razorpayx.API, webhookSecret string) *Service {
	s.payoutClient = client
	s.webhookSecret = webhookSecret
	return s
}

// PayoutRailEnabled reports whether payouts are enabled AND a provider
// client is configured. The workers and the webhook run only when it is.
func (s *Service) PayoutRailEnabled() bool {
	return s.payoutsEnabled && s.payoutClient != nil && s.webhookSecret != ""
}

// payoutReference is both the X-Payout-Idempotency key and the
// reference_id stored at the provider: the request id, and nothing else,
// so an attempt can always be found again.
func payoutReference(id uuid.UUID) string { return "payout:" + id.String() }

// ---------------------------------------------------------------------------
// Submitter
// ---------------------------------------------------------------------------

// SubmitPayouts is one pass of the submitter. Per-row failures are logged
// and do not stop the pass; the returned error is for the rail being off
// or the listing queries failing.
func (s *Service) SubmitPayouts(ctx context.Context) error {
	if !s.PayoutRailEnabled() {
		return ErrPayoutRailNotConfigured
	}
	now := time.Now()

	// 1. requested -> reserved (or held) once past the review window.
	cutoff := now.Add(-payoutReviewWindow)
	requested, err := s.store.ListPayoutRequestsByStatus(ctx, PayoutStatusRequested, &cutoff, payoutSubmitBatch)
	if err != nil {
		return fmt.Errorf("list requested payouts: %w", err)
	}
	for i := range requested {
		row := &requested[i]
		if row.AmountPaise > PayoutAutoApproveLimitPaise {
			s.holdPayoutForReview(ctx, row, fmt.Sprintf("amount %d paise is over the auto-approve limit of %d paise", row.AmountPaise, PayoutAutoApproveLimitPaise))
			continue
		}
		if err := s.transitionPayoutNoTx(ctx, row.ID, PayoutStatusRequested, PayoutStatusReserved, postgres.PayoutRequestPatch{}); err != nil {
			slog.Error("payout submitter: reserve", "request_id", row.ID, "error", err)
		}
	}

	// 2. reserved -> submitted, one CreatePayout each, looking first.
	reserved, err := s.store.ListPayoutRequestsByStatus(ctx, PayoutStatusReserved, nil, payoutSubmitBatch)
	if err != nil {
		return fmt.Errorf("list reserved payouts: %w", err)
	}
	for i := range reserved {
		row := &reserved[i]
		if err := s.submitReserved(ctx, row, now); err != nil {
			slog.Error("payout submitter: submit", "request_id", row.ID, "error", err)
		}
	}
	return nil
}

// holdOutcome is a submit failure that a human has to look at, as
// opposed to one that the next pass may simply retry.
type holdOutcome struct{ reason string }

func (h holdOutcome) Error() string { return "hold: " + h.reason }

// submitReserved takes one reserved row to the provider.
func (s *Service) submitReserved(ctx context.Context, row *postgres.PayoutRequestRow, now time.Time) error {
	ref := payoutReference(row.ID)
	if row.TransactionID == nil || row.NetPaise == nil || *row.NetPaise <= 0 {
		s.holdPayoutForReview(ctx, row, "reserved request has no priced transaction; cannot be paid")
		return nil
	}

	// The ambiguous-response rule: a previous attempt may have reached the
	// provider. Look before submitting.
	if row.RetryCount > 0 {
		matches, err := s.payoutClient.ListPayoutsByReference(ctx, ref)
		if err != nil {
			// Still cannot see; stays reserved.
			return fmt.Errorf("list payouts by reference after an ambiguous attempt: %w", err)
		}
		switch len(matches) {
		case 0:
			// Nothing reached the provider; resubmit with the same key.
		case 1:
			slog.Warn("payout submitter: adopting existing provider payout after an ambiguous attempt",
				"request_id", row.ID, "provider_reference", matches[0].ID, "provider_status", matches[0].Status)
			return s.adoptPayout(ctx, row, matches[0], now)
		default:
			ids := make([]string, 0, len(matches))
			for _, m := range matches {
				ids = append(ids, m.ID)
			}
			reason := fmt.Sprintf("ambiguous: %d payouts at the provider carry reference %s (%s); needs a human", len(matches), ref, strings.Join(ids, ", "))
			slog.Error("ALERT payout submitter: ambiguous provider state, request held", "request_id", row.ID, "user_id", row.UserID, "payouts", ids)
			s.holdPayoutForReview(ctx, row, reason)
			return nil
		}
	}

	fundAccountID, err := s.fundAccountForRequest(ctx, row)
	if err != nil {
		var h holdOutcome
		if errors.As(err, &h) {
			s.holdPayoutForReview(ctx, row, h.reason)
			return nil
		}
		return err // provider unreachable; stays reserved
	}

	// Count the attempt BEFORE the call, so a crash between the call and
	// the commit still makes the next pass look first.
	if err := s.store.MarkPayoutSubmitAttempt(ctx, row.ID); err != nil {
		return fmt.Errorf("mark submit attempt: %w", err)
	}
	amount := *row.NetPaise
	p, err := s.payoutClient.CreatePayout(ctx, ref, amount, fundAccountID, razorpayx.ModeFor(amount), ref)
	if err != nil {
		if razorpayx.IsAmbiguous(err) {
			slog.Warn("payout submitter: ambiguous response, request stays reserved and will be looked up before any resubmission",
				"request_id", row.ID, "error", err)
			return nil
		}
		// A definitive refusal: the money goes back now.
		slog.Warn("payout submitter: provider refused the payout", "request_id", row.ID, "error", err)
		return s.failReserved(ctx, row, "provider refused: "+err.Error(), now)
	}
	return s.adoptPayout(ctx, row, p, now)
}

// adoptPayout records the provider payout on a reserved row (reserved ->
// submitted with the reference) and converges it with the payout's
// current status, in one transaction.
func (s *Service) adoptPayout(ctx context.Context, row *postgres.PayoutRequestRow, p razorpayx.Payout, now time.Time) error {
	return s.store.WithTx(ctx, func(tx pgxTx) error {
		locked, err := s.store.GetPayoutRequestForUpdateTx(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		if locked == nil || locked.Status != PayoutStatusReserved {
			return fmt.Errorf("adopt payout: request %s is %s, not reserved: %w", row.ID, statusOf(locked), ErrIllegalTransition)
		}
		ps := p.Status
		if err := s.transitionPayout(ctx, tx, row.ID, PayoutStatusReserved, PayoutStatusSubmitted, postgres.PayoutRequestPatch{
			ProviderReference: &p.ID,
			ProviderStatus:    &ps,
			SubmittedAt:       &now,
			LastReconciledAt:  &now,
		}); err != nil {
			return err
		}
		locked.Status = PayoutStatusSubmitted
		locked.ProviderReference = &p.ID
		if _, err := s.convergeTx(ctx, tx, locked, p, now); err != nil && !errors.Is(err, ErrIllegalTransition) {
			return err
		}
		slog.Info("payout submitted", "request_id", row.ID, "provider_reference", p.ID, "provider_status", p.Status, "amount_paise", p.AmountPaise)
		return nil
	})
}

// failReserved is the definitive-refusal path: reserved -> failed with
// the reason, funds returned.
func (s *Service) failReserved(ctx context.Context, row *postgres.PayoutRequestRow, reason string, now time.Time) error {
	return s.store.WithTx(ctx, func(tx pgxTx) error {
		locked, err := s.store.GetPayoutRequestForUpdateTx(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		if locked == nil || locked.Status != PayoutStatusReserved {
			return fmt.Errorf("fail reserved: request %s is %s: %w", row.ID, statusOf(locked), ErrIllegalTransition)
		}
		if err := s.transitionPayout(ctx, tx, row.ID, PayoutStatusReserved, PayoutStatusFailed, postgres.PayoutRequestPatch{
			FailureReason: &reason, ProcessedAt: &now, LastReconciledAt: &now,
		}); err != nil {
			return err
		}
		return s.returnFundsTx(ctx, tx, locked, reason)
	})
}

// holdPayoutForReview moves a requested or reserved row to held with the
// reason and opens the manual review that explains it. Errors are logged:
// a hold that cannot be recorded leaves the row where it was, which is
// the safe direction.
func (s *Service) holdPayoutForReview(ctx context.Context, row *postgres.PayoutRequestRow, reason string) {
	err := s.store.WithTx(ctx, func(tx pgxTx) error {
		if err := s.transitionPayout(ctx, tx, row.ID, row.Status, PayoutStatusHeld, postgres.PayoutRequestPatch{FailureReason: &reason}); err != nil {
			return err
		}
		notes := fmt.Sprintf("payout request %s for %d paise held by the rail: %s", row.ID, row.AmountPaise, reason)
		_, err := s.store.CreateFraudReviewTx(ctx, tx, &postgres.FraudReview{
			CreatorID:  row.UserID,
			ReviewType: HoldReasonManual,
			RiskScore:  holdRiskScore,
			Status:     "pending",
			Notes:      &notes,
		})
		return err
	})
	if err != nil {
		slog.Error("payout rail: could not hold request for review", "request_id", row.ID, "reason", reason, "error", err)
		return
	}
	slog.Warn("payout held for manual review", "request_id", row.ID, "user_id", row.UserID, "amount_paise", row.AmountPaise, "reason", reason)
}

// fundAccountForRequest returns the provider fund account the request's
// payout method pays into, creating the contact and fund account when
// the method has bank details but no fund account yet. A holdOutcome
// means a human must look; any other error means try again later.
func (s *Service) fundAccountForRequest(ctx context.Context, row *postgres.PayoutRequestRow) (string, error) {
	if row.PayoutMethodID == nil {
		return "", holdOutcome{"request has no payout method"}
	}
	m, err := s.store.GetPayoutMethodByID(ctx, *row.PayoutMethodID)
	if err != nil {
		return "", fmt.Errorf("load payout method: %w", err)
	}
	if m == nil || m.UserID != row.UserID {
		return "", holdOutcome{"payout method is missing or is not the creator's"}
	}
	if m.RzpFundAccountID != nil && *m.RzpFundAccountID != "" {
		return *m.RzpFundAccountID, nil
	}
	if m.MethodType != PayoutMethodTypeBankAccount || m.IFSC == "" || strings.TrimSpace(m.HolderName) == "" {
		return "", holdOutcome{fmt.Sprintf("payout method %s (%s) has no bank account the rail can pay", m.ID, m.MethodType)}
	}
	accountNumber, err := s.decryptBankAccountNumber(m.DetailsEncrypted)
	if err != nil {
		return "", holdOutcome{"payout method's account number cannot be read: " + err.Error()}
	}
	contactID, err := s.ensureContact(ctx, row.UserID, m.HolderName)
	if err != nil {
		return "", s.providerOutcome("ensure contact", err)
	}
	fundAccountID, err := s.payoutClient.EnsureFundAccount(ctx, contactID, razorpayx.BankDetails{
		HolderName: m.HolderName, AccountNumber: accountNumber, IFSC: m.IFSC,
	})
	if err != nil {
		return "", s.providerOutcome("ensure fund account", err)
	}
	if err := s.store.SetPayoutMethodFundAccount(ctx, m.ID, fundAccountID); err != nil {
		return "", fmt.Errorf("record fund account: %w", err)
	}
	return fundAccountID, nil
}

// providerOutcome turns a provider error into "retry later" (ambiguous)
// or "hold" (a definitive refusal, e.g. a bad IFSC).
func (s *Service) providerOutcome(step string, err error) error {
	if razorpayx.IsAmbiguous(err) {
		return fmt.Errorf("%s: %w", step, err)
	}
	return holdOutcome{step + " refused by the provider: " + err.Error()}
}

// ensureContact returns the creator's provider contact, cached in
// creator_payout_accounts after the first lookup.
func (s *Service) ensureContact(ctx context.Context, userID uuid.UUID, name string) (string, error) {
	cached, err := s.store.GetPayoutContactID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("load contact id: %w", err)
	}
	if cached != "" {
		return cached, nil
	}
	contactID, err := s.payoutClient.EnsureContact(ctx, userID.String(), name, "")
	if err != nil {
		return "", err
	}
	if err := s.store.UpsertPayoutContactID(ctx, userID, contactID); err != nil {
		return "", fmt.Errorf("cache contact id: %w", err)
	}
	return contactID, nil
}

// ---------------------------------------------------------------------------
// Reconciler
// ---------------------------------------------------------------------------

// ReconcilePayouts is one pass of the reconciler.
func (s *Service) ReconcilePayouts(ctx context.Context) error {
	if !s.PayoutRailEnabled() {
		return ErrPayoutRailNotConfigured
	}
	now := time.Now()
	rows, err := s.store.ListPayoutRequestsToReconcile(ctx, now.Add(-payoutReconcileAfter), 200)
	if err != nil {
		return fmt.Errorf("list payouts to reconcile: %w", err)
	}
	for i := range rows {
		row := &rows[i]
		if row.ProviderReference == nil {
			continue
		}
		p, err := s.payoutClient.FetchPayout(ctx, *row.ProviderReference)
		if err != nil {
			slog.Warn("payout reconciler: fetch", "request_id", row.ID, "provider_reference", *row.ProviderReference, "error", err)
			continue
		}
		err = s.store.WithTx(ctx, func(tx pgxTx) error {
			locked, err := s.store.GetPayoutRequestForUpdateTx(ctx, tx, row.ID)
			if err != nil {
				return err
			}
			if locked == nil {
				return nil
			}
			updated, err := s.convergeTx(ctx, tx, locked, p, now)
			if errors.Is(err, ErrIllegalTransition) {
				slog.Warn("payout reconciler: provider status does not follow from our state",
					"request_id", row.ID, "status", locked.Status, "provider_status", p.Status)
				return nil
			}
			if err != nil {
				return err
			}
			if !updated {
				// Unknown provider status: record it and the time so the
				// row is not fetched again at once.
				_, err := s.store.TouchPayoutRequestTx(ctx, tx, row.ID, locked.Status, p.Status, nil, now)
				return err
			}
			return nil
		})
		if err != nil {
			slog.Error("payout reconciler: converge", "request_id", row.ID, "error", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Webhook
// ---------------------------------------------------------------------------

// WebhookOutcome is what a verified delivery did.
type WebhookOutcome string

const (
	WebhookProcessed WebhookOutcome = "processed" // a request row was updated; event consumed
	WebhookReplay    WebhookOutcome = "replay"    // (provider, event_id) already stored; nothing ran
	WebhookUnmatched WebhookOutcome = "unmatched" // no request carries this provider reference; stored, not consumed
	WebhookIgnored   WebhookOutcome = "ignored"   // matched, but no transition accepted it; stored, not consumed
)

// WebhookResult reports one delivery.
type WebhookResult struct {
	EventID   string         `json:"event_id"`
	EventType string         `json:"event_type"`
	Outcome   WebhookOutcome `json:"outcome"`
	RequestID *uuid.UUID     `json:"request_id,omitempty"`
}

// HandleProviderWebhook verifies, stores and converges one delivery.
func (s *Service) HandleProviderWebhook(ctx context.Context, headers http.Header, raw []byte) (WebhookResult, error) {
	if !s.PayoutRailEnabled() {
		return WebhookResult{}, ErrPayoutRailNotConfigured
	}
	ev, err := razorpayx.VerifyWebhook(s.webhookSecret, headers, raw)
	if err != nil {
		return WebhookResult{}, err
	}
	res := WebhookResult{EventID: ev.ID, EventType: ev.Type, Outcome: WebhookUnmatched}

	inserted, err := s.store.InsertProviderEvent(ctx, payoutProvider, ev.ID, ev.Type, ev.Payout.ID, raw)
	if err != nil {
		return res, err
	}
	if !inserted {
		res.Outcome = WebhookReplay
		slog.Info("payout webhook replayed, no-op", "event_id", ev.ID, "event", ev.Type)
		return res, nil
	}

	now := time.Now()
	err = s.store.WithTx(ctx, func(tx pgxTx) error {
		row, err := s.store.GetPayoutRequestByProviderReferenceTx(ctx, tx, ev.Payout.ID)
		if err != nil {
			return err
		}
		if row == nil {
			// Not ours yet (or never): stored, visible, not consumed.
			return nil
		}
		res.RequestID = &row.ID
		updated, err := s.convergeTx(ctx, tx, row, ev.Payout, now)
		if errors.Is(err, ErrIllegalTransition) {
			res.Outcome = WebhookIgnored
			return nil
		}
		if err != nil {
			return err
		}
		if !updated {
			res.Outcome = WebhookIgnored
			return nil
		}
		// Consumed ONLY because a request row was actually updated, and on
		// the same transaction as that update.
		res.Outcome = WebhookProcessed
		return s.store.MarkProviderEventConsumedTx(ctx, tx, payoutProvider, ev.ID, row.ID)
	})
	if err != nil {
		return res, err
	}
	switch res.Outcome {
	case WebhookUnmatched:
		slog.Warn("payout webhook matched no request; stored unconsumed", "event_id", ev.ID, "event", ev.Type, "provider_reference", ev.Payout.ID)
	case WebhookIgnored:
		slog.Warn("payout webhook accepted by no transition; stored unconsumed", "event_id", ev.ID, "event", ev.Type, "request_id", res.RequestID, "provider_status", ev.Payout.Status)
	default:
		slog.Info("payout webhook processed", "event_id", ev.ID, "event", ev.Type, "request_id", res.RequestID, "provider_status", ev.Payout.Status)
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// Convergence
// ---------------------------------------------------------------------------

// convergeTx moves row toward the provider's view of the payout, through
// the transition table, with the ledger effects of each terminal state.
// updated reports whether a row was written. ErrIllegalTransition means
// the provider's status does not follow from our state (a stale event);
// nothing was written.
func (s *Service) convergeTx(ctx context.Context, tx pgxTx, row *postgres.PayoutRequestRow, p razorpayx.Payout, now time.Time) (bool, error) {
	target, known := razorpayx.MapStatus(p.Status)
	if !known {
		return false, nil
	}
	providerStatus := p.Status
	var utr *string
	if p.UTR != "" {
		u := p.UTR
		utr = &u
	}

	if target == row.Status {
		// Same state: record what the provider said and when.
		return s.store.TouchPayoutRequestTx(ctx, tx, row.ID, row.Status, providerStatus, utr, now)
	}
	if !CanTransitionPayout(row.Status, target) {
		return false, ErrIllegalTransition
	}

	switch target {
	case PayoutStatusProcessing:
		if err := s.transitionPayout(ctx, tx, row.ID, row.Status, target, postgres.PayoutRequestPatch{
			ProviderStatus: &providerStatus, LastReconciledAt: &now, UTR: utr,
		}); err != nil {
			return false, err
		}

	case PayoutStatusPaid:
		if err := s.transitionPayout(ctx, tx, row.ID, row.Status, target, postgres.PayoutRequestPatch{
			ProviderStatus: &providerStatus, UTR: utr, ProcessedAt: &now, LastReconciledAt: &now,
		}); err != nil {
			return false, err
		}
		if err := s.paidEffectsTx(ctx, tx, row, p.UTR); err != nil {
			return false, err
		}

	case PayoutStatusFailed:
		reason := failureReason(p, "payout failed at the provider")
		if err := s.transitionPayout(ctx, tx, row.ID, row.Status, target, postgres.PayoutRequestPatch{
			ProviderStatus: &providerStatus, FailureReason: &reason, ProcessedAt: &now, LastReconciledAt: &now,
		}); err != nil {
			return false, err
		}
		if err := s.returnFundsTx(ctx, tx, row, reason); err != nil {
			return false, err
		}

	case PayoutStatusReversed:
		reason := failureReason(p, "payout reversed by the bank")
		wasPaid := row.Status == PayoutStatusPaid
		if err := s.transitionPayout(ctx, tx, row.ID, row.Status, target, postgres.PayoutRequestPatch{
			ProviderStatus: &providerStatus, FailureReason: &reason, ProcessedAt: &now, LastReconciledAt: &now, UTR: utr,
		}); err != nil {
			return false, err
		}
		if wasPaid {
			if err := s.reversalAfterPaidTx(ctx, tx, row, reason); err != nil {
				return false, err
			}
		} else if err := s.returnFundsTx(ctx, tx, row, reason); err != nil {
			return false, err
		}

	default:
		return false, ErrIllegalTransition
	}
	row.Status = target
	return true, nil
}

func failureReason(p razorpayx.Payout, fallback string) string {
	if r := strings.TrimSpace(p.FailureReason); r != "" {
		return r
	}
	return fallback + " (" + p.Status + ")"
}

// paidEffectsTx: the money has left. Complete the transaction, release
// pending_payout, and move the ledger leg from payout_hold to
// platform_revenue.
func (s *Service) paidEffectsTx(ctx context.Context, tx pgxTx, row *postgres.PayoutRequestRow, utr string) error {
	if row.TransactionID == nil {
		return fmt.Errorf("paid effects: request %s has no transaction", row.ID)
	}
	note := "paid"
	if utr != "" {
		note = "paid; UTR " + utr
	}
	if err := s.store.SetTransactionStatusTx(ctx, tx, *row.TransactionID, "completed", note); err != nil {
		return err
	}
	if err := s.store.ReleasePendingPayoutTx(ctx, tx, row.UserID, row.AmountPaise); err != nil {
		return fmt.Errorf("release pending payout for %s: %w", row.ID, err)
	}
	holdAcct, err := s.store.EnsureAccountTx(ctx, tx, row.UserID, payoutHoldAccountType)
	if err != nil {
		return fmt.Errorf("ensure payout_hold account: %w", err)
	}
	revAcct, err := s.store.EnsureAccountTx(ctx, tx, platformOwnerID, platformRevenueAccountType)
	if err != nil {
		return fmt.Errorf("ensure platform_revenue account: %w", err)
	}
	refID := row.ID
	return s.store.InsertLedgerEntryTx(ctx, tx, &postgres.LedgerEntry{
		DebitAccountID:  holdAcct.ID,
		CreditAccountID: revAcct.ID,
		AmountPaise:     row.AmountPaise,
		Currency:        "INR",
		ReferenceType:   payoutRequestReferenceType,
		ReferenceID:     &refID,
		IdempotencyKey:  "payout_paid:" + row.ID.String(),
		Description:     "Payout paid: reserved balance settled to the creator's bank",
	})
}

// returnFundsTx: the payout did not happen (failed, or reversed while in
// flight). The gross goes back from pending_payout to balance, the
// transaction is failed, the ledger leg comes back from payout_hold, and
// the TDS ledger gets a negative counter-entry so the yearly gross is
// right again.
func (s *Service) returnFundsTx(ctx context.Context, tx pgxTx, row *postgres.PayoutRequestRow, reason string) error {
	if row.TransactionID == nil {
		return fmt.Errorf("return funds: request %s has no transaction", row.ID)
	}
	if err := s.store.SetTransactionStatusTx(ctx, tx, *row.TransactionID, "failed", reason); err != nil {
		return err
	}
	if err := s.store.ReturnPendingPayoutTx(ctx, tx, row.UserID, row.AmountPaise); err != nil {
		return fmt.Errorf("return pending payout for %s: %w", row.ID, err)
	}
	walletAcct, err := s.store.EnsureAccountTx(ctx, tx, row.UserID, creatorWalletAccountType)
	if err != nil {
		return fmt.Errorf("ensure wallet account: %w", err)
	}
	holdAcct, err := s.store.EnsureAccountTx(ctx, tx, row.UserID, payoutHoldAccountType)
	if err != nil {
		return fmt.Errorf("ensure payout_hold account: %w", err)
	}
	refID := row.ID
	if err := s.store.InsertLedgerEntryTx(ctx, tx, &postgres.LedgerEntry{
		DebitAccountID:  holdAcct.ID,
		CreditAccountID: walletAcct.ID,
		AmountPaise:     row.AmountPaise,
		Currency:        "INR",
		ReferenceType:   payoutRequestReferenceType,
		ReferenceID:     &refID,
		IdempotencyKey:  "payout_return:" + row.ID.String(),
		Description:     "Payout not completed: reserved balance returned (" + reason + ")",
	}); err != nil {
		return fmt.Errorf("return ledger leg: %w", err)
	}
	return s.tdsCounterEntryTx(ctx, tx, row)
}

// reversalAfterPaidTx: the bank sent the money back after we had booked
// it as paid. pending_payout was already released, so balance += gross,
// the leg comes back from platform_revenue, and the TDS counter-entry is
// posted.
func (s *Service) reversalAfterPaidTx(ctx context.Context, tx pgxTx, row *postgres.PayoutRequestRow, reason string) error {
	if row.TransactionID == nil {
		return fmt.Errorf("reversal: request %s has no transaction", row.ID)
	}
	if err := s.store.SetTransactionStatusTx(ctx, tx, *row.TransactionID, "reversed", reason); err != nil {
		return err
	}
	if err := s.store.CreditBalanceTx(ctx, tx, row.UserID, row.AmountPaise); err != nil {
		return err
	}
	walletAcct, err := s.store.EnsureAccountTx(ctx, tx, row.UserID, creatorWalletAccountType)
	if err != nil {
		return fmt.Errorf("ensure wallet account: %w", err)
	}
	revAcct, err := s.store.EnsureAccountTx(ctx, tx, platformOwnerID, platformRevenueAccountType)
	if err != nil {
		return fmt.Errorf("ensure platform_revenue account: %w", err)
	}
	refID := row.ID
	if err := s.store.InsertLedgerEntryTx(ctx, tx, &postgres.LedgerEntry{
		DebitAccountID:  revAcct.ID,
		CreditAccountID: walletAcct.ID,
		AmountPaise:     row.AmountPaise,
		Currency:        "INR",
		ReferenceType:   payoutRequestReferenceType,
		ReferenceID:     &refID,
		IdempotencyKey:  "payout_reversal:" + row.ID.String(),
		Description:     "Payout reversed by the bank after payment: balance restored (" + reason + ")",
	}); err != nil {
		return fmt.Errorf("reversal ledger leg: %w", err)
	}
	return s.tdsCounterEntryTx(ctx, tx, row)
}

// tdsCounterEntryTx posts the negative twin of the request's TDS row: the
// gross that was counted toward the yearly threshold is uncounted, and
// the amount the priced row recorded is negated so the record for this
// request nets to zero.
//
// It mirrors the ledger row the request wrote when it was priced, not
// the request's tds_paise: with MONETIZATION_TDS_APPLY=false the ledger
// row carries the COMPUTED amount while the request carries 0 (nothing
// was deducted), and the counter-entry must cancel what was recorded,
// not what was withheld. Nothing is "released" to the creator in that
// mode because nothing was taken: the money coming back is the gross,
// which the caller has already returned to the balance. If no priced
// row exists (a request created before the ledger row existed) the
// request's own figures are used.
func (s *Service) tdsCounterEntryTx(ctx context.Context, tx pgxTx, row *postgres.PayoutRequestRow) error {
	refID := row.ID
	gross, tds := row.AmountPaise, row.TDSPaise
	if priced, err := s.store.GetTDSEntryByReferenceTx(ctx, tx, row.ID); err != nil {
		return fmt.Errorf("tds counter-entry: read priced row: %w", err)
	} else if priced != nil {
		gross, tds = priced.GrossAmountPaise, priced.TDSAmountPaise
	}
	return s.store.InsertTDSEntryTx(ctx, tx, &postgres.TDSEntry{
		CreatorID:        row.UserID,
		FinancialYear:    GetFinancialYear(),
		GrossAmountPaise: -gross,
		TDSAmountPaise:   -tds,
		Section:          s.TDSSection(),
		ReferenceID:      &refID,
	})
}

// transitionPayoutNoTx is transitionPayout in autocommit.
func (s *Service) transitionPayoutNoTx(ctx context.Context, id uuid.UUID, from, to string, patch postgres.PayoutRequestPatch) error {
	if !CanTransitionPayout(from, to) {
		return ErrIllegalTransition
	}
	return s.store.TransitionPayoutRequest(ctx, id, from, to, patch)
}

func statusOf(r *postgres.PayoutRequestRow) string {
	if r == nil {
		return "missing"
	}
	return r.Status
}
