package service

// Commerce P0: the server-side money authority.
//
// Three rules are enforced here and nowhere else:
//
//	1. Only a signature-verified provider webhook or a server-initiated
//	   provider fetch may create terminal payment state. A client callback
//	   is evidence (A1, R-3).
//	2. A refund is persisted, with a deterministic provider idempotency key,
//	   BEFORE the provider is contacted, and only a verified provider
//	   outcome settles it (A6, LB-8).
//	3. Every domain event leaves through the transactional outbox, in the
//	   same transaction as the effect it describes (LB-7, R-2).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/atpost/payments-service/internal/config"
	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/obs"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── Webhook application ─────────────────────────────────────────────

// WebhookInput is a provider webhook that has already had its signature
// verified by the transport layer.
type WebhookInput struct {
	Provider string
	// EventID is the provider's unique event identifier. For Razorpay this
	// is the `x-razorpay-event-id` HEADER — never a body field. R-5: the
	// old code read a body `id` that Razorpay does not send on payment
	// events, so the dedupe key was almost always empty.
	EventID           string
	EventType         string
	ProviderOrderID   string
	ProviderPaymentID string
	ProviderRefundID  string
	AmountMinor       int64
	// Currency is the provider's denomination for AmountMinor. Verified
	// against the intent inside the terminal transaction (B2).
	Currency string
}

// ErrWebhookDuplicate signals an already-processed event. The handler
// answers 200 so the provider stops retrying.
var ErrWebhookDuplicate = errors.New("payments: duplicate provider event")

// ApplyWebhook records, applies and enqueues in ONE transaction.
//
// A3. The failure this closes: the old handler inserted the inbox row, then
// applied the status, then published to Kafka. A crash between the first and
// second step meant the provider's retry hit the inbox row, was treated as a
// duplicate, and returned 200 — with the money effect never recorded and no
// trace that it was owed. Razorpay captures ₹10,000, the order stays unpaid
// forever.
func (s *Service) ApplyWebhook(ctx context.Context, in WebhookInput) error {
	if in.EventID == "" {
		return postgres.ErrBlankEventID
	}
	if in.Provider == "" {
		in.Provider = "razorpay"
	}

	// Refund settlement has its own idempotency key (the provider's refund
	// id), because one refund can arrive under more than one event id.
	if in.ProviderRefundID != "" {
		return s.applyRefundWebhook(ctx, in)
	}

	newStatus := ""
	switch in.EventType {
	case "payment.captured", "order.paid":
		newStatus = "succeeded"
	}
	// `payment.failed` deliberately changes NOTHING on the intent.
	//
	// Razorpay lets a customer retry on the same order after a failed attempt.
	// This used to finalise the intent FAILED on the first failure, and since
	// failed never becomes succeeded, the retry's `payment.captured` was
	// refused: the customer was charged and the order stayed failed. The
	// attempt is now recorded — the provider inbox row written below carries
	// the event type, order id and payment id, and a replay of it is still a
	// duplicate — and the intent stays pending. Only the reconciler finalises
	// FAILED, once the order has no captured or in-flight payment and has been
	// quiet for the failed-attempt window (reconcileOnce). Nothing is published.

	// B2: the amount and currency travel INTO the transaction. The check
	// that used to sit below this call, after the commit, is gone — by then
	// the terminal status and the `payment.succeeded` outbox row already
	// existed, and commerce acts on that event.
	_, err := s.store.ApplyWebhookAtomically(ctx, postgres.WebhookEffect{
		Provider:          in.Provider,
		EventID:           in.EventID,
		EventType:         in.EventType,
		ProviderOrderID:   in.ProviderOrderID,
		ProviderPaymentID: in.ProviderPaymentID,
		NewStatus:         newStatus,
		AmountMinor:       in.AmountMinor,
		Currency:          in.Currency,
	})
	switch {
	case errors.Is(err, postgres.ErrDuplicateEvent):
		return ErrWebhookDuplicate
	case errors.Is(err, postgres.ErrIntentNotFound):
		// An event for an intent we do not have. Recording it would need a
		// row we cannot write, so let the provider retry: a genuinely
		// unknown intent will keep failing and surface on the alarm, which
		// is better than silently acknowledging someone else's payment.
		return fmt.Errorf("payments: no intent for provider order %q: %w", in.ProviderOrderID, err)
	case errors.Is(err, postgres.ErrWebhookAmountMismatch):
		// Nothing was written: no terminal status, no outbox row, no inbox
		// row. The provider will retry, this will keep alarming, and the
		// intent stays non-terminal until the reconciler resolves it against
		// the provider. That is the correct end state for "a signature-valid
		// event disagrees with us about how much money moved".
		slog.Error("payments: PROVIDER AMOUNT MISMATCH — no terminal state written",
			"provider", in.Provider, "event_id", in.EventID,
			"provider_order_id", in.ProviderOrderID,
			"event_minor", in.AmountMinor, "currency", in.Currency, "error", err)
		return err
	case err != nil:
		return err
	}
	return nil
}

