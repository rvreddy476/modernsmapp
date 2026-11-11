package digilocker

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPKCE_ChallengeAndStateShape(t *testing.T) {
	// RFC 7636 appendix B.
	if got := CodeChallengeS256("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("challenge = %q", got)
	}
	s, err := NewState()
	if err != nil || !ValidState(s) {
		t.Fatalf("NewState() = %q, %v", s, err)
	}
	s2, _ := NewState()
	if s == s2 {
		t.Fatal("two states are equal")
	}
	for _, bad := range []string{"", "short", s + "x", strings.Repeat("!", 43)} {
		if ValidState(bad) {
			t.Fatalf("ValidState(%q) = true", bad)
		}
	}
	if HashState("a", s) == HashState("b", s) || HashState("a", s) == HashState("a", s2) {
		t.Fatal("state hash must depend on namespace and state")
	}
}

func TestNew_ModeSelection(t *testing.T) {
	cfg := HTTPConfig{TokenURL: "https://t", EAadhaarURL: "https://e", ClientID: "id", ClientSecret: "s", RedirectURI: "https://r"}
	if _, err := New("mock", true, cfg); err == nil {
		t.Fatal("mock must be refused in production")
	}
	if _, err := New("", true, cfg); err == nil {
		t.Fatal("unset mode must be refused in production")
	}
	if c, err := New("disabled", true, cfg); err != nil || c != nil {
		t.Fatalf("disabled = %v, %v", c, err)
	}
	if _, err := New("mokc", false, cfg); err == nil {
		t.Fatal("a typo must not select the mock")
	}
	if _, err := New("http", true, HTTPConfig{}); err == nil {
		t.Fatal("http without config must be refused")
	}
	if c, err := New("http", true, cfg); err != nil || c == nil {
		t.Fatalf("http = %v, %v", c, err)
	}
}

func TestSettingsFromEnv_ProductionDefaultsToDisabledAndHTTPS(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	s, err := SettingsFromEnv(get, true, "https://app.example/verify/return")
	if err != nil || s.Client != nil || s.Mode != ModeDisabled {
		t.Fatalf("production default = %+v, %v", s.Mode, err)
	}
	if _, err := SettingsFromEnv(get, true, "http://app.example/return"); err == nil {
		t.Fatal("an http redirect URI must be refused in production")
	}
	s, err = SettingsFromEnv(get, false, "")
	if err != nil || !s.Mock() {
		t.Fatalf("dev default must be the mock: %v", err)
	}
}

func TestMock_IdentityIsDeterministicAndCarriesTheCodedName(t *testing.T) {
	ctx := context.Background()
	m := NewMockClient()
	s, err := m.ExchangeCode(ctx, MockCodeForName("Asha Rao"), "v")
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.Identity(ctx, s)
	if err != nil || id.Name != "Asha Rao" || len(id.Photo) == 0 || len(id.AadhaarLast4) != 4 {
		t.Fatalf("identity = %v, %v", id, err)
	}
	k1, _ := id.PersonKey()
	s2, _ := m.ExchangeCode(ctx, MockCodeForName("Asha Rao"), "other")
	id2, _ := m.Identity(ctx, s2)
	k2, _ := id2.PersonKey()
	if k1 != k2 {
		t.Fatal("one code must be one person")
	}
	if _, err := m.ExchangeCode(ctx, MockFailCode, "v"); !errors.Is(err, ErrProvider) {
		t.Fatalf("fail code = %v", err)
	}
	s3, _ := m.ExchangeCode(ctx, MockNoAadhaarCode, "v")
	if _, err := m.Identity(ctx, s3); !errors.Is(err, ErrNoAadhaar) {
		t.Fatalf("no-aadhaar = %v", err)
	}
	if fmt.Sprintf("%v %+v %#v", *id, *id, *id) != strings.Repeat("digilocker.Identity{redacted} ", 2)+"digilocker.Identity{redacted}" {
		t.Fatal("identity must print redacted")
	}
}

