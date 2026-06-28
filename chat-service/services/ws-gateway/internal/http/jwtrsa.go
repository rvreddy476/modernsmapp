package http

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

// RS256 verification support (additive; HS256 stays in parallel). The ws-gateway
// holds only the PUBLIC key — it can verify tokens but never mint them.

// ParseRSAPublicKeyPEM parses a PKIX or PKCS1 PEM-encoded RSA public key.
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

func verifyRS256(signingInput string, sig []byte, pub *rsa.PublicKey) error {
	if pub == nil {
		return errors.New("no RSA public key configured")
	}
	h := sha256.Sum256([]byte(signingInput))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], sig)
}
