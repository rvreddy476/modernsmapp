package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/paymentmethod"
	"github.com/google/uuid"
)

// rupeesToPaise converts the API's rupees-major amount to the paise-minor
// units Razorpay (and other Indian payment gateways) require. `math.Round`
// pins ₹100.50 to 10050 paise rather than 10049 from float-truncation —
// reconciliation breaks otherwise. The API contract at the HTTP boundary
// is rupees-major: every site that ships an amount across that boundary
// must stay in that unit.
func rupeesToPaise(amountRupees float64) int64 { // money-exempt: this IS the rupees→paise conversion boundary; it consumes a float so nothing downstream has to
	return int64(math.Round(amountRupees * 100))
}

type Service struct {
	store   *postgres.Store
	gateway gateway.PaymentGateway
	// provider is the recoverable port (N3). main.go has always
	// constructed it — it was handed to the HTTP handler for webhook
	// verification — but the money path held only the legacy
	// PaymentGateway, which has no way to ask the provider what it already
	// created under a given key. So an ambiguous CreateOrder timeout had no
	// recovery at all, and A6's "provider-native idempotency" was true of
	// the adapter and false of the code that calls it.
	provider gateway.Provider
}

// WithProvider supplies the recoverable provider port. Optional: when it is
// absent (tests, the stub gateway) attachProviderOrder falls back to the
// legacy CreateOrder, which is idempotent at the PSP via the receipt but
// cannot recover a lost response.
func (s *Service) WithProvider(p gateway.Provider) *Service {
	s.provider = p
	return s
}

// New wires the service. Events are not written to Kafka from the request
// path: publishEvent INSERTs into payments.outbox_events (the same database
// as the intent row) and the shared/outbox.Publisher started in
// cmd/server/main.go relays them with at-least-once delivery. The legacy
// request-path kafka.Writer dropped payment.succeeded on any broker blip and
// left the order unpaid forever, so there is deliberately no broker
// parameter here — nothing in this package can reach Kafka directly.
func New(store *postgres.Store, gw gateway.PaymentGateway) *Service {
	return &Service{store: store, gateway: gw}
}

// ErrNotIntentParty is returned by the ownership-checked read paths when
// the acting user is neither the payer nor the payee of the intent. The
// gateway injects X-Internal-Service-Key on every proxied request, so the
// key alone cannot distinguish a service from a logged-in user; the
// user-facing routes therefore always check the intent's parties against
// X-User-Id, and service-only mutations live under /v1/payments/internal.
var ErrNotIntentParty = fmt.Errorf("not a party to this payment")

// IsParty reports whether actor is the payer or the payee of intent.
func IsParty(intent *postgres.PaymentIntent, actor uuid.UUID) bool {
	if intent == nil || actor == uuid.Nil {
		return false
	}
	return actor == intent.PayerID || actor == intent.PayeeID
}

// GetIntentForActor is GetIntent with the ownership check applied.
func (s *Service) GetIntentForActor(ctx context.Context, id, actor uuid.UUID) (*postgres.PaymentIntent, error) {
	intent, err := s.store.GetIntent(ctx, id)
	if err != nil {
		return nil, err
	}
	if !IsParty(intent, actor) {
		return nil, ErrNotIntentParty
	}
	return intent, nil
}

// ListByReferenceForActor lists the intents on a reference that the actor
// is a party to. Intents belonging to other users are filtered out rather
// than erroring so a buyer sees "no intents" instead of learning that a
// reference exists.
func (s *Service) ListByReferenceForActor(ctx context.Context, refType string, refID, actor uuid.UUID) ([]postgres.PaymentIntent, error) {
	all, err := s.store.ListByReference(ctx, refType, refID)
	if err != nil {
		return nil, err
	}
	out := make([]postgres.PaymentIntent, 0, len(all))
	for i := range all {
		if IsParty(&all[i], actor) {
			out = append(out, all[i])
		}
	}
	return out, nil
}

type InitiateInput struct {
	PayerID       uuid.UUID
	PayeeID       uuid.UUID
	ReferenceType string
	ReferenceID   uuid.UUID
	// Amount is the legacy rupees-major float entry point. Audit
	// P7-deep: new callers should populate AmountMinor (paise-minor
	// int64) directly. When only Amount is set, the service computes
	// AmountMinor via rupeesToPaise. When both are set, AmountMinor
	// wins (the float copy is kept on the row for the deprecated
	// `amount` column).
	Amount      float64
	AmountMinor int64
	Currency    string
	Method      string
	// IdempotencyKey is REQUIRED and caller-derived (B6). It is the only
	// thing that makes a retry of this call idempotent at the provider.
	IdempotencyKey string
	// OwnerDomain is the verified service identity of the caller (B4). It is
	// written with the intent, not stamped afterwards, and it is required:
	// an intent with no owner is refundable by any authorised service.
	OwnerDomain string
}

