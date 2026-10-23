package service

// Paid creator memberships on payments-service (phase 1 contract, pinned
// 23 Oct 2026).
//
// A join, a renewal and an upgrade difference each open ONE
// membership_payments row and ONE payments intent (application memberships,
// reference creator_membership:<payment id>). The member pays in the
// Razorpay sheet. Access, a renewed period or a new tier is applied only by
// ApplyMembershipPaymentEvent, from the signed payment event; the verify
// callback is advisory. Earnings are recorded for the creator on that event
// (fee 0, share = amount). Nothing here moves creator_ledger money between
// members.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/atpost/monetization-service/internal/events"
	"github.com/atpost/monetization-service/internal/membership"
	"github.com/atpost/monetization-service/internal/payments"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/kyc"
	"github.com/google/uuid"
)

var (
	// ErrMembershipPaymentsUnavailable: no payments client is configured
	// (MONETIZATION_SERVICE_TOKEN_KEY/KID unset) — 503.
	ErrMembershipPaymentsUnavailable = errors.New("MEMBERSHIP_PAYMENTS_UNAVAILABLE")
	// ErrPaymentSessionUnavailable: payments opened an intent without a
	// usable checkout session outside a local/dev stack — 502.
	ErrPaymentSessionUnavailable = errors.New("PAYMENT_SESSION_UNAVAILABLE")
	// ErrCheckoutExpired: the Idempotency-Key names a join that expired
	// unpaid; start again with a new key — 409.
	ErrCheckoutExpired = errors.New("CHECKOUT_EXPIRED")
	// ErrMembershipPaymentNotFound: no such payment, or not the caller's — 404.
	ErrMembershipPaymentNotFound = postgres.ErrMembershipPaymentNotFound
	// ErrNotEligible matches every *NotEligibleError — 403.
	ErrNotEligible = errors.New("MEMBERSHIPS_NOT_ELIGIBLE")
)

// NotEligibleError: the creator may not offer memberships yet.
type NotEligibleError struct{ Missing []string }

func (e *NotEligibleError) Error() string {
	return "MEMBERSHIPS_NOT_ELIGIBLE: missing " + strings.Join(e.Missing, ",")
}

// Is matches ErrNotEligible.
func (e *NotEligibleError) Is(target error) bool { return target == ErrNotEligible }

// MembershipEventPublisher publishes membership.* events (events.Producer).
type MembershipEventPublisher interface {
	PublishMembershipEvent(ctx context.Context, eventType, key string, p events.MembershipPayload) error
}

// WithMembershipPayments installs the payments client. localEnv is the
// ENV verdict: outside local/dev an intent without a checkout session is
// refused rather than presented as payable. nil leaves the membership money
// routes answering 503 MEMBERSHIP_PAYMENTS_UNAVAILABLE.
func (s *Service) WithMembershipPayments(api payments.API, localEnv bool) *Service {
	s.membershipPayments = api
	s.membershipLocalEnv = localEnv
	return s
}

// MembershipPaymentsConfigured reports whether a payments client is installed.
func (s *Service) MembershipPaymentsConfigured() bool { return s.membershipPayments != nil }

// WithMembershipEvents installs the membership.* publisher.
func (s *Service) WithMembershipEvents(p MembershipEventPublisher) *Service {
	s.membershipEvents = p
	return s
}

// ---------------------------------------------------------------------------
// Checkout
// ---------------------------------------------------------------------------

// Checkout is what a join, renewal or paid upgrade answers: the payment the
// app is to collect and the public checkout values.
type Checkout struct {
	SubscriptionID uuid.UUID `json:"subscription_id"`
	PaymentID      uuid.UUID `json:"payment_id"`
	Kind           string    `json:"kind"`
	// Status is "pending_payment" while the payment is open. A replay of a
	// join that is already paid answers the subscription's status instead,
	// with no client_session.
	Status        string                  `json:"status"`
	AmountPaise   int64                   `json:"amount_paise"`
	Currency      string                  `json:"currency"`
	ClientSession *payments.ClientSession `json:"client_session"`
}

