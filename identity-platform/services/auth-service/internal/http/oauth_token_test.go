package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/identity-auth-service/internal/service"
)

func postOAuthToken(o *oauthStub, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/oauth/google/token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "okhttp/4.12")
	req.RemoteAddr = "203.0.113.7:4321"
	return serve(oauthRouter(o), req)
}

func authCookieSet(w *httptest.ResponseRecorder) bool {
	for _, c := range w.Result().Cookies() {
		if c.Name == accessTokenCookieName || c.Name == refreshTokenCookieName {
			return true
		}
	}
	return false
}

// The token route opened Google sessions with IP "", user agent "" and device
// "oauth": the client is now passed through, like a password login's.
func TestOAuthTokenPassesTheClientThrough(t *testing.T) {
	o := &oauthStub{stubAuthService: &stubAuthService{}}
	w := postOAuthToken(o, `{"id_token":"eyJ.id.token","device_id":"install-7","platform":"android"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	want := service.OAuthClient{IP: "203.0.113.7", UserAgent: "okhttp/4.12", DeviceID: "install-7", Platform: "android"}
	if o.gotClient != want {
		t.Fatalf("client = %+v, want %+v", o.gotClient, want)
	}
}

func TestOAuthCallbackPassesTheClientThrough(t *testing.T) {
	o := &oauthStub{stubAuthService: &stubAuthService{}}
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/oauth/google/callback?code=c&state=s", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.RemoteAddr = "198.51.100.4:999"
	serve(oauthRouter(o), req)
	if o.gotClient.IP != "198.51.100.4" || o.gotClient.UserAgent != "Mozilla/5.0" {
		t.Fatalf("callback client = %+v", o.gotClient)
	}
}

// The anomaly step-up was dropped and answered as a generic failure; it now
// gets the password login's response: 200 with the envelope, no cookies.
func TestOAuthTokenRendersTheStepUpEnvelope(t *testing.T) {
	envelope := &service.AuthResponse{RequiresStepUp: true, PendingToken: "pending-1", StepUpMethods: []string{service.StepUpMethodEmail}}
	o := &oauthStub{
		stubAuthService: &stubAuthService{},
		tokenResult:     &service.OAuthCallbackResult{Auth: envelope},
		tokenErr:        service.ErrAnomalyStepUpRequired,
	}
	w := postOAuthToken(o, `{"id_token":"eyJ.id.token"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("step-up: status %d body %s", w.Code, w.Body.String())
	}
	var body struct {
		Data struct {
			RequiresStepUp bool   `json:"requires_step_up"`
			PendingToken   string `json:"pending_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	if !body.Data.RequiresStepUp || body.Data.PendingToken != "pending-1" {
		t.Fatalf("step-up envelope not rendered: %s", w.Body.String())
	}
	if authCookieSet(w) {
		t.Fatal("a step-up response set auth cookies")
	}
}

// A 2FA-pending provider sign-in has no tokens: no cookies are written.
func TestOAuthTokenTwoFactorPendingSetsNoCookies(t *testing.T) {
	o := &oauthStub{
		stubAuthService: &stubAuthService{},
		tokenResult:     &service.OAuthCallbackResult{Auth: &service.AuthResponse{Requires2FA: true, PendingToken: "p"}},
	}
	w := postOAuthToken(o, `{"id_token":"eyJ.id.token"}`)
	if w.Code != http.StatusOK || authCookieSet(w) {
		t.Fatalf("2FA pending: status %d, cookies set %v", w.Code, authCookieSet(w))
	}
}