func (s *Service) InitiatePayment(ctx context.Context, in InitiateInput) (*postgres.PaymentIntent, error) {
	// Audit P7-deep: AmountMinor (paise-minor int64) is the new source
	// of truth. Resolve it once at the entry point — every downstream
	// reference (gateway CreateOrder, CreateHold, the row's
	// AmountMinorRaw column) uses the resolved int64. The legacy
	// rupees-major float copy is preserved on the row only because the
	// deprecated `amount` NUMERIC column still has analytics readers;
	// it is NEVER used for arithmetic downstream of this resolution.
	amountMinor := in.AmountMinor
	if amountMinor == 0 {
		if in.Amount <= 0 {
			return nil, fmt.Errorf("amount must be positive")
		}
		amountMinor = rupeesToPaise(in.Amount)
	}
	if amountMinor <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	// Keep the float mirror in sync with whichever side the caller
	// supplied so the deprecated `amount` column is never inconsistent
	// with `amount_minor`.
	amountRupees := in.Amount
	if amountRupees <= 0 {
		amountRupees = float64(amountMinor) / 100.0
	}

	// B6 / A5. `wallet`, `cod` and `escrow` are gone from the allowlist.
	//
	// They were not merely unused: `cod` and `wallet` both SKIPPED provider
	// order creation, so they produced a committed intent with a blank
	// provider reference — a row that can never be captured, refunded or
	// reconciled, reachable because the method is caller-selectable. COD is
	// already fenced at the commerce edge (A5); this closes the same hole at
	// the payments boundary, where the fence belongs.
	// C3-LB-3: the vocabulary now comes from shared/paymentmethod, which the
	// commerce handler, service, store and the gated CHECK all read too. The
	// list used to be duplicated here as a literal — correct, but privately
	// correct, so commerce could and did disagree with it.
	if err := paymentmethod.Validate(in.Method); err != nil {
		return nil, fmt.Errorf("payments: %w", err)
	}
	if in.IdempotencyKey == "" {
		// B6: a generated key is not an idempotency key — two retries of the
		// same business request would each mint one and each create a
		// provider order. The caller derives it from the business event.
		return nil, fmt.Errorf("payments: idempotency_key is required")
	}
	// B4: the owning service identity is not optional. An intent with no
	// owner is one that every authorised caller may read and refund, because
	// the ownership checks treat empty as unrestricted.
	if in.OwnerDomain == "" {
		return nil, fmt.Errorf("payments: owner domain is required to create an intent")
	}

	// ── B6. The DB row is created BEFORE the PSP is contacted ──────────
	//
	// The previous order was: call Razorpay, then INSERT. Two consequences,
	// both live:
	//
	//   - a CreateOrder timeout could leave an order at the PSP that no local
	//     row references, and the retry created a SECOND provider order for
	//     the same business key — duplicate provider liabilities;
	//   - a CreateOrder failure was logged and execution CONTINUED, so the
	//     INSERT committed an intent with an empty provider_ref: a payable
	//     row with nothing to pay against, and no path back.
	//
	// Now the local row is the anchor. It is idempotent on the business key,
	// so a retry finds the same row; the provider order is attached to it
	// afterwards; and a provider failure returns an error while leaving a
	// `pending` intent that the reconciler can resolve or expire. Nothing is
	// ever committed claiming a provider reference it does not have.
	res, err := s.store.CreateIntent(ctx, postgres.PaymentIntent{
		PayerID:        in.PayerID,
		PayeeID:        in.PayeeID,
		ReferenceType:  in.ReferenceType,
		ReferenceID:    in.ReferenceID,
		Amount:         amountRupees,
		AmountMinorRaw: amountMinor,
		Currency:       orDefault(in.Currency, "INR"),
		Method:         in.Method,
		OwnerDomain:    in.OwnerDomain,
		IdempotencyKey: in.IdempotencyKey,
	})
	if err != nil {
		return nil, err
	}

	if res.WasExisting {
		// A genuine retry. If the first attempt never managed to attach a
		// provider order, finish that now rather than handing back a row
		// that can never be paid — using the SAME idempotency key, so the
		// PSP collapses this into the order it may already have created.
		if res.Intent.ProviderRef == "" && s.canOpenProviderOrder() {
			if ref, attachErr := s.attachProviderOrder(ctx, res.Intent.ID, amountMinor,
				orDefault(in.Currency, "INR"), in.IdempotencyKey); attachErr != nil {
				return nil, attachErr
			} else {
				res.Intent.ProviderRef = ref
			}
		}
		return res.Intent, nil
	}

	if s.canOpenProviderOrder() {
		ref, attachErr := s.attachProviderOrder(ctx, res.Intent.ID, amountMinor,
			orDefault(in.Currency, "INR"), in.IdempotencyKey)
		if attachErr != nil {
			// The intent stays `pending` with no provider reference. That is
			// a recoverable state the reconciler owns; it is emphatically
			// not a success, so the caller is told.
			return nil, attachErr
		}
		res.Intent.ProviderRef = ref
	}

	// UNREACHABLE since B6: `+"`"+`escrow`+"`"+` is not in the launch vocabulary, so
	// paymentmethod.Validate above refuses it before this line. Kept, rather
	// than deleted, because re-enabling escrow is a scope decision and the
	// hold semantics it describes are still the ones that would apply.
	if in.Method == "escrow" {
		// Holds are recorded in paise-minor so they line up with the
		// provider's amount; amountMinor is the resolved paise value.
		if holdErr := s.store.CreateHold(ctx, res.Intent.ID, amountMinor, orDefault(in.Currency, "INR"), "order_delivered"); holdErr != nil {
			slog.Error("payment: CreateHold failed", "intent_id", res.Intent.ID, "error", holdErr)
		}
	}

	s.publishEvent(ctx, "payment.initiated", res.Intent.PayerID.String(), res.Intent)
	return res.Intent, nil
}