func (s *Service) applyRefundWebhook(ctx context.Context, in WebhookInput) error {
	// B3. One transaction: inbox row, intent resolution and the refund
	// ledger effect. The three-step version this replaces committed the
	// inbox row on its own, so a failure in either later step left the event
	// recorded as seen with the money never credited — and the provider's
	// retry was then answered 200 as a duplicate.
	//
	// A refund amount must be present and positive. A zero-amount refund
	// event would otherwise "settle" a command by crediting nothing.
	if in.AmountMinor <= 0 {
		return fmt.Errorf("payments: refund event %q carried no amount; refusing to settle", in.EventID)
	}

	applied, status, err := s.store.ApplyRefundWebhookAtomically(ctx, postgres.WebhookEffect{
		Provider:          in.Provider,
		EventID:           in.EventID,
		EventType:         in.EventType,
		ProviderOrderID:   in.ProviderOrderID,
		ProviderPaymentID: in.ProviderPaymentID,
		AmountMinor:       in.AmountMinor,
		Currency:          in.Currency,
	}, in.ProviderRefundID)
	switch {
	case errors.Is(err, postgres.ErrDuplicateEvent):
		return ErrWebhookDuplicate
	case errors.Is(err, postgres.ErrIntentNotFound):
		// Nothing was committed, so the provider will retry and this will
		// keep alarming. Acknowledging an unattributable refund is what
		// loses the ledger entry.
		slog.Error("payments: refund webhook could not be attributed to an intent",
			"event_id", in.EventID, "provider_order_id", in.ProviderOrderID,
			"provider_payment_id", in.ProviderPaymentID, "error", err)
		return err
	case err != nil:
		return err
	}
	if applied {
		slog.Info("payments: refund settled",
			"provider_refund_id", in.ProviderRefundID, "status", status)
	}
	return nil
}

// ─── Durable refunds ─────────────────────────────────────────────────

// RefundRequest is a service-to-service refund instruction.
//
// There is no `actorID` any more. A refund is not something an end user
// asks payments for — the old signature let the PAYER initiate one, so a
// buyer could refund their own completed purchase. The calling domain
// decides who may refund what; payments verifies that the caller owns the
// intent and that the amount is within cap.
type RefundRequest struct {
	IntentID    uuid.UUID
	AmountMinor int64
	Reason      string
	// ProviderIdempotencyKey is deterministic and supplied by the caller,
	// derived from the business event (order + cancellation/return). The
	// same value is sent to the provider on every attempt, so an ambiguous
	// timeout followed by a retry yields ONE refund at the PSP (A6).
	ProviderIdempotencyKey string
	// CallerDomain is the verified service identity from the token. It is
	// matched against the intent's owner_domain, so food-service cannot
	// refund a commerce order even with a valid token of its own.
	CallerDomain string
}

// RequestRefund persists a refund command and returns immediately.
//
// The provider is NOT called here. That is the whole point: the command is
// durable before any network I/O, so a crash, a provider outage or a pod
// eviction leaves a row that the retry worker will finish. The response
// tells the caller a refund is now owed — never that money has moved.
func (s *Service) RequestRefund(ctx context.Context, req RefundRequest) (*postgres.RefundCommand, error) {
	if req.ProviderIdempotencyKey == "" {
		return nil, fmt.Errorf("payments: provider idempotency key is required for a refund")
	}
	if req.AmountMinor <= 0 {
		return nil, fmt.Errorf("payments: refund amount must be positive")
	}
	cmd, created, err := s.store.CreateRefundCommand(
		ctx, req.IntentID, req.AmountMinor, req.Reason,
		req.ProviderIdempotencyKey, req.CallerDomain, req.CallerDomain)
	if err != nil {
		return nil, err
	}
	if created {
		slog.Info("payments: refund command accepted",
			"intent_id", req.IntentID, "command_id", cmd.ID,
			"amount_minor", req.AmountMinor, "caller", req.CallerDomain)
	}
	return cmd, nil
}

// RunRefundWorker drains due refund commands until ctx is cancelled.
//
// Every attempt sends the SAME provider idempotency key, so the provider
// collapses retries into one refund. An attempt that fails leaves the
// command claimable; an attempt that fails permanently parks it in
// `needs_attention`, which is an alarm, not a shrug.
func (s *Service) RunRefundWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	slog.Info("payments: refund worker started", "interval", interval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.drainRefundCommands(ctx)
		}
	}
}

func (s *Service) drainRefundCommands(ctx context.Context) {
	cmds, err := s.store.ClaimDueRefundCommands(ctx, 25)
	if err != nil {
		slog.Warn("payments: claim refund commands failed", "error", err)
		return
	}
	for _, c := range cmds {
		s.attemptRefund(ctx, c)
	}
	// Every tick, including one with nothing due: a command resolved by an
	// operator on another replica must lower this replica's gauge too.
	s.refreshRefundAttentionGauge(ctx)
}

