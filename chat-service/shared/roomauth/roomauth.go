// Package roomauth is the owner-issued conversation-room entitlement
// protocol shared by message-service (issuer) and ws-gateway (verifier) —
// the production chat pass's scoped-rooms foundation (directive §5.3).
//
// A client can never select a raw room id: it presents a token that the
// membership OWNER (message-service) signed, and the gateway checks issuer
// (shared HMAC secret), audience, subject, conversation, version and expiry
// before subscribing the connection to the conversation channel.
package roomauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TTL bounds one subscription token's life. Clients refresh before expiry; a
// removed member's token dies with it (plus the eager subscription_revoked
// control frame).
const TTL = 5 * time.Minute

// Audience pins tokens to the gateway.
const Audience = "ws-gateway"

// DenyTTL is how long a revocation marker must outlive the member removal.
// It exceeds TTL so every token minted before the removal is dead — by
// expiry or by marker — before the marker lapses (P0-4: a signed token is
// otherwise replayable after the eager subscription_revoked frame).
const DenyTTL = TTL + time.Minute

// DenyKey names the Redis marker the issuer writes on member removal and the
// gateway checks on every conversation.subscribe. Shared here so the two
// services can never drift apart on the key shape.
//
// Marker VALUE contract (re-verification fix, hardened by the final
// verification): the marker holds the sever GENERATION — a value from the
// issuer's membership sequence, allocated while the conversation row lock is
// held, so generation order follows the actual serialization of membership
// writes (transaction-start timestamps could invert under the interleaving
// "removal tx starts first, rejoin commits first"). The marker is written
// with a ratchet (only ever increased) and NEVER deleted: a rejoined member
// is admitted not by clearing the marker (the clear raced a concurrent
// second removal) but by presenting a token whose Gen — the membership
// row's join generation — is NEWER than the marker. The gateway denies when
// marker exists and token Gen <= marker value; a token without a Gen (0) is
// always older than any marker.
func DenyKey(conversationID, userID string) string {
	return "chatdeny:" + conversationID + ":" + userID
}

// Claims is the versioned token payload.
type Claims struct {
	Version        int    `json:"v"`
	Subject        string `json:"sub"`
	ConversationID string `json:"conv"`
	Audience       string `json:"aud"`
	ExpiresAt      int64  `json:"exp"`
	Nonce          string `json:"nonce"`
	// Gen is the membership generation this token was issued under: the
	// active membership row's join generation from the issuer's membership
	// sequence — the same sequence the deny marker's sever generation comes
	// from, both allocated under the conversation lock. See DenyKey.
	Gen int64 `json:"gen,omitempty"`
}

// DeniedByMarker is the single deny decision both services share: a marker
// (sever generation) kills every token issued under an older or equal
// membership generation. An unparsable marker denies — fail closed.
func DeniedByMarker(markerValue string, tokenGen int64) bool {
	severGen, err := strconv.ParseInt(markerValue, 10, 64)
	if err != nil {
		return true
	}
	return tokenGen <= severGen
}

// Sign encodes claims as base64url(payload) + "." + base64url(HMAC-SHA256).
func Sign(claims Claims, secret string) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + sig, nil
}

// Verify checks signature, structure, version, audience and expiry. The
// CALLER must additionally check Subject against the connection's
// authenticated user and ConversationID against the requested room.
func Verify(token, secret string, now time.Time) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, errors.New("malformed entitlement")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return nil, errors.New("entitlement signature mismatch")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("malformed entitlement payload")
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, errors.New("malformed entitlement claims")
	}
	if claims.Version != 1 {
		return nil, fmt.Errorf("unsupported entitlement version %d", claims.Version)
	}
	if claims.Audience != Audience {
		return nil, errors.New("entitlement audience mismatch")
	}
	if claims.Subject == "" || claims.ConversationID == "" {
		return nil, errors.New("entitlement missing identity fields")
	}
	if now.Unix() >= claims.ExpiresAt {
		return nil, errors.New("entitlement expired")
	}
	return &claims, nil
}
