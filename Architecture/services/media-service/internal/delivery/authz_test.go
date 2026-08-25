package delivery

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Module 4 M4-P0-5 — the delivery gate.
//
// The property under test is that NOTHING serves protected bytes without a
// resolved yes. Every one of these cases is a way the old code would have
// served them: it had no viewer, no authorization call, and a stable URL.

func gateSigner(t *testing.T) *Signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	s, err := NewSigner(Config{
		CDNBaseURL: "https://d111.cloudfront.net",
		KeyPairID:  "K1",
		PrivateKeyPEM: pem.EncodeToMemory(&pem.Block{
			Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
		}),
	})
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return s
}

func TestAnyContentAuthorizerAllowsOneCanonicalSurface(t *testing.T) {
	authorizer := AnyContentAuthorizer{
		fakeAuthz{allow: false},
		fakeAuthz{allow: true},
	}
	if err := authorizer.Authorize(context.Background(), "viewer", "media"); err != nil {
		t.Fatalf("chat allow should resolve post denial: %v", err)
	}
}

func TestAnyContentAuthorizerPreservesUnresolvedWhenNobodyAllows(t *testing.T) {
	authorizer := AnyContentAuthorizer{
		fakeAuthz{allow: false},
		fakeAuthz{err: ErrDeliveryUnresolved},
	}
	if err := authorizer.Authorize(context.Background(), "viewer", "media"); !errors.Is(err, ErrDeliveryUnresolved) {
		t.Fatalf("expected unresolved, got %v", err)
	}
}

func TestProfileAuthorizerSendsAnonymousViewerToPrivacyAuthority(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/v1/profiles/internal/media-access" {
			t.Errorf("path=%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"allowed":true}`))
	}))
	defer server.Close()

	authorizer := NewHTTPProfileAuthorizer(server.URL, "internal", server.Client())
	if err := authorizer.Authorize(context.Background(), "", "media"); err != nil {
		t.Fatalf("anonymous public-profile photo was denied: %v", err)
	}
	if !called {
		t.Fatal("profile privacy authority was bypassed")
	}
}

type fakeAuthz struct {
	allow bool
	err   error
}

func (f fakeAuthz) Authorize(context.Context, string, string) error {
	if f.err != nil {
		return f.err
	}
	if !f.allow {
		return ErrDeliveryDenied
	}
	return nil
}

func TestProtectedMediaRequiresAuthorization(t *testing.T) {
	g := NewGate(gateSigner(t), fakeAuthz{allow: false})
	_, err := g.URLFor(context.Background(), "viewer", "media", ProtectedPrefix+"stories/a.jpg")
	if !errors.Is(err, ErrDeliveryDenied) {
		t.Fatalf("denied viewer got %v, want ErrDeliveryDenied", err)
	}
}

func TestAuthorizedViewerGetsABoundedSignedURL(t *testing.T) {
	g := NewGate(gateSigner(t), fakeAuthz{allow: true})
	got, err := g.URLFor(context.Background(), "viewer", "media", ProtectedPrefix+"stories/a.jpg")
	if err != nil {
		t.Fatalf("authorized viewer denied: %v", err)
	}
	for _, want := range []string{"Expires=", "Signature=", "Key-Pair-Id="} {
		if !strings.Contains(got, want) {
			t.Errorf("issued URL is missing %s — it is not a bounded signed URL: %s", want, got)
		}
	}
}

// An unresolved authorization must NOT be served and must NOT be reported as a
// plain denial: the caller needs to answer 503 so the client retries rather
// than caching "this does not exist".
func TestUnresolvedAuthorizationDeniesAndStaysDistinct(t *testing.T) {
	g := NewGate(gateSigner(t), fakeAuthz{err: ErrDeliveryUnresolved})
	_, err := g.URLFor(context.Background(), "viewer", "media", ProtectedPrefix+"a.jpg")
	if !errors.Is(err, ErrDeliveryUnresolved) {
		t.Fatalf("got %v, want ErrDeliveryUnresolved", err)
	}
	if errors.Is(err, ErrDeliveryDenied) {
		t.Fatal("an outage was reported as a resolved denial; the client would treat a " +
			"transient failure as permanent")
	}
}

