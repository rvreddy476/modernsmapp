package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestReadAccessTokenPrefersExplicitBearer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodPut, "/", nil)
	req.Header.Set("Authorization", "Bearer native-token")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: "ambient-cookie"})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	token, source := ReadAccessToken(c, "access_token")
	if token != "native-token" || source != CredentialBearer {
		t.Fatalf("got token=%q source=%q", token, source)
	}
}

func TestRequireCSRFUsesValidatedCredentialSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		method     string
		source     CredentialSource
		headerAuth bool
		platform   bool
		csrfHeader string
		csrfCookie string
		wantStatus int
	}{
		{name: "validated bearer write", method: http.MethodPut, source: CredentialBearer, wantStatus: http.StatusNoContent},
		{name: "cookie write with matching csrf", method: http.MethodPut, source: CredentialCookie, csrfHeader: "same", csrfCookie: "same", wantStatus: http.StatusNoContent},
		{name: "cookie write without csrf", method: http.MethodPut, source: CredentialCookie, wantStatus: http.StatusForbidden},
		{name: "raw bearer header is insufficient", method: http.MethodPut, headerAuth: true, wantStatus: http.StatusForbidden},
		{name: "client platform is insufficient", method: http.MethodPut, platform: true, wantStatus: http.StatusForbidden},
		{name: "safe method", method: http.MethodGet, wantStatus: http.StatusNoContent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			if tt.source != "" {
				r.Use(func(c *gin.Context) {
					MarkAuthenticatedCredential(c, tt.source)
					c.Next()
				})
			}
			r.Use(RequireCSRF("csrf_token", "X-CSRF-Token"))
			r.Any("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

			req := httptest.NewRequest(tt.method, "/", nil)
			if tt.headerAuth {
				req.Header.Set("Authorization", "Bearer unvalidated")
			}
			if tt.platform {
				req.Header.Set("X-Client-Platform", "android")
			}
			if tt.csrfHeader != "" {
				req.Header.Set("X-CSRF-Token", tt.csrfHeader)
			}
			if tt.csrfCookie != "" {
				req.AddCookie(&http.Cookie{Name: "csrf_token", Value: tt.csrfCookie})
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
