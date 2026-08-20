package middleware

import (
	"net/http"
	"strings"

	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
)

// CredentialSource records which non-ambient credential successfully passed
// authentication. It is intentionally set only after signature/claims
// validation; the mere presence of an Authorization header is not trusted.
type CredentialSource string

const (
	CredentialBearer CredentialSource = "bearer"
	CredentialCookie CredentialSource = "cookie"

	authenticatedCredentialSourceKey = "identity.authenticated_credential_source"
)

// ReadAccessToken gives an explicit bearer token priority over an ambient
// cookie. Native clients therefore have a deterministic non-cookie path even
// if a WebView or shared HTTP stack happens to carry cookies.
func ReadAccessToken(c *gin.Context, cookieName string) (string, CredentialSource) {
	if auth := c.GetHeader("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		if token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")); token != "" {
			return token, CredentialBearer
		}
	}
	if token, err := c.Cookie(cookieName); err == nil && token != "" {
		return token, CredentialCookie
	}
	return "", ""
}

// MarkAuthenticatedCredential must be called only after the selected token is
// fully validated. RequireCSRF relies on this provenance marker.
func MarkAuthenticatedCredential(c *gin.Context, source CredentialSource) {
	c.Set(authenticatedCredentialSourceKey, source)
}

// RequireCSRF protects ambient cookie authentication while allowing native
// clients that authenticated with a validated bearer token to perform writes.
// Client-declared platform headers are deliberately irrelevant.
func RequireCSRF(cookieName, headerName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if isSafeMethod(c.Request.Method) {
			c.Next()
			return
		}

		if source, ok := c.Get(authenticatedCredentialSourceKey); ok && source == CredentialBearer {
			c.Next()
			return
		}

		headerToken := c.GetHeader(headerName)
		cookieToken, err := c.Cookie(cookieName)
		if err != nil || headerToken == "" || cookieToken == "" || headerToken != cookieToken {
			api.Error(c.Writer, http.StatusForbidden, "CSRF_FAILED", "Missing or invalid CSRF token", nil, nil)
			c.Abort()
			return
		}

		c.Next()
	}
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}