// A gate with no authorizer must refuse protected media rather than serving it.
// This is the unwired-deployment case, and it has to fail closed.
func TestGateWithoutAuthorizerRefusesProtected(t *testing.T) {
	g := NewGate(gateSigner(t), nil)
	if _, err := g.URLFor(context.Background(), "v", "m", ProtectedPrefix+"a.jpg"); err == nil {
		t.Fatal("protected media was served with no content authorizer configured")
	}
	// ...but public media still works, so a missing authorizer does not take
	// down avatars.
	if _, err := g.URLFor(context.Background(), "v", "m", PublicPrefix+"avatar.png"); err != nil {
		t.Fatalf("public media failed without an authorizer: %v", err)
	}
}

// Public objects must not pay an authorization round trip.
func TestPublicMediaSkipsAuthorization(t *testing.T) {
	called := false
	g := NewGate(gateSigner(t), authzFunc(func() error { called = true; return ErrDeliveryDenied }))
	got, err := g.URLFor(context.Background(), "", "m", PublicPrefix+"avatar.png")
	if err != nil {
		t.Fatalf("public media denied: %v", err)
	}
	if called {
		t.Error("public media triggered a content authorization call")
	}
	if strings.Contains(got, "Signature=") {
		t.Error("public media was signed; it should be a stable cacheable URL")
	}
}

type authzFunc func() error

func (f authzFunc) Authorize(context.Context, string, string) error { return f() }

type batchAuthz struct {
	allowed map[string]bool
	err     error
	calls   int
}

func (b *batchAuthz) Authorize(context.Context, string, string) error {
	return errors.New("individual authorization must not be called")
}

func (b *batchAuthz) AuthorizeBatch(_ context.Context, _ string, _ []string) (map[string]bool, error) {
	b.calls++
	return b.allowed, b.err
}

// ── HTTP authorizer: every failure shape must fail closed ──────────────────

func TestHTTPAuthorizerFailsClosedOnEveryFailureShape(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"non-200": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) },
		"malformed body": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(200)
			_, _ = w.Write([]byte("{not json"))
		},
		"empty body": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(200)
		},
		"explicit no": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"allowed":false}`))
		},
		"not found": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) },
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			a := NewHTTPContentAuthorizer(srv.URL, "k", srv.Client())
			if err := a.Authorize(context.Background(), "viewer", "media"); err == nil {
				t.Fatal("authorization succeeded on a failure response; protected bytes " +
					"would be served")
			}
		})
	}
}

func TestHTTPAuthorizerAllowsAnExplicitYes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Service-Key") == "" {
			t.Error("internal service key was not sent; the content authority cannot " +
				"tell this call from an external one")
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"allowed":true}`))
	}))
	defer srv.Close()

	a := NewHTTPContentAuthorizer(srv.URL, "k", srv.Client())
	if err := a.Authorize(context.Background(), "viewer", "media"); err != nil {
		t.Fatalf("explicit allow was rejected: %v", err)
	}
}

// An anonymous caller can never receive protected bytes: with no viewer there
// is no audience decision to make.
func TestAnonymousCallerIsDeniedProtectedMedia(t *testing.T) {
	a := NewHTTPContentAuthorizer("http://unused", "k", nil)
	if err := a.Authorize(context.Background(), "", "media"); !errors.Is(err, ErrDeliveryDenied) {
		t.Fatalf("anonymous caller got %v, want ErrDeliveryDenied", err)
	}
}

// An unconfigured authorizer is unresolved, not permitted.
func TestUnconfiguredAuthorizerIsUnresolved(t *testing.T) {
	a := NewHTTPContentAuthorizer("", "k", nil)
	if err := a.Authorize(context.Background(), "viewer", "media"); !errors.Is(err, ErrDeliveryUnresolved) {
		t.Fatalf("got %v, want ErrDeliveryUnresolved", err)
	}
}

