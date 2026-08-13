package delivery

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Module 4 M4-P0-5 — delivery signing.
//
// The signature is verified against a REAL RSA key generated in the test, not
// asserted to be non-empty. A signing test that only checks "a Signature
// parameter is present" passes just as happily when the bytes are garbage, and
// the failure would first appear as CloudFront 403s in production.

func testSigner(t *testing.T) (*Signer, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	s, err := NewSigner(Config{
		CDNBaseURL:    "https://d111.cloudfront.net",
		KeyPairID:     "K2JCJMDEHXQW5F",
		PrivateKeyPEM: pemBytes,
	})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return s, key
}

// decodeCloudFrontBase64 reverses the URL-safe substitution.
func decodeCloudFrontBase64(t *testing.T, s string) []byte {
	t.Helper()
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "=")
	s = strings.ReplaceAll(s, "~", "/")
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	return raw
}

// THE ONE THAT MATTERS: the signature must verify against the key, over the
// exact canned policy CloudFront will reconstruct from the URL.
func TestProtectedSignatureVerifiesAgainstTheKey(t *testing.T) {
	s, key := testSigner(t)
	now := time.Unix(1_700_000_000, 0)

	raw, err := s.SignProtected(ProtectedPrefix+"stories/abc.jpg", 5*time.Minute, now)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	q := u.Query()

	expires, err := strconv.ParseInt(q.Get("Expires"), 10, 64)
	if err != nil {
		t.Fatalf("Expires is not an integer: %v", err)
	}
	if want := now.Add(5 * time.Minute).Unix(); expires != want {
		t.Fatalf("Expires=%d want %d", expires, want)
	}
	if q.Get("Key-Pair-Id") != "K2JCJMDEHXQW5F" {
		t.Fatalf("Key-Pair-Id=%q", q.Get("Key-Pair-Id"))
	}

	// Rebuild the policy exactly as CloudFront does: the resource is the URL
	// WITHOUT the query string.
	resource := u.Scheme + "://" + u.Host + u.Path
	policy, err := json.Marshal(cannedPolicy{
		Statement: []statement{{
			Resource:  resource,
			Condition: condition{DateLessThan: epoch{EpochTime: expires}},
		}},
	})
	if err != nil {
		t.Fatalf("marshal policy: %v", err)
	}
	sum := sha1.Sum(policy)
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA1, sum[:],
		decodeCloudFrontBase64(t, q.Get("Signature"))); err != nil {
		t.Fatalf("signature does not verify against the signing key: %v.\n"+
			"CloudFront would reject every protected fetch with 403.", err)
	}
}

// The TTL is the revocation window, so it must be enforced rather than
// clamped. A caller that asks for an hour has misunderstood something, and
// silently giving it five minutes hides the mistake.
func TestProtectedTTLIsBounded(t *testing.T) {
	s, _ := testSigner(t)
	now := time.Now()
	for _, ttl := range []time.Duration{0, -time.Second, MaxProtectedTTL + time.Second, time.Hour} {
		if _, err := s.SignProtected(ProtectedPrefix+"a.jpg", ttl, now); err == nil {
			t.Errorf("ttl %s was accepted; the signature would outlive its revocation window", ttl)
		}
	}
	if _, err := s.SignProtected(ProtectedPrefix+"a.jpg", MaxProtectedTTL, now); err != nil {
		t.Errorf("the maximum permitted ttl was rejected: %v", err)
	}
}

// An unknown prefix must be treated as protected. Defaulting the other way
// means any future key layout ships world-readable.
func TestUnknownPrefixIsProtected(t *testing.T) {
	for _, key := range []string{
		"stories/a.jpg", "", "avatars/x.png", "PUBLIC/a.jpg", "publicish/a.jpg",
	} {
		if got := ClassForKey(key); got != ClassProtected {
			t.Errorf("ClassForKey(%q)=%s, want protected. An unrecognised prefix must "+
				"never default to public delivery.", key, got)
		}
	}
	if got := ClassForKey(PublicPrefix + "a.jpg"); got != ClassPublic {
		t.Errorf("ClassForKey(public/a.jpg)=%s, want public", got)
	}
}

// The two issuance paths must refuse each other's keys. This is the guard that
// catches a caller passing the wrong key to the wrong function — the exact
// mistake that would republish protected bytes at a stable URL.
func TestIssuancePathsRefuseTheWrongClass(t *testing.T) {
	s, _ := testSigner(t)
	if _, err := s.PublicURL(ProtectedPrefix + "a.jpg"); err == nil {
		t.Error("a protected key was given a stable, unsigned public URL")
	}
	if _, err := s.SignProtected(PublicPrefix+"a.jpg", time.Minute, time.Now()); err == nil {
		t.Error("a public key was signed as protected")
	}
}

// No credential may appear in the URL. CloudFront caches on the full URL, so a
// token in the query both fragments the cache and lands in edge access logs.
func TestSignedURLCarriesNoCredential(t *testing.T) {
	s, _ := testSigner(t)
	raw, err := s.SignProtected(ProtectedPrefix+"stories/abc.jpg", time.Minute, time.Now())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	for _, banned := range []string{"Bearer", "authorization", "access_token", "session", "X-User-Id"} {
		if strings.Contains(strings.ToLower(raw), strings.ToLower(banned)) {
			t.Errorf("signed URL contains %q: %s", banned, raw)
		}
	}
	u, _ := url.Parse(raw)
	for k := range u.Query() {
		switch k {
		case "Expires", "Signature", "Key-Pair-Id":
		default:
			t.Errorf("unexpected query parameter %q in a signed URL; every parameter "+
				"becomes part of the CloudFront cache key", k)
		}
	}
}

// Misconfiguration must fail closed at construction, not degrade to unsigned
// delivery at request time.
func TestSignerRefusesIncompleteConfiguration(t *testing.T) {
	valid := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: func() []byte {
		k, _ := rsa.GenerateKey(rand.Reader, 2048)
		return x509.MarshalPKCS1PrivateKey(k)
	}()})

	cases := map[string]Config{
		"no CDN base":   {KeyPairID: "K", PrivateKeyPEM: valid},
		"no key pair":   {CDNBaseURL: "https://d1.cloudfront.net", PrivateKeyPEM: valid},
		"no key":        {CDNBaseURL: "https://d1.cloudfront.net", KeyPairID: "K"},
		"malformed key": {CDNBaseURL: "https://d1.cloudfront.net", KeyPairID: "K", PrivateKeyPEM: []byte("not a pem")},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewSigner(cfg); err == nil {
				t.Error("an incomplete signer configuration was accepted; media would " +
					"be delivered unsigned")
			}
		})
	}
}

// Two signatures for the same object must differ in expiry as time moves, so a
// URL cannot be treated as a durable capability.
func TestSignaturesAreTimeBound(t *testing.T) {
	s, _ := testSigner(t)
	base := time.Unix(1_700_000_000, 0)
	a, err := s.SignProtected(ProtectedPrefix+"a.jpg", time.Minute, base)
	if err != nil {
		t.Fatalf("sign a: %v", err)
	}
	b, err := s.SignProtected(ProtectedPrefix+"a.jpg", time.Minute, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("sign b: %v", err)
	}
	if a == b {
		t.Fatal("the same signed URL was issued an hour apart; it is a permanent capability")
	}
}
