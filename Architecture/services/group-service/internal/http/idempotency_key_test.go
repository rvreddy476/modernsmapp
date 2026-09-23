package http

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// ctxWithHeader builds a bare gin context carrying only the header under test.
func ctxWithHeader(header string) *gin.Context {
	req := httptest.NewRequest("POST", "/v1/groups", nil)
	if header != "" {
		req.Header.Set("Idempotency-Key", header)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return c
}

// TestResolveIdempotencyKeyFallsBackToHeader is the regression guard for the
// "idempotency_key is required" failure on POST /v1/groups: the web client
// stamps an Idempotency-Key header on every write and does not send the
// snake_case body field, so the header alone must satisfy the requirement.
func TestResolveIdempotencyKeyFallsBackToHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name    string
		bodyKey string
		header  string
		want    string
	}{
		// The bug: body field absent, header present. Must resolve.
		{name: "header only", bodyKey: "", header: "hdr-key", want: "hdr-key"},
		// Existing callers that send the body field keep working unchanged.
		{name: "body only", bodyKey: "body-key", header: "", want: "body-key"},
		// Body field is canonical when both arrive, so a client that has
		// deliberately chosen a stable key is not overridden by the
		// per-request UUID the axios interceptor stamps.
		{name: "body wins over header", bodyKey: "body-key", header: "hdr-key", want: "body-key"},
		// Whitespace is not a key in either position.
		{name: "blank body falls through to header", bodyKey: "   ", header: "hdr-key", want: "hdr-key"},
		{name: "whitespace header is absent", bodyKey: "", header: "   ", want: ""},
		{name: "neither", bodyKey: "", header: "", want: ""},
		// Values are trimmed, not passed through raw.
		{name: "trims body", bodyKey: "  body-key  ", header: "", want: "body-key"},
		{name: "trims header", bodyKey: "", header: "  hdr-key  ", want: "hdr-key"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveIdempotencyKey(ctxWithHeader(tc.header), tc.bodyKey)
			if got != tc.want {
				t.Fatalf("resolveIdempotencyKey(body=%q, header=%q) = %q, want %q",
					tc.bodyKey, tc.header, got, tc.want)
			}
		})
	}
}
