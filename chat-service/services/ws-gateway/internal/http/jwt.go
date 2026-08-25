package http

import (
	"crypto/rsa"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/chat-shared/accessauth"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// JWTKeySet — C7. Picks the verifying secret by `kid` so a kid-rotation
// window can verify both old and new tokens. A token without a `kid`
// header (pre-C7) falls back to the active secret.
type JWTKeySet struct {
	ActiveKID      string
	ActiveSecret   string
	PreviousKID    string
	PreviousSecret string
	// RSAKeys (optional) verify RS256 tokens, keyed by `kid`. HS256 stays
	// active in parallel so pre-cutover tokens keep verifying.
	RSAKeys map[string]*rsa.PublicKey
	Policy  accessauth.Policy
}

func (k JWTKeySet) rsaFor(kid string) (*rsa.PublicKey, bool) {
	if len(k.RSAKeys) == 0 {
		return nil, false
	}
	if kid != "" {
		pub, ok := k.RSAKeys[kid]
		return pub, ok
	}
	if len(k.RSAKeys) == 1 {
		for _, pub := range k.RSAKeys {
			return pub, true
		}
	}
	return nil, false
}

func (k JWTKeySet) secretFor(kid string) ([]byte, bool) {
	active := strings.TrimSpace(k.ActiveSecret)
	if kid == "" || kid == k.ActiveKID {
		if active == "" {
			return nil, false
		}
		return []byte(active), true
	}
	prev := strings.TrimSpace(k.PreviousSecret)
	if prev != "" && kid == k.PreviousKID {
		return []byte(prev), true
	}
	return nil, false
}

func authenticateUserFromJWT(r *http.Request, jwtSecret string, allowQueryToken bool) (uuid.UUID, error) {
	userID, _, err := authenticateUserFromJWTWithExpiry(r, jwtSecret, allowQueryToken)
	return userID, err
}

// authenticateUserFromJWTWithExpiry is the same one-shot validation
// as authenticateUserFromJWT, plus returns the token's `exp` claim
// (or zero Time when the token omits exp — uncommon, but the parser
// tolerates it because the existing tests do).
//
// Used by handleWS so the connection can be closed when the JWT
// expires mid-session — without it the audit's C6 stays open: a
// revoked or expired token keeps the WS alive until the client
// disconnects on its own.
func authenticateUserFromJWTWithExpiry(r *http.Request, jwtSecret string, allowQueryToken bool) (uuid.UUID, time.Time, error) {
	return authenticateUserFromJWTWithKeys(r, JWTKeySet{ActiveSecret: jwtSecret}, allowQueryToken)
}

// authenticateUserFromJWTWithKeys is the C7 entry point. Callers with a
// rotation window construct a key set from JWT_KID / JWT_SECRET_PREVIOUS
// / JWT_KID_PREVIOUS and pass it here.
func authenticateUserFromJWTWithKeys(r *http.Request, keys JWTKeySet, allowQueryToken bool) (uuid.UUID, time.Time, error) {
	if strings.TrimSpace(keys.ActiveSecret) == "" && len(keys.RSAKeys) == 0 {
		return uuid.Nil, time.Time{}, errors.New("jwt secret not configured")
	}
	token := readBearerToken(r, allowQueryToken)
	if token == "" {
		return uuid.Nil, time.Time{}, errors.New("missing bearer token")
	}
	userID, exp, err := parseAndValidateJWTWithKeys(token, keys)
	if err != nil {
		return uuid.Nil, time.Time{}, err
	}
	return userID, exp, nil
}

func readBearerToken(r *http.Request, allowQueryToken bool) string {
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(authz, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	}

	if token := readSubprotocolBearerToken(r); token != "" {
		return token
	}

	if allowQueryToken {
		if q := strings.TrimSpace(r.URL.Query().Get("access_token")); q != "" {
			return q
		}
	}
	return ""
}

func readSubprotocolBearerToken(r *http.Request) string {
	for _, protocol := range websocket.Subprotocols(r) {
		switch {
		case strings.HasPrefix(protocol, "bearer."):
			return strings.TrimSpace(strings.TrimPrefix(protocol, "bearer."))
		case strings.HasPrefix(protocol, "jwt."):
			return strings.TrimSpace(strings.TrimPrefix(protocol, "jwt."))
		}
	}
	return ""
}

func parseAndValidateJWT(token string, secret []byte) (uuid.UUID, time.Time, error) {
	return parseAndValidateJWTWithKeys(token, JWTKeySet{ActiveSecret: string(secret)})
}

func parseAndValidateJWTWithKeys(token string, keys JWTKeySet) (uuid.UUID, time.Time, error) {
	policy := keys.Policy
	if !policy.Production && len(policy.AllowedIssuers) == 0 && policy.RequiredAudience == "" && !policy.AllowHS256 {
		policy = accessauth.Policy{AllowHS256: true, ClockSkew: time.Minute}
	}
	identity, err := accessauth.Verify(token, accessauth.KeySet{
		ActiveKID: keys.ActiveKID, ActiveSecret: keys.ActiveSecret,
		PreviousKID: keys.PreviousKID, PreviousSecret: keys.PreviousSecret,
		RSAKeys: keys.RSAKeys,
	}, policy, time.Now())
	if err != nil {
		return uuid.Nil, time.Time{}, err
	}
	id, err := uuid.Parse(identity.UserID)
	if err != nil {
		return uuid.Nil, time.Time{}, errors.New("invalid subject claim")
	}
	return id, identity.ExpiresAt, nil
}
