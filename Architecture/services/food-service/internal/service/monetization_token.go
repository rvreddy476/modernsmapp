package service

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/shared/servicetoken"
)

// Feast wallet payments and their refunds go through monetization-service's
// POST /v1/monetization/internal/charge-and-credit. Since the security sweep
// of 21 Oct 2026 (#17) that route needs, besides the internal key, a signed
// food-service token (aud monetization, scope
// monetization:charge-and-credit): the shared key is stamped by the gateway
// on every proxied request and held by every pod, so it never said who was
// moving the money.
//
// The token is signed with food's existing service key — the one it already
// presents to payments-service (FOOD_SERVICE_TOKEN_KEY / _KID) — and
// monetization registers food-service's public key under SERVICE_CALLERS.

const (
	// monetizationAudience is the audience monetization-service accepts.
	monetizationAudience = "monetization"
	// OpMonetizationChargeAndCredit is the one operation food asks for.
	OpMonetizationChargeAndCredit = "monetization:charge-and-credit" //nolint:gosec // G101: service-token operation name, not a credential
	// monetizationTokenTTL bounds each minted token; one call, one token.
	monetizationTokenTTL = time.Minute
	// monetizationServiceAuthHeader carries the token (monetization's
	// ServiceAuthHeader).
	monetizationServiceAuthHeader = "X-Service-Authorization"
)

// ErrMonetizationTokenRequired: no signing key, so a wallet charge or refund
// cannot be authorised and is refused before any call.
var ErrMonetizationTokenRequired = errors.New("FOOD_SERVICE_TOKEN_KEY is required for wallet payments and refunds")

// MonetizationSignerFromEnv builds food's signer for monetization from
// FOOD_SERVICE_TOKEN_KEY / FOOD_SERVICE_TOKEN_KID (default kid as for
// payments). A blank key returns (nil, nil): wallet calls are then refused.
func MonetizationSignerFromEnv(getenv func(string) string) (*servicetoken.Signer, error) {
	key := strings.TrimSpace(getenv("FOOD_SERVICE_TOKEN_KEY"))
	if key == "" {
		return nil, nil
	}
	kid := strings.TrimSpace(getenv("FOOD_SERVICE_TOKEN_KID"))
	if kid == "" {
		kid = payments.DefaultTokenKID
	}
	signer, err := servicetoken.NewSignerFromBase64("food-service", kid, key)
	if err != nil {
		return nil, fmt.Errorf("FOOD_SERVICE_TOKEN_KEY: %w", err)
	}
	return signer, nil
}

// WithMonetizationSigner wires the signer wallet charges and refunds mint
// their monetization token with.
func (s *Service) WithMonetizationSigner(signer *servicetoken.Signer) *Service {
	s.monetizationSigner = signer
	return s
}

// authorizeChargeAndCredit stamps one freshly minted charge-and-credit token
// on req, or refuses when food has no key.
func (s *Service) authorizeChargeAndCredit(req *http.Request) error {
	if s.monetizationSigner == nil {
		return ErrMonetizationTokenRequired
	}
	tok, err := s.monetizationSigner.Mint(monetizationAudience, "charge-and-credit",
		[]string{OpMonetizationChargeAndCredit}, nil, monetizationTokenTTL)
	if err != nil {
		return fmt.Errorf("mint monetization token: %w", err)
	}
	req.Header.Set(monetizationServiceAuthHeader, "Bearer "+tok)
	return nil
}
