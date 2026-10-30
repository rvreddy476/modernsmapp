package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	testGoogleKID     = "g-kid-1"
	testWebClientID   = "web-client.apps.googleusercontent.com"
	testAndroidClient = "android-client.apps.googleusercontent.com"
)

var (
	googleTestKeyOnce sync.Once
	googleTestKey     *rsa.PrivateKey
	otherTestKey      *rsa.PrivateKey
)

func testKeys() (*rsa.PrivateKey, *rsa.PrivateKey) {
	googleTestKeyOnce.Do(func() {
		googleTestKey, _ = rsa.GenerateKey(rand.Reader, 2048)
		otherTestKey, _ = rsa.GenerateKey(rand.Reader, 2048)
	})
	return googleTestKey, otherTestKey
}

// jwksServer serves the test Google key, the way googleapis.com does.
func jwksServer(t *testing.T) *httptest.Server {
	t.Helper()
	key, _ := testKeys()
	body, _ := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kid": testGoogleKID, "kty": "RSA", "alg": "RS256", "use": "sig",
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newOAuthFixture is an admin fixture with Google configured and its key
// cache pointed at a local JWKS server.
func newOAuthFixture(t *testing.T) *adminFixture {
	t.Helper()
	f := newAdminFixture(t)
	f.svc.cfg.OAuth = &config.OAuthConfig{Google: config.OAuthProviderConfig{
		ClientID: testWebClientID, ClientSecret: "secret", RedirectURL: "https://api.example/cb",
		IDTokenAudiences: []string{testAndroidClient},
	}}
	srv := jwksServer(t)
	f.svc.googleKeySet = &googleKeySet{url: srv.URL, client: srv.Client(), now: time.Now}
	return f
}

func googleClaims(email string) jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"iss": "https://accounts.google.com", "aud": testAndroidClient, "azp": testAndroidClient,
		"sub": "1234567890", "email": email, "email_verified": true, "name": "Test User",
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
}

func signGoogle(t *testing.T, claims jwt.MapClaims, key *rsa.PrivateKey) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = testGoogleKID
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// verifiedUser adds an active, email-verified local account.
func (f *adminFixture) verifiedUser(t *testing.T, status string) *store.User {
	t.Helper()
	u := f.addUser(t, false)
	u.EmailVerified = true
	u.AccountStatus = status
	return u
}

// ── C1: the token route takes only a Google ID token issued to us ──────────

func TestOAuthTokenAcceptsGoogleIDTokenForOurClient(t *testing.T) {
	f := newOAuthFixture(t)
	key, _ := testKeys()
	u := f.verifiedUser(t, store.AccountStatusActive)
	for _, aud := range []string{testAndroidClient, testWebClientID} {
		c := googleClaims(*u.Email)
		c["aud"] = aud
		res, err := f.svc.HandleOAuthToken(context.Background(), "google", signGoogle(t, c, key))
		if err != nil || res.Auth == nil || res.Auth.Tokens.AccessToken == "" {
			t.Fatalf("aud %s: valid ID token refused: %v", aud, err)
		}
	}
}