// ── URLsForAsset: the path the read endpoints actually use ──────────────────
//
// These exist because two negative controls (skip authorization; treat
// unresolved as allowed) both PASSED against the earlier suite. Every test
// covered URLFor, while GetMediaURL / BatchMediaURLs call URLsForAsset — so the
// batch path, which is the one an attacker would use, had no coverage at all.

func assetKeys() map[string]string {
	return map[string]string{
		"original": ProtectedPrefix + "stories/a.jpg",
		"thumb":    ProtectedPrefix + "stories/a_thumb.jpg",
	}
}

func TestURLsForAssetRequiresAuthorization(t *testing.T) {
	g := NewGate(gateSigner(t), fakeAuthz{allow: false})
	got, err := g.URLsForAsset(context.Background(), "viewer", "media", assetKeys())
	if !errors.Is(err, ErrDeliveryDenied) {
		t.Fatalf("denied viewer got %v (urls %v), want ErrDeliveryDenied", err, got)
	}
	if len(got) != 0 {
		t.Fatalf("a denied viewer received %d URLs", len(got))
	}
}

func TestURLsForAssetUnresolvedIsNotServed(t *testing.T) {
	g := NewGate(gateSigner(t), fakeAuthz{err: ErrDeliveryUnresolved})
	got, err := g.URLsForAsset(context.Background(), "viewer", "media", assetKeys())
	if !errors.Is(err, ErrDeliveryUnresolved) {
		t.Fatalf("got %v, want ErrDeliveryUnresolved", err)
	}
	if len(got) != 0 {
		t.Fatalf("an unresolved authorization still produced %d URLs", len(got))
	}
}

func TestURLsForAssetSignsEveryKeyOnceAuthorized(t *testing.T) {
	g := NewGate(gateSigner(t), fakeAuthz{allow: true})
	got, err := g.URLsForAsset(context.Background(), "viewer", "media", assetKeys())
	if err != nil {
		t.Fatalf("authorized viewer denied: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d URLs, want 2 — a dropped variant is invisible to the client", len(got))
	}
	for name, u := range got {
		if !strings.Contains(u, "Signature=") {
			t.Errorf("%s was not signed: %s", name, u)
		}
	}
}

// A single authorization covers the whole asset: the variants are the same
// content, and asking per key multiplies both load and failure chances.
func TestURLsForAssetAuthorizesExactlyOnce(t *testing.T) {
	calls := 0
	g := NewGate(gateSigner(t), authzFunc(func() error { calls++; return nil }))
	keys := assetKeys()
	keys["hls"] = ProtectedPrefix + "stories/a.m3u8"
	if _, err := g.URLsForAsset(context.Background(), "v", "m", keys); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("authorized %d times for one asset, want 1", calls)
	}
}

// A MIXED asset must be treated as protected. A public thumbnail beside a
// protected original leaks the thing it is a thumbnail of.
func TestMixedClassAssetIsTreatedAsProtected(t *testing.T) {
	g := NewGate(gateSigner(t), fakeAuthz{allow: false})
	mixed := map[string]string{
		"original": ProtectedPrefix + "stories/a.jpg",
		"thumb":    PublicPrefix + "thumbs/a.jpg",
	}
	if _, err := g.URLsForAsset(context.Background(), "v", "m", mixed); !errors.Is(err, ErrDeliveryDenied) {
		t.Fatalf("a mixed-class asset skipped authorization: %v", err)
	}
}

// A fully public asset needs no authorization call.
func TestURLsForAssetSkipsAuthorizationWhenAllPublic(t *testing.T) {
	called := false
	g := NewGate(gateSigner(t), authzFunc(func() error { called = true; return ErrDeliveryDenied }))
	got, err := g.URLsForAsset(context.Background(), "", "m", map[string]string{
		"original": PublicPrefix + "avatars/a.png",
	})
	if err != nil {
		t.Fatalf("public asset denied: %v", err)
	}
	if called {
		t.Error("a fully public asset triggered an authorization call")
	}
	if strings.Contains(got["original"], "Signature=") {
		t.Error("public asset was signed instead of returned as a stable URL")
	}
}

