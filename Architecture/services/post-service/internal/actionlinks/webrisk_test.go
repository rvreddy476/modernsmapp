package actionlinks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const testKey = "test-web-risk-key-not-real"

// The Lookup API client: the request it sends, and each answer it reads.
func TestGoogleWebRiskLookup(t *testing.T) {
	var gotQuery, gotKey string
	answer, status := `{}`, http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotKey = r.URL.RawQuery, r.Header.Get("X-Goog-Api-Key")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	defer srv.Close()
	g := NewGoogleWebRisk(testKey, srv.URL, nil)

	v, err := g.Check(context.Background(), "https://example.com/a?b=1")
	if err != nil || v.Unsafe {
		t.Fatalf("empty answer: %+v %v, want safe", v, err)
	}
	if gotKey != testKey {
		t.Fatalf("key header = %q", gotKey)
	}
	if strings.Contains(gotQuery, testKey) || strings.Contains(gotQuery, "key=") {
		t.Fatalf("the key travelled in the query string: %s", gotQuery)
	}
	for _, want := range []string{"threatTypes=MALWARE", "threatTypes=SOCIAL_ENGINEERING", "threatTypes=UNWANTED_SOFTWARE", "uri=https%3A%2F%2Fexample.com%2Fa%3Fb%3D1"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q lacks %q", gotQuery, want)
		}
	}

	answer = `{"threat":{"threatTypes":["SOCIAL_ENGINEERING"],"expireTime":"2026-10-24T00:00:00Z"}}`
	if v, err := g.Check(context.Background(), "https://phish.example"); err != nil || !v.Unsafe || v.ThreatTypes[0] != "SOCIAL_ENGINEERING" {
		t.Fatalf("threat answer: %+v %v, want unsafe", v, err)
	}

	for _, tc := range []struct {
		status int
		body   string
	}{{http.StatusInternalServerError, `{}`}, {http.StatusForbidden, `{"error":{}}`}, {http.StatusOK, `not json`}, {http.StatusOK, `[]`}, {http.StatusOK, ``}} {
		answer, status = tc.body, tc.status
		_, err := g.Check(context.Background(), "https://example.com")
		if !errors.Is(err, ErrCheckUnavailable) {
			t.Errorf("status %d body %q: err = %v, want ErrCheckUnavailable", tc.status, tc.body, err)
		}
		if err != nil && strings.Contains(err.Error(), testKey) {
			t.Errorf("error quotes the key: %v", err)
		}
	}
}

func TestGoogleWebRiskTimeoutIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	g := NewGoogleWebRisk(testKey, srv.URL, &http.Client{Timeout: 20 * time.Millisecond})
	if _, err := g.Check(context.Background(), "https://example.com"); !errors.Is(err, ErrCheckUnavailable) {
		t.Fatalf("err = %v, want ErrCheckUnavailable", err)
	}
}

func TestMockChecker(t *testing.T) {
	m := &Mock{}
	for url, unsafe := range map[string]bool{
		"https://testsafebrowsing.appspot.com/s/malware.html":  true,
		"https://testsafebrowsing.appspot.com/s/phishing.html": true,
		"https://testsafebrowsing.appspot.com/s/unwanted.html": true,
		"https://testsafebrowsing.appspot.com/":                false,
		"https://example.com/s/malware.html":                   false,
	} {
		v, err := m.Check(context.Background(), url)
		if err != nil || v.Unsafe != unsafe {
			t.Errorf("%s: %+v %v, want unsafe=%v", url, v, err, unsafe)
		}
	}
	down := &Mock{Err: ErrCheckUnavailable}
	if _, err := down.Check(context.Background(), "https://example.com"); !errors.Is(err, ErrCheckUnavailable) {
		t.Fatalf("outage mock: %v", err)
	}
}

// The key builds Google's client; the mock only outside production; nothing
// otherwise.
func TestCheckerFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if c := CheckerFromEnv(env(map[string]string{EnvWebRiskAPIKey: testKey}), true); c == nil || c.Name() != "google_web_risk" {
		t.Fatalf("with a key: %v", c)
	}
	if c := CheckerFromEnv(env(map[string]string{EnvWebRiskMock: "true"}), false); c == nil || c.Name() != "mock" {
		t.Fatalf("dev mock: %v", c)
	}
	if c := CheckerFromEnv(env(map[string]string{EnvWebRiskMock: "true"}), true); c != nil {
		t.Fatalf("production got the mock: %v", c.Name())
	}
	if c := CheckerFromEnv(env(nil), false); c != nil {
		t.Fatalf("no key, no mock: %v", c.Name())
	}
}

// The identity reads: the facts, and every answer that is not one.
func TestEligibilitySources(t *testing.T) {
	user := uuid.New()
	created := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var accountBody, contactBody string
	var accountStatus, contactStatus int
	var sawKey, sawUser bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey = r.Header.Get("X-Internal-Service-Key") == "k"
		sawUser = r.Header.Get("X-User-Id") != ""
		switch r.URL.Path {
		case "/v1/users/" + user.String():
			w.WriteHeader(accountStatus)
			_, _ = w.Write([]byte(accountBody))
		case "/v1/auth/internal/users/" + user.String():
			w.WriteHeader(contactStatus)
			_, _ = w.Write([]byte(contactBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	acc, mail := NewHTTPAccounts(srv.URL, "k"), NewHTTPEmails(srv.URL, "k")
	body := func(v any) string { b, _ := json.Marshal(map[string]any{"data": v}); return string(b) }

	accountStatus, accountBody = 200, body(map[string]any{"id": user, "status": "active", "created_at": created})
	info, err := acc.Account(context.Background(), user)
	if err != nil || !info.Found || !info.Active || !info.CreatedAt.Equal(created) || !sawKey || sawUser {
		t.Fatalf("account: %+v %v key=%v user=%v", info, err, sawKey, sawUser)
	}
	accountStatus = 404
	if info, err := acc.Account(context.Background(), user); err != nil || info.Found {
		t.Fatalf("404: %+v %v", info, err)
	}
	for _, b := range []string{body(map[string]any{"id": uuid.New(), "status": "active", "created_at": created}),
		body(map[string]any{"id": user, "status": "active"}), `nope`} {
		accountStatus, accountBody = 200, b
		if _, err := acc.Account(context.Background(), user); err == nil {
			t.Errorf("account body %s accepted", b)
		}
	}
	accountStatus = 500
	if _, err := acc.Account(context.Background(), user); err == nil {
		t.Error("500 accepted")
	}

	contactStatus, contactBody = 200, body(map[string]any{"user_id": user, "email": "a@example.com", "email_verified": true})
	if ok, err := mail.EmailVerified(context.Background(), user); err != nil || !ok {
		t.Fatalf("verified: %v %v", ok, err)
	}
	contactBody = body(map[string]any{"user_id": user, "email": "", "email_verified": true})
	if ok, err := mail.EmailVerified(context.Background(), user); err != nil || ok {
		t.Fatalf("no address: %v %v", ok, err)
	}
	contactBody = body(map[string]any{"user_id": user, "email": "a@example.com"})
	if _, err := mail.EmailVerified(context.Background(), user); err == nil {
		t.Fatal("no flag accepted")
	}
	contactStatus = 503
	if _, err := mail.EmailVerified(context.Background(), user); err == nil {
		t.Fatal("503 accepted")
	}
	if NewHTTPAccounts(" ", "k") != nil || NewHTTPEmails("", "k") != nil {
		t.Fatal("an empty origin built a source")
	}
}