// canOpenProviderOrder reports whether a PSP order can be opened at all.
//
// MRC-1: both attach sites used to test `s.gateway != nil` — the LEGACY
// adapter — even though createOrRecoverProviderOrder prefers the
// provider-neutral port. A deployment wired with only the provider (which is
// the direction this service is moving) would therefore commit intents and
// silently never open a PSP order, leaving every one of them pending with a
// blank reference. Production sets both today, so this was latent rather than
// live, but it is the same misrouting MRC-1 exists to remove.
func (s *Service) canOpenProviderOrder() bool {
	return s.provider != nil || s.gateway != nil
}

// attachProviderOrder creates the PSP order for an intent that already
// exists locally, and records the reference against it.
//
// B6. The idempotency key is the caller's business key and is passed through
// unchanged on every attempt, so a retry after an ambiguous timeout resolves
// to the SAME provider order rather than creating a second one. If the
// provider call succeeds but the local UPDATE fails, the next attempt asks
// the provider again with the same key and gets the same order back — the
// reference is recoverable, which is precisely what the old
// call-provider-then-insert ordering made impossible.
func (s *Service) attachProviderOrder(
	ctx context.Context,
	intentID uuid.UUID,
	amountMinor int64,
	currency, idempotencyKey string,
) (string, error) {
	providerOrderID, err := s.createOrRecoverProviderOrder(ctx, intentID, amountMinor, currency, idempotencyKey)
	if err != nil {
		return "", err
	}
	if providerOrderID == "" {
		return "", fmt.Errorf("payments: provider returned no order id for intent %s", intentID)
	}
	if err := s.store.SetProviderOrder(ctx, intentID, providerOrderID); err != nil {
		// N3: a zero-row attach is no longer silent. Either the retry
		// converged on the same reference (SetProviderOrder returns nil) or
		// the intent already holds a DIFFERENT one, which means two PSP
		// orders exist for one intent and must not be papered over.
		return "", fmt.Errorf("payments: could not record provider order %q on intent %s: %w",
			providerOrderID, intentID, err)
	}
	return providerOrderID, nil
}

