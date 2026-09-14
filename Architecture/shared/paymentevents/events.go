// Package paymentevents is the consumer side of payments-service's events:
// typed payloads, a dispatcher, the money check a consumer runs before it
// applies a payment, and the transactional dedupe contract.
//
// # Transport
//
// payments-service writes every event to its transactional outbox in the
// same database transaction as the state change, and relays it to Kafka topic
// social.events.v1 inside a shared events.EventEnvelope:
//
//	event_id       unique per event; a consumer's durable dedupe key
//	event_type     one of the four types below
//	occurred_at    when payments-service enqueued it
//	trace_id       propagation id, may be empty
//	actor_user_id  the payer for payment.succeeded / payment.failed; absent
//	               on refund events
//	payload        the JSON object described below
//
// The topic is shared by every domain. A consumer MUST ignore event types it
// does not know without returning an error (Dispatch does), and MUST ignore a
// payment whose reference_type is not its own. Delivery is at least once:
// dedupe on event_id in the same transaction as the effect (ApplyOnce).
//
// # Applications
//
// Every payment belongs to one application (Feast, MStore, …). Each payload
// below has an `application_id` field, populated once payments-service stores
// application_id; today's events do not carry it and it decodes as empty. A
// consumer can pass ForApplication to Dispatch to ignore events stamped with
// another application. It is off unless passed, and even when passed an event
// with no application_id is still delivered, so it changes nothing until
// payments-service starts stamping events.
//
// # payment.succeeded and payment.failed
//
// Published when a signature-verified provider webhook (or the reconciler)
// moves an intent to succeeded or failed. The payload is the intent row:
//
//	id                     intent id (uuid)
//	payer_id               the paying user (uuid)
//	payee_id               the receiving party (uuid; commerce: sellers.id,
//	                       food: the restaurant owner's user id)
//	reference_type         owning domain's record type: order | food_order | …
//	reference_id           owning domain's record id (uuid)
//	amount_minor           intent amount, integer minor units (paise)
//	amount                 DEPRECATED float mirror in major units. Never read
//	                       it; Payment does not declare it on purpose.
//	currency               ISO 4217, e.g. INR
//	method                 upi | card
//	status                 succeeded | failed
//	provider_ref           the provider ORDER id (omitted when empty)
//	upi_intent_url         omitted when empty
//	idempotency_key        the key the intent was created with
//	refunded_amount_minor  total refunded so far
//	owner_domain           the service that created the intent (omitted when empty)
//	created_at, updated_at RFC 3339 timestamps
//	application_id         populated once payments-service stores application_id
//
// # payment.refunded
//
// Published when a refund settles: a provider refund webhook, or an operator
// resolving a parked refund as refunded_manually. Payload:
//
//	id, intent_id          both the intent id (uuid)
//	provider               the intent's provider (razorpay | cashfree | …)
//	provider_refund_id     the provider's refund id; manual:<command_id> for a
//	                       manual resolution
//	amount_minor           THIS refund's amount, integer minor units
//	status                 the intent's status after the refund:
//	                       refunded | partially_refunded
//	reference_type         the intent's reference type
//	reference_id           the intent's reference id
//	command_id             present only when manual: the refund command
//	manual                 present and true only for an operator's manual
//	                       resolution; no provider refund exists
//	application_id         populated once payments-service stores application_id
//
// # payment.refund_failed
//
// Published once, in the transaction that parks a refund command in
// needs_attention: the refund was NOT placed and the money is still owed.
// Payload:
//
//	id, intent_id          both the intent id (uuid)
//	command_id             the parked refund command (uuid)
//	reference_type         the intent's reference type (may be empty)
//	reference_id           the intent's reference id (may be empty)
//	provider               the provider the command targeted
//	amount_minor           the refund's amount, integer minor units
//	currency               ISO 4217
//	reason_code            closed vocabulary from payments' obs package
//	reason                 REDACTED reason: status, provider error code and a
//	                       bounded description; never a raw provider body,
//	                       a credential or a signature
//	status                 always needs_attention
//	application_id         populated once payments-service stores application_id
package paymentevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/atpost/shared/events"
)

