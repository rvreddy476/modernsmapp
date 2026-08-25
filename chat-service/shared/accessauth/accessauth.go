// Package accessauth is the canonical access-token verifier for the chat
// workspace. It mirrors the production contract established by Module 3:
// auth-service is the only RS256 minter; downstream services hold only the
// public key and require exp/iss/aud/sid/type plus a UUID subject.
package accessauth

import (
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type KeySet struct {
	ActiveKID      string
	ActiveSecret   string
	PreviousKID    string
	PreviousSecret string
	RSAKeys        map[string]*rsa.PublicKey
}

type Policy struct {
	Production       bool
	AllowedIssuers   []string
	RequiredAudience string
	AllowHS256       bool
	ClockSkew        time.Duration
}

type Identity struct {
	UserID    string
	SessionID string
	Scopes    string
	ExpiresAt time.Time
}

type Claims struct {
	Sub       string `json:"sub"`
	UserID    string `json:"user_id"`
	Exp       int64  `json:"exp"`
	Nbf       int64  `json:"nbf"`
	Iat       int64  `json:"iat"`
	Iss       string `json:"iss"`
	Aud       any    `json:"aud"`
	Sid       string `json:"sid"`
	TokenType string `json:"typ"`
	Type      string `json:"type"`
	Scopes    string `json:"scopes"`
}

// LoadFromEnv returns both key material and policy. Production is inferred
// from APP_ENV, ENVIRONMENT, or ENV, matching the gateway/auth-service rule.
func LoadFromEnv() (KeySet, Policy, error) {
	production := isProductionEnv()
	policy := Policy{
		Production:       production,
		RequiredAudience: strings.TrimSpace(os.Getenv("JWT_AUDIENCE")),
		AllowHS256:       !production && !strings.EqualFold(strings.TrimSpace(os.Getenv("JWT_DISABLE_HS256")), "true"),
		ClockSkew:        time.Minute,
	}
	for _, issuer := range strings.Split(os.Getenv("JWT_ISSUER"), ",") {
		if issuer = strings.TrimSpace(issuer); issuer != "" {
			policy.AllowedIssuers = append(policy.AllowedIssuers, issuer)
		}
	}

	keys := KeySet{
		ActiveKID:      env("JWT_KID", "v1"),
		ActiveSecret:   os.Getenv("JWT_SECRET"),
		PreviousKID:    os.Getenv("JWT_KID_PREVIOUS"),
		PreviousSecret: os.Getenv("JWT_SECRET_PREVIOUS"),
	}
	if raw := strings.TrimSpace(os.Getenv("JWT_PUBLIC_KEY_PEM")); raw != "" {
		pub, err := ParseRSAPublicKeyPEM(raw)
		if err != nil {
			return keys, policy, fmt.Errorf("parse JWT_PUBLIC_KEY_PEM: %w", err)
		}
		keys.RSAKeys = map[string]*rsa.PublicKey{env("JWT_RS256_KID", "rsa-1"): pub}
	}

	if production {
		switch {
		case len(policy.AllowedIssuers) == 0:
			return keys, policy, errors.New("JWT_ISSUER is required in production")
		case policy.RequiredAudience == "":
			return keys, policy, errors.New("JWT_AUDIENCE is required in production")
		case len(keys.RSAKeys) == 0:
			return keys, policy, errors.New("JWT_PUBLIC_KEY_PEM is required in production")
		}
	}
	return keys, policy, nil
}

func Verify(raw string, keys KeySet, policy Policy, now time.Time) (Identity, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Identity{}, errors.New("malformed token")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Identity{}, errors.New("invalid token header")
	}
	var header struct {
		Alg string `json:"alg"`
		KID string `json:"kid"`
	}
	if json.Unmarshal(headerBytes, &header) != nil {
		return Identity{}, errors.New("invalid token header")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Identity{}, errors.New("invalid token signature")
	}
	signingInput := parts[0] + "." + parts[1]
	switch header.Alg {
	case "RS256":
		pub, ok := rsaFor(keys, header.KID)
		if !ok {
			return Identity{}, errors.New("unknown token key")
		}
		digest := sha256.Sum256([]byte(signingInput))
		if rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], signature) != nil {
			return Identity{}, errors.New("invalid token signature")
		}
	case "HS256":
		if !policy.AllowHS256 {
			return Identity{}, errors.New("HS256 is not accepted")
		}
		secret, ok := secretFor(keys, header.KID)
		if !ok || secret == "" {
			return Identity{}, errors.New("unknown token key")
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(signingInput))
		if !hmac.Equal(signature, mac.Sum(nil)) {
			return Identity{}, errors.New("invalid token signature")
		}
	default:
		return Identity{}, errors.New("unsupported token algorithm")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Identity{}, errors.New("invalid token payload")
	}
	var claims Claims
	if json.Unmarshal(payload, &claims) != nil {
		return Identity{}, errors.New("invalid token payload")
	}
	if err := validateClaims(claims, header.Alg, policy, now); err != nil {
		return Identity{}, err
	}
	userID := claims.Sub
	if userID == "" {
		userID = claims.UserID
	}
	return Identity{
		UserID:    userID,
		SessionID: claims.Sid,
		Scopes:    claims.Scopes,
		ExpiresAt: time.Unix(claims.Exp, 0),
	}, nil
}

