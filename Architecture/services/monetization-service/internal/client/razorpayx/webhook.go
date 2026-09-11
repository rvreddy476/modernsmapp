package razorpayx

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Webhook payloads — https://razorpay.com/docs/webhooks/payloads/x/payouts/
//
//	POST <our url>
//	  X-Razorpay-Signature: hex(HMAC-SHA256(webhook secret, raw body))
//	  X-Razorpay-Event-Id:  evt_00000000000001
//	  {"entity":"event","account_id":"acc_...","event":"payout.processed",
//	   "contains":["payout"],
//	   "payload":{"payout":{"entity":{payout entity}}},
//	   "created_at":1567674606}
//
// Events: payout.pending, payout.rejected, payout.queued, payout.initiated,
// payout.processed, payout.reversed, payout.failed, payout.updated. The
// rail keys off the entity's status, not the event name, so an event name
// it has never seen still converges correctly.
//
// Signature validation — https://razorpay.com/docs/webhooks/validate-test/

// Header names.
const (
	HeaderSignature = "X-Razorpay-Signature"
	HeaderEventID   = "X-Razorpay-Event-Id"
)

var (
	// ErrSignatureInvalid: the body was not signed with our secret, no
	// signature was sent, or no secret is configured (fail closed).
	ErrSignatureInvalid = errors.New("razorpayx: webhook signature invalid")
	// ErrMissingEventID: X-Razorpay-Event-Id is the dedupe key and an
	// empty one is unusable.
	ErrMissingEventID = errors.New("razorpayx: webhook event id missing")
	// ErrNotAPayoutEvent: a verified event that carries no payout entity.
	ErrNotAPayoutEvent = errors.New("razorpayx: webhook carries no payout")
)

// Event is one verified delivery.
type Event struct {
	ID     string
	Type   string
	Payout Payout
}

// VerifyWebhook authenticates the raw body and extracts the payout. Copied
// from payments-service/internal/gateway/razorpay_provider.go:149-172:
// verification is over the RAW body, the secret must be configured, and
// the event id header is required.
func VerifyWebhook(secret string, headers http.Header, rawBody []byte) (Event, error) {
	if secret == "" {
		// Fail CLOSED: a missing secret must not accept every unsigned
		// delivery.
		return Event{}, ErrSignatureInvalid
	}
	sig := headers.Get(HeaderSignature)
	if sig == "" {
		return Event{}, ErrSignatureInvalid
	}
	if !hmac.Equal([]byte(Sign(secret, rawBody)), []byte(sig)) {
		return Event{}, ErrSignatureInvalid
	}
	eventID := headers.Get(HeaderEventID)
	if eventID == "" {
		return Event{}, ErrMissingEventID
	}

	var env struct {
		Event   string `json:"event"`
		Payload struct {
			Payout struct {
				Entity payoutEntity `json:"entity"`
			} `json:"payout"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(rawBody, &env); err != nil {
		return Event{}, fmt.Errorf("razorpayx: decode webhook: %w", err)
	}
	if env.Payload.Payout.Entity.ID == "" {
		return Event{}, ErrNotAPayoutEvent
	}
	return Event{ID: eventID, Type: env.Event, Payout: env.Payload.Payout.Entity.toPayout()}, nil
}

// Sign is the hex HMAC-SHA256 RazorpayX puts in X-Razorpay-Signature.
// Exported for the stub and for tests.
func Sign(secret string, rawBody []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(rawBody)
	return hex.EncodeToString(mac.Sum(nil))
}