// The four payment event types. Aliases of the shared events constants.
const (
	TypeSucceeded    = events.EventPaymentSucceeded
	TypeFailed       = events.EventPaymentFailed
	TypeRefunded     = events.EventPaymentRefunded
	TypeRefundFailed = events.EventPaymentRefundFailed
)

// ErrMalformed wraps a payload that does not decode. It will never decode,
// so a consumer should park it (kafka.Permanent) rather than retry it.
var ErrMalformed = errors.New("paymentevents: malformed payment payload")

// IsPaymentEvent reports whether eventType is one of the four payment types.
func IsPaymentEvent(eventType string) bool {
	switch eventType {
	case TypeSucceeded, TypeFailed, TypeRefunded, TypeRefundFailed:
		return true
	}
	return false
}

// Payment is the intent row carried by payment.succeeded and payment.failed.
// IDs are strings: each consumer decides how to treat one that does not
// parse. The deprecated float `amount` is deliberately not declared.
type Payment struct {
	ID            string `json:"id"`
	PayerID       string `json:"payer_id"`
	PayeeID       string `json:"payee_id"`
	ReferenceType string `json:"reference_type"`
	ReferenceID   string `json:"reference_id"`
	AmountMinor   int64  `json:"amount_minor"`
	Currency      string `json:"currency"`
	Method        string `json:"method"`
	Status        string `json:"status"`
	ProviderRef   string `json:"provider_ref,omitempty"`
	// ApplicationID is populated once payments-service stores application_id.
	ApplicationID string `json:"application_id,omitempty"`
}

// Succeeded is the payment.succeeded payload.
type Succeeded struct{ Payment }

// Failed is the payment.failed payload.
type Failed struct{ Payment }

// Refunded is the payment.refunded payload.
type Refunded struct {
	ID               string `json:"id"`
	IntentID         string `json:"intent_id"`
	Provider         string `json:"provider"`
	ProviderRefundID string `json:"provider_refund_id"`
	AmountMinor      int64  `json:"amount_minor"`
	Status           string `json:"status"`
	ReferenceType    string `json:"reference_type"`
	ReferenceID      string `json:"reference_id"`
	CommandID        string `json:"command_id,omitempty"`
	Manual           bool   `json:"manual,omitempty"`
	// ApplicationID is populated once payments-service stores application_id.
	ApplicationID string `json:"application_id,omitempty"`
}

// Intent is the refunded intent: `id`, or `intent_id` when `id` is absent.
func (r Refunded) Intent() string {
	if r.ID != "" {
		return r.ID
	}
	return r.IntentID
}

// RefundFailed is the payment.refund_failed payload.
type RefundFailed struct {
	ID            string `json:"id"`
	IntentID      string `json:"intent_id"`
	CommandID     string `json:"command_id"`
	ReferenceType string `json:"reference_type"`
	ReferenceID   string `json:"reference_id"`
	Provider      string `json:"provider"`
	AmountMinor   int64  `json:"amount_minor"`
	Currency      string `json:"currency"`
	ReasonCode    string `json:"reason_code"`
	Reason        string `json:"reason"`
	Status        string `json:"status"`
	// ApplicationID is populated once payments-service stores application_id.
	ApplicationID string `json:"application_id,omitempty"`
}

// Handler receives decoded payment events. Embed NopHandler to implement
// only the methods a consumer cares about.
type Handler interface {
	OnSucceeded(ctx context.Context, env *events.EventEnvelope, ev Succeeded) error
	OnFailed(ctx context.Context, env *events.EventEnvelope, ev Failed) error
	OnRefunded(ctx context.Context, env *events.EventEnvelope, ev Refunded) error
	OnRefundFailed(ctx context.Context, env *events.EventEnvelope, ev RefundFailed) error
}

