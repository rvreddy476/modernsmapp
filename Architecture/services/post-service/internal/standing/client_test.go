package standing

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// fakeTrustSafety is trust-safety's standing route as its handler serves
// it: internal key, a verified post-service token carrying the operation,
// a strong ETag over the body, 304 on If-None-Match.
type fakeTrustSafety struct {
	t        *testing.T
	verifier *servicetoken.Verifier
	body     atomic.Value // string: the {"data":…} envelope
	status   atomic.Int32 // 0 = normal
	calls    atomic.Int32
	notMod   atomic.Int32
	delay    time.Duration
	lastAuth atomic.Value // string: the raw token
	lastINM  atomic.Value // string: If-None-Match seen
}

const testInternalKey = "test-internal-key"

func newFake(t *testing.T) (*fakeTrustSafety, *httptest.Server, *servicetoken.Signer) {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	v := servicetoken.NewVerifier(Audience)
	if err := v.RegisterBase64(Issuer, "p1", pub, []string{OpStandingRead}, nil); err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64(Issuer, "p1", priv)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeTrustSafety{t: t, verifier: v}
	f.body.Store(okBody())
	f.lastAuth.Store("")
	f.lastINM.Store("")
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv, signer
}

func (f *fakeTrustSafety) serve(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if !strings.HasPrefix(r.URL.Path, Path) {
		http.Error(w, `{"error":{"code":"NOT_FOUND"}}`, http.StatusNotFound)
		return
	}
	if r.Header.Get(HeaderInternalServiceKey) != testInternalKey {
		http.Error(w, `{"error":{"code":"UNAUTHORIZED"}}`, http.StatusUnauthorized)
		return
	}
	raw := strings.TrimPrefix(r.Header.Get(HeaderServiceAuthorization), "Bearer ")
	f.lastAuth.Store(raw)
	f.lastINM.Store(r.Header.Get("If-None-Match"))
	if _, err := f.verifier.Verify(raw, OpStandingRead, ""); err != nil {
		http.Error(w, `{"error":{"code":"SERVICE_CALLER_REFUSED"}}`, http.StatusForbidden)
		return
	}
	if st := int(f.status.Load()); st != 0 {
		http.Error(w, `{"error":{"code":"STANDING_UNAVAILABLE"}}`, st)
		return
	}
	body := f.body.Load().(string)
	sum := sha256.Sum256([]byte(body))
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=60")
	if r.Header.Get("If-None-Match") == etag {
		f.notMod.Add(1)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func okBody() string {
	return `{"data":{"standing":"ok","policy_version":"standing-v1","suspended_until":null,"active_strikes":[]}}`
}

// suspendedBody is the pinned contract fixture (trust-safety
// testdata/contracts/standing.v1.json).
func suspendedBody() string {
	return `{"data":{"standing":"suspended","policy_version":"standing-v1","suspended_until":"2026-12-27T09:30:00Z","active_strikes":[{"id":"0b6c1d4e-8f2a-4c3b-9d1e-2f3a4b5c6d7e","severity":"severe_strike","reason":"copyright: upheld case","case_id":"7d2f9c3a-1b4e-4f6a-8c9d-0e1f2a3b4c5d","issued_at":"2026-09-28T09:30:00Z","expires_at":"2026-12-27T09:30:00Z"},{"id":"1c7d2e5f-9a3b-4d4c-8e2f-3a4b5c6d7e8f","severity":"warning","reason":"community guidelines: first notice","case_id":null,"issued_at":"2026-09-01T00:00:00Z","expires_at":"2026-11-30T00:00:00Z"}]}}`
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func newClient(t *testing.T, srv *httptest.Server, signer *servicetoken.Signer, ck *clock) *Client {
	t.Helper()
	c, err := New(Config{BaseURL: srv.URL, InternalKey: testInternalKey, Signer: signer, Now: ck.now,
		HTTPClient: &http.Client{Timeout: 300 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCheck_OKAllowsAndIsCached(t *testing.T) {
	f, srv, signer := newFake(t)
	ck := &clock{t: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	c := newClient(t, srv, signer, ck)
	user := uuid.New()

	res, err := c.Check(context.Background(), user)
	if err != nil {
		t.Fatalf("ok must allow: %v", err)
	}
	if res.Standing != StandingOK || res.PolicyVersion != "standing-v1" || res.ActiveStrikes == nil {
		t.Fatalf("result=%+v", res)
	}
	for i := 0; i < 5; i++ {
		if _, err := c.Check(context.Background(), user); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("an answer must be reused for %s: %d calls", CacheTTL, got)
	}
}

func TestCheck_SuspendedDeniesWithUntilAndIsCached(t *testing.T) {
	f, srv, signer := newFake(t)
	f.body.Store(suspendedBody())
	ck := &clock{t: time.Now()}
	c := newClient(t, srv, signer, ck)
	user := uuid.New()

	_, err := c.Check(context.Background(), user)
	var denied *DeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("suspended must be a *DeniedError, got %v", err)
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatal("errors.Is(err, ErrDenied) must hold")
	}
	if errors.Is(err, ErrUnknown) {
		t.Fatal("a deny is not unknown")
	}
	want := time.Date(2026, 12, 27, 9, 30, 0, 0, time.UTC)
	if denied.Standing != StandingSuspended || denied.SuspendedUntil == nil || !denied.SuspendedUntil.Equal(want) || denied.UserID != user {
		t.Fatalf("denied=%+v", denied)
	}
	if denied.PolicyVersion != "standing-v1" {
		t.Fatalf("policy_version=%q", denied.PolicyVersion)
	}
	// Deny is cached exactly like allow.
	if _, err := c.Check(context.Background(), user); !errors.Is(err, ErrDenied) {
		t.Fatalf("second call: %v", err)
	}
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("a deny must be cached too: %d calls", got)
	}
}

func TestCheck_RestrictedDenies(t *testing.T) {
	f, srv, signer := newFake(t)
	f.body.Store(`{"data":{"standing":"restricted","policy_version":"standing-v1","suspended_until":null,"active_strikes":[]}}`)
	c := newClient(t, srv, signer, &clock{t: time.Now()})
	_, err := c.Check(context.Background(), uuid.New())
	var denied *DeniedError
	if !errors.As(err, &denied) || denied.Standing != StandingRestricted || denied.SuspendedUntil != nil {
		t.Fatalf("restricted must deny without an end: %v", err)
	}
}

func TestCheck_CacheExpiresThenRevalidatesWith304(t *testing.T) {
	f, srv, signer := newFake(t)
	ck := &clock{t: time.Now()}
	c := newClient(t, srv, signer, ck)
	user := uuid.New()

	if _, err := c.Check(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	ck.add(CacheTTL - time.Second)
	if _, err := c.Check(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("still fresh at %s: %d calls", CacheTTL-time.Second, f.calls.Load())
	}
	ck.add(2 * time.Second)
	if _, err := c.Check(context.Background(), user); err != nil {
		t.Fatalf("revalidated answer must still allow: %v", err)
	}
	if f.calls.Load() != 2 || f.notMod.Load() != 1 {
		t.Fatalf("expired entry must revalidate with If-None-Match and be renewed by a 304: calls=%d 304s=%d", f.calls.Load(), f.notMod.Load())
	}
	if inm := f.lastINM.Load().(string); inm == "" {
		t.Fatal("revalidation carried no If-None-Match")
	}
	// The 304 renewed the entry for another TTL.
	ck.add(CacheTTL / 2)
	if _, err := c.Check(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 2 {
		t.Fatalf("304 must renew the cache entry: %d calls", f.calls.Load())
	}
	// And a changed answer replaces it at the next revalidation.
	f.body.Store(suspendedBody())
	ck.add(CacheTTL)
	if _, err := c.Check(context.Background(), user); !errors.Is(err, ErrDenied) {
		t.Fatalf("a new suspension must be seen once the entry expires: %v", err)
	}
}

func TestCheck_304WithoutCacheIsUnknown(t *testing.T) {
	// A server that answers 304 to a request that carried no validator is
	// outside the contract; nothing can be reused.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	t.Cleanup(srv.Close)
	_, priv, _ := servicetoken.GenerateKeypair()
	signer, _ := servicetoken.NewSignerFromBase64(Issuer, "p1", priv)
	c, _ := New(Config{BaseURL: srv.URL, Signer: signer})
	if _, err := c.Check(context.Background(), uuid.New()); !errors.Is(err, ErrUnknown) {
		t.Fatalf("got %v", err)
	}
}

func TestCheck_FailuresAreUnknownAndNeverCached(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *fakeTrustSafety)
		calls int32 // expected calls for ONE Check
	}{
		{"401", func(f *fakeTrustSafety) { f.status.Store(http.StatusUnauthorized) }, 1},
		{"403", func(f *fakeTrustSafety) { f.status.Store(http.StatusForbidden) }, 1},
		{"404", func(f *fakeTrustSafety) { f.status.Store(http.StatusNotFound) }, 1},
		{"500 retried once", func(f *fakeTrustSafety) { f.status.Store(http.StatusInternalServerError) }, 2},
		{"503 retried once", func(f *fakeTrustSafety) { f.status.Store(http.StatusServiceUnavailable) }, 2},
		{"timeout retried once", func(f *fakeTrustSafety) { f.delay = 600 * time.Millisecond }, 2},
		{"malformed body", func(f *fakeTrustSafety) { f.body.Store(`{"data":[]}`) }, 1},
		{"no data", func(f *fakeTrustSafety) { f.body.Store(`{"standing":"ok"}`) }, 1},
		{"unknown standing value", func(f *fakeTrustSafety) {
			f.body.Store(`{"data":{"standing":"good","policy_version":"standing-v1","suspended_until":null,"active_strikes":[]}}`)
		}, 1},
		{"empty standing", func(f *fakeTrustSafety) {
			f.body.Store(`{"data":{"policy_version":"standing-v1","suspended_until":null,"active_strikes":[]}}`)
		}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, srv, signer := newFake(t)
			tc.setup(f)
			c := newClient(t, srv, signer, &clock{t: time.Now()})
			user := uuid.New()
			res, err := c.Check(context.Background(), user)
			if !errors.Is(err, ErrUnknown) || res != nil {
				t.Fatalf("must be ErrUnknown with no result: res=%v err=%v", res, err)
			}
			if errors.Is(err, ErrDenied) {
				t.Fatal("unknown is not a deny")
			}
			if got := f.calls.Load(); got != tc.calls {
				t.Fatalf("calls=%d want %d", got, tc.calls)
			}
			// Nothing was cached: the next call asks again.
			f.calls.Store(0)
			_, _ = c.Check(context.Background(), user)
			if f.calls.Load() == 0 {
				t.Fatal("an unknown answer must not be cached")
			}
		})
	}
}

func TestCheck_TransportFailureIsUnknown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close() // connection refused from here on
	_, priv, _ := servicetoken.GenerateKeypair()
	signer, _ := servicetoken.NewSignerFromBase64(Issuer, "p1", priv)
	c, _ := New(Config{BaseURL: srv.URL, Signer: signer})
	if _, err := c.Check(context.Background(), uuid.New()); !errors.Is(err, ErrUnknown) {
		t.Fatalf("got %v", err)
	}
}

func TestCheck_ServerRefusesWrongKeyOrScope(t *testing.T) {
	// The fake verifies like trust-safety does; a client whose key is not
	// registered gets 403 and therefore unknown, never an allow.
	f, srv, _ := newFake(t)
	_, otherPriv, _ := servicetoken.GenerateKeypair()
	other, _ := servicetoken.NewSignerFromBase64(Issuer, "p1", otherPriv)
	c, _ := New(Config{BaseURL: srv.URL, InternalKey: testInternalKey, Signer: other})
	if _, err := c.Check(context.Background(), uuid.New()); !errors.Is(err, ErrUnknown) {
		t.Fatalf("got %v", err)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("a 403 must not be retried: %d calls", f.calls.Load())
	}
	// Missing internal key → 401 → unknown.
	_, srv2, signer := newFake(t)
	c2, _ := New(Config{BaseURL: srv2.URL, Signer: signer})
	if _, err := c2.Check(context.Background(), uuid.New()); !errors.Is(err, ErrUnknown) {
		t.Fatalf("got %v", err)
	}
}

// The token is what trust-safety keys its allowlist on; pin every claim.
func TestCheck_TokenClaims(t *testing.T) {
	f, srv, signer := newFake(t)
	c := newClient(t, srv, signer, &clock{t: time.Now()})
	if _, err := c.Check(context.Background(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	raw := f.lastAuth.Load().(string)
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatalf("not a token: %q", raw)
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	var hdr map[string]any
	if err := json.Unmarshal(hb, &hdr); err != nil {
		t.Fatal(err)
	}
	if hdr["alg"] != servicetoken.Algorithm || hdr["kid"] != "p1" {
		t.Fatalf("header=%v", hdr)
	}
	cb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(cb, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["iss"] != Issuer || claims["aud"] != Audience || claims["sub"] != Issuer {
		t.Fatalf("iss/aud/sub: %v", claims)
	}
	scope, _ := claims["scope"].([]any)
	if len(scope) != 1 || scope[0] != OpStandingRead {
		t.Fatalf("scope=%v want [%s]", claims["scope"], OpStandingRead)
	}
	if _, has := claims["act"]; has {
		t.Fatal("a standing token must not carry an act claim: post-service acts for no admin here")
	}
	if rt, has := claims["ref_types"]; has && rt != nil {
		t.Fatalf("ref_types must be empty: %v", rt)
	}
	exp, _ := claims["exp"].(float64)
	iat, _ := claims["iat"].(float64)
	if ttl := time.Duration(exp-iat) * time.Second; ttl != TokenTTL || ttl > servicetoken.MaxTTL {
		t.Fatalf("ttl=%s want %s (≤ %s)", ttl, TokenTTL, servicetoken.MaxTTL)
	}
	if TokenTTL > 5*time.Minute {
		t.Fatal("the contract caps the token at 5 minutes")
	}
	// A fresh token per request (jti differs).
	c.Forget(uuid.Nil)
	c2 := newClient(t, srv, signer, &clock{t: time.Now()})
	if _, err := c2.Check(context.Background(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if f.lastAuth.Load().(string) == raw {
		t.Fatal("token was reused across requests")
	}
}

func TestFromEnv(t *testing.T) {
	pub, priv, _ := servicetoken.GenerateKeypair()
	_ = pub
	env := map[string]string{}
	get := func(k string) string { return env[k] }

	if _, err := FromEnv(get, "k"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("blank env must be ErrNotConfigured: %v", err)
	}
	env[EnvTokenKey] = priv
	if _, err := FromEnv(get, "k"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("key without kid must be ErrNotConfigured: %v", err)
	}
	env[EnvTokenKID] = "p1"
	c, err := FromEnv(get, "k")
	if err != nil {
		t.Fatal(err)
	}
	if c.baseURL != DefaultURL {
		t.Fatalf("default URL=%q want %q (trust-safety listens on 8091)", c.baseURL, DefaultURL)
	}
	if !strings.HasSuffix(DefaultURL, ":8091") {
		t.Fatalf("DefaultURL=%q must name port 8091", DefaultURL)
	}
	env[EnvURL] = "http://ts.internal:8091/"
	c, _ = FromEnv(get, "k")
	if c.baseURL != "http://ts.internal:8091" {
		t.Fatalf("baseURL=%q", c.baseURL)
	}
	env[EnvTokenKey] = "not base64 at all!!"
	if _, err := FromEnv(get, "k"); err == nil || errors.Is(err, ErrNotConfigured) {
		t.Fatalf("a malformed key is a distinct error: %v", err)
	}
	if _, err := New(Config{}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("New without a signer: %v", err)
	}
}

func TestCheck_NilUserIsUnknown(t *testing.T) {
	f, srv, signer := newFake(t)
	c := newClient(t, srv, signer, &clock{t: time.Now()})
	if _, err := c.Check(context.Background(), uuid.Nil); !errors.Is(err, ErrUnknown) {
		t.Fatalf("got %v", err)
	}
	if f.calls.Load() != 0 {
		t.Fatal("no call for a nil user")
	}
}
