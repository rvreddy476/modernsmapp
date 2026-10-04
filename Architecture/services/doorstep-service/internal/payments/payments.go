// Package payments is Doorstep's side of payments-service: the client
// binding (application doorstep, reference doorstep_booking), the
// deterministic keys, and the pure decision table the payment-event
// consumer applies.
//
// A booking is paid ONLY by a signed payment.succeeded event applied once
// (shared/paymentevents ApplyOnce + CheckCapture) in the same transaction as
// the inbox row; the intent call, a client callback and the dev stub confirm
// are never evidence of payment.
package payments

import (
	"errors"
	"strings"

	"github.com/atpost/shared/events"
	"github.com/atpost/shared/paymentevents"
	"github.com/atpost/shared/paymentmethod"
	"github.com/atpost/shared/paymentsclient"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

const (
	// ApplicationID is Doorstep's payments application.
	ApplicationID = "doorstep"
	// RefBooking / RefExtras are the reference types Doorstep owns.
	RefBooking = servicetoken.RefDoorstepBooking
	RefExtras  = servicetoken.RefDoorstepExtras
	// Currency is the only currency Doorstep prices in.
	Currency = "INR"
	// Service is doorstep-service's caller name at payments-service.
	Service = "doorstep-service"
	// StubProvider is the client_session.provider of payments-service's
	// stub gateway; only it admits the dev stub confirm.
	StubProvider = "stub"
)

// PayeeID is the payee on every Doorstep intent: the platform, which
// collects as the e-commerce operator and settles professionals separately.
// Name-based, so every replica names the same payee.
var PayeeID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://momentum.app/payee/doorstep-bookings"))

// IntentKey is the booking intent's idempotency key.
func IntentKey(bookingID uuid.UUID) string { return "doorstep:booking:" + bookingID.String() }

// ExtrasIntentKey is an extras bill's intent key (a visit-extras bill, or
// a change-of-professional difference, B1).
func ExtrasIntentKey(billID uuid.UUID) string { return "doorstep:extras:" + billID.String() }

// RefundKey is a refund's idempotency key: one per booking and cause, so a
// retry (or a worker resubmission) can never become a second refund.
func RefundKey(bookingID uuid.UUID, cause string) string {
	return "doorstep:refund:" + bookingID.String() + ":" + cause
}

// Refund causes.
const (
	CauseCustomerCancel = "customer_cancel"
	CauseAdminCancel    = "admin_cancel"
	CauseLateCapture    = "late_capture"
	// A4: nobody accepted in time (T-45 or a failed rescue), and a
	// professional who never arrived with no replacement.
	CauseUnassigned  = "unassigned"
	CauseProNoShow   = "pro_no_show"
	CauseAdminPrefix = "admin_" // + a hash of the forwarded Idempotency-Key
	// B1: no choice of professional within 30 minutes of pro_unavailable;
	// a cheaper professional picked (+ a hash of the change id); a
	// change-of-professional difference captured after the change lapsed
	// (+ a hash of the bill id).
	CauseProUnavailable   = "pro_unavailable"
	CauseProChangePrefix  = "pro_change_"
	CauseLateChangePrefix = "late_change_"
	CauseExternalPrefix   = "external_" // a refund made outside Doorstep, seen in an event
)

// Config builds the client.
type Config struct {
	BaseURL     string
	TokenKey    string
	TokenKID    string
	InternalKey string
	// LegacyAllowed is the caller's verdict that this is local/dev, where
	// the shared internal key may stand in for a service token.
	LegacyAllowed bool
}

// ErrNotConfigured: neither a token key nor (in dev) the internal key.
var ErrNotConfigured = errors.New("payments: DOORSTEP_SERVICE_TOKEN_KEY/KID are not set")

// NewClient binds the shared client to doorstep_booking. No credential at
// all is ErrNotConfigured (the payment routes answer 503), never a boot
// failure outside production (config refuses production without the key).
func NewClient(cfg Config) (*paymentsclient.Client, error) {
	if cfg.TokenKey == "" && (!cfg.LegacyAllowed || strings.TrimSpace(cfg.InternalKey) == "") {
		return nil, ErrNotConfigured
	}
	return paymentsclient.New(paymentsclient.Config{
		BaseURL:       cfg.BaseURL,
		Service:       Service,
		ReferenceType: RefBooking,
		Auth: paymentsclient.Auth{TokenKey: cfg.TokenKey, TokenKID: cfg.TokenKID,
			InternalKey: cfg.InternalKey, LegacyAllowed: cfg.LegacyAllowed},
		MaxResponseBytes:      1 << 20,
		ValidateMethod:        paymentmethod.Validate,
		VerifyReferenceEcho:   true,
		RequirePositiveRefund: true,
	})
}

// ---------------------------------------------------------------- decisions

// Event is a payments-service event about a Doorstep booking, reduced to
// what a decision reads. For payment.refunded AmountMinor is the refund's.
type Event struct {
	EventID     string
	EventType   string
	IntentID    string
	BookingID   uuid.UUID
	PayerID     uuid.UUID
	AmountMinor int64
	Currency    string
	Status      string
	ProviderRef string
	CommandID   string // refund events
	Reason      string // payment.refund_failed
	// ExtrasBillID is the reference of a doorstep_extras event (the bill);
	// BookingID is then resolved from the bill by the store (B1).
	ExtrasBillID uuid.UUID
}

// Snapshot is the booking and its booking payment row, read FOR UPDATE in
// the applying transaction.
type Snapshot struct {
	BookingStatus string
	CustomerID    uuid.UUID
	// AmountPaise is the payment row's amount (the quote total the intent
	// was opened for).
	AmountPaise   int64
	PaymentStatus string
	// IntentID is the payments-service intent id stored on the row; empty
	// until the intent call answered.
	IntentID   string
	HoldActive bool
}

// Outcome is recorded on the doorstep.payment_inbox row.
type Outcome string

const (
	OutcomeConfirmed            Outcome = "confirmed"
	OutcomeAlreadyPaid          Outcome = "already_paid"
	OutcomeMismatch             Outcome = "amount_mismatch"
	OutcomeLateCapture          Outcome = "late_capture"
	OutcomeLateCaptureConfirmed Outcome = "late_capture_confirmed"
	OutcomeLateCaptureRefund    Outcome = "late_capture_refund"
	OutcomeMarkedFailed         Outcome = "marked_failed"
	OutcomeIgnoredAfterCapture  Outcome = "ignored_after_capture"
	OutcomeRefunded             Outcome = "refunded"
	OutcomeRefundIgnored        Outcome = "refund_ignored"
	OutcomeRefundFailed         Outcome = "refund_failed"
	OutcomeIgnored              Outcome = "ignored"
	// Decided by the store.
	OutcomeDuplicate       Outcome = "duplicate"
	OutcomeBookingNotFound Outcome = "booking_not_found"
	// B1: a change-of-professional difference was paid and the change
	// applied (the booking is confirmed again with the new professional).
	OutcomeProChanged Outcome = "pro_changed"
	// OutcomeUnclaimed: a doorstep_extras event for a visit-extras bill (A5)
	// is left unapplied and unrecorded, for the visit lane.
	OutcomeUnclaimed Outcome = "unclaimed"
)

// Effect is what the store writes in the same transaction.
type Effect int

const (
	EffectNone Effect = iota
	// EffectConfirm: captured for a pending booking whose hold is live —
	// payment succeeded, booking confirmed, the hold becomes the booking block.
	EffectConfirm
	// EffectLateCapture: captured after the hold lapsed or the booking was
	// cancelled — confirm if the reserved professional is still free (expired
	// only), else record the money and refund it in full.
	EffectLateCapture
	// EffectMarkFailed: the payment row is failed; the booking keeps waiting
	// until its hold lapses (the customer may try again).
	EffectMarkFailed
	// EffectRefunded: a refund settled at payments-service.
	EffectRefunded
	// EffectRefundFailed: payments could not make a refund; ops must act.
	EffectRefundFailed
	// EffectAttention: money that does not match the booking. Recorded,
	// flagged, never confirmed.
	EffectAttention
)

// Decision is the pure result of Decide.
type Decision struct {
	Outcome Outcome
	Effect  Effect
	Detail  string
}

var moneyTaken = map[string]bool{"succeeded": true, "refunded": true, "partially_refunded": true}

// Decide is the decision table. It has no I/O.
func Decide(s Snapshot, ev Event) Decision {
	switch ev.EventType {
	case events.EventPaymentSucceeded:
		return decideSucceeded(s, ev)
	case events.EventPaymentFailed:
		if moneyTaken[s.PaymentStatus] {
			return Decision{Outcome: OutcomeIgnoredAfterCapture, Detail: "payment already " + s.PaymentStatus}
		}
		return Decision{Outcome: OutcomeMarkedFailed, Effect: EffectMarkFailed}
	case events.EventPaymentRefunded:
		if err := paymentevents.CheckRefund(
			paymentevents.Expected{AmountMinor: s.AmountPaise, IntentID: s.IntentID},
			paymentevents.Observed{AmountMinor: ev.AmountMinor, IntentID: ev.IntentID},
		); err != nil {
			return Decision{Outcome: OutcomeMismatch, Effect: EffectAttention, Detail: err.Error()}
		}
		if !moneyTaken[s.PaymentStatus] {
			return Decision{Outcome: OutcomeRefundIgnored, Detail: "payment is " + s.PaymentStatus}
		}
		return Decision{Outcome: OutcomeRefunded, Effect: EffectRefunded}
	case events.EventPaymentRefundFailed:
		return Decision{Outcome: OutcomeRefundFailed, Effect: EffectRefundFailed, Detail: ev.Reason}
	}
	return Decision{Outcome: OutcomeIgnored, Detail: "unhandled event type " + ev.EventType}
}

func decideSucceeded(s Snapshot, ev Event) Decision {
	// Every party and the amount are checked before any state: a capture
	// that does not match is never recorded as paid, nor as already paid.
	if err := paymentevents.CheckCapture(
		paymentevents.Expected{AmountMinor: s.AmountPaise, Currency: Currency, PayerID: s.CustomerID, IntentID: s.IntentID},
		paymentevents.Observed{AmountMinor: ev.AmountMinor, Currency: ev.Currency, PayerID: ev.PayerID, IntentID: ev.IntentID},
		paymentevents.RequireStated,
	); err != nil {
		return Decision{Outcome: OutcomeMismatch, Effect: EffectAttention, Detail: err.Error()}
	}
	if moneyTaken[s.PaymentStatus] {
		return Decision{Outcome: OutcomeAlreadyPaid, Detail: "payment already " + s.PaymentStatus}
	}
	switch s.BookingStatus {
	case "pending_payment":
		if s.HoldActive {
			return Decision{Outcome: OutcomeConfirmed, Effect: EffectConfirm}
		}
		return Decision{Outcome: OutcomeLateCapture, Effect: EffectLateCapture, Detail: "hold no longer active"}
	case "expired", "cancelled":
		return Decision{Outcome: OutcomeLateCapture, Effect: EffectLateCapture, Detail: "captured on a " + s.BookingStatus + " booking"}
	}
	return Decision{Outcome: OutcomeAlreadyPaid, Detail: "booking already " + s.BookingStatus}
}

// Method is the method an intent is opened with; the checkout lets the
// customer pay by any method payments enables for the application.
const Method = paymentmethod.UPI

// NewExtrasClient binds the shared client to doorstep_extras (B1: a
// change-of-professional difference; A5: visit extras). Same credentials
// as NewClient.
func NewExtrasClient(cfg Config) (*paymentsclient.Client, error) {
	if cfg.TokenKey == "" && (!cfg.LegacyAllowed || strings.TrimSpace(cfg.InternalKey) == "") {
		return nil, ErrNotConfigured
	}
	return paymentsclient.New(paymentsclient.Config{
		BaseURL:       cfg.BaseURL,
		Service:       Service,
		ReferenceType: RefExtras,
		Auth: paymentsclient.Auth{TokenKey: cfg.TokenKey, TokenKID: cfg.TokenKID,
			InternalKey: cfg.InternalKey, LegacyAllowed: cfg.LegacyAllowed},
		MaxResponseBytes:      1 << 20,
		ValidateMethod:        paymentmethod.Validate,
		VerifyReferenceEcho:   true,
		RequirePositiveRefund: true,
	})
}
