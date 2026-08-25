package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-shared/roomauth"
	"github.com/google/uuid"
)

// Production chat pass — owner-issued conversation-room entitlements
// (directive §5.3). The protocol itself lives in chat-shared/roomauth so the
// gateway verifies with the exact same code the issuer signs with.
//
// Revocation: expiry bounds the window; member removal additionally publishes
// a subscription_revoked control frame to the removed user's personal channel
// so connected gateways drop the room immediately.

// SubscriptionEntitlement is the issuance response.
type SubscriptionEntitlement struct {
	Token          string    `json:"token"`
	ConversationID string    `json:"conversation_id"`
	ExpiresAt      time.Time `json:"expires_at"`
}

var errEntitlementDisabled = errors.New("entitlement secret is not configured")

// SetEntitlementSecret wires the HMAC secret shared with ws-gateway. Empty
// disables issuance (the personal channel remains the delivery path).
func (s *Service) SetEntitlementSecret(secret string) {
	s.entitlementSecret = secret
}

// IssueSubscriptionEntitlement validates ACTIVE membership and returns a
// signed, expiring, audience-bound room token.
func (s *Service) IssueSubscriptionEntitlement(ctx context.Context, userID, conversationID uuid.UUID) (*SubscriptionEntitlement, error) {
	if s.entitlementSecret == "" {
		return nil, errEntitlementDisabled
	}
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("not a conversation member")
	}
	// The membership generation (re-verification P0-4, hardened by the final
	// verification): tokens carry the active row's SEQUENCE generation —
	// allocated under the conversation lock, so ordered by serialization,
	// never by transaction-start clock — and a sever marker kills every
	// token of an equal-or-older generation while a legitimately rejoined
	// member's fresh token outranks any older marker. No marker clearing,
	// no clear/remove race, no clock inversion.
	gen, err := s.groupStore().GetMemberGen(ctx, conversationID, userID)
	if err != nil {
		if errors.Is(err, postgres.ErrNotAMember) {
			return nil, errors.New("not a conversation member")
		}
		return nil, err
	}
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	claims := roomauth.Claims{
		Version:        1,
		Subject:        userID.String(),
		ConversationID: conversationID.String(),
		Audience:       roomauth.Audience,
		ExpiresAt:      time.Now().Add(roomauth.TTL).Unix(),
		Nonce:          hex.EncodeToString(nonce),
		Gen:            gen,
	}
	token, err := roomauth.Sign(claims, s.entitlementSecret)
	if err != nil {
		return nil, err
	}
	return &SubscriptionEntitlement{
		Token:          token,
		ConversationID: conversationID.String(),
		ExpiresAt:      time.Unix(claims.ExpiresAt, 0),
	}, nil
}