func TestOAuthTokenRefusesUnverifiableIDTokens(t *testing.T) {
	key, other := testKeys()
	cases := []struct {
		name   string
		mutate func(jwt.MapClaims)
		signer *rsa.PrivateKey
		hs256  bool
		raw    string
	}{
		{name: "issued to another app", mutate: func(c jwt.MapClaims) { c["aud"] = "quiz-site.apps.googleusercontent.com"; c["azp"] = c["aud"] }},
		{name: "missing audience", mutate: func(c jwt.MapClaims) { delete(c, "aud") }},
		{name: "foreign issuer", mutate: func(c jwt.MapClaims) { c["iss"] = "https://evil.example" }},
		{name: "expired", mutate: func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-10 * time.Minute).Unix() }},
		{name: "no exp", mutate: func(c jwt.MapClaims) { delete(c, "exp") }},
		{name: "email not verified", mutate: func(c jwt.MapClaims) { c["email_verified"] = false }},
		{name: "email_verified absent", mutate: func(c jwt.MapClaims) { delete(c, "email_verified") }},
		{name: "no subject", mutate: func(c jwt.MapClaims) { delete(c, "sub") }},
		{name: "signed by another key", signer: other},
		{name: "HS256", hs256: true},
		{name: "bare access token", raw: "ya29.a0AfH6SMBaccess-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newOAuthFixture(t)
			u := f.verifiedUser(t, store.AccountStatusActive)
			c := googleClaims(*u.Email)
			if tc.mutate != nil {
				tc.mutate(c)
			}
			raw := tc.raw
			switch {
			case raw != "":
			case tc.hs256:
				tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
				tok.Header["kid"] = testGoogleKID
				raw, _ = tok.SignedString([]byte("secret"))
			case tc.signer != nil:
				raw = signGoogle(t, c, tc.signer)
			default:
				raw = signGoogle(t, c, key)
			}
			res, err := f.svc.HandleOAuthToken(context.Background(), "google", raw)
			if err == nil || res != nil {
				t.Fatalf("%s: token accepted", tc.name)
			}
			if !errors.Is(err, ErrOAuthIDTokenInvalid) {
				t.Fatalf("%s: err = %v, want ErrOAuthIDTokenInvalid", tc.name, err)
			}
			if len(f.st.byID) != 0 {
				t.Fatalf("%s: a session was created", tc.name)
			}
		})
	}
}

func TestOAuthTokenRefusesProvidersWithoutIDTokenVerification(t *testing.T) {
	f := newOAuthFixture(t)
	for _, p := range []string{"github", "apple"} {
		if _, err := f.svc.HandleOAuthToken(context.Background(), p, "anything"); !errors.Is(err, ErrOAuthTokenProviderUnsupported) {
			t.Fatalf("%s: err = %v", p, err)
		}
	}
}

func TestOAuthTokenFailsClosedWhenUnconfigured(t *testing.T) {
	key, _ := testKeys()
	for name, oc := range map[string]*config.OAuthConfig{
		"no oauth config": nil,
		"no client ids":   {Google: config.OAuthProviderConfig{ClientSecret: "s"}},
	} {
		f := newOAuthFixture(t)
		f.svc.cfg.OAuth = oc
		u := f.verifiedUser(t, store.AccountStatusActive)
		if _, err := f.svc.HandleOAuthToken(context.Background(), "google", signGoogle(t, googleClaims(*u.Email), key)); !errors.Is(err, ErrOAuthNotConfigured) {
			t.Fatalf("%s: err = %v, want ErrOAuthNotConfigured", name, err)
		}
	}
}

// ── M6: OAuth sign-in applies the password path's account rules ───────────

// linkedStore makes GetUserByLoginProvider find the account, so the
// already-linked branch is exercised too.
type linkedStore struct {
	*adminSessionStore
	linked *store.User
}

func (l linkedStore) GetUserByLoginProvider(_ context.Context, _, email string) (*store.User, error) {
	if l.linked != nil && l.linked.Email != nil && *l.linked.Email == email {
		return l.linked, nil
	}
	return nil, nil
}

func TestOAuthSignInAppliesLifecycleRules(t *testing.T) {
	key, _ := testKeys()
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(72 * time.Hour)
	cases := []struct {
		name       string
		status     string
		purge      *time.Time
		wantErr    error
		wantStatus string
	}{
		{"deactivated is reactivated", store.AccountStatusDeactivated, nil, nil, store.AccountStatusActive},
		{"deletion in window is cancelled", store.AccountStatusPendingDeletion, &future, nil, store.AccountStatusActive},
		{"deletion past window is refused", store.AccountStatusPendingDeletion, &past, ErrAccountPendingPurge, store.AccountStatusPendingDeletion},
		{"purged is refused", store.AccountStatusPurged, nil, ErrAccountPurged, store.AccountStatusPurged},
		{"pending verification is refused", store.AccountStatusPendingVerification, nil, ErrEmailNotVerified, store.AccountStatusPendingVerification},
	}
	for _, tc := range cases {
		for _, branch := range []string{"linked", "email-link"} {
			t.Run(tc.name+"/"+branch, func(t *testing.T) {
				f := newOAuthFixture(t)
				u := f.verifiedUser(t, tc.status)
				u.ScheduledPurgeDate = tc.purge
				svc := f.svc
				if branch == "linked" {
					svc = New(linkedStore{f.st, u}, &fakeProducer{}, f.svc.cfg, f.svc.log, f.svc.rdb, nil)
					svc.googleKeySet = f.svc.googleKeySet
				}
				res, err := svc.HandleOAuthToken(context.Background(), "google", signGoogle(t, googleClaims(*u.Email), key))
				if tc.wantErr != nil {
					if !errors.Is(err, tc.wantErr) {
						t.Fatalf("err = %v, want %v", err, tc.wantErr)
					}
					if len(f.st.byID) != 0 {
						t.Fatal("a session was created for a refused account")
					}
				} else if err != nil || res.Auth == nil || res.Auth.Tokens.AccessToken == "" {
					t.Fatalf("sign-in refused: %v", err)
				}
				if u.AccountStatus != tc.wantStatus {
					t.Fatalf("status %q, want %q", u.AccountStatus, tc.wantStatus)
				}
			})
		}
	}
}