// createOrRecoverProviderOrder opens the PSP order, and on an ambiguous
// failure asks the provider what it already holds under the same key.
//
// N3. The failure this closes: CreateOrder times out after the PSP has
// created order A. The response is lost, so nothing local references A. The
// retry called CreateOrder again — and while the deterministic key means a
// well-behaved PSP returns A rather than creating B, that was an assumption
// with no fallback and no way to observe which had happened. If the provider
// did create B, two orders existed for one intent and nothing detected it.
//
// The recovery call is `FetchByIdempotencyKey`, which the Provider port has
// carried since A6 and which nothing in the money path used. Providers that
// cannot look up by key return ErrLookupNotSupported, and the error is
// surfaced rather than swallowed: an unrecoverable ambiguous timeout leaves a
// `pending` intent for the reconciler, which is the correct end state.
func (s *Service) createOrRecoverProviderOrder(
	ctx context.Context,
	intentID uuid.UUID,
	amountMinor int64,
	currency, idempotencyKey string,
) (string, error) {
	if s.provider != nil {
		order, err := s.provider.CreateOrder(ctx,
			gateway.Money{Minor: amountMinor, Currency: currency},
			idempotencyKey, nil)
		if err == nil {
			// R4-LB-1 / A1. The ORDINARY success path is a money path too.
			//
			// Review 4: "the claim 'one policy on every money path' is false."
			// It was. The recovery path, the repair path, the reconciler and
			// the webhook all verified their tuple; the one path that runs on
			// every single checkout did not. It kept `ProviderOrderID` and
			// discarded `order.Amount`, which both adapters decode and return.
			//
			// A PSP that answers HTTP 200 naming a different order, a
			// different amount, or no currency is not a transport failure —
			// nothing retries it, and the buyer is handed that identifier and
			// pays against it. This is the last unguarded provider fact.
			if vErr := verifyOpenedOrder(intentID, order.ProviderOrderID,
				order.Amount, amountMinor, currency); vErr != nil {
				return "", vErr
			}
			return order.ProviderOrderID, nil
		}
		slog.Warn("payments: provider CreateOrder failed; attempting idempotency-key recovery",
			"intent_id", intentID, "error", err)

		state, recoverErr := s.provider.FetchByIdempotencyKey(ctx, idempotencyKey)
		switch {
		case recoverErr == nil && state.ProviderOrderID != "":
			// C3-LB-1 / A-LB-1. THIS is the check review 3 found missing.
			//
			// The old code returned `state.ProviderOrderID` here on the sole
			// basis that it was non-blank. A recovered order is an object the
			// provider chose to hand back for a key; believing it identifies
			// OUR intent, without comparing what it is for, is how an intent
			// gets pointed at somebody else's money — and the local unique
			// index cannot detect it, because the wrong reference is still
			// internally unique.
			//
			// A recovered order that does not verify is NOT attached. The
			// intent stays pending with a blank reference, which the
			// reconciler owns and can repair from the same provider record
			// on a later tick. Pending-and-recoverable beats
			// attached-and-wrong.
			if vErr := gateway.VerifyProviderMoney(gateway.MoneyCheck{
				Operation:      "immediate ambiguous-create recovery for intent " + intentID.String(),
				IdentifierKind: "recovered provider order id",
				Identifier:     state.ProviderOrderID,
				Provider:       state.Amount,
				Expected:       gateway.Money{Minor: amountMinor, Currency: currency},
			}); vErr != nil {
				slog.Error("payments: RECOVERY REFUSED — recovered provider order does not verify; "+
					"leaving the intent pending with no reference",
					"intent_id", intentID, "provider_order_id", state.ProviderOrderID,
					"provider_minor", state.Amount.Minor, "provider_currency", state.Amount.Currency,
					"intent_minor", amountMinor, "intent_currency", currency, "error", vErr)
				return "", vErr
			}
			slog.Info("payments: recovered an existing provider order after an ambiguous failure",
				"intent_id", intentID, "provider_order_id", state.ProviderOrderID)
			return state.ProviderOrderID, nil

		case errors.Is(recoverErr, gateway.ErrAmbiguousLookup):
			// One deterministic key matched more than one provider object.
			// There is no correct choice, so this alarms rather than picking.
			slog.Error("payments: AMBIGUOUS RECOVERY — one idempotency key matches several provider objects",
				"intent_id", intentID, "error", recoverErr)
			return "", fmt.Errorf("payments: ambiguous provider recovery for intent %s: %w",
				intentID, recoverErr)

		case recoverErr != nil && !errors.Is(recoverErr, gateway.ErrLookupNotSupported):
			slog.Error("payments: idempotency-key recovery failed",
				"intent_id", intentID, "error", recoverErr)
		}
		return "", fmt.Errorf("payments: could not open a provider order: %w", err)
	}

	// The LEGACY adapter. A1 requires that this not be a second, weaker path:
	// its GatewayOrder carries the same {ID, Amount, Currency} tuple, so it is
	// normalized into the same MoneyCheck rather than being trusted because it
	// is old. Production wires the provider port and never reaches here (see
	// canOpenProviderOrder), but "unreachable in production" is a claim about
	// configuration, and configuration changes.
	order, err := s.gateway.CreateOrder(ctx, amountMinor, currency, idempotencyKey)
	if err != nil {
		slog.Error("payments: provider CreateOrder failed; intent stays pending with no provider reference",
			"intent_id", intentID, "error", err)
		return "", fmt.Errorf("payments: could not open a provider order: %w", err)
	}
	if vErr := verifyOpenedOrder(intentID, order.ID,
		gateway.Money{Minor: order.Amount, Currency: order.Currency},
		amountMinor, currency); vErr != nil {
		return "", vErr
	}
	return order.ID, nil
}

// verifyOpenedOrder checks a freshly opened provider order against the intent
// it was opened for, through the one shared policy.
//
// Refusal returns the error and attaches nothing, so the intent stays pending
// with a blank reference: a state the reconciler owns and can repair from the
// provider's own record. Pending-and-recoverable beats attached-and-wrong,
// because a wrong reference is internally unique and nothing downstream can
// detect it.
func verifyOpenedOrder(
	intentID uuid.UUID,
	providerOrderID string,
	provider gateway.Money,
	expectedMinor int64,
	expectedCurrency string,
) error {
	err := gateway.VerifyProviderMoney(gateway.MoneyCheck{
		Operation:      "opening a provider order for intent " + intentID.String(),
		IdentifierKind: "provider order id",
		Identifier:     providerOrderID,
		Provider:       provider,
		Expected:       gateway.Money{Minor: expectedMinor, Currency: expectedCurrency},
	})
	if err != nil {
		slog.Error("payments: PROVIDER ORDER REFUSED — the opened order does not match the intent; "+
			"leaving the intent pending with no reference",
			"intent_id", intentID, "provider_order_id", providerOrderID,
			"provider_minor", provider.Minor, "provider_currency", provider.Currency,
			"intent_minor", expectedMinor, "intent_currency", expectedCurrency,
			"error", err)
	}
	return err
}

