package service

// Bank offers through Razorpay Offers (migration 014).
//
// payments-service owns the money rule. This file does the service's three
// parts of it, all behind PAYMENTS_OFFERS_ENABLED:
//
//   - on intent creation, restrict the provider order to OUR active offers
//     that the amount qualifies for (no offers ⇒ the request is exactly as
//     before);
//   - when a capture is LOWER than the intent, fetch the provider's own
//     account of the offers applied to that payment and hand it, verified,
//     to the one matching rule (store.ApplyWebhookAtomically →
//     gateway.MatchOfferCapture) — from the webhook and the reconciler alike;
//   - serve the registry to commerce (public fields) and to the admin console.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/google/uuid"
)

// errOfferEvidence marks evidence the provider fetch could not supply or that
// did not agree with the event it was fetched for.
var errOfferEvidence = errors.New("payments: offer evidence does not verify")

// orderOffers is the provider offer ids a new provider order may apply: the
// application's offers for this provider that are active now and whose
// minimum the amount meets. Nil when offers are off, the provider cannot
// restrict an order to offers, or nothing applies — and then the order is
// created exactly as before. A registry read failure is logged and treated as
// "no offers": the payment must not fail because a discount could not be
// listed.
func (s *Service) orderOffers(ctx context.Context, intentID uuid.UUID, applicationID string, amountMinor int64) []string {
	if !s.offersEnabled || s.provider == nil || s.store == nil || applicationID == "" {
		return nil
	}
	if _, ok := s.provider.(gateway.OfferOrderCreator); !ok {
		return nil
	}
	offers, err := s.store.ApplicableOffers(ctx, applicationID, s.provider.Name(), amountMinor, s.clock())
	if err != nil {
		slog.Warn("payments: could not read the offer registry; opening the order without offers",
			"intent_id", intentID, "application_id", applicationID, "error", err)
		return nil
	}
	ids := make([]string, 0, len(offers))
	for _, o := range offers {
		ids = append(ids, o.ProviderOfferID)
	}
	return ids
}

// offerEvidence asks the provider which offers a captured payment used — a
// server-initiated fetch, GET /payments/{id}?expand[]=offers — and returns it
// only if the fetched payment is the one the event or listing named: same
// payment, same order, captured, same amount, same currency. Anything else is
// errOfferEvidence and the capture keeps its ordinary mismatch outcome.
func (s *Service) offerEvidence(ctx context.Context, providerOrderID, providerPaymentID string, captured gateway.Money) (*postgres.OfferEvidence, error) {
	fetcher, ok := s.provider.(gateway.OfferFetcher)
	if !ok || s.provider == nil {
		return nil, fmt.Errorf("%w: provider cannot report applied offers", errOfferEvidence)
	}
	if strings.TrimSpace(providerPaymentID) == "" {
		return nil, fmt.Errorf("%w: the capture names no provider payment", errOfferEvidence)
	}
	po, err := fetcher.FetchPaymentWithOffers(ctx, providerPaymentID)
	if err != nil {
		return nil, err
	}
	p := po.Payment
	switch {
	case p.ProviderPaymentID != providerPaymentID:
		return nil, fmt.Errorf("%w: fetched payment %q, asked for %q", errOfferEvidence, p.ProviderPaymentID, providerPaymentID)
	case providerOrderID == "" || p.ProviderOrderID != providerOrderID:
		return nil, fmt.Errorf("%w: payment %s belongs to order %q, the capture to %q",
			errOfferEvidence, providerPaymentID, p.ProviderOrderID, providerOrderID)
	case p.State != gateway.StateCaptured:
		return nil, fmt.Errorf("%w: payment %s is %s, not captured", errOfferEvidence, providerPaymentID, p.State)
	case p.Amount.Minor != captured.Minor:
		return nil, fmt.Errorf("%w: payment %s is %d at the provider, the capture says %d",
			errOfferEvidence, providerPaymentID, p.Amount.Minor, captured.Minor)
	case strings.TrimSpace(p.Amount.Currency) == "" ||
		!strings.EqualFold(strings.TrimSpace(p.Amount.Currency), strings.TrimSpace(captured.Currency)):
		return nil, fmt.Errorf("%w: payment %s is in %q at the provider, the capture says %q",
			errOfferEvidence, providerPaymentID, p.Amount.Currency, captured.Currency)
	}
	return &postgres.OfferEvidence{ProviderOfferIDs: po.OfferIDs, PaidAt: po.PaidAt}, nil
}