func checkoutFrom(rows *postgres.MembershipCheckout, status string, session *payments.ClientSession) *Checkout {
	return &Checkout{
		SubscriptionID: rows.Sub.ID, PaymentID: rows.Payment.ID, Kind: rows.Payment.Kind, Status: status,
		AmountPaise: rows.Payment.AmountPaise, Currency: rows.Payment.Currency, ClientSession: session,
	}
}

// openCheckout opens (or, under the row's deterministic key, re-reads) the
// payments intent for a payment row and binds it. Nothing is applied.
func (s *Service) openCheckout(ctx context.Context, rows *postgres.MembershipCheckout, status string) (*Checkout, error) {
	if s.membershipPayments == nil {
		return nil, ErrMembershipPaymentsUnavailable
	}
	p := rows.Payment
	intent, err := s.membershipPayments.CreateIntent(ctx, payments.IntentInput{
		PaymentID: p.ID, PayerID: p.PayerID, AmountMinor: p.AmountPaise, Currency: p.Currency,
	})
	if err != nil {
		slog.ErrorContext(ctx, "memberships: create intent failed", "payment_id", p.ID, "kind", p.Kind, "error", err)
		return nil, fmt.Errorf("%w: %v", ErrMembershipPaymentsUnavailable, err)
	}
	orderID := intent.ProviderRef
	if intent.ClientSession != nil && intent.ClientSession.OrderID != "" {
		orderID = intent.ClientSession.OrderID
	}
	if err := s.members.SetMembershipPaymentIntent(ctx, p.ID, intent.ID, orderID); err != nil {
		return nil, err
	}
	if intent.ClientSession == nil && !s.membershipLocalEnv {
		return nil, ErrPaymentSessionUnavailable
	}
	return checkoutFrom(rows, status, intent.ClientSession), nil
}

// Renew opens (or replays) the renewal payment for the next period of a
// subscription the caller owns. The period is extended only by the signed
// event, from max(now, current_period_end).
func (s *Service) Renew(ctx context.Context, subscriberID, subscriptionID uuid.UUID) (*Checkout, error) {
	if s.membershipPayments == nil {
		return nil, ErrMembershipPaymentsUnavailable
	}
	rows, err := s.members.CreateMembershipRenewal(ctx, postgres.RenewalInput{
		SubscriptionID: subscriptionID, SubscriberID: subscriberID, Now: s.clock(),
	})
	if err != nil {
		return nil, err
	}
	return s.openCheckout(ctx, rows, membership.StatusPendingPayment)
}

// ---------------------------------------------------------------------------
// Payment status (the app's poll) and the advisory callback
// ---------------------------------------------------------------------------

// PaymentView is a membership payment as its payer sees it.
type PaymentView struct {
	PaymentID      uuid.UUID `json:"payment_id"`
	SubscriptionID uuid.UUID `json:"subscription_id"`
	Kind           string    `json:"kind"`
	// Status: confirming (open or callback verified) | paid (applied) |
	// failed (did not go through, or captured too late to apply — then
	// refund_status says where the money is).
	Status       string `json:"status"`
	RefundStatus string `json:"refund_status,omitempty"`
	AmountPaise  int64  `json:"amount_paise"`
	Currency     string `json:"currency"`
	LateCapture  bool   `json:"late_capture,omitempty"`
}

func paymentView(p *membership.Payment) *PaymentView {
	v := &PaymentView{PaymentID: p.ID, SubscriptionID: p.SubscriptionID, Kind: p.Kind,
		AmountPaise: p.AmountPaise, Currency: p.Currency, LateCapture: p.LateCapture}
	switch {
	case p.LateCapture:
		v.Status = membership.PayFailed
	case p.Status == membership.PayPaid || p.Status == membership.PayRefunded:
		v.Status = membership.PayPaid
	case p.Status == membership.PayFailed:
		v.Status = membership.PayFailed
	default:
		v.Status = membership.PayConfirming
	}
	switch p.RefundStatus {
	case membership.RefundRequested, membership.RefundPending:
		v.RefundStatus = "pending"
	case membership.RefundRefunded:
		v.RefundStatus = "refunded"
	case membership.RefundFailed:
		v.RefundStatus = "failed"
	}
	return v
}