func validateClaims(c Claims, alg string, p Policy, now time.Time) error {
	if p.ClockSkew <= 0 {
		p.ClockSkew = time.Minute
	}
	if p.Production && alg != "RS256" {
		return errors.New("production requires RS256")
	}
	if c.Exp == 0 {
		return errors.New("token has no exp claim")
	}
	if now.Add(-p.ClockSkew).Unix() > c.Exp {
		return errors.New("token expired")
	}
	if c.Nbf != 0 && now.Add(p.ClockSkew).Unix() < c.Nbf {
		return errors.New("token not active yet")
	}
	typ := c.TokenType
	if typ == "" {
		typ = c.Type
	}
	if typ != "" && !strings.EqualFold(typ, "access") && !strings.EqualFold(typ, "at+jwt") {
		return errors.New("token is not an access token")
	}
	if p.Production && typ == "" {
		return errors.New("token has no access type")
	}
	if len(p.AllowedIssuers) > 0 {
		allowed := false
		for _, issuer := range p.AllowedIssuers {
			allowed = allowed || c.Iss == issuer
		}
		if !allowed {
			return errors.New("token issuer is not allowed")
		}
	}
	if p.RequiredAudience != "" && !audienceContains(c.Aud, p.RequiredAudience) {
		return errors.New("token audience is not allowed")
	}
	userID := c.Sub
	if userID == "" {
		userID = c.UserID
	}
	if !isUUID(userID) {
		return errors.New("token subject is not a UUID")
	}
	if p.Production && strings.TrimSpace(c.Sid) == "" {
		return errors.New("token has no session id")
	}
	return nil
}

func ParseRSAPublicKeyPEM(raw string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	if parsed, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		pub, ok := parsed.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("public key is not RSA")
		}
		return pub, nil
	}
	if pub, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return pub, nil
	}
	return nil, errors.New("unsupported RSA public key format")
}

func isProductionEnv() bool {
	for _, key := range []string{"APP_ENV", "ENVIRONMENT", "ENV"} {
		value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
		switch value {
		case "prod", "production":
			return true
		case "":
			continue
		default:
			return false
		}
	}
	return false
}

func rsaFor(keys KeySet, kid string) (*rsa.PublicKey, bool) {
	if kid != "" {
		pub, ok := keys.RSAKeys[kid]
		return pub, ok
	}
	if len(keys.RSAKeys) == 1 {
		for _, pub := range keys.RSAKeys {
			return pub, true
		}
	}
	return nil, false
}

func secretFor(keys KeySet, kid string) (string, bool) {
	if kid == "" || kid == keys.ActiveKID {
		return keys.ActiveSecret, keys.ActiveSecret != ""
	}
	if kid == keys.PreviousKID && keys.PreviousSecret != "" {
		return keys.PreviousSecret, true
	}
	return "", false
}

func audienceContains(raw any, expected string) bool {
	switch value := raw.(type) {
	case string:
		return value == expected
	case []any:
		for _, item := range value {
			if text, ok := item.(string); ok && text == expected {
				return true
			}
		}
	}
	return false
}

func isUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, char := range value {
		switch index {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
				return false
			}
		}
	}
	return true
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
