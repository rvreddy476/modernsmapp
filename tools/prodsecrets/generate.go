package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
)

// Keypair holds both halves of a generated key in the encoding the services
// read: base64 (Ed25519 seed / public key) or PEM (RSA).
type Keypair struct {
	Private string `json:"private"`
	Public  string `json:"public"`
}

// Generator produces fresh values. Rand defaults to crypto/rand.
type Generator struct {
	Rand io.Reader
}

func (g Generator) rand() io.Reader {
	if g.Rand == nil {
		return rand.Reader
	}
	return g.Rand
}

func (g Generator) bytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(g.rand(), b); err != nil {
		return nil, fmt.Errorf("random source: %w", err)
	}
	return b, nil
}

// Scalar generates a single-string value for the named generator.
func (g Generator) Scalar(name string) (string, error) {
	switch name {
	case GenHex32:
		b, err := g.bytes(16)
		if err != nil {
			return "", err
		}
		return hex.EncodeToString(b), nil
	case GenHex64, GenTOTPHex64:
		b, err := g.bytes(32)
		if err != nil {
			return "", err
		}
		return hex.EncodeToString(b), nil
	case GenPassword:
		return g.password(32)
	case GenPIIKeyring:
		b, err := g.bytes(32)
		if err != nil {
			return "", err
		}
		return "v1:" + base64.StdEncoding.EncodeToString(b), nil
	default:
		return "", fmt.Errorf("%q is not a scalar generator", name)
	}
}

const passwordAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// password draws n characters from [A-Za-z0-9] with rejection sampling so
// every character is equally likely. The alphabet is URL- and DSN-safe.
func (g Generator) password(n int) (string, error) {
	out := make([]byte, 0, n)
	buf := make([]byte, 64)
	// 62 symbols: accept bytes below 248 (= 4*62) and take modulo 62.
	for len(out) < n {
		if _, err := io.ReadFull(g.rand(), buf); err != nil {
			return "", fmt.Errorf("random source: %w", err)
		}
		for _, b := range buf {
			if b >= 248 {
				continue
			}
			out = append(out, passwordAlphabet[int(b)%len(passwordAlphabet)])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}

// Pair generates a keypair for the named generator.
func (g Generator) Pair(name string) (Keypair, error) {
	switch name {
	case GenEd25519:
		pub, priv, err := ed25519.GenerateKey(g.rand())
		if err != nil {
			return Keypair{}, err
		}
		// The services accept a 32-byte seed or the 64-byte expanded key
		// (shared/servicetoken.NewSignerFromBase64); the seed is shorter.
		return Keypair{
			Private: base64.StdEncoding.EncodeToString(priv.Seed()),
			Public:  base64.StdEncoding.EncodeToString(pub),
		}, nil
	case GenRSA2048:
		key, err := rsa.GenerateKey(g.rand(), 2048)
		if err != nil {
			return Keypair{}, err
		}
		privPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
		pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
		if err != nil {
			return Keypair{}, err
		}
		pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
		return Keypair{Private: string(privPEM), Public: string(pubPEM)}, nil
	default:
		return Keypair{}, fmt.Errorf("%q is not a keypair generator", name)
	}
}
