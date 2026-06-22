package main

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

// RS256 support for the gateway's hand-rolled JWT verifier.
//
// Why RS256: today every service shares one HS256 secret, so any compromised
// service can MINT platform-wide tokens. With RS256 only auth-service holds the
// private (signing) key; verifiers hold the PUBLIC key and can verify but never
// mint. This file lets the gateway verify RS256 tokens; HS256 stays supported in
// parallel so existing (long-lived) tokens keep working — no forced logout.

// parseRSAPublicKeyPEM accepts either a PKIX ("BEGIN PUBLIC KEY") or PKCS1
// ("BEGIN RSA PUBLIC KEY") PEM-encoded RSA public key.
func parseRSAPublicKeyPEM(pemStr string) (*rsa.PublicKey, error) {
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

// verifyRS256 checks an RS256 signature over signingInput using pub.
func verifyRS256(signingInput string, sig []byte, pub *rsa.PublicKey) error {
	if pub == nil {
		return errors.New("no RSA public key configured")
	}
	h := sha256.Sum256([]byte(signingInput))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], sig)
}