// ── M7: the browser flow's state is single use, browser-bound, with PKCE ──

func TestOAuthRedirectStoresBoundStateWithPKCE(t *testing.T) {
	f := newOAuthFixture(t)
	start, err := f.svc.GetOAuthRedirectURL(context.Background(), "google")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(start.URL)
	q := u.Query()
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("no PKCE challenge in %s", start.URL)
	}
	if start.Binding == "" || strings.Contains(start.URL, start.Binding) {
		t.Fatal("binding secret missing or leaked into the provider URL")
	}
	raw, err := f.mr.Get(oauthStatePrefix + q.Get("state"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, start.Binding) {
		t.Fatal("binding stored in clear; only its hash belongs in Redis")
	}
}

func TestOAuthRedirectFailsClosedWhenUnconfigured(t *testing.T) {
	f := newOAuthFixture(t)
	f.svc.cfg.OAuth.Google.ClientID = ""
	if _, err := f.svc.GetOAuthRedirectURL(context.Background(), "google"); !errors.Is(err, ErrOAuthNotConfigured) {
		t.Fatalf("err = %v", err)
	}
}

// fakeTokenEndpoint stands in for Google's token endpoint and records what
// the code exchange sent.
func fakeTokenEndpoint(t *testing.T) (*httptest.Server, *[]url.Values) {
	t.Helper()
	var got []url.Values
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		got = append(got, r.PostForm)
		mu.Unlock()
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	orig := googleEndpoint
	googleEndpoint.TokenURL = srv.URL
	t.Cleanup(func() { googleEndpoint = orig })
	return srv, &got
}

func startFlow(t *testing.T, f *adminFixture) (state, binding, challenge string) {
	t.Helper()
	start, err := f.svc.GetOAuthRedirectURL(context.Background(), "google")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(start.URL)
	return u.Query().Get("state"), start.Binding, u.Query().Get("code_challenge")
}

func TestOAuthCallbackSendsPKCEVerifierFromTheBoundBrowser(t *testing.T) {
	f := newOAuthFixture(t)
	_, got := fakeTokenEndpoint(t)
	state, binding, challenge := startFlow(t, f)
	_, err := f.svc.HandleOAuthCallback(context.Background(), "google", "code-1", state, binding)
	if err == nil || errors.Is(err, ErrOAuthStateInvalid) {
		t.Fatalf("expected the (fake) exchange to be attempted and fail, got %v", err)
	}
	if len(*got) != 1 {
		t.Fatalf("token endpoint hit %d times", len(*got))
	}
	sum := sha256.Sum256([]byte((*got)[0].Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
		t.Fatal("code_verifier sent to the token endpoint does not match the challenge")
	}
}

func TestOAuthCallbackRefusesUnboundOrReplayedState(t *testing.T) {
	cases := map[string]func(state, binding string) (string, string, string){
		"no binding cookie": func(s, _ string) (string, string, string) { return "google", s, "" },
		"another browser":   func(s, _ string) (string, string, string) { return "google", s, "attacker-binding" },
		"other provider":    func(s, b string) (string, string, string) { return "github", s, b },
		"unknown state":     func(_, b string) (string, string, string) { return "google", "forged", b },
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			f := newOAuthFixture(t)
			_, got := fakeTokenEndpoint(t)
			state, binding, _ := startFlow(t, f)
			p, s, b := mk(state, binding)
			if _, err := f.svc.HandleOAuthCallback(context.Background(), p, "code", s, b); !errors.Is(err, ErrOAuthStateInvalid) {
				t.Fatalf("err = %v, want ErrOAuthStateInvalid", err)
			}
			if len(*got) != 0 {
				t.Fatal("the code was exchanged for a refused callback")
			}
		})
	}
}