// reconcileOfferEvidence fetches offer evidence for every captured payment on
// the order that is LOWER than the intent in its currency — the reconciler's
// counterpart of the webhook's retry. Nil when offers are off.
func (s *Service) reconcileOfferEvidence(ctx context.Context, intent postgres.PaymentIntent, providerOrderID string, payments []gateway.ProviderPaymentState) map[string]*postgres.OfferEvidence {
	if !s.offersEnabled {
		return nil
	}
	var out map[string]*postgres.OfferEvidence
	for _, p := range payments {
		if p.State != gateway.StateCaptured || p.Amount.Minor <= 0 || p.Amount.Minor >= intent.AmountMinor() ||
			strings.TrimSpace(p.Amount.Currency) == "" ||
			!strings.EqualFold(strings.TrimSpace(p.Amount.Currency), strings.TrimSpace(intent.Currency)) {
			continue
		}
		ev, err := s.offerEvidence(ctx, providerOrderID, p.ProviderPaymentID, p.Amount)
		if err != nil {
			slog.Warn("payments: reconcile could not check a lower capture against a bank offer",
				"intent_id", intent.ID, "provider_payment_id", p.ProviderPaymentID, "error", err)
			continue
		}
		if out == nil {
			out = map[string]*postgres.OfferEvidence{}
		}
		out[p.ProviderPaymentID] = ev
	}
	return out
}

// ─── Registry reads and writes ───────────────────────────────────────

// PublicOffer is what a buyer-facing caller (commerce) may see of an offer.
type PublicOffer struct {
	ID               uuid.UUID `json:"id"`
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	PaymentMethod    string    `json:"payment_method"`
	DiscountType     string    `json:"discount_type"`
	DiscountValue    int64     `json:"discount_value"`
	MaxDiscountMinor *int64    `json:"max_discount_minor"`
	MinAmountMinor   int64     `json:"min_amount_minor"`
	EndsAt           *string   `json:"ends_at"`
}

// ApplicableOffers lists the application's active offers, filtered by
// minimum when amountMinor > 0, as public fields only. Empty — never an error
// — while PAYMENTS_OFFERS_ENABLED is off.
func (s *Service) ApplicableOffers(ctx context.Context, applicationID string, amountMinor int64) ([]PublicOffer, error) {
	out := []PublicOffer{}
	if !s.offersEnabled || s.provider == nil {
		return out, nil
	}
	offers, err := s.store.ApplicableOffers(ctx, applicationID, s.provider.Name(), amountMinor, s.clock())
	if err != nil {
		return nil, err
	}
	for _, o := range offers {
		p := PublicOffer{
			ID: o.ID, Title: o.Title, Description: o.Description, PaymentMethod: o.PaymentMethod,
			DiscountType: o.DiscountType, DiscountValue: o.DiscountValue,
			MaxDiscountMinor: o.MaxDiscountMinor, MinAmountMinor: o.MinAmountMinor,
		}
		if o.EndsAt != nil {
			v := o.EndsAt.UTC().Format("2006-01-02T15:04:05Z07:00")
			p.EndsAt = &v
		}
		out = append(out, p)
	}
	return out, nil
}

// ListOffers is the admin registry view; application "" means every one.
func (s *Service) ListOffers(ctx context.Context, applicationID string) ([]postgres.PaymentOffer, error) {
	return s.store.ListOffers(ctx, applicationID)
}

// CreateOffer registers an offer that already exists at the provider.
func (s *Service) CreateOffer(ctx context.Context, o postgres.PaymentOffer, w postgres.OfferWrite) (*postgres.PaymentOffer, error) {
	out, err := s.store.CreateOffer(ctx, o, w)
	if err != nil {
		return nil, err
	}
	slog.Warn("payments: bank offer registered from the admin console",
		"offer_id", out.ID, "application_id", out.Application, "provider_offer_id", out.ProviderOfferID,
		"operator_id", w.OperatorID, "credential", w.Credential)
	return out, nil
}

// UpdateOffer edits an offer (deactivation is active=false).
func (s *Service) UpdateOffer(ctx context.Context, id uuid.UUID, applicationID string, p postgres.OfferPatch, w postgres.OfferWrite) (*postgres.PaymentOffer, bool, error) {
	out, changed, err := s.store.UpdateOffer(ctx, id, applicationID, p, w)
	if err != nil {
		return nil, false, err
	}
	if changed {
		slog.Warn("payments: bank offer changed from the admin console",
			"offer_id", out.ID, "application_id", out.Application, "active", out.Active,
			"operator_id", w.OperatorID, "credential", w.Credential)
	}
	return out, changed, nil
}