func (s *Service) ownedPayment(ctx context.Context, payerID, paymentID uuid.UUID) (*membership.Payment, error) {
	p, err := s.members.GetMembershipPayment(ctx, paymentID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.PayerID != payerID {
		return nil, ErrMembershipPaymentNotFound
	}
	return p, nil
}

// GetMembershipPayment answers the payer's poll; anyone else gets 404.
func (s *Service) GetMembershipPayment(ctx context.Context, payerID, paymentID uuid.UUID) (*PaymentView, error) {
	p, err := s.ownedPayment(ctx, payerID, paymentID)
	if err != nil {
		return nil, err
	}
	return paymentView(p), nil
}

// MembershipCallback is the checkout callback the app relays.
type MembershipCallback struct {
	OrderID   string `json:"razorpay_order_id"`
	PaymentID string `json:"razorpay_payment_id"`
	Signature string `json:"razorpay_signature"`
}

// VerifyMembershipPayment checks the app's checkout callback with payments.
// ADVISORY ONLY: a verified callback for this payment, payer and amount moves
// the row to confirming so the poll can say "confirming"; it never marks it
// paid and never grants access. Only the signed event does.
func (s *Service) VerifyMembershipPayment(ctx context.Context, payerID, paymentID uuid.UUID, cb MembershipCallback) (*PaymentView, error) {
	if s.membershipPayments == nil {
		return nil, ErrMembershipPaymentsUnavailable
	}
	p, err := s.ownedPayment(ctx, payerID, paymentID)
	if err != nil {
		return nil, err
	}
	intentID, err := uuid.Parse(p.IntentID)
	if err != nil {
		return paymentView(p), nil
	}
	v, err := s.membershipPayments.VerifyCallback(ctx, intentID, payments.Callback{
		OrderID: cb.OrderID, PaymentID: cb.PaymentID, Signature: cb.Signature, ExpectedAmountMinor: p.AmountPaise,
	})
	if err != nil {
		slog.WarnContext(ctx, "memberships: verify callback failed (advisory)", "payment_id", p.ID, "error", err)
		return paymentView(p), nil
	}
	ours := v.ReferenceType == membership.RefType && v.ReferenceID == p.ID && v.PayerID == p.PayerID &&
		v.AmountMinor == p.AmountPaise && (v.ApplicationID == "" || v.ApplicationID == membership.ApplicationID)
	if v.Verified && ours && p.Status == membership.PayCreated {
		if err := s.members.MarkMembershipPaymentConfirming(ctx, p.ID); err != nil {
			return nil, err
		}
		p.Status = membership.PayConfirming
	}
	return paymentView(p), nil
}

// ---------------------------------------------------------------------------
// The signed payment event
// ---------------------------------------------------------------------------

// ApplyMembershipPaymentEvent applies one payments event (the consumer's
// Applier) and, after it committed, publishes what it changed and files any
// refund it queued. A publish or refund-filing failure never undoes the
// commit: the refund worker retries a queued refund.
func (s *Service) ApplyMembershipPaymentEvent(ctx context.Context, ev membership.Event) (membership.Applied, error) {
	applied, err := s.members.ApplyMembershipPaymentEvent(ctx, ev, s.clock())
	if err != nil {
		return applied, err
	}
	s.afterApplied(ctx, ev, applied)
	return applied, nil
}

func (s *Service) afterApplied(ctx context.Context, ev membership.Event, a membership.Applied) {
	d := a.Decision
	for _, et := range d.Notify {
		switch et {
		case membership.EventEnded:
			s.publishMembership(ctx, et, EndedEventKey(a.Sub.ID, a.Sub.CurrentPeriodEnd), endedPayload(a.Sub))
		case membership.EventPaymentFailed:
			// A row can fail, be retried and fail again: each failure is its
			// own change, keyed by the payments event that reported it.
			s.publishMembership(ctx, et, ev.EventID, membershipPayload(a.Sub, a.Payment.TierID, a.Payment.TierName, a.Payment.AmountPaise, a.Payment.Currency))
		default:
			// started / renewed: one per applied payment.
			s.publishMembership(ctx, et, a.Payment.ID.String(), membershipPayload(a.Sub, a.Payment.TierID, a.Payment.TierName, a.Payment.AmountPaise, a.Payment.Currency))
		}
	}
	if d.Entitlement != "" {
		s.publishEntitlementChanged(ctx, a.Sub.ID, a.Sub.SubscriberID, a.Sub.CreatorID, d.Entitlement)
	}
	if d.Refund != nil {
		intentID, err := uuid.Parse(a.Payment.IntentID)
		if err != nil {
			slog.ErrorContext(ctx, "memberships: queued refund has no intent", "payment_id", a.Payment.ID)
			return
		}
		s.submitMembershipRefund(ctx, postgres.MembershipRefundDue{
			PaymentID: a.Payment.ID, IntentID: intentID, Key: d.Refund.Key, AmountPaise: d.Refund.AmountPaise, Cause: d.Refund.Cause,
		})
	}
}

func membershipPayload(sub membership.Sub, tierID uuid.UUID, tierName string, amount int64, currency string) events.MembershipPayload {
	if currency == "" {
		currency = membership.Currency
	}
	return events.MembershipPayload{
		SubscriptionID: sub.ID.String(), SubscriberID: sub.SubscriberID.String(), CreatorID: sub.CreatorID.String(),
		TierID: tierID.String(), TierName: tierName, AmountPaise: amount, Currency: currency,
		PeriodEnd: sub.CurrentPeriodEnd.UTC().Format(time.RFC3339),
	}
}

func endedPayload(sub membership.Sub) events.MembershipPayload {
	return membershipPayload(sub, sub.TierID, sub.TierName, sub.PricePaise, sub.Currency)
}

// MembershipEndedPayload is the membership.ended payload for a subscription
// row (the expiry worker's).
func MembershipEndedPayload(sub *postgres.Subscription) events.MembershipPayload {
	return endedPayload(postgres.ToMembershipSub(sub))
}

// RenewalDuePayload is the membership.renewal_due payload.
func RenewalDuePayload(sub *postgres.Subscription, renewalPricePaise int64, daysLeft int) events.MembershipPayload {
	p := membershipPayload(postgres.ToMembershipSub(sub), sub.TierID, sub.TierName, renewalPricePaise, sub.Currency)
	if sub.PendingTierID != nil {
		p.TierID = sub.PendingTierID.String()
	}
	d := daysLeft
	p.DaysLeft = &d
	return p
}

func (s *Service) publishMembership(ctx context.Context, eventType, key string, p events.MembershipPayload) {
	if s.membershipEvents == nil {
		return
	}
	if err := s.membershipEvents.PublishMembershipEvent(ctx, eventType, key, p); err != nil {
		slog.WarnContext(ctx, "memberships: publish event failed", "event_type", eventType, "subscription_id", p.SubscriptionID, "error", err)
	}
}

func (s *Service) publishMembershipEnded(ctx context.Context, sub *postgres.Subscription) {
	s.publishMembership(ctx, membership.EventEnded, EndedEventKey(sub.ID, sub.CurrentPeriodEnd), MembershipEndedPayload(sub))
}

// ---------------------------------------------------------------------------
// Refunds
// ---------------------------------------------------------------------------

const (
	refundRetryBase = time.Minute
	refundRetryMax  = time.Hour
	maxRefundError  = 300
)

func refundBackoff(attempts int) time.Duration {
	d := refundRetryBase
	for i := 0; i < attempts && d < refundRetryMax; i++ {
		d *= 2
	}
	if d > refundRetryMax {
		d = refundRetryMax
	}
	return d
}

func refundReason(r postgres.MembershipRefundDue) string {
	if r.Cause == membership.CauseLateCapture {
		return "membership payment captured after it could no longer be applied"
	}
	if strings.TrimSpace(r.Reason) != "" {
		return r.Reason
	}
	return "membership refund (support)"
}

// submitMembershipRefund files one queued refund under its deterministic
// key. Accepted → pending; refused (4xx) → failed, for a person; unreachable
// → retried with backoff. The money is back only on payment.refunded.
func (s *Service) submitMembershipRefund(ctx context.Context, r postgres.MembershipRefundDue) {
	if s.membershipPayments == nil {
		return
	}
	acc, err := s.membershipPayments.Refund(ctx, r.IntentID, r.AmountPaise, refundReason(r), r.Key)
	switch {
	case err == nil:
		if merr := s.members.MarkMembershipRefundSubmitted(ctx, r.PaymentID, acc.CommandID); merr != nil {
			slog.ErrorContext(ctx, "memberships: record refund submission", "payment_id", r.PaymentID, "error", merr)
		}
	case errors.Is(err, payments.ErrRefused):
		slog.ErrorContext(ctx, "memberships: payments refused a refund; needs attention", "payment_id", r.PaymentID, "key", r.Key, "error", err)
		if merr := s.members.MarkMembershipRefundFailed(ctx, r.PaymentID, truncateErr(err)); merr != nil {
			slog.ErrorContext(ctx, "memberships: record refund refusal", "payment_id", r.PaymentID, "error", merr)
		}
	default:
		next := s.clock().Add(refundBackoff(r.Attempts))
		if merr := s.members.MarkMembershipRefundRetry(ctx, r.PaymentID, next, truncateErr(err)); merr != nil {
			slog.ErrorContext(ctx, "memberships: record refund retry", "payment_id", r.PaymentID, "error", merr)
		}
	}
}

func truncateErr(err error) string {
	s := err.Error()
	if len(s) > maxRefundError {
		return s[:maxRefundError]
	}
	return s
}

// SubmitDueMembershipRefunds files every queued refund whose attempt is due
// (the refund worker's pass). Returns how many it tried.
func (s *Service) SubmitDueMembershipRefunds(ctx context.Context) (int, error) {
	if s.membershipPayments == nil {
		return 0, nil
	}
	due, err := s.members.DueMembershipRefunds(ctx, s.clock(), 100)
	if err != nil {
		return 0, err
	}
	for _, r := range due {
		s.submitMembershipRefund(ctx, r)
	}
	return len(due), nil
}

// RefundMembershipPayment is support's refund decision (the contract's
// exceptions: a payment that failed to unlock, a double charge, or support's
// call). It queues a full refund under membership-refund:{paymentId} and
// files it at once; the period it bought is removed when payment.refunded
// arrives, not now.
func (s *Service) RefundMembershipPayment(ctx context.Context, actorID, paymentID uuid.UUID, reason string) (*PaymentView, error) {
	if s.membershipPayments == nil {
		return nil, ErrMembershipPaymentsUnavailable
	}
	p, err := s.members.RequestMembershipSupportRefund(ctx, postgres.SupportRefundInput{
		PaymentID: paymentID, RequestedBy: actorID, Reason: reason, Now: s.clock(),
	})
	if err != nil {
		return nil, err
	}
	if p.RefundStatus == membership.RefundRequested {
		if intentID, err := uuid.Parse(p.IntentID); err == nil {
			s.submitMembershipRefund(ctx, postgres.MembershipRefundDue{
				PaymentID: p.ID, IntentID: intentID, Key: p.RefundKey, AmountPaise: p.RefundPaise,
				Cause: membership.CauseSupport, Reason: reason,
			})
		}
	}
	if fresh, err := s.members.GetMembershipPayment(ctx, paymentID); err == nil && fresh != nil {
		p = fresh
	}
	return paymentView(p), nil
}

// ---------------------------------------------------------------------------
// My memberships
// ---------------------------------------------------------------------------

// MembershipView is one row of GET /memberships.
type MembershipView struct {
	SubscriptionID   uuid.UUID          `json:"subscription_id"`
	Creator          MembershipCreator  `json:"creator"`
	Tier             MembershipTierView `json:"tier"`
	Status           string             `json:"status"`
	CurrentPeriodEnd time.Time          `json:"current_period_end"`
	AccessUntil      *time.Time         `json:"access_until"`
	RenewAvailable   bool               `json:"renew_available"`
}

// MembershipCreator is the creator of a membership.
type MembershipCreator struct {
	ID uuid.UUID `json:"id"`
}

// MembershipTierView is the tier a membership is on, at its locked price.
type MembershipTierView struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	PricePaise int64     `json:"price_paise"`
}

