package digilocker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/kyc"
)

func synthAadhaarShaped(t *testing.T) string {
	t.Helper()
	for c := '0'; c <= '9'; c++ {
		if n := "34567890123" + string(c); kyc.LooksLikeAadhaar(n) {
			return n
		}
	}
	t.Fatal("no Aadhaar-shaped synthetic number")
	return ""
}

func byKind(docs []IssuedDocument) map[DocumentKind]IssuedDocument {
	out := map[DocumentKind]IssuedDocument{}
	for _, d := range docs {
		out[d.Kind] = d
	}
	return out
}

func TestMockClient_IssuesValidDLAndRCAndAnAadhaarReferenceOnly(t *testing.T) {
	ctx := context.Background()
	m := NewMockClient()
	session, err := m.ExchangeCode(ctx, "mock-abc", "verifier-value")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	docs, err := m.IssuedDocuments(ctx, session)
	if err != nil {
		t.Fatalf("documents: %v", err)
	}
	k := byKind(docs)
	if len(docs) != 3 || len(k) != 3 {
		t.Fatalf("documents = %d kinds %d, want AADHAAR, DRIVING_LICENCE, VEHICLE_RC", len(docs), len(k))
	}
	a := k[KindAadhaar]
	if a.Number != "" || a.Reference == "" || kyc.LooksLikeAadhaar(a.Reference) || a.DocType == "" {
		t.Fatal("the Aadhaar document must carry a reference and never a number")
	}
	dl := k[KindDrivingLicence]
	if _, err := kyc.ValidateDrivingLicence(dl.Number); err != nil {
		t.Fatalf("mock DL fails shared/kyc: %v", err)
	}
	rc := k[KindVehicleRC]
	if _, err := kyc.ValidateVehicleRegistration(rc.Number); err != nil {
		t.Fatalf("mock RC fails shared/kyc: %v", err)
	}
	for _, d := range []IssuedDocument{dl, rc} {
		if d.ValidUntil == nil || !d.ValidUntil.After(time.Now()) || d.NameOnDocument == "" {
			t.Fatalf("%s has no future validity or name", d.Kind)
		}
		if strings.Contains(d.Reference, d.Number) {
			t.Fatalf("%s reference embeds the number", d.Kind)
		}
	}
	// Deterministic per code, distinct across codes.
	again, _ := m.ExchangeCode(ctx, "mock-abc", "another-verifier")
	docs2, _ := m.IssuedDocuments(ctx, again)
	if byKind(docs2)[KindDrivingLicence].Number != dl.Number || byKind(docs2)[KindVehicleRC].Number != rc.Number {
		t.Fatal("mock numbers are not deterministic for one code")
	}
	other, _ := m.ExchangeCode(ctx, "mock-xyz", "verifier-value")
	docs3, _ := m.IssuedDocuments(ctx, other)
	if byKind(docs3)[KindDrivingLicence].Number == dl.Number {
		t.Fatal("two codes produced the same DL")
	}
}

func TestMockClient_FailAndMissingInputs(t *testing.T) {
	ctx := context.Background()
	m := NewMockClient()
	if _, err := m.ExchangeCode(ctx, "fail", "verifier-value"); err == nil {
		t.Fatal("code fail did not simulate a provider failure")
	}
	if _, err := m.ExchangeCode(ctx, "", "verifier-value"); err == nil {
		t.Fatal("empty code accepted")
	}
	if _, err := m.ExchangeCode(ctx, "mock-abc", ""); err == nil {
		t.Fatal("empty code verifier accepted")
	}
	if _, err := m.IssuedDocuments(ctx, nil); err == nil {
		t.Fatal("nil session accepted")
	}
}

// The mock verifies everyone. In production that is a forged KYC record for
// every partner, so it must refuse to be selected there.
func TestNew_RefusesMockInProduction(t *testing.T) {
	for _, mode := range []string{"mock", "", " MOCK "} {
		c, err := New(mode, true, HTTPConfig{})
		if err == nil || c != nil {
			t.Fatalf("mode %q in production: client %T, err %v; want refused", mode, c, err)
		}
	}
}

