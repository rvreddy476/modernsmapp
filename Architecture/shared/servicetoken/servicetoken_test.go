package servicetoken

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// harness builds a payments verifier that knows two callers with distinct
// keys and distinct reference types — the real deployment shape, and the one
// that makes the cross-caller tests meaningful.
type harness struct {
	v        *Verifier
	commerce *Signer
	food     *Signer
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	cPub, cPriv, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	fPub, fPriv, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	cs, err := NewSignerFromBase64("commerce-service", "c1", cPriv)
	if err != nil {
		t.Fatal(err)
	}
	fs, err := NewSignerFromBase64("food-service", "f1", fPriv)
	if err != nil {
		t.Fatal(err)
	}

	v := NewVerifier(AudiencePayments)
	if err := v.RegisterBase64("commerce-service", "c1", cPub,
		[]string{OpIntentCreate, OpIntentRead, OpRefundCreate, OpPaymentFetch},
		[]string{RefOrder}); err != nil {
		t.Fatal(err)
	}
	if err := v.RegisterBase64("food-service", "f1", fPub,
		[]string{OpIntentCreate, OpIntentRead, OpRefundCreate},
		[]string{RefFoodOrder}); err != nil {
		t.Fatal(err)
	}
	return &harness{v: v, commerce: cs, food: fs}
}