// ListMyMemberships returns the caller's memberships: live, pending, and
// ended ones still inside the 30-day renewal window.
func (s *Service) ListMyMemberships(ctx context.Context, subscriberID uuid.UUID) ([]MembershipView, error) {
	now := s.clock()
	subs, err := s.members.ListSubscriberMemberships(ctx, subscriberID, now.Add(-membership.RenewAfterExpiryWindow))
	if err != nil {
		return nil, err
	}
	out := make([]MembershipView, 0, len(subs))
	for i := range subs {
		sub := &subs[i]
		v := MembershipView{
			SubscriptionID: sub.ID, Creator: MembershipCreator{ID: sub.CreatorID},
			Tier:   MembershipTierView{ID: sub.TierID, Name: sub.TierName, PricePaise: sub.PricePaise},
			Status: sub.Status, CurrentPeriodEnd: sub.CurrentPeriodEnd,
			RenewAvailable: membership.CanRenew(sub.Status, sub.ActivatedAt != nil, sub.CurrentPeriodEnd, now),
		}
		if until, ok := SubscriptionAccessUntil(sub); ok {
			u := until
			v.AccessUntil = &u
		}
		out = append(out, v)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Eligibility to offer memberships
// ---------------------------------------------------------------------------

// BirthDateSource answers a user's date of birth as identity stands behind
// it (dob nil = none recorded); found=false for no such account.
type BirthDateSource interface {
	BirthDate(ctx context.Context, userID uuid.UUID) (dob *time.Time, found bool, err error)
}

// FollowerCountSource answers a user's follower count.
type FollowerCountSource interface {
	FollowerCount(ctx context.Context, userID uuid.UUID) (int, error)
}

// StandingAnswer is trust-safety's standing for a user.
type StandingAnswer = membership.StandingAnswer

// StandingSource answers a user's trust-safety standing.
type StandingSource interface {
	Standing(ctx context.Context, userID uuid.UUID) (StandingAnswer, error)
}

// EligibilitySources are where the eligibility facts are read. A nil source
// is unknown, and unknown is missing: the check fails closed.
type EligibilitySources struct {
	BirthDates BirthDateSource
	Followers  FollowerCountSource
	Standing   StandingSource
	// PANFormatSuffices is MEMBERSHIPS_REQUIRE_PAN_VERIFIED=false: nothing
	// sets creator_tax_profiles.verified_at yet, so a PAN on file that passes
	// kyc.ValidatePAN (layout + holder type; a format check, not an Income Tax
	// lookup) counts as the PAN item. The zero value is strict: verified_at
	// must be set. Applies to membership eligibility only; payout KYC still
	// reads verified_at.
	PANFormatSuffices bool
}

// panSatisfied is the PAN item: a non-blank PAN on file, and verified_at set
// (strict) or — when formatSuffices — verified_at set or a PAN that passes
// the shared format check. The stored value is never logged or returned.
func panSatisfied(p *postgres.CreatorTaxProfile, formatSuffices bool) bool {
	if p == nil || p.PANEncrypted == nil || strings.TrimSpace(*p.PANEncrypted) == "" {
		return false
	}
	if p.VerifiedAt != nil {
		return true
	}
	if !formatSuffices {
		return false
	}
	_, err := kyc.ValidatePAN(*p.PANEncrypted)
	return err == nil
}

// WithEligibilitySources installs the eligibility fact sources.
func (s *Service) WithEligibilitySources(src EligibilitySources) *Service {
	s.eligibility = src
	return s
}

// Eligibility is whether a creator may offer memberships, and what is missing.
type Eligibility struct {
	Eligible bool     `json:"eligible"`
	Missing  []string `json:"missing"`
}

// MembershipEligibility decides the four items (founder, 23 Oct 2026): 18 or
// older (identity's date of birth), PAN verified (this service's creator tax
// profile: a PAN on file AND verified_at set; with PANFormatSuffices a PAN
// that passes the format check also counts), at least 500 followers
// (graph-service), no active strikes (trust-safety standing "ok" with no
// active strike). Every source that is missing, errors or answers without the
// fact puts its item in Missing: it never reads as satisfied.
func (s *Service) MembershipEligibility(ctx context.Context, creatorID uuid.UUID) (*Eligibility, error) {
	now := s.clock()
	var (
		wg                         sync.WaitGroup
		ageOK, panOK, folOK, strOK bool
	)
	wg.Add(4)
	go func() {
		defer wg.Done()
		if s.eligibility.BirthDates == nil {
			return
		}
		dob, found, err := s.eligibility.BirthDates.BirthDate(ctx, creatorID)
		if err != nil {
			slog.WarnContext(ctx, "memberships: eligibility age unknown", "creator_id", creatorID, "error", err)
			return
		}
		ageOK = found && dob != nil && membership.AgeYears(*dob, now) >= membership.MinAgeYears
	}()
	go func() {
		defer wg.Done()
		p, err := s.members.GetCreatorTaxProfile(ctx, creatorID)
		if err != nil {
			slog.WarnContext(ctx, "memberships: eligibility PAN unknown", "creator_id", creatorID, "error", err)
			return
		}
		panOK = panSatisfied(p, s.eligibility.PANFormatSuffices)
	}()
	go func() {
		defer wg.Done()
		if s.eligibility.Followers == nil {
			return
		}
		n, err := s.eligibility.Followers.FollowerCount(ctx, creatorID)
		if err != nil {
			slog.WarnContext(ctx, "memberships: eligibility followers unknown", "creator_id", creatorID, "error", err)
			return
		}
		folOK = n >= membership.MinFollowers
	}()
	go func() {
		defer wg.Done()
		if s.eligibility.Standing == nil {
			return
		}
		st, err := s.eligibility.Standing.Standing(ctx, creatorID)
		if err != nil {
			slog.WarnContext(ctx, "memberships: eligibility standing unknown", "creator_id", creatorID, "error", err)
			return
		}
		strOK = st.Standing == "ok" && st.ActiveStrikes == 0
	}()
	wg.Wait()

	missing := []string{}
	if !ageOK {
		missing = append(missing, membership.MissingAge)
	}
	if !panOK {
		missing = append(missing, membership.MissingPAN)
	}
	if !folOK {
		missing = append(missing, membership.MissingFollowers)
	}
	if !strOK {
		missing = append(missing, membership.MissingStrikes)
	}
	return &Eligibility{Eligible: len(missing) == 0, Missing: missing}, nil
}

// EndedEventKey keys membership.ended: one per subscription and the period
// end it ended at.
func EndedEventKey(subID uuid.UUID, periodEnd time.Time) string {
	return subID.String() + "|" + periodEnd.UTC().Format(time.RFC3339Nano)
}

// RenewalDueEventKey keys membership.renewal_due: one per subscription,
// period end and days_left (3 or 0).
func RenewalDueEventKey(subID uuid.UUID, periodEnd time.Time, daysLeft int) string {
	return fmt.Sprintf("%s|%s|%d", subID, periodEnd.UTC().Format(time.RFC3339Nano), daysLeft)
}
