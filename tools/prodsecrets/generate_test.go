package main

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"regexp"
	"strings"
	"testing"
)

func TestScalarGeneratorFormats(t *testing.T) {
	g := Generator{}
	hexRe := regexp.MustCompile(`^[0-9a-f]+$`)
	for _, name := range []string{GenHex32, GenHex64, GenTOTPHex64} {
		v, err := g.Scalar(name)
		if err != nil {
			t.Fatal(err)
		}
		want := 64
		if name == GenHex32 {
			want = 32
		}
		if len(v) != want || !hexRe.MatchString(v) {
			t.Errorf("%s: %q is not %d lowercase hex chars", name, v, want)
		}
		if b, _ := hex.DecodeString(v); len(b) != want/2 {
			t.Errorf("%s: decodes to %d bytes", name, len(b))
		}
	}
	pw, err := g.Scalar(GenPassword)
	if err != nil {
		t.Fatal(err)
	}
	if len(pw) != 32 || !regexp.MustCompile(`^[A-Za-z0-9]{32}$`).MatchString(pw) {
		t.Errorf("password %q", pw)
	}
	ring, err := g.Scalar(GenPIIKeyring)
	if err != nil {
		t.Fatal(err)
	}
	ver, key, ok := strings.Cut(ring, ":")
	if !ok || ver != "v1" {
		t.Fatalf("keyring %q", ring)
	}
	if raw, err := base64.StdEncoding.DecodeString(key); err != nil || len(raw) != 32 {
		t.Fatalf("keyring key must be base64 of 32 bytes: %v len=%d", err, len(raw))
	}
	if _, err := g.Scalar(GenEd25519); err == nil {
		t.Error("keypair generator must not be a scalar")
	}
	if _, err := g.Scalar("nope"); err == nil {
		t.Error("unknown generator must fail")
	}
	// Two draws differ.
	a, _ := g.Scalar(GenHex64)
	b, _ := g.Scalar(GenHex64)
	if a == b {
		t.Error("generator returned the same value twice")
	}
}

func TestPasswordUsesWholeAlphabetUniformly(t *testing.T) {
	g := Generator{}
	seen := map[byte]int{}
	for i := 0; i < 200; i++ {
		pw, _ := g.password(32)
		for j := 0; j < len(pw); j++ {
			seen[pw[j]]++
		}
	}
	if len(seen) != len(passwordAlphabet) {
		t.Fatalf("only %d of %d symbols seen in 6400 draws", len(seen), len(passwordAlphabet))
	}
}

func TestEd25519PairIsTheFormatServicetokenReads(t *testing.T) {
	pair, err := Generator{}.Pair(GenEd25519)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := base64.StdEncoding.DecodeString(pair.Private)
	if err != nil || len(seed) != ed25519.SeedSize {
		t.Fatalf("private must be std base64 of a 32-byte seed: %v len=%d", err, len(seed))
	}
	pub, err := base64.StdEncoding.DecodeString(pair.Public)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		t.Fatalf("public must be std base64 of 32 bytes: %v len=%d", err, len(pub))
	}
	priv := ed25519.NewKeyFromSeed(seed)
	msg := []byte("service token")
	if !ed25519.Verify(ed25519.PublicKey(pub), msg, ed25519.Sign(priv, msg)) {
		t.Fatal("public half does not verify a signature from the private half")
	}
}

func TestRSAPairIsPKCS1PrivateAndPKIXPublic(t *testing.T) {
	pair, err := Generator{}.Pair(GenRSA2048)
	if err != nil {
		t.Fatal(err)
	}
	pb, _ := pem.Decode([]byte(pair.Private))
	if pb == nil || pb.Type != "RSA PRIVATE KEY" {
		t.Fatalf("private PEM type: %v", pb)
	}
	priv, err := x509.ParsePKCS1PrivateKey(pb.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if priv.N.BitLen() != 2048 {
		t.Fatalf("bits %d", priv.N.BitLen())
	}
	ub, _ := pem.Decode([]byte(pair.Public))
	if ub == nil || ub.Type != "PUBLIC KEY" {
		t.Fatalf("public PEM type: %v", ub)
	}
	pubAny, err := x509.ParsePKIXPublicKey(ub.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub := pubAny.(*rsa.PublicKey)
	digest := sha256.Sum256([]byte("token"))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatal("public half does not verify the private half's signature")
	}
}

func TestPairFromPrivateRebuildsPublic(t *testing.T) {
	g := Generator{}
	for _, gen := range []string{GenEd25519, GenRSA2048} {
		pair, _ := g.Pair(gen)
		rebuilt, err := pairFromPrivate(gen, pair.Private)
		if err != nil {
			t.Fatalf("%s: %v", gen, err)
		}
		if rebuilt.Public != pair.Public {
			t.Errorf("%s: rebuilt public half differs", gen)
		}
	}
	if _, err := pairFromPrivate(GenEd25519, "not base64!"); err == nil {
		t.Error("garbage must fail")
	}
}
