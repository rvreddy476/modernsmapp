package gateway

// The bank-offer capture rule — the ONE place it lives.
//
// Founder, 1 Oct 2026: bank offers go through Razorpay Offers, and he knowingly
// authorised changing payment matching for it, in exactly one narrow way. With
// an INSTANT offer the Razorpay ORDER keeps its full amount and the PAYMENT is
// captured at the discounted amount ("the customer pays only the discounted
// amount while making the payment" — razorpay.com/docs/payments/offers/create/).
// VerifyProviderMoney refuses that capture, correctly, because it is not the
// intent's amount. This file is the only thing that may accept it, and it does
// so only when every clause below holds:
//
//  1. the provider names exactly ONE applied offer (the payment's expanded
//     `offers` collection, fetched server-side), and that offer is in OUR
//     registry for this intent's application and provider;
//  2. the offer was active at the time the customer paid — the registry's
//     state AT THAT TIME (its append-only change log), inside its
//     starts_at/ends_at window;
//  3. 0 < intent − captured ≤ allowed, where allowed is the flat value, or
//     floor(intent × bps / 10000), capped by max_discount_minor;
//  4. intent ≥ min_amount_minor.
//
// It never accepts a capture ABOVE the intent, nor one in another currency,
// nor one with no identifier. Anything it refuses goes down the existing
// mismatch path unchanged. Both the webhook (store.ApplyWebhookAtomically) and
// the reconciler (which applies through the same transaction) reach it; there
// is no second copy.

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Offer discount types and the payment methods an offer may name.
const (
	OfferDiscountPercentage = "percentage"
	OfferDiscountFlat       = "flat"
	// OfferBasisPoints is 100%: a percentage offer's discount_value is in basis
	// points.
	OfferBasisPoints = 10000
)

// Refusal reasons. Every one wraps ErrProviderMoneyUnverified, so a caller that
// only knows "the provider disagrees with us" keeps treating it that way.
var (
	ErrOfferCaptureNotLower    = errors.New("offer capture: the captured amount is not below the intent")
	ErrOfferNotNamed           = errors.New("offer capture: the provider names no applied offer")
	ErrOfferAmbiguous          = errors.New("offer capture: the provider names more than one applied offer")
	ErrOfferUnknown            = errors.New("offer capture: the applied offer is not in our registry")
	ErrOfferWrongApplication   = errors.New("offer capture: the applied offer belongs to another application")
	ErrOfferInactive           = errors.New("offer capture: the offer was not active when the customer paid")
	ErrOfferExpired            = errors.New("offer capture: the offer had ended when the customer paid")
	ErrOfferBelowMinimum       = errors.New("offer capture: the intent is below the offer's minimum amount")
	ErrOfferDiscountExceedsCap = errors.New("offer capture: the discount is larger than the offer allows")
	ErrOfferNoPaymentTime      = errors.New("offer capture: the provider stated no payment time")
)

// OfferTerms is one registry offer as it stood at a point in time.
type OfferTerms struct {
	// OfferID is OUR registry id.
	OfferID         string
	Application     string
	Provider        string
	ProviderOfferID string

	// Known is false when the offer had no registry version at the time asked
	// about (it was registered later). Such an offer was not active then.
	Known            bool
	Title            string
	FundedBy         string
	Active           bool
	StartsAt         time.Time
	EndsAt           *time.Time
	DiscountType     string
	DiscountValue    int64
	MaxDiscountMinor *int64
	MinAmountMinor   int64
}

// AllowedOfferDiscount is the most an offer may take off intentMinor: the flat
// value, or floor(intent × bps / 10000), then capped by max_discount_minor.
// Integer arithmetic only. An unknown discount type allows nothing.
func AllowedOfferDiscount(intentMinor int64, t OfferTerms) int64 {
	if intentMinor <= 0 || t.DiscountValue <= 0 {
		return 0
	}
	var allowed int64
	switch t.DiscountType {
	case OfferDiscountFlat:
		allowed = t.DiscountValue
	case OfferDiscountPercentage:
		if t.DiscountValue > OfferBasisPoints {
			return 0
		}
		// intent ≤ 9.2e14 paise (₹9.2 trillion) keeps the product in int64.
		if intentMinor > (1<<63-1)/OfferBasisPoints {
			return 0
		}
		allowed = intentMinor * t.DiscountValue / OfferBasisPoints
	default:
		return 0
	}
	if t.MaxDiscountMinor != nil && *t.MaxDiscountMinor >= 0 && allowed > *t.MaxDiscountMinor {
		allowed = *t.MaxDiscountMinor
	}
	return allowed
}

