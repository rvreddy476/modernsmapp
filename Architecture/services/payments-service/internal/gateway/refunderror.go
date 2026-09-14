package gateway

// What a provider refusal means for a refund: retry it, park it, or settle it.
//
// The refund worker used to treat every provider error as transient. That is
// right for an unreachable provider — the durable command exists for exactly
// that — and wrong for a refusal that can never succeed. A refund against a
// payment Razorpay does not know is a 400 on the first attempt and on the
// thousandth, so those commands retried for ever, filling last_error and the
// log without ever telling anyone the money was still owed.
//
// The classification lives here, next to the adapter that produces the error,
// so the worker branches on a class and never on a provider's wording.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrNotAPaymentID refuses a call to a provider payments route whose id is not
// a payment id — blank, or a provider ORDER id.
//
// Razorpay answers POST /v1/payments/order_…/refund with 400 on every attempt.
// That is how no refund could ever succeed: the worker sent the intent's order
// reference where the captured payment's id belonged. The adapter now refuses
// the request before it is built, so the mistake cannot leave the process.
var ErrNotAPaymentID = errors.New("gateway: refusing to call a provider payments route without a payment id")

// razorpayOrderIDPrefix begins every Razorpay order id, and every stub order id.
const razorpayOrderIDPrefix = "order_"

// guardPaymentPath refuses a `/payments/{id}…` path whose id is blank or is an
// order id. Other paths pass through untouched.
func guardPaymentPath(path string) error {
	rest, ok := strings.CutPrefix(path, "/payments/")
	if !ok {
		return nil
	}
	id, _, _ := strings.Cut(rest, "/")
	id, _, _ = strings.Cut(id, "?")
	switch {
	case strings.TrimSpace(id) == "":
		return fmt.Errorf("%w: the payment id is blank", ErrNotAPaymentID)
	case strings.HasPrefix(id, razorpayOrderIDPrefix):
		return fmt.Errorf("%w: %q is an order id, not a payment id", ErrNotAPaymentID, id)
	}
	return nil
}

// ProviderError is a provider's HTTP refusal, with its structured error body
// decoded.
type ProviderError struct {
	Provider   string
	Method     string
	Path       string
	HTTPStatus int
	// Code, Description, Reason, Source and Step are Razorpay's
	// `error.{code,description,reason,source,step}`. Any may be blank.
	Code        string
	Description string
	Reason      string
	Source      string
	Step        string

	raw string
}

// Error keeps the message the adapter has always returned, raw body included,
// for callers that log it locally. Anything that PERSISTS the error uses
// Redacted instead.
func (e *ProviderError) Error() string {
	return fmt.Sprintf("%s: %s %s returned %d: %s", e.Provider, e.Method, e.Path, e.HTTPStatus, e.raw)
}

// Redacted is the error as it may be stored: the status, the provider's error
// code and reason, and a bounded description. Never the raw body — its
// `metadata` echoes whatever the provider chooses to put there.
func (e *ProviderError) Redacted() string {
	s := fmt.Sprintf("%s: %s %s returned %d", e.Provider, e.Method, e.Path, e.HTTPStatus)
	if e.Code != "" || e.Reason != "" {
		s += fmt.Sprintf(" (code=%s reason=%s)", boundedText(e.Code, 64), boundedText(e.Reason, 64))
	}
	if e.Description != "" {
		s += ": " + boundedText(e.Description, 200)
	}
	return s
}

func newRazorpayError(method, path string, status int, raw []byte) *ProviderError {
	var body struct {
		Error struct {
			Code        string `json:"code"`
			Description string `json:"description"`
			Reason      string `json:"reason"`
			Source      string `json:"source"`
			Step        string `json:"step"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &body) // a non-JSON body still classifies by status
	return &ProviderError{
		Provider:    "razorpay",
		Method:      method,
		Path:        path,
		HTTPStatus:  status,
		Code:        body.Error.Code,
		Description: body.Error.Description,
		Reason:      body.Error.Reason,
		Source:      body.Error.Source,
		Step:        body.Error.Step,
		raw:         string(raw),
	}
}

// RefundErrorClass is what a refund attempt's error means.
type RefundErrorClass int

const (
	// RefundRetryable: the provider may yet accept it. The command stays
	// claimable and backs off; the idempotency key makes the retry safe.
	RefundRetryable RefundErrorClass = iota
	// RefundTerminal: a refusal that will never succeed. The command is parked
	// for a human and not attempted again.
	RefundTerminal
	// RefundAlreadyRefunded: the provider says the payment is already fully
	// refunded — the outcome the command wanted.
	RefundAlreadyRefunded
)

func (c RefundErrorClass) String() string {
	switch c {
	case RefundTerminal:
		return "terminal"
	case RefundAlreadyRefunded:
		return "already_refunded"
	default:
		return "retryable"
	}
}

// ClassifyRefundError sorts a refund-path error (placing a refund, listing an
// order's payments, listing a payment's refunds).
//
//	no HTTP response (connection, timeout, undecodable 2xx) → retryable
//	408, 409, 429, 5xx                                      → retryable
//	401, 403                                                → retryable (credentials are fixed by a deploy, not by the refund)
//	other 4xx, "fully refunded"                             → already refunded
//	other 4xx                                               → terminal
//	a payments route without a payment id                   → terminal
func ClassifyRefundError(err error) RefundErrorClass {
	if errors.Is(err, ErrNotAPaymentID) {
		return RefundTerminal
	}
	var pe *ProviderError
	if !errors.As(err, &pe) {
		// No provider verdict at all. An ambiguous timeout may even have
		// created the refund; the same idempotency key returns it on retry.
		return RefundRetryable
	}
	switch s := pe.HTTPStatus; {
	case s >= 500, s == http.StatusTooManyRequests, s == http.StatusRequestTimeout:
		return RefundRetryable
	case s == http.StatusConflict:
		// A request under the same idempotency key still in flight.
		return RefundRetryable
	case s == http.StatusUnauthorized, s == http.StatusForbidden:
		return RefundRetryable
	case s >= 400:
		if strings.Contains(strings.ToLower(pe.Description+" "+pe.Reason), "fully refunded") {
			return RefundAlreadyRefunded
		}
		// Non-existent payment id, payment not captured, amount above what is
		// refundable, a malformed request: none of these changes on retry.
		return RefundTerminal
	}
	return RefundRetryable
}

// RedactError renders any refund-path error for storage in last_error.
func RedactError(err error) string {
	if err == nil {
		return ""
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Redacted()
	}
	return boundedText(err.Error(), 300)
}

// boundedText flattens control characters and caps the length in runes.
func boundedText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
