package servicetoken_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/servicetoken"
	"github.com/atpost/identity-auth-service/internal/servicetoken/tokentest"
)

const (
	issuer = "admin-service"
	kid    = "k1"
	aud    = "identity"
	op     = "platform:roles.manage"
	actor  = "7b1e4c1a-2f3d-4e5f-8a9b-0c1d2e3f4a5b"
)

func verifier(t *testing.T, kp tokentest.Keypair, ops ...string) *servicetoken.Verifier {
	t.Helper()
	v := servicetoken.NewVerifier(aud)
	if err := v.RegisterBase64(issuer, kid, kp.PubB64, ops); err != nil {
		t.Fatal(err)
	}
	return v
}

func opts() tokentest.Options {
	return tokentest.Options{Issuer: issuer, KID: kid, Subject: "admin-console", Audience: aud, Scope: []string{op}, Actor: actor}
}

func TestHappyPath(t *testing.T) {
	kp := tokentest.NewKeypair()
	v := verifier(t, kp, op)
	got, err := v.Verify(tokentest.Mint(kp, opts()), op)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Issuer != issuer || got.Actor != actor || got.JTI == "" || got.Subject != "admin-console" {
		t.Fatalf("verified = %+v", got)
	}
	if got.ExpiresAt.Before(time.Now().Add(50 * time.Second)) {
		t.Fatalf("expires_at = %v, want ~60 s ahead", got.ExpiresAt)
	}
}

func TestRefusals(t *testing.T) {
	kp := tokentest.NewKeypair()
	other := tokentest.NewKeypair()
	v := verifier(t, kp, op)
	now := time.Now()
	cases := []struct {
		name string
		tok  string
		op   string
		want error
	}{
		{"wrong audience", tokentest.Mint(kp, func() tokentest.Options { o := opts(); o.Audience = "dating"; return o }()), op, servicetoken.ErrWrongAudience},
		{"unknown issuer", tokentest.Mint(kp, func() tokentest.Options { o := opts(); o.Issuer = "food-service"; return o }()), op, servicetoken.ErrUnknownIssuer},
		{"unknown kid", tokentest.Mint(kp, func() tokentest.Options { o := opts(); o.KID = "k2"; return o }()), op, servicetoken.ErrUnknownIssuer},
		{"wrong key", tokentest.Mint(other, opts()), op, servicetoken.ErrBadSignature},
		{"expired", tokentest.Mint(kp, func() tokentest.Options { o := opts(); o.Now = now.Add(-2 * time.Minute); return o }()), op, servicetoken.ErrExpired},
		{"not yet valid", tokentest.Mint(kp, func() tokentest.Options { o := opts(); o.Now = now.Add(2 * time.Minute); return o }()), op, servicetoken.ErrNotYetValid},
		{"ttl too long", tokentest.Mint(kp, func() tokentest.Options { o := opts(); o.TTL = time.Hour; return o }()), op, servicetoken.ErrTTLTooLong},
		{"scope missing from token", tokentest.Mint(kp, func() tokentest.Options { o := opts(); o.Scope = []string{"platform:roles.read"}; return o }()), op, servicetoken.ErrScopeDenied},
		{"scope outside caller policy", tokentest.Mint(kp, func() tokentest.Options { o := opts(); o.Scope = []string{"platform:users.suspend"}; return o }()), "platform:users.suspend", servicetoken.ErrScopeDenied},
		{"no jti", tokentest.Mint(kp, func() tokentest.Options { o := opts(); o.NoJTI = true; return o }()), op, servicetoken.ErrNoJTI},
		{"alg none", tokentest.Mint(kp, func() tokentest.Options { o := opts(); o.Alg = "none"; return o }()), op, servicetoken.ErrBadAlgorithm},
		{"alg HS256", tokentest.Mint(kp, func() tokentest.Options { o := opts(); o.Alg = "HS256"; return o }()), op, servicetoken.ErrBadAlgorithm},
		{"malformed", "a.b", op, servicetoken.ErrMalformed},
		{"empty", "", op, servicetoken.ErrMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := v.Verify(tc.tok, tc.op); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestTamperedClaimsRejected(t *testing.T) {
	kp := tokentest.NewKeypair()
	v := verifier(t, kp, op)
	tok := tokentest.Mint(kp, opts())
	parts := strings.Split(tok, ".")
	body, _ := base64.RawURLEncoding.DecodeString(parts[1])
	forged := strings.Replace(string(body), actor, "00000000-0000-4000-8000-000000000001", 1)
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(forged))
	if _, err := v.Verify(strings.Join(parts, "."), op); !errors.Is(err, servicetoken.ErrBadSignature) {
		t.Fatalf("tampered actor: got %v want ErrBadSignature", err)
	}
}

func TestNoOperationSkipsScope(t *testing.T) {
	kp := tokentest.NewKeypair()
	v := verifier(t, kp, op)
	if _, err := v.Verify(tokentest.Mint(kp, opts()), ""); err != nil {
		t.Fatal(err)
	}
}

func TestUnconfiguredVerifierAcceptsNothing(t *testing.T) {
	kp := tokentest.NewKeypair()
	v := servicetoken.NewVerifier(aud)
	if v.Callers() != 0 {
		t.Fatal("fresh verifier has callers")
	}
	if _, err := v.Verify(tokentest.Mint(kp, opts()), op); !errors.Is(err, servicetoken.ErrUnknownIssuer) {
		t.Fatalf("got %v", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	v := servicetoken.NewVerifier(aud)
	if err := v.RegisterBase64("", kid, tokentest.NewKeypair().PubB64, nil); err == nil {
		t.Fatal("empty issuer accepted")
	}
	if err := v.RegisterBase64(issuer, kid, "not base64!", nil); err == nil {
		t.Fatal("bad key accepted")
	}
	if err := v.RegisterBase64(issuer, kid, base64.StdEncoding.EncodeToString([]byte("short")), nil); err == nil {
		t.Fatal("short key accepted")
	}
}