func (s *Service) attemptRefund(ctx context.Context, c postgres.RefundCommand) {
	intent, err := s.store.GetIntent(ctx, c.IntentID)
	if err != nil || intent == nil {
		s.parkRefund(ctx, c, RefundFailIntentNotFound, "intent not found")
		return
	}
	providerOrder := intent.ProviderRef
	if providerOrder == "" {
		// Nothing was ever charged at the provider (COD or wallet). There
		// is no PSP refund to place; ops settles it out of band. Park it
		// visibly rather than retrying forever.
		s.parkRefund(ctx, c, RefundFailNoProviderReference, "intent has no provider reference; refund must be settled out of band")
		return
	}

	// A stub-gateway reference was never a provider object. It is decided
	// before anything provider-shaped is touched, so no branch below can send
	// one to a PSP.
	if gateway.IsStubOrderRef(providerOrder) {
		s.attemptStubRefund(ctx, c, intent)
		return
	}

	if s.provider == nil {
		// A real provider reference on a deployment with no provider adapter
		// (a stack moved from Razorpay back to the stub). The stub gateway
		// would "refund" it without money moving; park it instead.
		s.parkRefund(ctx, c, RefundFailNoProviderAdapter,
			"no provider adapter is configured, so a refund of a provider-captured payment cannot be placed")
		return
	}

	// REFUND-BY-PAYMENT. providerOrder is the provider ORDER id. It used to be
	// passed straight to POST /payments/{id}/refund, which Razorpay answers
	// with 400 on every attempt, so no refund could ever succeed. A refund is
	// placed against the captured PAYMENT.
	paymentID, err := s.refundPaymentID(ctx, intent, providerOrder)
	if err != nil {
		s.refundAttemptFailed(ctx, c, "resolving the captured payment to refund", err)
		return
	}

	// A6: provider-native idempotency. Razorpay honours X-Refund-Idempotency
	// on refund creation, so a retry after an ambiguous timeout returns the
	// ORIGINAL refund instead of making a second one. The key is the
	// command's own, unchanged across attempts.
	res, err := s.provider.Refund(ctx, paymentID,
		gateway.Money{Minor: c.AmountMinor, Currency: c.Currency}, c.ProviderIdempotencyKey)
	if err != nil {
		if gateway.ClassifyRefundError(err) == gateway.RefundAlreadyRefunded {
			s.settleAlreadyRefunded(ctx, c, intent, providerOrder, paymentID)
			return
		}
		s.refundAttemptFailed(ctx, c, "placing the refund", err)
		return
	}
	if err := s.store.MarkRefundSubmitted(ctx, c.ID, res.ProviderRefundID); err != nil {
		slog.Warn("payments: could not mark refund submitted", "command_id", c.ID, "error", err)
		return
	}
	// Settled later by the provider's refund webhook, which credits the ledger
	// and publishes payment.refunded once.
	slog.Info("payments: refund submitted to provider",
		"command_id", c.ID, "provider_payment_id", paymentID,
		"provider_refund_id", res.ProviderRefundID, "amount_minor", c.AmountMinor)
}

// errRefundUnresolvable marks a refund that cannot be placed as the data
// stands: no captured payment matches the intent, more than one does, or the
// intent already holds a different payment. Retrying changes none of that.
var errRefundUnresolvable = errors.New("refund cannot be placed")

// refundPaymentID returns the captured provider PAYMENT id to refund.
//
// The webhook stores it on the intent when a capture settles it
// (ApplyWebhookAtomically writes provider_payment_id), and so does the
// reconciler through the same transaction. An intent without one — settled
// before that column was written — is resolved from the provider: the order's
// payments are listed and the ONE captured (or already refunded) payment whose
// money verifies against the intent is chosen, under the same
// VerifyProviderMoney policy every money path uses. It is persisted, so the
// next attempt skips the lookup.
func (s *Service) refundPaymentID(ctx context.Context, intent *postgres.PaymentIntent, providerOrder string) (string, error) {
	stored, err := s.store.IntentProviderPaymentID(ctx, intent.ID)
	if err != nil {
		return "", err
	}
	if stored != "" {
		// Not re-checked here: an id that is really an order id is refused by
		// the adapter before any request is made (gateway.ErrNotAPaymentID).
		return stored, nil
	}

	payments, err := s.provider.FetchOrderPayments(ctx, providerOrder)
	if err != nil {
		return "", err
	}
	expected := gateway.Money{Minor: intent.AmountMinor(), Currency: intent.Currency}
	var matched []string
	for _, p := range payments {
		// A fully refunded payment is included: Razorpay then refuses the
		// refund as already complete, and that settles the command.
		if p.State != gateway.StateCaptured && p.State != gateway.StateRefunded {
			continue
		}
		if err := gateway.VerifyProviderMoney(gateway.MoneyCheck{
			Operation:      "refund payment lookup for intent " + intent.ID.String(),
			IdentifierKind: "provider payment id",
			Identifier:     p.ProviderPaymentID,
			Provider:       p.Amount,
			Expected:       expected,
		}); err != nil {
			slog.Warn("payments: a captured payment on the order does not verify against the intent; not refunding it",
				"intent_id", intent.ID, "provider_payment_id", p.ProviderPaymentID, "error", err)
			continue
		}
		matched = append(matched, p.ProviderPaymentID)
	}
	switch len(matched) {
	case 0:
		return "", fmt.Errorf("%w: provider order %s has no captured payment matching the intent's %d %s (%d payment(s) listed)",
			errRefundUnresolvable, providerOrder, expected.Minor, expected.Currency, len(payments))
	case 1:
	default:
		return "", fmt.Errorf("%w: %w: provider order %s has %d captured payments matching the intent (%s); refusing to choose one",
			errRefundUnresolvable, errAmbiguousPayment, providerOrder, len(matched), strings.Join(matched, ", "))
	}

	paymentID := matched[0]
	switch err := s.store.AttachProviderPaymentID(ctx, intent.ID, paymentID); {
	case errors.Is(err, postgres.ErrProviderPaymentConflict):
		return "", fmt.Errorf("%w: %w: %v", errRefundUnresolvable, errAmbiguousPayment, err)
	case err != nil:
		// The lookup is still sound; only the shortcut for the next attempt
		// is lost.
		slog.Warn("payments: could not persist the looked-up provider payment id; the next attempt looks it up again",
			"intent_id", intent.ID, "provider_payment_id", paymentID, "error", err)
	default:
		slog.Info("payments: resolved the captured payment to refund from the provider's order",
			"intent_id", intent.ID, "provider_order_id", providerOrder, "provider_payment_id", paymentID)
	}
	return paymentID, nil
}

