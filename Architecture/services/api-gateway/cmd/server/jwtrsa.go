package main

import (
	"crypto/rsa"

	"github.com/atpost/api-gateway/pkg/tokenpolicy"
)

// RS256 support for the gateway's hand-rolled JWT verifier.
//
// Why RS256: today every service shares one HS256 secret, so any compromised
// service can MINT platform-wide tokens. With RS256 only auth-service holds the
// private (signing) key; verifiers hold the PUBLIC key and can verify but never
// mint. This file lets the gateway verify RS256 tokens; HS256 stays supported in
// parallel so existing (long-lived) tokens keep working — no forced logout.

// Module 3 SR-1: the implementations moved to internal/tokenpolicy so
// auth-service's contract test can call the REAL gateway verifier instead of a
// copy of it. These stay as thin aliases because cmd/server is under a
// .gitignore rule that hides new files — editing tracked files is safe,
// adding new ones is not.

// parseRSAPublicKeyPEM accepts either a PKIX ("BEGIN PUBLIC KEY") or PKCS1
// ("BEGIN RSA PUBLIC KEY") PEM-encoded RSA public key.
func parseRSAPublicKeyPEM(pemStr string) (*rsa.PublicKey, error) {
	return tokenpolicy.ParseRSAPublicKeyPEM(pemStr)
}

// verifyRS256 checks an RS256 signature over signingInput using pub.
func verifyRS256(signingInput string, sig []byte, pub *rsa.PublicKey) error {
	return tokenpolicy.VerifyRS256(signingInput, sig, pub)
}
