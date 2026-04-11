package service

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type MiniAppSessionResponse struct {
	AppID              string   `json:"app_id"`
	UserID             string   `json:"user_id"`
	TokenType          string   `json:"token_type"`
	AccessToken        string   `json:"access_token"`
	ExpiresAt          string   `json:"expires_at"`
	ExpiresIn          int64    `json:"expires_in"`
	Issuer             string   `json:"issuer"`
	Audience           string   `json:"audience"`
	GrantedPermissions []string `json:"granted_permissions"`
}

type MiniAppSessionClaims struct {
	jwt.RegisteredClaims
	AppID       string   `json:"app_id"`
	UserID      string   `json:"user_id"`
	Permissions []string `json:"permissions"`
	Type        string   `json:"typ"`
}

type JSONWebKey struct {
	Kty string `json:"kty"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	Kid string `json:"kid,omitempty"`
	N   string `json:"n,omitempty"`
	E   string `json:"e,omitempty"`
}

type JSONWebKeySet struct {
	Keys []JSONWebKey `json:"keys"`
}

type MiniAppSessionSigner struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
	issuer     string
	keyID      string
	ttl        time.Duration
	jwks       JSONWebKeySet
}

func NewMiniAppSessionSigner(cfg *config.Config, logger *slog.Logger) (*MiniAppSessionSigner, error) {
	if logger == nil {
		logger = slog.Default()
	}

	privateKey, err := loadMiniAppPrivateKey(cfg.MiniAppSessionPrivateKey)
	if err != nil {
		return nil, err
	}
	if privateKey == nil {
		logger.Warn("MINI_APP_SESSION_PRIVATE_KEY_PEM not configured; generating ephemeral mini app session key")
		privateKey, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, fmt.Errorf("generate mini app rsa key: %w", err)
		}
	}

	publicKey := &privateKey.PublicKey
	jwk := JSONWebKey{
		Kty: "RSA",
		Use: "sig",
		Alg: "RS256",
		Kid: cfg.MiniAppSessionKeyID,
		N:   base64.RawURLEncoding.EncodeToString(publicKey.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(bigEndianInt(publicKey.E)),
	}

	return &MiniAppSessionSigner{
		privateKey: privateKey,
		publicKey:  publicKey,
		issuer:     cfg.MiniAppSessionIssuer,
		keyID:      cfg.MiniAppSessionKeyID,
		ttl:        cfg.MiniAppSessionTTL,
		jwks:       JSONWebKeySet{Keys: []JSONWebKey{jwk}},
	}, nil
}

func (s *MiniAppSessionSigner) Issue(appID, userID uuid.UUID, grantedPermissions []string) (*MiniAppSessionResponse, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(s.ttl)
	audience := "mini-app:" + appID.String()

	claims := MiniAppSessionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   userID.String(),
			Audience:  jwt.ClaimStrings{audience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
		AppID:       appID.String(),
		UserID:      userID.String(),
		Permissions: append([]string(nil), grantedPermissions...),
		Type:        "mini_app_session",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = s.keyID

	signedToken, err := token.SignedString(s.privateKey)
	if err != nil {
		return nil, fmt.Errorf("sign mini app session token: %w", err)
	}

	return &MiniAppSessionResponse{
		AppID:              appID.String(),
		UserID:             userID.String(),
		TokenType:          "Bearer",
		AccessToken:        signedToken,
		ExpiresAt:          expiresAt.Format(time.RFC3339),
		ExpiresIn:          int64(time.Until(expiresAt).Seconds()),
		Issuer:             s.issuer,
		Audience:           audience,
		GrantedPermissions: append([]string(nil), grantedPermissions...),
	}, nil
}

func (s *MiniAppSessionSigner) JWKS() *JSONWebKeySet {
	keys := make([]JSONWebKey, len(s.jwks.Keys))
	copy(keys, s.jwks.Keys)
	return &JSONWebKeySet{Keys: keys}
}

func loadMiniAppPrivateKey(raw string) (*rsa.PrivateKey, error) {
	pemValue := strings.TrimSpace(strings.ReplaceAll(raw, "\\n", "\n"))
	if pemValue == "" {
		return nil, nil
	}

	block, _ := pem.Decode([]byte(pemValue))
	if block == nil {
		return nil, fmt.Errorf("invalid mini app private key pem")
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse mini app private key: %w", err)
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("mini app private key must be rsa")
	}
	return rsaKey, nil
}

func bigEndianInt(value int) []byte {
	if value == 0 {
		return []byte{0}
	}

	var bytes []byte
	for value > 0 {
		bytes = append([]byte{byte(value & 0xff)}, bytes...)
		value >>= 8
	}
	return bytes
}