// settleAlreadyRefunded handles the provider refusing a refund because the
// payment is already fully refunded — which is the outcome the command asked
// for, typically a refund whose response was lost.
//
// It goes through the existing idempotent success path rather than beside it:
// the refund that did it is found at the provider (a server-initiated provider
// fetch), bound to the command, and applied through ApplyWebhook — the same
// atomic inbox + ledger + outbox transaction a refund.processed webhook takes.
// provider_refunds_applied dedupes on the refund id, so the ledger is credited
// and payment.refunded published at most once, whether the real webhook came
// before this, comes after it, or never comes.
func (s *Service) settleAlreadyRefunded(ctx context.Context, c postgres.RefundCommand, intent *postgres.PaymentIntent, providerOrder, paymentID string) {
	const step = "settling a refund the provider reports as already complete"
	lister, ok := s.provider.(gateway.RefundLister)
	if !ok {
		s.parkRefund(ctx, c, RefundFailAlreadyRefundedUnmatched, fmt.Sprintf(
			"provider %s reports payment %s fully refunded but cannot list its refunds to settle this command",
			s.provider.Name(), paymentID))
		return
	}
	refunds, err := lister.FetchPaymentRefunds(ctx, paymentID)
	if err != nil {
		s.refundAttemptFailed(ctx, c, step, err)
		return
	}
	bound, err := s.store.ProviderRefundIDsOnOtherCommands(ctx, c.IntentID, c.ID)
	if err != nil {
		s.refundAttemptFailed(ctx, c, step, err)
		return
	}
	var matched []gateway.ProviderRefund
	for _, r := range refunds {
		currency := strings.TrimSpace(r.Amount.Currency)
		if r.State != gateway.StateRefunded || r.ProviderRefundID == "" || bound[r.ProviderRefundID] ||
			r.Amount.Minor != c.AmountMinor || currency == "" ||
			!strings.EqualFold(currency, strings.TrimSpace(intent.Currency)) {
			continue
		}
		matched = append(matched, r)
	}
	if len(matched) != 1 {
		s.parkRefund(ctx, c, RefundFailAlreadyRefundedUnmatched, fmt.Sprintf(
			"provider reports payment %s fully refunded, but %d of its %d refund(s) are processed, unclaimed and match this command's %d %s; settle it by hand",
			paymentID, len(matched), len(refunds), c.AmountMinor, intent.Currency))
		return
	}
	r := matched[0]

	if err := s.store.MarkRefundSubmitted(ctx, c.ID, r.ProviderRefundID); err != nil {
		s.refundAttemptFailed(ctx, c, step, err)
		return
	}
	// The provider name comes off the ROW, as the refund webhook's
	// attribution matches on it.
	provider, _, err := s.store.IntentProviderAndOrder(ctx, intent.ID)
	if err != nil {
		slog.Warn("payments: could not resolve the intent's provider; the command stays submitted and is retried",
			"command_id", c.ID, "error", err)
		return
	}
	err = s.ApplyWebhook(ctx, WebhookInput{
		Provider:          provider,
		EventID:           "refund_settle_" + r.ProviderRefundID,
		EventType:         "refund.processed",
		ProviderOrderID:   providerOrder,
		ProviderPaymentID: paymentID,
		ProviderRefundID:  r.ProviderRefundID,
		AmountMinor:       r.Amount.Minor,
		Currency:          r.Amount.Currency,
	})
	if err != nil && !errors.Is(err, ErrWebhookDuplicate) {
		slog.Error("payments: settling an already-refunded payment failed; the command stays submitted and is retried",
			"command_id", c.ID, "provider_refund_id", r.ProviderRefundID, "error", err)
		return
	}
	slog.Info("payments: provider reported the payment already fully refunded; command settled by that refund",
		"command_id", c.ID, "provider_payment_id", paymentID, "provider_refund_id", r.ProviderRefundID)
}

// refundAttemptFailed records a failed attempt: retried with backoff when the
// provider may yet accept it, parked when it never will.
func (s *Service) refundAttemptFailed(ctx context.Context, c postgres.RefundCommand, step string, err error) {
	reason := step + ": " + gateway.RedactError(err)
	if errors.Is(err, errRefundUnresolvable) || gateway.ClassifyRefundError(err) != gateway.RefundRetryable {
		s.parkRefund(ctx, c, refundFailureCode(err), reason)
		return
	}
	// Never terminal on a transport error, a 5xx or a 429: an unreachable
	// provider is exactly the case the durable command exists for.
	slog.Warn("payments: refund attempt failed; will retry",
		"command_id", c.ID, "attempt", c.Attempts, "error", reason)
	if merr := s.store.MarkRefundAttemptFailed(ctx, c.ID, reason); merr != nil {
		slog.Warn("payments: could not record the failed refund attempt", "command_id", c.ID, "error", merr)
	}
}

// parkRefund moves a command to `needs_attention` and, in the same
// transaction, publishes payment.refund_failed — never payment.refunded, which
// would claim money moved that did not. It is no longer claimed, so it is
// logged at ERROR and counted exactly once, here, and only by the call that
// actually parked it. The alarm is payments_refunds_needs_attention.
func (s *Service) parkRefund(ctx context.Context, c postgres.RefundCommand, code, reason string) {
	parked, err := s.store.ParkRefundCommand(ctx, c.ID, code, reason)
	if err != nil {
		slog.Warn("payments: could not park the refund command; it will be attempted again",
			"command_id", c.ID, "error", err)
		return
	}
	if !parked {
		// Settled, resolved or parked by someone else since it was claimed.
		return
	}
	obs.RefundParked(code)
	slog.Error("payments: REFUND NEEDS ATTENTION — parked and not retried; the money is still owed",
		"command_id", c.ID, "intent_id", c.IntentID, "amount_minor", c.AmountMinor,
		"attempt", c.Attempts, "reason_code", code, "reason", reason)
}