// A gate with no authorizer must refuse protected assets.
func TestURLsForAssetWithoutAuthorizerRefusesProtected(t *testing.T) {
	g := NewGate(gateSigner(t), nil)
	if _, err := g.URLsForAsset(context.Background(), "v", "m", assetKeys()); err == nil {
		t.Fatal("protected asset served with no content authorizer configured")
	}
}

func TestURLsForAssetsAuthorizesPageExactlyOnceAndOmitsDenied(t *testing.T) {
	authz := &batchAuthz{allowed: map[string]bool{"m1": true, "m2": false}}
	g := NewGate(gateSigner(t), authz)
	got, err := g.URLsForAssets(context.Background(), "viewer", map[string]map[string]string{
		"m1": {"original": ProtectedPrefix + "posts/one.jpg", "thumb": ProtectedPrefix + "posts/one-thumb.jpg"},
		"m2": {"original": ProtectedPrefix + "posts/two.jpg"},
	})
	if err != nil {
		t.Fatalf("batch URLs: %v", err)
	}
	if authz.calls != 1 {
		t.Fatalf("authorization calls=%d want 1", authz.calls)
	}
	if len(got["m1"]) != 2 {
		t.Fatalf("allowed asset variants=%v", got["m1"])
	}
	if _, exists := got["m2"]; exists {
		t.Fatal("denied asset was not omitted")
	}
}

func TestURLsForAssetsFailsWholePageWhenAuthorizationUnresolved(t *testing.T) {
	authz := &batchAuthz{err: ErrDeliveryUnresolved}
	g := NewGate(gateSigner(t), authz)
	got, err := g.URLsForAssets(context.Background(), "viewer", map[string]map[string]string{
		"m1": {"original": ProtectedPrefix + "posts/one.jpg"},
	})
	if !errors.Is(err, ErrDeliveryUnresolved) {
		t.Fatalf("err=%v want unresolved", err)
	}
	if len(got) != 0 {
		t.Fatalf("unresolved page returned URLs: %v", got)
	}
}

func TestAnyContentAuthorizerBatchPreservesUnresolvedWhenCandidateAuthorityFails(t *testing.T) {
	// Post authorizer resolves m1=true, m2=false.
	// Chat authorizer is unreachable (ErrDeliveryUnresolved).
	// Because chat might own m2 and chat is unresolved, m2 cannot be treated as a resolved denial.
	authorizers := AnyContentAuthorizer{
		&batchAuthz{allowed: map[string]bool{"m1": true, "m2": false}},
		fakeAuthz{err: ErrDeliveryUnresolved},
	}
	_, err := authorizers.AuthorizeBatch(context.Background(), "viewer", []string{"m1", "m2"})
	if !errors.Is(err, ErrDeliveryUnresolved) {
		t.Fatalf("got %v, want ErrDeliveryUnresolved", err)
	}
}

func TestAnyContentAuthorizerBatchDeniesWhenAllCandidateAuthoritiesResolveDenial(t *testing.T) {
	// Post authorizer resolves m1=true, m2=false.
	// Chat authorizer resolves m1=false, m2=false (allow=false).
	// Both authorities resolved; m2 is a truthful resolved denial.
	authorizers := AnyContentAuthorizer{
		&batchAuthz{allowed: map[string]bool{"m1": true, "m2": false}},
		fakeAuthz{allow: false},
	}
	allowed, err := authorizers.AuthorizeBatch(context.Background(), "viewer", []string{"m1", "m2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed["m1"] {
		t.Error("m1 should be allowed")
	}
	if allowed["m2"] {
		t.Error("m2 should be denied")
	}
}

func TestAnyContentAuthorizerBatchFailsWhenAllAuthorizersUnresolved(t *testing.T) {
	// Both post and chat authorizers are unreachable.
	authorizers := AnyContentAuthorizer{
		&batchAuthz{err: ErrDeliveryUnresolved},
		fakeAuthz{err: ErrDeliveryUnresolved},
	}
	_, err := authorizers.AuthorizeBatch(context.Background(), "viewer", []string{"m1", "m2"})
	if !errors.Is(err, ErrDeliveryUnresolved) {
		t.Fatalf("got %v, want ErrDeliveryUnresolved", err)
	}
}