func (s *Service) GetIntent(ctx context.Context, id uuid.UUID) (*postgres.PaymentIntent, error) {
	return s.store.GetIntent(ctx, id)
}

func (s *Service) UpdateStatus(ctx context.Context, id uuid.UUID, oldStatus, newStatus, providerRef string, actorID uuid.UUID) (*postgres.PaymentIntent, error) {
	if err := s.store.UpdateStatus(ctx, id, oldStatus, newStatus, providerRef, actorID); err != nil {
		return nil, err
	}
	intent, err := s.store.GetIntent(ctx, id)
	if err != nil {
		return nil, err
	}
	eventType := "payment.status_changed"
	if newStatus == "succeeded" {
		eventType = "payment.succeeded"
	} else if newStatus == "failed" {
		eventType = "payment.failed"
	}
	s.publishEvent(ctx, eventType, actorID.String(), intent)
	return intent, nil
}

// ErrRefundNotAuthorized is returned when the actor isn't entitled to
// refund this intent. Audit P1: previously InitiateRefund did no
// authorization check — any caller with X-User-Id could refund any
// payment in the table, moving money out of any seller's account.
// Errors surfaced by VerifyIntent so HTTP callers can distinguish a bad
// signature (which must produce a 401/400, never a 200) from a missing
// intent or a misconfigured gateway.
var (
	ErrSignatureVerificationFailed = fmt.Errorf("razorpay signature verification failed")
	ErrAmountMismatch              = fmt.Errorf("payment amount does not match intent")
	ErrProviderRefMismatch         = fmt.Errorf("razorpay order id does not match intent")
	ErrGatewayNotConfigured        = fmt.Errorf("payment gateway not configured")
)

var ErrRefundNotAuthorized = fmt.Errorf("not authorized to refund this payment")

// ErrRefundAmountExceedsIntent is returned when the caller asks to
// refund more than the intent's outstanding refundable balance. Audit
// P6: previously the InitiateRefund signature took no amount and
// blanket-flipped status to 'refunded', so commerce-service computing a
// per-line return refund worth ₹X on an order paid as ₹Y > X would
// refund the entire ₹Y. The amount cap is now enforced both at the
// service layer (with this error) and atomically at the DB layer (the
// store's WHERE clause).
var ErrRefundAmountExceedsIntent = fmt.Errorf("refund amount exceeds intent")

// ErrHoldReleaseNotAuthorized is the equivalent for escrow holds.
// Audit P4: ReleaseHold previously took an arbitrary X-User-Id string
// and skipped any ownership check, so a buyer could release the
// seller's escrow before delivery.
var ErrHoldReleaseNotAuthorized = fmt.Errorf("not authorized to release this hold")

// resolveRefundAmount validates an InitiateRefund request against the
// stored intent and returns the paise-minor refund amount the store
// should apply, plus the intent's total amount in paise-minor (the
// store uses the latter as the upper bound for its atomic UPDATE).
//
// Extracted as a pure function so the audit P6 + P7 validation —
// status check, ownership check, amount cap, full-vs-partial selection —
// is unit-testable without a real Postgres pool. Errors map 1:1 to the
// surfaceable Err* constants InitiateRefund returns.
func resolveRefundAmount(intent *postgres.PaymentIntent, actorID uuid.UUID, amountMinor int64) (refundMinor, intentAmountMinor int64, err error) {
	// Allow refunds on succeeded (first refund) and partially_refunded
	// (subsequent partial top-ups until fully refunded).
	if intent.Status != "succeeded" && intent.Status != "partially_refunded" {
		return 0, 0, fmt.Errorf("can only refund succeeded payments, current status: %s", intent.Status)
	}
	if actorID != intent.PayerID && actorID != intent.PayeeID {
		return 0, 0, ErrRefundNotAuthorized
	}

	intentAmountMinor = intent.AmountMinor()
	remaining := intentAmountMinor - intent.RefundedAmountMinor
	if remaining <= 0 {
		return 0, 0, fmt.Errorf("intent already fully refunded")
	}

	// amountMinor == 0 means "refund the whole remaining balance". This
	// preserves the historical semantics of the no-amount signature.
	refundMinor = amountMinor
	if refundMinor == 0 {
		refundMinor = remaining
	}
	if refundMinor < 0 {
		return 0, 0, fmt.Errorf("refund amount must be non-negative")
	}
	if refundMinor > remaining {
		return 0, 0, ErrRefundAmountExceedsIntent
	}
	return refundMinor, intentAmountMinor, nil
}

