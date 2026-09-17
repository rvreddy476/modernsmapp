// Package tokentest mints service tokens for TESTS of identity's verifier and
// its token-only admin routes. It is the shared package's Signer, reduced:
// production identity never signs a service token, so the signer lives here,
// out of the production import graph, and stays wire-compatible with
// Architecture/shared/servicetoken (same header, claim order and framing).
package tokentest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/atpost/identity-auth-service/internal/servicetoken"
)

// Keypair is one caller's Ed25519 key pair with the public half as base64,
// the shape a deployment registers.
type Keypair struct {
	Private ed25519.PrivateKey
	PubB64  string
}

// NewKeypair generates a fresh key pair.
func NewKeypair() Keypair {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return Keypair{Private: priv, PubB64: base64.StdEncoding.EncodeToString(pub)}
}

// Options adjusts one minted token. Zero values mean "as admin-service does
// it": iat now, nbf now-30s, exp now+60s, a random jti.
type Options struct {
	Issuer   string
	KID      string
	Subject  string
	Audience string
	Scope    []string
	Actor    string
	TTL      time.Duration
	// Now overrides the mint time.
	Now time.Time
	// NoJTI omits the jti claim.
	NoJTI bool
	// Alg overrides the header alg (to prove "none" and HS256 are refused).
	Alg string
}

// Mint signs a token with kp. Panics on a marshalling failure, which is a
// test bug, not a condition to handle.
func Mint(kp Keypair, o Options) string {
	if o.Alg == "" {
		o.Alg = servicetoken.Algorithm
	}
	if o.TTL == 0 {
		o.TTL = 60 * time.Second
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	claims := servicetoken.Claims{
		Issuer:    o.Issuer,
		Subject:   o.Subject,
		Audience:  o.Audience,
		Expiry:    now.Add(o.TTL).Unix(),
		NotBefore: now.Add(-30 * time.Second).Unix(),
		IssuedAt:  now.Unix(),
		Scope:     o.Scope,
		Actor:     o.Actor,
	}
	if !o.NoJTI {
		jti := make([]byte, 16)
		if _, err := rand.Read(jti); err != nil {
			panic(err)
		}
		claims.JTI = base64.RawURLEncoding.EncodeToString(jti)
	}
	hb, err := json.Marshal(servicetoken.Header{Alg: o.Alg, Typ: "JWT", KID: o.KID})
	if err != nil {
		panic(err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		panic(err)
	}
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	sig := ed25519.Sign(kp.Private, []byte(signing))
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}