// OfferCapture is one lower-than-intent capture awaiting the rule.
type OfferCapture struct {
	// Operation names the path in the refusal message.
	Operation  string
	Identifier string // the provider payment id

	Application string // the intent's
	Provider    string // the intent's

	Intent   Money
	Captured Money
	// PaidAt is when the customer paid, as the provider states it.
	PaidAt time.Time

	// ProviderOfferIDs are the offers the PROVIDER says it applied.
	ProviderOfferIDs []string
	// Registry is our registry's terms, as of PaidAt, for those provider ids
	// (looked up by provider + provider offer id, any application).
	Registry []OfferTerms
}

// OfferMatch is an accepted offer capture.
type OfferMatch struct {
	Terms         OfferTerms
	DiscountMinor int64
	AllowedMinor  int64
}

// MatchOfferCapture applies the whole rule. A nil error means the capture is a
// full payment of the intent through that offer.
func MatchOfferCapture(c OfferCapture) (OfferMatch, error) {
	fail := func(reason error, format string, args ...any) (OfferMatch, error) {
		return OfferMatch{}, fmt.Errorf("%w: %w: %s: %s", ErrProviderMoneyUnverified, reason, c.Operation,
			fmt.Sprintf(format, args...))
	}

	if strings.TrimSpace(c.Identifier) == "" {
		return fail(ErrOfferNotNamed, "provider payment id is blank")
	}
	if c.Captured.Minor <= 0 || c.Intent.Minor <= 0 {
		return fail(ErrOfferCaptureNotLower, "captured %d / intent %d minor", c.Captured.Minor, c.Intent.Minor)
	}
	pc, ic := strings.TrimSpace(c.Captured.Currency), strings.TrimSpace(c.Intent.Currency)
	if pc == "" || ic == "" || !strings.EqualFold(pc, ic) {
		// Same refusal VerifyProviderMoney makes: a discount in one currency
		// says nothing about an intent in another.
		return fail(ErrOfferCaptureNotLower, "captured currency %q does not match the intent's %q", pc, ic)
	}
	// Clause 3, lower bound: never above the intent, never equal (an equal
	// capture is not an offer capture and VerifyProviderMoney owns it).
	discount := c.Intent.Minor - c.Captured.Minor
	if discount <= 0 {
		return fail(ErrOfferCaptureNotLower, "captured %d is not below the intent's %d", c.Captured.Minor, c.Intent.Minor)
	}

	// Clause 1: exactly one applied offer, and it is ours.
	named := map[string]bool{}
	for _, id := range c.ProviderOfferIDs {
		if id = strings.TrimSpace(id); id != "" {
			named[id] = true
		}
	}
	switch len(named) {
	case 0:
		return fail(ErrOfferNotNamed, "payment %q carries no applied offer", c.Identifier)
	case 1:
	default:
		return fail(ErrOfferAmbiguous, "payment %q names %d offers", c.Identifier, len(named))
	}
	var providerOfferID string
	for id := range named {
		providerOfferID = id
	}
	var terms *OfferTerms
	for i := range c.Registry {
		r := c.Registry[i]
		if r.ProviderOfferID == providerOfferID && r.Provider == c.Provider {
			terms = &c.Registry[i]
			break
		}
	}
	if terms == nil {
		return fail(ErrOfferUnknown, "offer %q (provider %s) is not registered", providerOfferID, c.Provider)
	}
	if terms.Application != c.Application {
		return fail(ErrOfferWrongApplication, "offer %q belongs to %q, the intent to %q",
			providerOfferID, terms.Application, c.Application)
	}

	// Clause 2: active when the customer paid.
	if c.PaidAt.IsZero() {
		return fail(ErrOfferNoPaymentTime, "payment %q", c.Identifier)
	}
	if !terms.Known || !terms.Active || c.PaidAt.Before(terms.StartsAt) {
		return fail(ErrOfferInactive, "offer %q at %s (registered=%t active=%t starts=%s)",
			providerOfferID, c.PaidAt.UTC().Format(time.RFC3339), terms.Known, terms.Active,
			terms.StartsAt.UTC().Format(time.RFC3339))
	}
	if terms.EndsAt != nil && !c.PaidAt.Before(*terms.EndsAt) {
		return fail(ErrOfferExpired, "offer %q ended %s, payment at %s", providerOfferID,
			terms.EndsAt.UTC().Format(time.RFC3339), c.PaidAt.UTC().Format(time.RFC3339))
	}

	// Clause 4: the intent qualifies.
	if c.Intent.Minor < terms.MinAmountMinor {
		return fail(ErrOfferBelowMinimum, "intent %d is below the offer's minimum %d", c.Intent.Minor, terms.MinAmountMinor)
	}

	// Clause 3, upper bound: within the computed cap.
	allowed := AllowedOfferDiscount(c.Intent.Minor, *terms)
	if discount > allowed {
		return fail(ErrOfferDiscountExceedsCap, "discount %d exceeds the %d offer %q allows on %d",
			discount, allowed, providerOfferID, c.Intent.Minor)
	}
	return OfferMatch{Terms: *terms, DiscountMinor: discount, AllowedMinor: allowed}, nil
}
