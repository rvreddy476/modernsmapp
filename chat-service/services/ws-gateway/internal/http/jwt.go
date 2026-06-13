package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

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
	if strings.TrimSpace(keys.ActiveSecret) == "" {
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
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return uuid.Nil, time.Time{}, errors.New("invalid token format")
	}

	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return uuid.Nil, time.Time{}, errors.New("invalid token header")
	}
	payloadRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return uuid.Nil, time.Time{}, errors.New("invalid token payload")
	}
	signatureRaw, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return uuid.Nil, time.Time{}, errors.New("invalid token signature")
	}

	var header map[string]any
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return uuid.Nil, time.Time{}, errors.New("invalid token header json")
	}
	alg, _ := header["alg"].(string)
	if alg != "HS256" {
		return uuid.Nil, time.Time{}, errors.New("unsupported jwt algorithm")
	}
	kid, _ := header["kid"].(string)
	secret, ok := keys.secretFor(kid)
	if !ok {
		return uuid.Nil, time.Time{}, errors.New("unknown kid")
	}

	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(signingInput))
	expected := mac.Sum(nil)
	if !hmac.Equal(signatureRaw, expected) {
		return uuid.Nil, time.Time{}, errors.New("invalid token signature")
	}

	var payload map[string]any
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		return uuid.Nil, time.Time{}, errors.New("invalid token payload json")
	}

	nowUnix := time.Now().Unix()
	var expAt time.Time
	if exp, ok := readNumericClaim(payload["exp"]); ok {
		if nowUnix >= exp {
			return uuid.Nil, time.Time{}, errors.New("token expired")
		}
		expAt = time.Unix(exp, 0)
	}
	if nbf, ok := readNumericClaim(payload["nbf"]); ok && nowUnix < nbf {
		return uuid.Nil, time.Time{}, errors.New("token not active yet")
	}

	idStr, _ := payload["sub"].(string)
	if idStr == "" {
		idStr, _ = payload["user_id"].(string)
	}
	if idStr == "" {
		return uuid.Nil, time.Time{}, errors.New("missing subject claim")
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return uuid.Nil, time.Time{}, errors.New("invalid subject claim")
	}
	return id, expAt, nil
}

func readNumericClaim(raw any) (int64, bool) {
	switch v := raw.(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}
