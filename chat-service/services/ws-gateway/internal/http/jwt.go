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
)

func authenticateUserFromJWT(r *http.Request, jwtSecret string) (uuid.UUID, error) {
	secret := []byte(strings.TrimSpace(jwtSecret))
	if len(secret) == 0 {
		return uuid.Nil, errors.New("jwt secret not configured")
	}
	token := readBearerToken(r)
	if token == "" {
		return uuid.Nil, errors.New("missing bearer token")
	}
	userID, err := parseAndValidateJWT(token, secret)
	if err != nil {
		return uuid.Nil, err
	}
	return userID, nil
}

func readBearerToken(r *http.Request) string {
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(authz, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	}
	// Browser WebSocket clients cannot set Authorization headers, so allow query token.
	if q := strings.TrimSpace(r.URL.Query().Get("access_token")); q != "" {
		return q
	}
	return ""
}

func parseAndValidateJWT(token string, secret []byte) (uuid.UUID, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return uuid.Nil, errors.New("invalid token format")
	}

	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return uuid.Nil, errors.New("invalid token header")
	}
	payloadRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return uuid.Nil, errors.New("invalid token payload")
	}
	signatureRaw, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return uuid.Nil, errors.New("invalid token signature")
	}

	var header map[string]any
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return uuid.Nil, errors.New("invalid token header json")
	}
	alg, _ := header["alg"].(string)
	if alg != "HS256" {
		return uuid.Nil, errors.New("unsupported jwt algorithm")
	}

	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(signingInput))
	expected := mac.Sum(nil)
	if !hmac.Equal(signatureRaw, expected) {
		return uuid.Nil, errors.New("invalid token signature")
	}

	var payload map[string]any
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		return uuid.Nil, errors.New("invalid token payload json")
	}

	nowUnix := time.Now().Unix()
	if exp, ok := readNumericClaim(payload["exp"]); ok && nowUnix >= exp {
		return uuid.Nil, errors.New("token expired")
	}
	if nbf, ok := readNumericClaim(payload["nbf"]); ok && nowUnix < nbf {
		return uuid.Nil, errors.New("token not active yet")
	}

	idStr, _ := payload["sub"].(string)
	if idStr == "" {
		idStr, _ = payload["user_id"].(string)
	}
	if idStr == "" {
		return uuid.Nil, errors.New("missing subject claim")
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return uuid.Nil, errors.New("invalid subject claim")
	}
	return id, nil
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