// NopHandler is the no-op default for every Handler method.
type NopHandler struct{}

func (NopHandler) OnSucceeded(context.Context, *events.EventEnvelope, Succeeded) error { return nil }
func (NopHandler) OnFailed(context.Context, *events.EventEnvelope, Failed) error       { return nil }
func (NopHandler) OnRefunded(context.Context, *events.EventEnvelope, Refunded) error   { return nil }
func (NopHandler) OnRefundFailed(context.Context, *events.EventEnvelope, RefundFailed) error {
	return nil
}

// Option narrows Dispatch.
type Option func(*dispatchConfig)

type dispatchConfig struct {
	only        map[string]bool
	application string
}

// Only restricts Dispatch to the given payment types. Any other type,
// payment or not, is ignored exactly like an unknown one: not decoded, no
// error. A consumer that has never looked at a type should not start parking
// malformed copies of it.
func Only(eventTypes ...string) Option {
	only := make(map[string]bool, len(eventTypes))
	for _, t := range eventTypes {
		only[t] = true
	}
	return func(c *dispatchConfig) { c.only = only }
}

// ForApplication ignores (nil, handler not called) an event whose
// application_id is set and is not applicationID. An event without an
// application_id, which is every event until payments-service stores it, is
// still delivered. Without this option nothing is filtered by application.
func ForApplication(applicationID string) Option {
	return func(c *dispatchConfig) { c.application = applicationID }
}

// BelongsToApplication is the ForApplication rule for a consumer that does not
// use Dispatch: true when the event names no application, or names this one.
func BelongsToApplication(eventApplicationID, applicationID string) bool {
	return eventApplicationID == "" || eventApplicationID == applicationID
}

func (c dispatchConfig) skip(eventApplicationID string) bool {
	return c.application != "" && !BelongsToApplication(eventApplicationID, c.application)
}

// Dispatch decodes one envelope and calls the matching Handler method.
//
// An unknown event type (or one excluded by Only) returns nil without
// decoding. A payload that does not decode returns an error wrapping
// ErrMalformed and calls nothing. An event excluded by ForApplication returns
// nil. Otherwise the handler's error is returned unchanged.
func Dispatch(ctx context.Context, env *events.EventEnvelope, h Handler, opts ...Option) error {
	if env == nil || !IsPaymentEvent(env.EventType) {
		return nil
	}
	var cfg dispatchConfig
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.only != nil && !cfg.only[env.EventType] {
		return nil
	}
	switch env.EventType {
	case TypeSucceeded:
		var ev Succeeded
		if err := decode(env, &ev); err != nil {
			return err
		}
		if cfg.skip(ev.ApplicationID) {
			return nil
		}
		return h.OnSucceeded(ctx, env, ev)
	case TypeFailed:
		var ev Failed
		if err := decode(env, &ev); err != nil {
			return err
		}
		if cfg.skip(ev.ApplicationID) {
			return nil
		}
		return h.OnFailed(ctx, env, ev)
	case TypeRefunded:
		var ev Refunded
		if err := decode(env, &ev); err != nil {
			return err
		}
		if cfg.skip(ev.ApplicationID) {
			return nil
		}
		return h.OnRefunded(ctx, env, ev)
	default: // TypeRefundFailed
		var ev RefundFailed
		if err := decode(env, &ev); err != nil {
			return err
		}
		if cfg.skip(ev.ApplicationID) {
			return nil
		}
		return h.OnRefundFailed(ctx, env, ev)
	}
}

func decode(env *events.EventEnvelope, out any) error {
	if err := json.Unmarshal(env.Payload, out); err != nil {
		return fmt.Errorf("%w: %s event %s: %v", ErrMalformed, env.EventType, env.EventID, err)
	}
	return nil
}
