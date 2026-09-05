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
	"time"

	"github.com/atpost/payments-service/internal/gateway"
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
	case "payment.failed":
		newStatus = "failed"
	}

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
}

func (s *Service) attemptRefund(ctx context.Context, c postgres.RefundCommand) {
	if s.gateway == nil {
		_ = s.store.MarkRefundAttemptFailed(ctx, c.ID, "gateway not configured", true)
		return
	}
	intent, err := s.store.GetIntent(ctx, c.IntentID)
	if err != nil || intent == nil {
		_ = s.store.MarkRefundAttemptFailed(ctx, c.ID, "intent not found", true)
		return
	}
	providerOrder := intent.ProviderRef
	if providerOrder == "" {
		// Nothing was ever charged at the provider (COD or wallet). There
		// is no PSP refund to place; ops settles it out of band. Park it
		// visibly rather than retrying forever.
		_ = s.store.MarkRefundAttemptFailed(ctx, c.ID,
			"intent has no provider reference; refund must be settled out of band", true)
		return
	}

	idem, ok := s.gateway.(gateway.IdempotentRefunder)
	var (
		res     gateway.GatewayRefund
		callErr error
	)
	if ok {
		// A6: provider-native idempotency. Razorpay honours
		// X-Refund-Idempotency on refund creation, so a retry after an
		// ambiguous timeout returns the ORIGINAL refund instead of making
		// a second one.
		res, callErr = idem.InitiateRefundIdempotent(ctx, providerOrder, c.AmountMinor, c.ProviderIdempotencyKey)
	} else {
		res, callErr = s.gateway.InitiateRefund(ctx, providerOrder, c.AmountMinor)
	}
	if callErr != nil {
		slog.Warn("payments: refund attempt failed; will retry",
			"command_id", c.ID, "attempt", c.Attempts, "error", callErr)
		// Never terminal on a transport error: an unreachable provider is
		// exactly the case the durable command exists for.
		_ = s.store.MarkRefundAttemptFailed(ctx, c.ID, callErr.Error(), false)
		return
	}
	if err := s.store.MarkRefundSubmitted(ctx, c.ID, res.ID); err != nil {
		slog.Warn("payments: could not mark refund submitted", "command_id", c.ID, "error", err)
		return
	}
	slog.Info("payments: refund submitted to provider",
		"command_id", c.ID, "provider_refund_id", res.ID, "amount_minor", c.AmountMinor)
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
	slog.Info("payments: reconciler started", "interval", interval, "pending_age", pendingAge)
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
	for _, intent := range stale {
		providerRef := intent.ProviderRef
		if providerRef == "" {
			// MRC-2: repair the missing reference before anything else.
			recovered, err := s.repairMissingProviderRef(ctx, intent)
			if err != nil {
				slog.Warn("payments: could not repair a blank provider reference",
					"intent_id", intent.ID, "error", err)
				continue
			}
			if recovered == "" {
				// Nothing exists at the provider under this key yet. The
				// intent stays pending and is retried next tick.
				continue
			}
			providerRef = recovered
		}

		p, err := s.provider.FetchPayment(ctx, providerRef)
		if err != nil {
			slog.Warn("payments: reconcile fetch failed", "intent_id", intent.ID, "error", err)
			continue
		}

		newStatus := ""
		switch p.State {
		case gateway.StateCaptured:
			newStatus = "succeeded"
		case gateway.StateFailed:
			newStatus = "failed"
		default:
			continue // still genuinely in flight
		}

		if newStatus == "succeeded" {
			// MRC-1.2/1.3: the FULL tuple, or nothing. A capture we cannot
			// verify is not a capture we may act on, and every one of these
			// is a refusal rather than a defaulted value.
			if err := verifyProviderTuple(intent, p); err != nil {
				slog.Error("payments: RECONCILIATION REFUSED — provider tuple does not verify",
					"intent_id", intent.ID,
					"intent_minor", intent.AmountMinor(), "intent_currency", intent.Currency,
					"provider_minor", p.Amount.Minor, "provider_currency", p.Amount.Currency,
					"provider_payment_id", p.ProviderPaymentID,
					"error", err)
				continue
			}
		}

		// Synthesise an inbox key so the reconciler is idempotent with the
		// webhook: if the webhook later arrives for the same payment, its
		// own event id is different but the status transition is already
		// terminal and will be refused by the state machine.
		_, err = s.store.ApplyWebhookAtomically(ctx, postgres.WebhookEffect{
			Provider:          s.provider.Name(),
			EventID:           "reconcile:" + p.ProviderPaymentID,
			EventType:         "reconcile." + string(p.State),
			ProviderOrderID:   providerRef,
			ProviderPaymentID: p.ProviderPaymentID,
			NewStatus:         newStatus,
			AmountMinor:       p.Amount.Minor,
			Currency:          p.Amount.Currency,
		})
		if err != nil && !errors.Is(err, postgres.ErrDuplicateEvent) {
			slog.Warn("payments: reconcile apply failed", "intent_id", intent.ID, "error", err)
			continue
		}
		slog.Info("payments: reconciled a stale intent",
			"intent_id", intent.ID, "status", newStatus, "provider_payment_id", p.ProviderPaymentID)
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
