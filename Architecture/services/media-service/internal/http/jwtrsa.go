package http

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// RS256 verification support (additive; HS256 stays in parallel). With RS256,
// this service holds only the PUBLIC key — it can verify tokens but never mint
// them, so a compromise here can't forge platform-wide credentials.

// ParseRSAPublicKeyPEM is exported so cmd/server can load the key at startup.
func ParseRSAPublicKeyPEM(pemStr string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("no PEM block found in public key")
	}
	if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaPub, ok := pub.(*rsa.PublicKey); ok {
			return rsaPub, nil
		}
		return nil, errors.New("PKIX key is not RSA")
	}
	if rsaPub, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return rsaPub, nil
	}
	return nil, fmt.Errorf("unsupported RSA public key format")
}

// RS256 key environment variables: the same names and rules as the
// api-gateway and chat-service/shared/accessauth. The previous pair exists
// for a key rotation; a token is picked by its `kid`, and one without a kid
// verifies only while exactly one key is configured.
const (
	EnvRSAPublicKey         = "JWT_PUBLIC_KEY_PEM"
	EnvRSAKID               = "JWT_RS256_KID"
	EnvRSAPublicKeyPrevious = "JWT_PUBLIC_KEY_PEM_PREVIOUS"
	EnvRSAKIDPrevious       = "JWT_RS256_KID_PREVIOUS"
	defaultRSAKID           = "rsa-1"
)

// RSAKeysFromEnv builds the RS256 key map from the current key and the
// optional previous one; nil when no current key is configured. A previous
// key without its kid, a previous kid equal to the current one, or a previous
// key with no current key is an error.
func RSAKeysFromEnv(getenv func(string) string) (map[string]*rsa.PublicKey, error) {
	curPEM := strings.TrimSpace(getenv(EnvRSAPublicKey))
	prevPEM := strings.TrimSpace(getenv(EnvRSAPublicKeyPrevious))
	prevKID := strings.TrimSpace(getenv(EnvRSAKIDPrevious))
	if curPEM == "" {
		if prevPEM != "" || prevKID != "" {
			return nil, fmt.Errorf("%s is set without %s", EnvRSAPublicKeyPrevious, EnvRSAPublicKey)
		}
		return nil, nil
	}
	curKID := strings.TrimSpace(getenv(EnvRSAKID))
	if curKID == "" {
		curKID = defaultRSAKID
	}
	cur, err := ParseRSAPublicKeyPEM(curPEM)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", EnvRSAPublicKey, err)
	}
	keys := map[string]*rsa.PublicKey{curKID: cur}
	if prevPEM == "" && prevKID == "" {
		return keys, nil
	}
	if prevPEM == "" || prevKID == "" {
		return nil, fmt.Errorf("%s and %s must be set together", EnvRSAPublicKeyPrevious, EnvRSAKIDPrevious)
	}
	if prevKID == curKID {
		return nil, fmt.Errorf("%s must differ from %s (%q): tokens are told apart by kid", EnvRSAKIDPrevious, EnvRSAKID, curKID)
	}
	prev, err := ParseRSAPublicKeyPEM(prevPEM)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", EnvRSAPublicKeyPrevious, err)
	}
	keys[prevKID] = prev
	return keys, nil
}

func verifyRS256(signingInput string, sig []byte, pub *rsa.PublicKey) error {
	if pub == nil {
		return errors.New("no RSA public key configured")
	}
	h := sha256.Sum256([]byte(signingInput))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], sig)
}