// computeRefundStatus mirrors the CASE inside store.ApplyRefund so the
// state machine can be unit-tested without a DB. If applying `refundMinor`
// brings the running total up to (or above) `intentAmountMinor`, the
// status flips to 'refunded' (full); otherwise 'partially_refunded'.
//
// Kept as a sibling helper rather than a method on PaymentIntent to
// emphasise it's the projection of the SQL CASE expression — change one,
// change both.
func computeRefundStatus(currentRefundedMinor, refundMinor, intentAmountMinor int64) string {
	if currentRefundedMinor+refundMinor >= intentAmountMinor {
		return "refunded"
	}
	return "partially_refunded"
}

// There is no InitiateRefund any more — RequestRefund (p0.go) is the only
// refund entry point.
//
// A6 / LB-8 / review §2.1-5.13. The old body did this, in this order:
//
//  1. mark the intent refunded in the database
//  2. ask Razorpay to refund it
//  3. log and swallow any error from step 2
//  4. publish payment.refunded regardless
//
// So a provider outage produced a ledger that said the customer had been
// refunded, a commerce order that said the same, a notification telling the
// customer so — and no money moving, with nothing left in the system that
// remembered the debt. Nothing retried, nothing alarmed.
//
// RequestRefund inverts the order: the refund is persisted as a durable
// command with a deterministic provider idempotency key BEFORE any network
// call, and only a verified provider outcome advances it to settled. The
// /v1/payments/internal refund route (and its legacy internal-key callers,
// which have already authorised the actor against their own order model)
// feed that path; resolveRefundAmount above is the cap the handler applies
// when a legacy caller omits the amount.

func (s *Service) ListByReference(ctx context.Context, refType string, refID uuid.UUID) ([]postgres.PaymentIntent, error) {
	return s.store.ListByReference(ctx, refType, refID)
}

// VerifyResult is returned by VerifyIntent. Verified is only ever true
// when the signature, the provider order id, and (when supplied) the
// amount all match the stored intent.
type VerifyResult struct {
	Verified bool `json:"verified"`
	// Advisory is always true, and is on the wire so a caller cannot read
	// this result as an authorisation by accident. A1 / review R-3: a
	// client callback is evidence, not authority. `Status` is the status
	// the intent ACTUALLY holds — verification does not change it.
	Advisory    bool      `json:"advisory"`
	IntentID    uuid.UUID `json:"intent_id"`
	Status      string    `json:"status"`
	AmountMinor int64     `json:"amount_minor"`
	ProviderRef string    `json:"provider_ref"`
	// Parties + reference are echoed so the calling service can assert the
	// intent belongs to the order/actor it is confirming (no cross-order or
	// cross-user replay of a genuinely valid signature). Advisory too: they
	// let the caller REFUSE a callback, never accept one as payment.
	PayerID       uuid.UUID `json:"payer_id"`
	PayeeID       uuid.UUID `json:"payee_id"`
	ReferenceType string    `json:"reference_type"`
	ReferenceID   uuid.UUID `json:"reference_id"`
}

