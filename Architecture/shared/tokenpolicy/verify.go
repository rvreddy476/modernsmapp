package tokenpolicy

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
	"strings"
	"time"
)

// KeySet holds the verification material.
type KeySet struct {
	ActiveKID      string
	ActiveSecret   string
	PreviousKID    string
	PreviousSecret string
	// RSAKeys maps a `kid` to an RSA public key for verifying RS256 tokens.
	RSAKeys map[string]*rsa.PublicKey
}

// SecretFor resolves the HS256 secret for a kid.
func (k KeySet) SecretFor(kid string) (string, bool) {
	if kid == "" || kid == k.ActiveKID {
		return k.ActiveSecret, true
	}
	if k.PreviousSecret != "" && kid == k.PreviousKID {
		return k.PreviousSecret, true
	}
	return "", false
}

// RSAFor returns the RSA public key for a kid.
func (k KeySet) RSAFor(kid string) (*rsa.PublicKey, bool) {
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

// Identity is what a verified token yields.
type Identity struct {
	UserID   string
	Scopes   string
	DeviceID string
}

// Error is a verification failure.
type Error struct{ Msg string }

func (e *Error) Error() string { return "jwt: " + e.Msg }

// Verify parses, signature-checks and policy-checks a token.
func Verify(tokenStr string, keys KeySet, policy Policy, now time.Time) (Identity, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return Identity{}, &Error{"malformed token"}
	}

	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Identity{}, &Error{"invalid header encoding"}
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return Identity{}, &Error{"invalid header JSON"}
	}

	signingInput := parts[0] + "." + parts[1]
	actualSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Identity{}, &Error{"invalid signature encoding"}
	}

	switch header.Alg {
	case "HS256":
		secret, ok := keys.SecretFor(header.Kid)
		if !ok {
			return Identity{}, &Error{"unknown kid"}
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(signingInput))
		if !hmac.Equal(mac.Sum(nil), actualSig) {
			return Identity{}, &Error{"signature verification failed"}
		}
	case "RS256":
		pub, ok := keys.RSAFor(header.Kid)
		if !ok {
			return Identity{}, &Error{"unknown kid"}
		}
		if err := VerifyRS256(signingInput, actualSig, pub); err != nil {
			return Identity{}, &Error{"signature verification failed"}
		}
	default:
		return Identity{}, &Error{"unsupported jwt algorithm"}
	}

	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Identity{}, &Error{"invalid payload encoding"}
	}
	var claims Claims
	if err := json.Unmarshal(data, &claims); err != nil {
		return Identity{}, &Error{"invalid payload JSON"}
	}

	if err := ValidateClaims(claims, header.Alg, policy, now); err != nil {
		return Identity{}, &Error{err.Error()}
	}

	userID := claims.Sub
	if userID == "" {
		userID = claims.UserID
	}
	return Identity{UserID: userID, Scopes: claims.Scopes, DeviceID: claims.DeviceID}, nil
}

// ParseRSAPublicKeyPEM accepts a PKIX ("BEGIN PUBLIC KEY") or PKCS1
// ("BEGIN RSA PUBLIC KEY") PEM-encoded RSA public key.
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

// VerifyRS256 checks an RS256 signature over signingInput using pub.
func VerifyRS256(signingInput string, sig []byte, pub *rsa.PublicKey) error {
	if pub == nil {
		return errors.New("no RSA public key configured")
	}
	h := sha256.Sum256([]byte(signingInput))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], sig)
}
