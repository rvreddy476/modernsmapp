package digilocker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestModeSelection(t *testing.T) {
	cfg := HTTPConfig{TokenURL: "https://dl.example.test/token", ClientID: "c", ClientSecret: "s", RedirectURI: "https://app.example.test/dl"}
	if c, err := New("mock", true, cfg); err == nil || c != nil {
		t.Fatal("mock accepted in production")
	}
	if c, err := New("", true, cfg); err == nil || c != nil {
		t.Fatal("unset mode (mock) accepted in production")
	}
	if c, err := New("mock", false, cfg); err != nil || c == nil {
		t.Fatalf("mock in dev: %v", err)
	}
	if c, err := New("disabled", true, cfg); err != nil || c != nil {
		t.Fatalf("disabled: %v %v", c, err)
	}
	if _, err := New("mokc", false, cfg); err == nil {
		t.Fatal("typo accepted")
	}
	if _, err := New("http", true, HTTPConfig{}); err == nil {
		t.Fatal("http without registration accepted")
	}
	if c, err := New("http", true, cfg); err != nil || c == nil {
		t.Fatalf("http: %v", err)
	}
}

func TestMockGender(t *testing.T) {
	m := &MockClient{}
	for code, want := range map[string]string{"mock-female": GenderFemale, "mock-male": GenderMale, "x-other": GenderOther} {
		s, err := m.ExchangeCode(context.Background(), code, "v")
		if err != nil {
			t.Fatal(err)
		}
		a, err := m.Aadhaar(context.Background(), s)
		if err != nil || a.Gender != want || !strings.HasPrefix(a.Reference, "digilocker:mock-") {
			t.Fatalf("%s: %+v %v", code, a, err)
		}
	}
	if _, err := m.ExchangeCode(context.Background(), "fail", "v"); !errors.Is(err, ErrProvider) {
		t.Fatalf("fail: %v", err)
	}
	s, _ := m.ExchangeCode(context.Background(), "no-aadhaar", "v")
	if _, err := m.Aadhaar(context.Background(), s); !errors.Is(err, ErrNoAadhaar) {
		t.Fatalf("no aadhaar: %v", err)
	}
	if _, err := m.ExchangeCode(context.Background(), "mock", ""); err == nil {
		t.Fatal("no verifier accepted")
	}
}

func TestHTTPExchangeSendsPKCEAndReadsGender(t *testing.T) {
	var form url.Values
	gender, eaadhaar := "F", "Y"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		fmt.Fprintf(w, `{"access_token":"at","digilockerid":"acc-1","name":"N","dob":"01011990","gender":%q,"eaadhaar":%q}`, gender, eaadhaar)
	}))
	defer srv.Close()
	c, err := New("http", true, HTTPConfig{TokenURL: srv.URL, ClientID: "cid", ClientSecret: "sec", RedirectURI: "https://app.example.test/dl"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.ExchangeCode(context.Background(), "code-1", "verifier-1")
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("code_verifier") != "verifier-1" || form.Get("code") != "code-1" || form.Get("grant_type") != "authorization_code" {
		t.Fatalf("form %v", form)
	}
	a, err := c.Aadhaar(context.Background(), s)
	if err != nil || a.Gender != GenderFemale || a.Reference != "digilocker:acc-1:ADHAR" || a.PhotoMediaID != nil {
		t.Fatalf("aadhaar %+v %v", a, err)
	}
	if strings.Contains(fmt.Sprintf("%v %#v %v", a, a, s), "acc-1") {
		t.Fatal("result or session leaks through fmt")
	}
	gender = "X"
	s, _ = c.ExchangeCode(context.Background(), "code-2", "v")
	if _, err := c.Aadhaar(context.Background(), s); !errors.Is(err, ErrNoAadhaar) {
		t.Fatalf("unknown gender: %v", err)
	}
	gender, eaadhaar = "M", "N"
	s, _ = c.ExchangeCode(context.Background(), "code-3", "v")
	if _, err := c.Aadhaar(context.Background(), s); !errors.Is(err, ErrNoAadhaar) {
		t.Fatalf("no eaadhaar: %v", err)
	}
}

func TestHTTPProviderErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	c, _ := New("http", false, HTTPConfig{TokenURL: srv.URL, ClientID: "c", ClientSecret: "s", RedirectURI: "https://a.example.test"})
	if _, err := c.ExchangeCode(context.Background(), "c", "v"); !errors.Is(err, ErrProvider) {
		t.Fatalf("500: %v", err)
	}
}

func TestAuthorizeURL(t *testing.T) {
	s := Settings{Mode: ModeHTTP, Client: &HTTPClient{}, AuthorizeURL: DefaultAuthorizeURL, ClientID: "cid", RedirectURI: "https://app.example.test/dl"}
	raw, err := AuthorizeURL(s, "st", CodeChallengeS256("v"))
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != CodeChallengeS256("v") || q.Get("state") != "st" || q.Get("redirect_uri") != s.RedirectURI {
		t.Fatalf("authorize %s", raw)
	}
	mock := Settings{Mode: ModeMock, Client: &MockClient{}, RedirectURI: "http://localhost:8080/doorstep-pro/digilocker"}
	raw, _ = AuthorizeURL(mock, "st", "c")
	if !strings.HasPrefix(raw, mock.RedirectURI+"?") || !strings.Contains(raw, "state=st") {
		t.Fatalf("mock authorize %s", raw)
	}
}

func TestPKCE(t *testing.T) {
	st, err := NewState()
	if err != nil || !ValidState(st) {
		t.Fatalf("state %q %v", st, err)
	}
	if ValidState(st+"x") || ValidState("short") || ValidState(strings.Repeat("!", 43)) {
		t.Fatal("bad state accepted")
	}
	// RFC 7636 appendix B.
	if got := CodeChallengeS256("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("challenge %s", got)
	}
	if HashState("a") == HashState("b") || len(HashState("a")) != 64 {
		t.Fatal("hash")
	}
	if MapGender(" f ") != GenderFemale || MapGender("M") != GenderMale || MapGender("T") != GenderOther || MapGender("") != "" {
		t.Fatal("gender map")
	}
}