// attemptStubRefund refunds an intent the stub gateway minted.
//
// The stub's refund is synchronous and final (StubGateway.InitiateRefund
// returns "processed" and makes no network call), so on a stub deployment it
// is placed and settled here, exactly as before.
//
// On any other deployment — a real provider adapter, or no stub settlement —
// the intent is PARKED, never sent. No provider has ever heard of an
// `order_stub_…` reference and no money was ever captured for it, so there is
// nothing a PSP could refund; and settling it as refunded would publish
// payment.refunded for a refund that did not happen on this deployment's
// provider. Parking leaves that decision, visibly, to an operator.
func (s *Service) attemptStubRefund(ctx context.Context, c postgres.RefundCommand, intent *postgres.PaymentIntent) {
	stub, isStub := s.gateway.(*gateway.StubGateway)
	if !isStub || !s.stubSettlement {
		s.parkRefund(ctx, c, RefundFailStubOnRealProvider,
			"intent was paid through the stub gateway (order_stub_ reference), so no provider holds this payment "+
				"and nothing may be sent to one; settle it out of band")
		return
	}
	res, err := stub.InitiateRefund(ctx, intent.ProviderRef, c.AmountMinor)
	if err != nil {
		s.refundAttemptFailed(ctx, c, "placing the stub refund", err)
		return
	}
	if err := s.store.MarkRefundSubmitted(ctx, c.ID, res.ID); err != nil {
		slog.Warn("payments: could not mark refund submitted", "command_id", c.ID, "error", err)
		return
	}
	slog.Info("payments: refund submitted to provider",
		"command_id", c.ID, "provider_refund_id", res.ID, "amount_minor", c.AmountMinor)

	// Stub only: settle the refund the provider has already finished.
	//
	// A submitted refund stays `submitted` until a `payment.refunded`
	// webhook credits the ledger. The stub has no webhook, so on a dev stack
	// every refund stopped one step short: the command was durable and the
	// provider refund id was recorded, but the intent's refunded balance
	// stayed 0 and the order never left `refund_pending`. Testing a refund
	// end to end was impossible for the same reason testing a payment was.
	//
	// The stub's refund is synchronous and final (StubGateway returns
	// "processed"), so there is nothing left to wait for. It is applied
	// through applyRefundWebhook — the same atomic inbox + ledger + outbox
	// transaction a real refund event takes — keyed on the provider refund
	// id, so it is deduped exactly like a redelivery would be.
	//
	// Guarded by the same boot-configured flag as payment settlement, so a
	// real provider is untouched: there, the webhook credits the refund.
	if s.stubSettlement {
		// The provider name comes off the ROW, not from the running
		// gateway: intents are stamped `provider='razorpay'` by default even
		// on a stub deployment, and this lookup matches on it.
		provider, providerOrderID, perr := s.store.IntentProviderAndOrder(ctx, intent.ID)
		if perr != nil {
			slog.Error("payments: stub refund settlement could not resolve the intent's provider",
				"command_id", c.ID, "intent_id", intent.ID, "error", perr)
			return
		}
		if err := s.ApplyWebhook(ctx, WebhookInput{
			Provider:         provider,
			EventID:          "stub_refund_" + res.ID,
			EventType:        "refund.processed",
			ProviderOrderID:  providerOrderID,
			ProviderRefundID: res.ID,
			AmountMinor:      c.AmountMinor,
			Currency:         intent.Currency,
		}); err != nil && !errors.Is(err, ErrWebhookDuplicate) {
			slog.Error("payments: stub refund settlement failed; the command stays submitted",
				"command_id", c.ID, "provider_refund_id", res.ID, "error", err)
		}
	}
}

// ─── Reconciliation ──────────────────────────────────────────────────

// RunReconciler resolves intents that have been pending too long by asking
// the provider what actually happened.
//
// LB-9. Without this, a webhook that never arrives leaves a captured payment
// invisible to us indefinitely. It is also the only server-side path other
// than the webhook that may create terminal state, which is why it goes
// through the same atomic apply.
func (s *Service) RunReconciler(ctx context.Context, interval, pendingAge time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	if pendingAge <= 0 {
		pendingAge = 10 * time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	slog.Info("payments: reconciler started", "interval", interval, "pending_age", pendingAge,
		"failed_attempt_window", s.failedAttemptWindowOrDefault())
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.reconcileOnce(ctx, pendingAge)
		}
	}
}