func TestOAuthStateIsSingleUseUnderConcurrency(t *testing.T) {
	f := newOAuthFixture(t)
	_, got := fakeTokenEndpoint(t)
	state, binding, _ := startFlow(t, f)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = f.svc.HandleOAuthCallback(context.Background(), "google", "code", state, binding)
		}()
	}
	wg.Wait()
	if len(*got) != 1 {
		t.Fatalf("one state was redeemed %d times", len(*got))
	}
}

// ── M5: password reset kills live access tokens at once ────────────────────

type liveSessionsStore struct{ *adminSessionStore }

func (l liveSessionsStore) ListActiveSessions(_ context.Context, uid uuid.UUID) ([]store.Session, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []store.Session
	for _, s := range l.byID {
		if s.UserID == uid && s.RevokedAt == nil {
			out = append(out, *s)
		}
	}
	return out, nil
}

func TestPasswordResetWritesRevocationCache(t *testing.T) {
	f := newAdminFixture(t)
	svc := New(liveSessionsStore{f.st}, &fakeProducer{}, f.svc.cfg, f.svc.log, f.svc.rdb, nil)
	u := f.addUser(t, false)
	resp, err := svc.startSession(context.Background(), u, "d", "android", "1.1.1.1", "okhttp/4", []string{AMRPassword})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ResetPassword(context.Background(), *u.Email, "123456", "N3w!Passw0rd"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if !f.mr.Exists("sess_revoked:" + resp.SessionID.String()) {
		t.Fatal("password reset left the session's access token live")
	}
}

// ── M2: the session janitor is bounded ─────────────────────────────────────

type fakePurger struct {
	batches []int64
	calls   int
	limit   int
	ret     time.Duration
}

func (p *fakePurger) PurgeDeadSessions(_ context.Context, retention time.Duration, limit int) (int64, error) {
	p.limit, p.ret = limit, retention
	if p.calls >= len(p.batches) {
		p.calls++
		return 0, nil
	}
	n := p.batches[p.calls]
	p.calls++
	return n, nil
}

func TestSessionJanitorStopsAtShortBatch(t *testing.T) {
	p := &fakePurger{batches: []int64{1000, 1000, 10}}
	j := NewSessionJanitor(p, nil)
	if n := j.Sweep(context.Background()); n != 2010 || p.calls != 3 {
		t.Fatalf("swept %d in %d calls", n, p.calls)
	}
	if p.limit != 1000 || p.ret != 30*24*time.Hour {
		t.Fatalf("limit %d retention %v", p.limit, p.ret)
	}
}

func TestSessionJanitorIsBoundedPerTick(t *testing.T) {
	full := make([]int64, 100)
	for i := range full {
		full[i] = 1000
	}
	p := &fakePurger{batches: full}
	j := NewSessionJanitor(p, nil)
	j.Sweep(context.Background())
	if p.calls != j.MaxBatches {
		t.Fatalf("%d batches in one tick, cap is %d", p.calls, j.MaxBatches)
	}
}

// The verifier refuses on its own when given no audiences, whatever its
// caller checked first.
func TestVerifyGoogleIDTokenWithoutAudiencesIsNotConfigured(t *testing.T) {
	f := newOAuthFixture(t)
	key, _ := testKeys()
	raw := signGoogle(t, googleClaims("a@b.test"), key)
	if _, err := verifyGoogleIDToken(context.Background(), f.svc.googleKeys(), raw, nil, time.Now()); !errors.Is(err, ErrOAuthNotConfigured) {
		t.Fatalf("err = %v", err)
	}
}