// VerifyIntent is the synchronous gateway-verification path commerce-service
// uses to confirm a payment immediately after the customer completes Razorpay
// checkout. The webhook remains the canonical async signal; this exists so
// the order page can transition without waiting for webhook delivery.
//
// Checks, in order:
//  1. The intent's stored provider_ref matches the razorpay_order_id the
//     client returned (no cross-order replay).
//  2. The supplied amount_minor (if > 0) matches the intent amount in paise
//     (prevents a low-amount payment confirming a high-amount order).
//  3. The Razorpay signature HMAC-verifies for (order_id|payment_id).
//
// It is ADVISORY and NON-MUTATING (A1 / review R-3). See the body.
func (s *Service) VerifyIntent(ctx context.Context, id uuid.UUID, rzpOrderID, rzpPaymentID, rzpSignature string, amountMinor int64) (*VerifyResult, error) {
	intent, err := s.store.GetIntent(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.gateway == nil {
		return nil, ErrGatewayNotConfigured
	}

	// Audit P7-deep: AmountMinor() reads the new int64 column; the
	// float fallback only fires for legacy pre-migration rows.
	intentAmountMinor := intent.AmountMinor()

	if intent.ProviderRef == "" || intent.ProviderRef != rzpOrderID {
		return nil, ErrProviderRefMismatch
	}
	if amountMinor > 0 && amountMinor != intentAmountMinor {
		return nil, ErrAmountMismatch
	}
	if !s.gateway.VerifySignature(rzpOrderID, rzpPaymentID, rzpSignature) {
		return nil, ErrSignatureVerificationFailed
	}

	// A1 / review R-3 — VerifyIntent NO LONGER MUTATES.
	//
	// It used to run `UpdateStatus(pending → succeeded)` right here, which
	// made a browser callback an approval-capable evaluator: whatever the
	// client handed back became terminal payment state as long as the HMAC
	// checked out. Two things are wrong with that. A provider state can be
	// reversed or left uncaptured after the callback, and we would already
	// have fulfilled against it. And it grants the client's round trip an
	// authority that belongs only to the provider.
	//
	// The signature check above still earns its keep — it is a cheap, fast
	// signal that the callback is genuine, so the app can leave the spinner
	// and show "confirming". But it is EVIDENCE, not authority. Terminal
	// state now comes from exactly two places, both server-side:
	//
	//	ApplyWebhookAtomically  — a signature-verified provider webhook
	//	ReconcileIntent         — a server-initiated provider fetch
	//
	// Callers must read Verified=true as "looks genuine, keep polling",
	// never as "paid". commerce-service's payment/status endpoint reports
	// the stored status, which is unchanged by this call.
	current, err := s.store.GetIntent(ctx, id)
	if err != nil {
		return nil, err
	}
	return &VerifyResult{
		Verified:      true,
		Advisory:      true,
		IntentID:      id,
		Status:        current.Status,
		AmountMinor:   intentAmountMinor,
		ProviderRef:   current.ProviderRef,
		PayerID:       current.PayerID,
		PayeeID:       current.PayeeID,
		ReferenceType: current.ReferenceType,
		ReferenceID:   current.ReferenceID,
	}, nil
}

// ReleaseHold marks an escrow hold released. Audit P4: actor must
// match the payee (seller — the party the hold is protecting) or the
// payer (buyer — for buyer-initiated escrow release flows). Empty or
// non-UUID actor strings are rejected.
func (s *Service) ReleaseHold(ctx context.Context, intentID uuid.UUID, releasedBy string) error {
	intent, err := s.store.GetIntent(ctx, intentID)
	if err != nil {
		return err
	}
	actor, err := uuid.Parse(releasedBy)
	if err != nil {
		return ErrHoldReleaseNotAuthorized
	}
	if actor != intent.PayeeID && actor != intent.PayerID {
		return ErrHoldReleaseNotAuthorized
	}
	return s.store.ReleaseHold(ctx, intentID, releasedBy)
}

// MarkWebhookSeen is the dedup gate the webhook handler calls before
// applying the status update. Audit P3: returns (fresh=true) the first
// time an event_id arrives and (false) on every retry, so duplicate
// deliveries from Razorpay don't re-publish Kafka events.
func (s *Service) MarkWebhookSeen(ctx context.Context, eventID, eventType, providerRef string) (bool, error) {
	return s.store.RecordWebhookEventIfNew(ctx, eventID, eventType, providerRef)
}

// UpdateStatusByProviderRef is invoked from the webhook handler. The
// state machine inside the store rejects forbidden transitions (audit
// P2), so a late payment.captured arriving after refund.processed no
// longer reverts the status. We publish a Kafka event only when the
// transition actually applied — the store returns
// ErrInvalidStatusTransition / ErrPaymentNotFound for the no-op cases.
func (s *Service) UpdateStatusByProviderRef(ctx context.Context, providerRef, newStatus, paymentID string) {
	intent, err := s.store.UpdateStatusByProviderRef(ctx, providerRef, newStatus, paymentID)
	if err != nil {
		// Quiet log for the expected no-op cases; loud for everything else.
		if errors.Is(err, postgres.ErrInvalidStatusTransition) || errors.Is(err, postgres.ErrPaymentNotFound) {
			slog.Info("payment: webhook status update skipped",
				"provider_ref", providerRef, "new_status", newStatus, "reason", err.Error())
			return
		}
		slog.Error("payment: UpdateStatusByProviderRef failed", "provider_ref", providerRef, "error", err)
		return
	}
	// Publish only when the row actually changed.
	eventType := "payment.status_changed"
	switch newStatus {
	case "succeeded":
		eventType = "payment.succeeded"
	case "failed":
		eventType = "payment.failed"
	case "refunded":
		eventType = "payment.refunded"
	}
	// Publish the row the UPDATE returned so the event carries
	// reference_type + reference_id (what commerce-service keys on).
	// Previously this re-read the intent by the OLD provider_ref after
	// the UPDATE had just replaced provider_ref with the payment id, so
	// the lookup always missed and a bare {provider_ref, payment_id,
	// new_status} payload went out — which the commerce consumer drops
	// for lack of a reference. Every webhook-driven order confirmation
	// was lost that way.
	s.publishEvent(ctx, eventType, "", intent)
}

// ApplyWebhookRefund settles a refund.processed webhook from Razorpay.
//
// Closes the follow-up the partial-refund commit flagged: the old
// webhook arm did UpdateStatusByProviderRef(providerRef, "refunded"),
// which (a) ignored the refund amount entirely so partials got booked
// as fulls, and (b) tried partially_refunded → refunded
// unconditionally — the post-P6 state machine correctly refuses that
// when refunded_amount_minor hasn't caught up.
//
// Idempotency:
//   - Refund-level: store.RecordRefundIfFresh INSERTs the
//     refund_provider_ref ON CONFLICT DO NOTHING. A retry returns
//     "not fresh" and we short-circuit without re-applying. The
//     existing webhook_events dedup only catches identical event_ids;
//     Razorpay can re-deliver the same refund with a new event_id.
//   - DB-level: ApplyRefund's `refunded_amount_minor + $2 <= $3`
//     WHERE clause is the second line of defense — if dedup somehow
//     misses, the cap still refuses to oversubscribe.
//
// Best-effort logging on failures so the webhook handler can still
// 200 — Razorpay's retry loop will redeliver on a 5xx, but a refund
// that genuinely can't be booked (intent not found, amount overflows
// cap) is a permanent failure that re-trying doesn't fix.
//
// Lookup order: provider_ref holds the Razorpay ORDER id until capture and
// the Razorpay PAYMENT id afterwards (UpdateStatusByProviderRef swaps it so
// gateway refunds can address the payment). A refund always follows a
// capture, so the payment id is tried first and the order id second.
func (s *Service) ApplyWebhookRefund(ctx context.Context, paymentID, orderProviderRef, refundProviderRef string, amountMinor int64) {
	if amountMinor <= 0 {
		slog.Warn("webhook refund: skipping zero/negative amount",
			"refund_id", refundProviderRef, "amount_minor", amountMinor)
		return
	}
	var intent *postgres.PaymentIntent
	var err error
	for _, ref := range []string{paymentID, orderProviderRef} {
		if ref == "" {
			continue
		}
		if intent, err = s.store.GetIntentByProviderRef(ctx, ref); err == nil && intent != nil {
			break
		}
	}
	if intent == nil {
		slog.Warn("webhook refund: intent not found for provider refs",
			"payment_id", paymentID, "order_id", orderProviderRef, "refund_id", refundProviderRef, "error", err)
		return
	}

	fresh, err := s.store.RecordRefundIfFresh(ctx, refundProviderRef, intent.ID, amountMinor)
	if err != nil {
		slog.Error("webhook refund: dedup record failed",
			"intent_id", intent.ID, "refund_id", refundProviderRef, "error", err)
		return
	}
	if !fresh {
		slog.Info("webhook refund: replay skipped",
			"intent_id", intent.ID, "refund_id", refundProviderRef)
		return
	}

	intentAmountMinor := intent.AmountMinor()
	if amountMinor > intentAmountMinor-intent.RefundedAmountMinor {
		// Refund exceeds the remaining balance — the cap WHERE clause
		// in ApplyRefund will refuse, but we log the impossible case
		// loudly. A legitimate over-refund means provider/local state
		// drifted; needs an ops investigation rather than a retry.
		slog.Error("webhook refund: amount exceeds remaining intent balance",
			"intent_id", intent.ID, "refund_id", refundProviderRef,
			"requested_minor", amountMinor,
			"remaining_minor", intentAmountMinor-intent.RefundedAmountMinor)
		return
	}

	newStatus, newRefundedTotal, err := s.store.ApplyRefund(ctx, intent.ID, amountMinor, intentAmountMinor, uuid.Nil)
	if err != nil {
		slog.Error("webhook refund: ApplyRefund failed",
			"intent_id", intent.ID, "refund_id", refundProviderRef, "error", err)
		return
	}

	intent.Status = newStatus
	intent.RefundedAmountMinor = newRefundedTotal
	s.publishEvent(ctx, "payment.refunded", "", intent)
	slog.Info("webhook refund: applied",
		"intent_id", intent.ID, "refund_id", refundProviderRef,
		"amount_minor", amountMinor, "new_status", newStatus,
		"refunded_total_minor", newRefundedTotal)
}

func (s *Service) publishEvent(ctx context.Context, eventType, key string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("failed to marshal event", "event_type", eventType, "error", err)
		return
	}
	// Wrap the payload in the shared EventEnvelope so consumers using the
	// shared/kafka.Consumer can decode it. The actor (key) goes into
	// ActorUserID when it parses as a UUID; otherwise it's left nil.
	envelope := events.EventEnvelope{
		EventID:    uuid.New().String(),
		EventType:  eventType,
		OccurredAt: time.Now().UTC(),
		Payload:    data,
	}
	if key != "" {
		if _, err := uuid.Parse(key); err == nil {
			k := key
			envelope.ActorUserID = &k
		}
	}
	value, err := json.Marshal(envelope)
	if err != nil {
		slog.Error("failed to marshal envelope", "event_type", eventType, "error", err)
		return
	}
	// Transactional-outbox write. The row lands in the same database as
	// the intent it describes; shared/outbox.Publisher relays it to Kafka
	// with retries. A failed INSERT is logged loudly — it means Postgres
	// itself is unhealthy, in which case the status write that preceded
	// this call would also have failed.
	if s.store == nil {
		return
	}
	if err := s.store.EnqueueOutboxEvent(ctx, eventType, key, value); err != nil {
		slog.Error("failed to enqueue outbox event", "event_type", eventType, "error", err)
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