// reconcileOnce repairs stale pending intents from provider truth.
//
// MRC-1 — this loop used the LEGACY `PaymentGateway` adapter, whose
// `FetchPayment` response struct has no `currency` field at all
// (internal/gateway/razorpay.go). So `p.Currency` was always "", and
// `ApplyWebhookAtomically` treats an empty event currency as "nothing to
// compare". The previous pass reported the currency check closed because its
// test used a hand-built `GatewayPayment` carrying `Currency: "INR"` — a fake
// supplying the exact field the real adapter drops. Production could mark an
// INR intent succeeded on a settlement of the same numeric amount in another
// currency.
//
// It now reads through the provider-neutral `gateway.Provider` port, which
// decodes currency, and which is the same port the ambiguous-timeout recovery
// uses. One adapter contract, one tuple, one place to get it wrong.
//
// MRC-2 — a stale intent with a BLANK provider reference is no longer
// skipped. That state is produced by an ambiguous CreateOrder (the response
// was lost), and the previous pass claimed the reconciler owned it while the
// loop's first statement was `if intent.ProviderRef == "" { continue }`. It is
// now repaired by looking the order up under the intent's own deterministic
// idempotency key.
//
// ORDER-PAYMENTS — the reference an intent holds is the provider ORDER id
// (`order_…`), and this loop used to hand it to FetchPayment, which takes a
// PAYMENT id. Razorpay answers GET /v1/payments/order_… with 400, so every tick
// logged an error and a real payment whose webhook was lost was never
// reconciled. It now lists the order's payments (FetchOrderPayments), chooses
// the outcome the webhook would have produced (reconcileOutcome), and applies
// it through the webhook's own atomic transaction.
//
// Stub-gateway references (`order_stub_…`) were never provider objects, and
// StalePending excludes them in SQL: they used to be selected and skipped
// here, so a pile of dev leftovers filled the 50-row window and pushed real
// stale intents out of it. A guard below still refuses to send one to the
// provider. A failing lookup backs off per intent instead of being
// re-requested and re-logged every tick.
//
// FAILED-ATTEMPT WINDOW — an order whose attempts all failed, or that has none,
// is a candidate for FAILED, not a verdict: the customer may retry on the same
// provider order. It is finalised only once the order has been quiet for the
// window, measured from the later of the intent's creation and the latest
// failed attempt recorded by the webhook. Until then it stays pending,
// silently. A reference this tick had to repair is never failed on the same
// tick.
func (s *Service) reconcileOnce(ctx context.Context, pendingAge time.Duration) {
	if s.provider == nil {
		// MRC-2.6: without the provider port there is no lookup-by-key and
		// therefore no repair path. Say so rather than running a loop that
		// silently cannot do its job.
		slog.Error("payments: reconciler has no provider port; stale intents cannot be repaired")
		return
	}
	stale, err := s.store.StalePending(ctx, pendingAge, 50)
	if err != nil {
		slog.Warn("payments: reconciler query failed", "error", err)
		return
	}
	s.recon.forgetAllBut(stale)
	now := time.Now()
	window := s.failedAttemptWindowOrDefault()
	for _, intent := range stale {
		if !s.recon.due(intent.ID, now) {
			continue // backing off after an earlier failure
		}

		providerRef := intent.ProviderRef
		if providerRef == "" {
			// MRC-2: repair the missing reference before anything else.
			recovered, err := s.repairMissingProviderRef(ctx, intent)
			if err != nil {
				s.recon.backOff(intent.ID, now, "payments: could not repair a blank provider reference", err)
				continue
			}
			if recovered == "" {
				// Nothing exists at the provider under this key yet. The
				// intent stays pending and is retried next tick.
				continue
			}
			providerRef = recovered
		}

		if gateway.IsStubOrderRef(providerRef) {
			// Defensive only: StalePending excludes stub references, so this is
			// reachable only if that query regresses. The stub gateway minted
			// this id locally and no provider has ever heard of it; it must
			// never be sent to one.
			slog.Error("payments: reconciler was handed a stub-gateway order reference; not sending it to the provider",
				"intent_id", intent.ID)
			continue
		}

		payments, err := s.provider.FetchOrderPayments(ctx, providerRef)
		if err != nil {
			s.recon.backOff(intent.ID, now, "payments: reconcile could not list the order's payments", err,
				"provider_order_id", providerRef)
			continue
		}
		s.recon.clearFailures(intent.ID)

		p, newStatus := reconcileOutcome(intent, providerRef, payments)
		if newStatus == "" {
			// A payment still in flight or authorized, or a capture that does
			// not verify (alarmed inside reconcileOutcome). Not a failure.
			slog.Debug("payments: stale intent has no terminal provider outcome yet",
				"intent_id", intent.ID, "provider_order_id", providerRef, "payments", len(payments))
			continue
		}

		eventID := "reconcile:" + p.ProviderPaymentID
		eventType := "reconcile." + string(p.State)
		if newStatus == "failed" {
			if intent.ProviderRef == "" {
				// The reference was repaired on this tick; nobody can have been
				// told about it long enough ago to have finished retrying.
				continue
			}
			elapsed, err := s.store.FailedAttemptWindowElapsed(ctx, intent.ID, providerRef, window)
			if err != nil {
				s.recon.backOff(intent.ID, now, "payments: reconcile could not check the failed-attempt window", err,
					"provider_order_id", providerRef)
				continue
			}
			if !elapsed {
				// Every attempt failed (or there is none) but the customer may
				// still retry on this order. Pending, and nothing is published.
				slog.Debug("payments: stale intent has only failed attempts but is inside the retry window",
					"intent_id", intent.ID, "provider_order_id", providerRef, "payments", len(payments),
					"window", window)
				continue
			}
			// One payment.failed per INTENT, whichever attempts the list holds.
			eventID = "reconcile:failed:" + intent.ID.String()
			eventType = "reconcile.failed"
		}

		// Exactly once, by the webhook's own machinery. ApplyWebhookAtomically
		// takes the intent row FOR UPDATE, and writes the status change and the
		// payment.succeeded / payment.failed outbox row in one transaction only
		// when the state machine allows pending → terminal. A later real
		// webhook (its own event id) or a concurrent tick therefore finds a
		// terminal intent and publishes nothing; the synthesised inbox key
		// additionally collapses two reconciler passes over the same payment
		// into ErrDuplicateEvent.
		_, err = s.store.ApplyWebhookAtomically(ctx, postgres.WebhookEffect{
			Provider:          s.provider.Name(),
			EventID:           eventID,
			EventType:         eventType,
			ProviderOrderID:   providerRef,
			ProviderPaymentID: p.ProviderPaymentID,
			NewStatus:         newStatus,
			AmountMinor:       p.Amount.Minor,
			Currency:          p.Amount.Currency,
		})
		switch {
		case errors.Is(err, postgres.ErrDuplicateEvent):
			continue
		case err != nil:
			s.recon.backOff(intent.ID, now, "payments: reconcile apply failed", err,
				"provider_order_id", providerRef)
			continue
		}
		slog.Info("payments: reconciled a stale intent",
			"intent_id", intent.ID, "status", newStatus, "provider_payment_id", p.ProviderPaymentID)
	}
}