func TestNames_NormaliseAndMatch(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"Ravi Kumar", "ravi  kumar", true},
		{"KUMAR, Ravi", "Ravi Kumar", true},
		{"Ravi Kumar", "R Kumar", false},
		{"Ravi Kumar", "Ravi Kumar Official", false},
		{"", "", false},
		{"Ravi Kumar 🔥", "Ravi Kumar", true},
	} {
		if got := NamesMatch(tc.a, tc.b); got != tc.want {
			t.Errorf("NamesMatch(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}

const eaadhaarXML = `<?xml version="1.0" encoding="UTF-8"?>
<Certificate language="99" name="Aadhaar Card"><CertificateData><KycRes code="x" ret="Y">
<UidData uid="xxxxxxxx1234">
<Poi dob="05-06-1992" gender="F" name="Asha Rao"/>
<Poa co="secret" house="secret"/>
<Pht>%s</Pht>
</UidData></KycRes></CertificateData><Signature/></Certificate>`

func TestHTTP_ExchangeSendsVerifierAndIdentityReadsEAadhaar(t *testing.T) {
	photo := []byte{0xff, 0xd8, 0xff, 0x01, 0x02}
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			gotForm = r.PostForm
			_, _ = w.Write([]byte(`{"access_token":"tok","digilockerid":"dl-1","eaadhaar":"Y"}`))
		case "/eaadhaar":
			if r.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = fmt.Fprintf(w, eaadhaarXML, base64.StdEncoding.EncodeToString(photo))
		}
	}))
	defer srv.Close()
	c := NewHTTPClient(HTTPConfig{TokenURL: srv.URL + "/token", EAadhaarURL: srv.URL + "/eaadhaar", ClientID: "cid", ClientSecret: "sec", RedirectURI: "https://r"}, srv.Client())
	s, err := c.ExchangeCode(context.Background(), "code-1", "verifier-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotForm.Get("code_verifier") != "verifier-1" || gotForm.Get("code") != "code-1" || gotForm.Get("redirect_uri") != "https://r" {
		t.Fatal("token request must carry code, code_verifier and redirect_uri")
	}
	id, err := c.Identity(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if id.Name != "Asha Rao" || id.DOB.Format("2006-01-02") != "1992-06-05" || id.AadhaarLast4 != "1234" || string(id.Photo) != string(photo) {
		t.Fatal("identity fields not read from eAadhaar")
	}
}

func TestHTTP_NoAadhaarAndProviderFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token-noaadhaar":
			_, _ = w.Write([]byte(`{"access_token":"tok","eaadhaar":"N"}`))
		case "/token-500":
			w.WriteHeader(http.StatusInternalServerError)
		case "/e":
			// A full record: only the token's eaadhaar flag may refuse it.
			_, _ = fmt.Fprintf(w, eaadhaarXML, base64.StdEncoding.EncodeToString([]byte{1}))
		}
	}))
	defer srv.Close()
	c := NewHTTPClient(HTTPConfig{TokenURL: srv.URL + "/token-noaadhaar", EAadhaarURL: srv.URL + "/e"}, srv.Client())
	s, err := c.ExchangeCode(context.Background(), "c", "v")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Identity(context.Background(), s); !errors.Is(err, ErrNoAadhaar) {
		t.Fatalf("eaadhaar=N must be ErrNoAadhaar, got %v", err)
	}
	c = NewHTTPClient(HTTPConfig{TokenURL: srv.URL + "/token-500"}, srv.Client())
	if _, err := c.ExchangeCode(context.Background(), "c", "v"); !errors.Is(err, ErrProvider) {
		t.Fatalf("500 must be ErrProvider, got %v", err)
	}
}

func TestLastFourDigits(t *testing.T) {
	for in, want := range map[string]string{"xxxxxxxx1234": "1234", "123456789012": "9012", "xx12": "", "": "", "xxxx12a4": ""} {
		if got := lastFourDigits(in); got != want {
			t.Errorf("lastFourDigits(%q) = %q, want %q", in, got, want)
		}
	}
}