func TestHappyPath(t *testing.T) {
	h := newHarness(t)
	tok, err := h.commerce.Mint(AudiencePayments, "commerce", []string{OpIntentCreate}, []string{RefOrder}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.v.Verify(tok, OpIntentCreate, RefOrder)
	if err != nil {
		t.Fatalf("expected the token to verify, got %v", err)
	}
	if got.Issuer != "commerce-service" {
		t.Fatalf("issuer = %q", got.Issuer)
	}
}

// ─── The cross-caller cases the review named explicitly ──────────────

// food-service must not be able to touch a commerce order, even with a
// perfectly valid token of its own. This is why the reference type is
// checked against the caller policy and not only against the token.
func TestFoodTokenCannotRefundCommerceOrder(t *testing.T) {
	h := newHarness(t)
	// Food asks for RefOrder in its own token — a compromised or buggy
	// food-service could mint exactly this.
	tok, err := h.food.Mint(AudiencePayments, "food", []string{OpRefundCreate}, []string{RefOrder}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.v.Verify(tok, OpRefundCreate, RefOrder); err != ErrRefTypeDenied {
		t.Fatalf("food token on a commerce order should be denied by policy, got %v", err)
	}
}

func TestCommerceTokenCannotActOnFoodReference(t *testing.T) {
	h := newHarness(t)
	tok, err := h.commerce.Mint(AudiencePayments, "commerce", []string{OpRefundCreate}, []string{RefFoodOrder}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.v.Verify(tok, OpRefundCreate, RefFoodOrder); err != ErrRefTypeDenied {
		t.Fatalf("commerce token on a food order should be denied by policy, got %v", err)
	}
}

// A token minted for one operation must not be replayable as another.
func TestScopeIsPerOperation(t *testing.T) {
	h := newHarness(t)
	tok, err := h.commerce.Mint(AudiencePayments, "commerce", []string{OpIntentCreate}, []string{RefOrder}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.v.Verify(tok, OpRefundCreate, RefOrder); err != ErrScopeDenied {
		t.Fatalf("an intent-create token must not authorise a refund, got %v", err)
	}
}

// ─── Claim validation (A2: iss, sub, aud, exp, nbf, kid, alg) ────────

func TestWrongAudienceRejected(t *testing.T) {
	h := newHarness(t)
	tok, _ := h.commerce.Mint("food", "commerce", []string{OpIntentCreate}, []string{RefOrder}, time.Minute)
	if _, err := h.v.Verify(tok, OpIntentCreate, RefOrder); err != ErrWrongAudience {
		t.Fatalf("got %v, want ErrWrongAudience", err)
	}
}

func TestUnknownIssuerRejected(t *testing.T) {
	h := newHarness(t)
	_, priv, _ := GenerateKeypair()
	rogue, _ := NewSignerFromBase64("attacker-service", "c1", priv)
	tok, _ := rogue.Mint(AudiencePayments, "x", []string{OpRefundCreate}, []string{RefOrder}, time.Minute)
	if _, err := h.v.Verify(tok, OpRefundCreate, RefOrder); err != ErrUnknownIssuer {
		t.Fatalf("got %v, want ErrUnknownIssuer", err)
	}
}

// A rogue signer claiming a KNOWN issuer must still fail, because the key is
// resolved from the registry and the signature will not match.
func TestForgedIssuerWithWrongKeyFailsSignature(t *testing.T) {
	h := newHarness(t)
	_, priv, _ := GenerateKeypair()
	rogue, _ := NewSignerFromBase64("commerce-service", "c1", priv)
	tok, _ := rogue.Mint(AudiencePayments, "x", []string{OpRefundCreate}, []string{RefOrder}, time.Minute)
	if _, err := h.v.Verify(tok, OpRefundCreate, RefOrder); err != ErrBadSignature {
		t.Fatalf("got %v, want ErrBadSignature", err)
	}
}

func TestUnknownKIDRejected(t *testing.T) {
	h := newHarness(t)
	cs, _ := NewSignerFromBase64("commerce-service", "rotated-away", mustPriv(t))
	tok, _ := cs.Mint(AudiencePayments, "c", []string{OpIntentCreate}, []string{RefOrder}, time.Minute)
	if _, err := h.v.Verify(tok, OpIntentCreate, RefOrder); err != ErrUnknownIssuer {
		// issuer+kid is the registry key, so an unknown kid surfaces here
		t.Fatalf("got %v, want rejection for an unregistered kid", err)
	}
}

func TestExpiredRejected(t *testing.T) {
	h := newHarness(t)
	tok, _ := h.commerce.Mint(AudiencePayments, "c", []string{OpIntentCreate}, []string{RefOrder}, time.Minute)
	h.v.SetClock(func() time.Time { return time.Now().Add(2 * time.Minute) })
	if _, err := h.v.Verify(tok, OpIntentCreate, RefOrder); err != ErrExpired {
		t.Fatalf("got %v, want ErrExpired", err)
	}
}

func TestNotYetValidRejected(t *testing.T) {
	h := newHarness(t)
	tok, _ := h.commerce.Mint(AudiencePayments, "c", []string{OpIntentCreate}, []string{RefOrder}, time.Minute)
	h.v.SetClock(func() time.Time { return time.Now().Add(-10 * time.Minute) })
	if _, err := h.v.Verify(tok, OpIntentCreate, RefOrder); err != ErrNotYetValid {
		t.Fatalf("got %v, want ErrNotYetValid", err)
	}
}

func TestLongTTLRefusedAtMint(t *testing.T) {
	h := newHarness(t)
	if _, err := h.commerce.Mint(AudiencePayments, "c", []string{OpIntentCreate}, []string{RefOrder}, time.Hour); err != ErrTTLTooLong {
		t.Fatalf("got %v, want ErrTTLTooLong", err)
	}
}

// A caller that bypasses Mint and hand-rolls a long-lived token must still
// be refused at verification — the mint-side guard is not the control.
func TestLongTTLRefusedAtVerify(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	tok := handRoll(t, h.commerce, Claims{
		Issuer: "commerce-service", Subject: "c", Audience: AudiencePayments,
		IssuedAt: now.Unix(), NotBefore: now.Add(-time.Minute).Unix(),
		Expiry: now.Add(24 * time.Hour).Unix(),
		Scope:  []string{OpRefundCreate}, RefTypes: []string{RefOrder},
	}, "c1")
	if _, err := h.v.Verify(tok, OpRefundCreate, RefOrder); err != ErrTTLTooLong {
		t.Fatalf("got %v, want ErrTTLTooLong", err)
	}
}

// ─── Algorithm confusion ─────────────────────────────────────────────

func TestAlgNoneRejected(t *testing.T) {
	h := newHarness(t)
	hdr := b64(t, `{"alg":"none","typ":"JWT","kid":"c1"}`)
	now := time.Now()
	body, _ := json.Marshal(Claims{
		Issuer: "commerce-service", Audience: AudiencePayments,
		IssuedAt: now.Unix(), Expiry: now.Add(time.Minute).Unix(),
		Scope: []string{OpRefundCreate}, RefTypes: []string{RefOrder},
	})
	tok := hdr + "." + base64.RawURLEncoding.EncodeToString(body) + "."
	if _, err := h.v.Verify(tok, OpRefundCreate, RefOrder); err != ErrBadAlgorithm {
		t.Fatalf("got %v, want ErrBadAlgorithm", err)
	}
}

func TestHS256HeaderRejected(t *testing.T) {
	h := newHarness(t)
	hdr := b64(t, `{"alg":"HS256","typ":"JWT","kid":"c1"}`)
	tok := hdr + ".e30." + base64.RawURLEncoding.EncodeToString([]byte("sig"))
	if _, err := h.v.Verify(tok, OpIntentCreate, RefOrder); err != ErrBadAlgorithm {
		t.Fatalf("got %v, want ErrBadAlgorithm", err)
	}
}

func TestTamperedClaimsRejected(t *testing.T) {
	h := newHarness(t)
	tok, _ := h.commerce.Mint(AudiencePayments, "c", []string{OpIntentCreate}, []string{RefOrder}, time.Minute)
	parts := strings.Split(tok, ".")
	body, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c Claims
	_ = json.Unmarshal(body, &c)
	c.Scope = []string{OpRefundCreate} // escalate
	nb, _ := json.Marshal(c)
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(nb) + "." + parts[2]
	if _, err := h.v.Verify(tampered, OpRefundCreate, RefOrder); err != ErrBadSignature {
		t.Fatalf("got %v, want ErrBadSignature", err)
	}
}

func TestMalformedRejected(t *testing.T) {
	h := newHarness(t)
	for _, bad := range []string{"", "a", "a.b", "a.b.c.d", "...", "!!!.???.***"} {
		if _, err := h.v.Verify(bad, OpIntentCreate, RefOrder); err == nil {
			t.Fatalf("malformed token %q verified", bad)
		}
	}
}

// An ordinary end-user JWT from the edge has none of the service claims and
// is not signed by a registered service key, so it cannot pass.
func TestEndUserTokenCannotPass(t *testing.T) {
	h := newHarness(t)
	hdr := b64(t, `{"alg":"RS256","typ":"JWT","kid":"auth-1"}`)
	body := b64(t, `{"sub":"11111111-1111-1111-1111-111111111111","scopes":"user"}`)
	tok := hdr + "." + body + "." + base64.RawURLEncoding.EncodeToString([]byte("whatever"))
	if _, err := h.v.Verify(tok, OpIntentCreate, RefOrder); err == nil {
		t.Fatal("an end-user token must never authorise a service operation")
	}
}

// ─── helpers ─────────────────────────────────────────────────────────

func b64(t *testing.T, s string) string {
	t.Helper()
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func mustPriv(t *testing.T) string {
	t.Helper()
	_, priv, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

// handRoll signs arbitrary claims with a signer's key, bypassing Mint's
// guards, so verify-side enforcement can be tested independently.
func handRoll(t *testing.T, s *Signer, c Claims, kid string) string {
	t.Helper()
	hb, _ := json.Marshal(Header{Alg: Algorithm, Typ: "JWT", KID: kid})
	cb, _ := json.Marshal(c)
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	sig := ed25519.Sign(s.key, []byte(signing))
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}