// reconcileOutcome chooses what the webhook would have done with an order's
// payments (oldest first). An empty status leaves the intent pending.
//
// It mirrors ApplyWebhook's event mapping:
//
//	payment.captured   → succeeded, and only on a capture whose full money
//	                     tuple verifies against the intent — the webhook runs
//	                     the same VerifyProviderMoney check inside its
//	                     transaction and refuses a mismatch
//	payment.authorized → no change; authorized is not captured
//	payment.failed     → no change on its own
//
// "failed" is returned when EVERY attempt on the order failed, or there is no
// attempt at all. That is a CANDIDATE: failed → succeeded is not a transition
// the state machine allows, so the caller applies it only once the order has
// been quiet for the failed-attempt window. An attempt still in flight or
// authorized may yet capture, so any such attempt keeps the intent pending.
func reconcileOutcome(intent postgres.PaymentIntent, providerOrderID string, payments []gateway.ProviderPaymentState) (gateway.ProviderPaymentState, string) {
	var (
		captured   []gateway.ProviderPaymentState
		lastFailed gateway.ProviderPaymentState
		unsettled  int
	)
	for _, p := range payments {
		switch p.State {
		case gateway.StateCaptured:
			captured = append(captured, p)
		case gateway.StateFailed:
			lastFailed = p
		default:
			unsettled++ // authorized, created/pending, refunded, unknown
		}
	}

	if len(captured) > 1 {
		ids := make([]string, 0, len(captured))
		for _, p := range captured {
			ids = append(ids, p.ProviderPaymentID)
		}
		slog.Error("payments: MORE THAN ONE CAPTURED PAYMENT ON ONE ORDER — the extra capture needs a refund",
			"intent_id", intent.ID, "provider_order_id", providerOrderID, "captured_payment_ids", ids)
	}
	for _, p := range captured {
		// MRC-1.2/1.3: the FULL tuple, or nothing. A capture we cannot
		// verify is not a capture we may act on, and every one of these is a
		// refusal rather than a defaulted value.
		if err := verifyProviderTuple(intent, p); err != nil {
			slog.Error("payments: RECONCILIATION REFUSED — provider tuple does not verify",
				"intent_id", intent.ID,
				"intent_minor", intent.AmountMinor(), "intent_currency", intent.Currency,
				"provider_minor", p.Amount.Minor, "provider_currency", p.Amount.Currency,
				"provider_payment_id", p.ProviderPaymentID,
				"error", err)
			continue
		}
		return p, "succeeded"
	}
	if len(captured) > 0 {
		// Money was captured but none of it verifies. That is an alarm, never
		// a reason to mark the intent failed.
		return gateway.ProviderPaymentState{}, ""
	}
	if unsettled == 0 {
		// Every attempt failed, or none was made (lastFailed is then the zero
		// value).
		return lastFailed, "failed"
	}
	return gateway.ProviderPaymentState{}, ""
}

// WithFailedAttemptWindow sets how long an order with only failed attempts, or
// none, stays pending for a retry before the reconciler finalises it FAILED.
// main.go passes config.Resolve's validated PAYMENTS_FAILED_ATTEMPT_WINDOW.
func (s *Service) WithFailedAttemptWindow(d time.Duration) *Service {
	s.failedAttemptWindow = d
	return s
}

func (s *Service) failedAttemptWindowOrDefault() time.Duration {
	if s.failedAttemptWindow <= 0 {
		return config.DefaultFailedAttemptWindow
	}
	return s.failedAttemptWindow
}

// ─── Reconciler memory between ticks ─────────────────────────────────

const (
	reconcileBackoffBase = time.Minute
	reconcileBackoffMax  = time.Hour
)

// reconcileTracker is what the reconciler remembers between ticks, in memory:
// which intents are backing off after a failure. Losing it on restart costs
// one early retry per intent; the money state lives entirely in the database.
// The zero value is ready to use.
type reconcileTracker struct {
	mu       sync.Mutex
	failures map[uuid.UUID]reconcileFailure
}

type reconcileFailure struct {
	attempts int
	next     time.Time
}

// due reports whether an intent's backoff, if any, has elapsed.
func (r *reconcileTracker) due(id uuid.UUID, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, backingOff := r.failures[id]
	return !backingOff || !now.Before(f.next)
}

// backOff records a failure and logs it once. The retry delay doubles per
// consecutive failure — 1m, 2m, 4m … capped at 1h — and the warning fires only
// when an attempt is actually made, so a provider outage yields a decaying
// trickle of warnings rather than one per intent per tick.
func (r *reconcileTracker) backOff(id uuid.UUID, now time.Time, msg string, err error, attrs ...any) {
	r.mu.Lock()
	if r.failures == nil {
		r.failures = map[uuid.UUID]reconcileFailure{}
	}
	f := r.failures[id]
	f.attempts++
	shift := f.attempts - 1
	if shift > 6 {
		shift = 6
	}
	delay := reconcileBackoffBase << shift
	if delay > reconcileBackoffMax {
		delay = reconcileBackoffMax
	}
	f.next = now.Add(delay)
	r.failures[id] = f
	r.mu.Unlock()

	slog.Warn(msg, append([]any{
		"intent_id", id, "attempt", f.attempts, "retry_in", delay, "error", err,
	}, attrs...)...)
}