func TestNew_ModesOutsideProduction(t *testing.T) {
	for _, mode := range []string{"mock", "", "Mock"} {
		c, err := New(mode, false, HTTPConfig{})
		if err != nil {
			t.Fatalf("mode %q outside production: %v", mode, err)
		}
		if _, ok := c.(*MockClient); !ok {
			t.Fatalf("mode %q: got %T, want *MockClient", mode, c)
		}
	}
	for _, production := range []bool{true, false} {
		if c, err := New("disabled", production, HTTPConfig{}); err != nil || c != nil {
			t.Fatalf("disabled (production=%v): %T, %v", production, c, err)
		}
	}
	if _, err := New("mokc", false, HTTPConfig{}); err == nil {
		t.Fatal("a typo selected a client")
	}
	if _, err := New("http", true, HTTPConfig{TokenURL: "https://example.test/token"}); err == nil {
		t.Fatal("http mode accepted an incomplete config")
	}
}

func TestSettingsFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if _, err := SettingsFromEnv(env(map[string]string{"ENV": "production"})); err == nil {
		t.Fatal("production without DIGILOCKER_MODE selected the mock")
	}
	s, err := SettingsFromEnv(env(map[string]string{"ENV": "dev", "FOOD_PUBLIC_BASE_URL": "http://localhost:8080/"}))
	if err != nil || !s.Mock() || s.Client == nil {
		t.Fatalf("dev default = %+v, %v; want mock", s.Mode, err)
	}
	if got := s.RedirectURI(); got != "http://localhost:8080"+ReturnPath {
		t.Fatalf("redirect uri = %s", got)
	}
	if _, err := SettingsFromEnv(env(map[string]string{"ENV": "production", "DIGILOCKER_MODE": "disabled", "FOOD_RIDER_APP_LINK_URL": "http://rider.example.test/return"})); err == nil {
		t.Fatal("production accepted a non-https App Link")
	}
}

func TestHTTPClient_PKCEExchangeAndAadhaarNumberDropped(t *testing.T) {
	aadhaar := synthAadhaarShaped(t)
	var sawVerifier bool
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		sawVerifier = r.PostForm.Get("code_verifier") == "the-verifier" &&
			r.PostForm.Get("grant_type") == "authorization_code" &&
			r.PostForm.Get("code") == "the-code" && r.PostForm.Get("redirect_uri") == "https://api.example.test/return"
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "digilockerid": "dl-account-1", "name": "Test Rider"})
	})
	mux.HandleFunc("/issued", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
			{"doctype": "ADHAR", "uri": "in.gov.uidai-ADHAR-" + aadhaar, "name": "Aadhaar Card"},
			{"doctype": "DRVLC", "uri": "in.gov.transport-DRVLC-KA0120200000001", "name": "Driving License"},
			{"doctype": "RVCER", "uri": "in.gov.transport-RVCER-KA01AB1234", "name": "Registration of Vehicles"},
			{"doctype": "OTHER", "uri": "in.gov.other-OTHER-1"},
		}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New("http", true, HTTPConfig{TokenURL: srv.URL + "/token", IssuedDocumentsURL: srv.URL + "/issued",
		ClientID: "cid", ClientSecret: "csecret", RedirectURI: "https://api.example.test/return"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := c.ExchangeCode(context.Background(), "the-code", "the-verifier")
	if err != nil || !sawVerifier {
		t.Fatalf("exchange = %v, verifier sent = %v", err, sawVerifier)
	}
	docs, err := c.IssuedDocuments(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	k := byKind(docs)
	if len(k) != 3 {
		t.Fatalf("kinds = %d", len(k))
	}
	for _, d := range docs {
		if strings.Contains(d.Reference, aadhaar) || strings.Contains(d.Number, aadhaar) || strings.Contains(d.NameOnDocument, aadhaar) {
			t.Fatal("the Aadhaar number crossed the client boundary")
		}
	}
	if k[KindAadhaar].Number != "" || k[KindDrivingLicence].Number != "KA0120200000001" || k[KindVehicleRC].Number != "KA01AB1234" {
		t.Fatal("document numbers mapped wrongly")
	}
	if strings.Contains(session.String(), "tok") {
		t.Fatal("session String leaks the access token")
	}
}