func (r *reconcileTracker) clearFailures(id uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.failures, id)
}

// forgetAllBut drops backoff state for intents that are no longer stale, so
// the map is bounded by the reconciler's window rather than by history.
func (r *reconcileTracker) forgetAllBut(stale []postgres.PaymentIntent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.failures) == 0 {
		return
	}
	keep := make(map[uuid.UUID]struct{}, len(stale))
	for _, in := range stale {
		keep[in.ID] = struct{}{}
	}
	for id := range r.failures {
		if _, ok := keep[id]; !ok {
			delete(r.failures, id)
		}
	}
}

// verifyProviderTuple adapts a reconciliation tuple onto the shared policy.
//
// C3-LB-1: the rule itself now lives in gateway.VerifyProviderMoney and is
// called by all four money paths. It used to be written out here, which is
// how the immediate-recovery path came to have no copy of it at all.
func verifyProviderTuple(intent postgres.PaymentIntent, p gateway.ProviderPaymentState) error {
	return gateway.VerifyProviderMoney(gateway.MoneyCheck{
		Operation:      "stale-intent reconciliation of intent " + intent.ID.String(),
		IdentifierKind: "provider payment id",
		Identifier:     p.ProviderPaymentID,
		Provider:       p.Amount,
		Expected:       gateway.Money{Minor: intent.AmountMinor(), Currency: intent.Currency},
	})
}

// repairMissingProviderRef recovers the provider order for an intent whose
// CreateOrder response was lost (MRC-2).
//
// Returns the attached reference, or "" when nothing exists at the provider
// yet — which is a legitimate "try again next tick", not a failure.
//
// The order of operations matters. The provider is asked FIRST, its tuple is
// verified against the intent, and only then is the reference attached
// through the conflict-aware SetProviderOrder. A recovered order whose amount
// or currency disagrees with the intent is never attached: attaching it would
// point our intent at someone else's money.
func (s *Service) repairMissingProviderRef(ctx context.Context, intent postgres.PaymentIntent) (string, error) {
	if intent.IdempotencyKey == "" {
		// Without the deterministic key there is nothing to look up by. This
		// cannot happen for intents created after B6 made the key mandatory;
		// a legacy row is reported rather than guessed at.
		return "", fmt.Errorf("intent %s has no idempotency key to recover by", intent.ID)
	}

	state, err := s.provider.FetchByIdempotencyKey(ctx, intent.IdempotencyKey)
	switch {
	case errors.Is(err, gateway.ErrLookupNotSupported):
		// MRC-2.6: no lookup, no repair. Do not pretend otherwise.
		return "", fmt.Errorf("provider %s cannot look up by idempotency key: %w",
			s.provider.Name(), err)
	case errors.Is(err, gateway.ErrAmbiguousLookup):
		// More than one provider object under one deterministic key. There
		// is no correct choice, so this alarms instead of picking one.
		return "", fmt.Errorf("ambiguous provider lookup for intent %s: %w", intent.ID, err)
	case err != nil:
		return "", err
	}

	if state.ProviderOrderID == "" {
		// The provider holds nothing under this key. The CreateOrder call
		// never landed, so there is no orphan to adopt.
		return "", nil
	}

	// MRC-2.2 / C3-LB-1: the recovered object must be OUR order, checked by
	// the one shared policy rather than a local copy of it.
	if err := gateway.VerifyProviderMoney(gateway.MoneyCheck{
		Operation:      "blank-provider-reference repair of intent " + intent.ID.String(),
		IdentifierKind: "recovered provider order id",
		Identifier:     state.ProviderOrderID,
		Provider:       state.Amount,
		Expected:       gateway.Money{Minor: intent.AmountMinor(), Currency: intent.Currency},
	}); err != nil {
		return "", err
	}

	// MRC-2.3/2.5: conflict-aware attach. A concurrent repair that already
	// attached the SAME reference returns nil (converged); a different one
	// returns ErrProviderOrderConflict and is surfaced. Either way the stored
	// reference is never overwritten, so this is restart-safe: a process that
	// dies between the lookup and the attach re-runs both and converges.
	if err := s.store.SetProviderOrder(ctx, intent.ID, state.ProviderOrderID); err != nil {
		return "", fmt.Errorf("attaching recovered provider order %s to intent %s: %w",
			state.ProviderOrderID, intent.ID, err)
	}
	slog.Info("payments: repaired a blank provider reference from the provider's own record",
		"intent_id", intent.ID, "provider_order_id", state.ProviderOrderID)
	return state.ProviderOrderID, nil
}

// OldestUnsettledRefund exposes the refund-pending-age gauge.
func (s *Service) OldestUnsettledRefund(ctx context.Context) (time.Duration, error) {
	return s.store.UnsettledRefundAge(ctx)
}

// ─── Domain ownership (D4) ───────────────────────────────────────────

// StampOwnerDomain records which calling service owns a new intent.
//
// payments is shared with food-service, so a bare intent UUID cannot carry
// authority: without this, knowing an id would be enough to read or refund
// it from the wrong domain.
func (s *Service) StampOwnerDomain(ctx context.Context, id uuid.UUID, domain string) error {
	if domain == "" {
		return nil
	}
	return s.store.SetOwnerDomain(ctx, id, domain)
}

// IntentOwnerDomain reports the owning service, for the handler's
// authorization check.
func (s *Service) IntentOwnerDomain(ctx context.Context, id uuid.UUID) (string, error) {
	return s.store.IntentOwnerDomain(ctx, id)
}
