// Package integration wires HTTP-level integration tests against a
// running atpost stack. Build-tagged so `go test ./...` doesn't try
// to run them; activate with `go test -tags integration`.
//
// Required env vars (all optional but the relevant tests skip when
// missing):
//
//	ATPOST_POST_URL          default http://localhost:8084
//	ATPOST_MONETIZATION_URL  default http://localhost:8099
//	ATPOST_USER_URL          default http://localhost:8082
//	ATPOST_GRAPH_URL         default http://localhost:8083
//	ATPOST_API_GATEWAY_URL   default http://localhost:8080
//	ATPOST_INTERNAL_KEY      default ""  (X-Internal-Service-Key, optional)
//
// Auth: each request carries a real HS256 JWT (Authorization: Bearer) minted
// for the synthetic user id, signed with the dev JWT_SECRET, AND the legacy
// X-User-Id header. The gateway strips inbound X-User-Id and derives identity
// from the verified token (so gateway-routed tests work); services dialed
// directly still read X-User-Id. Set ATPOST_JWT_SECRET to match the stack's
// JWT_SECRET (defaults to the docker-compose dev value).
//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// jwtSecret / jwtKID match the dev stack (docker-compose default). Override via
// env when the target stack uses a different signing secret / kid.
func jwtSecret() string { return envOr("ATPOST_JWT_SECRET", "local_dev_jwt_change_me") }
func jwtKID() string    { return envOr("ATPOST_JWT_KID", "v1") }

// mintToken builds an HS256 access token for userID (hand-rolled, no external
// dep — mirrors what identity-auth issues). scopes is space-separated and
// embedded as the `scopes` claim so gateway-routed admin calls authorize too.
func mintToken(userID uuid.UUID, scopes string) string {
	b64 := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT", "kid": jwtKID()})
	claims, _ := json.Marshal(map[string]any{
		"sub":     userID.String(),
		"user_id": userID.String(),
		"scopes":  scopes,
		"iat":     time.Now().Unix(),
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	signingInput := b64(header) + "." + b64(claims)
	mac := hmac.New(sha256.New, []byte(jwtSecret()))
	mac.Write([]byte(signingInput))
	return signingInput + "." + b64(mac.Sum(nil))
}

// ServiceURLs is the set of base URLs the tests dial. Defaults match
// run-local.sh; override per-test via env if your stack uses
// different ports.
type ServiceURLs struct {
	Post         string
	Monetization string
	User         string
	Graph        string
	APIGateway   string
}

// LoadServiceURLs reads env (with the run-local.sh defaults).
func LoadServiceURLs() ServiceURLs {
	return ServiceURLs{
		Post:         envOr("ATPOST_POST_URL", "http://localhost:8084"),
		Monetization: envOr("ATPOST_MONETIZATION_URL", "http://localhost:8099"),
		User:         envOr("ATPOST_USER_URL", "http://localhost:8082"),
		Graph:        envOr("ATPOST_GRAPH_URL", "http://localhost:8083"),
		APIGateway:   envOr("ATPOST_API_GATEWAY_URL", "http://localhost:8080"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// internalKey returns the X-Internal-Service-Key value (empty if not
// configured — services accept an empty header in dev mode).
func internalKey() string { return os.Getenv("ATPOST_INTERNAL_KEY") }

// HTTPClient is a thin wrapper that adds X-User-Id and the optional
// internal service key on every request. Methods return the parsed
// JSON envelope `{data, error, meta}` plus the raw HTTP status code.
type HTTPClient struct {
	BaseURL string
	UserID    uuid.UUID
	AdminRole string
	// BearerOverride, when set, is sent as the Authorization token verbatim
	// instead of a minted one — used by the auth E2E flow to carry the real
	// access token returned by login/refresh.
	BearerOverride string
	c              *http.Client
}

// NewHTTPClient constructs a client whose every request carries
// X-User-Id: userID. Pass uuid.Nil for unauthenticated calls.
func NewHTTPClient(baseURL string, userID uuid.UUID) *HTTPClient {
	return &HTTPClient{
		BaseURL: baseURL,
		UserID:  userID,
		c:       &http.Client{Timeout: 10 * time.Second},
	}
}

// WithAdminRole returns a copy of the client that sends X-Admin-Role
// on every request, used by admin-gated endpoints (creator-fund
// settlement, settlement queue, etc.). The downstream services
// honor X-Admin-Role only when the request also carries the
// X-Internal-Service-Key, so dev stacks need INTERNAL_SERVICE_KEY
// set + ATPOST_INTERNAL_KEY exported for this to work.
func (h *HTTPClient) WithAdminRole() *HTTPClient {
	clone := *h
	clone.AdminRole = "admin"
	return &clone
}

// WithBearer returns a copy that sends the given access token (e.g. the one
// returned by /v1/auth/login) instead of a minted one. Use for the auth flow
// where the real token's claims are what's under test.
func (h *HTTPClient) WithBearer(token string) *HTTPClient {
	clone := *h
	clone.BearerOverride = token
	return &clone
}

// Envelope mirrors the shared/api.Response shape so tests can read
// data + error fields without redeclaring per-call.
type Envelope struct {
	Status int             `json:"-"`
	Data   json.RawMessage `json:"data,omitempty"`
	Error  *struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Details json.RawMessage `json:"details,omitempty"`
	} `json:"error,omitempty"`
	Meta json.RawMessage `json:"meta,omitempty"`
}

// Do executes the call. Body may be nil. If body is non-nil it is
// JSON-encoded and sent with Content-Type: application/json.
func (h *HTTPClient) Do(ctx context.Context, method, path string, body interface{}) (*Envelope, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.BaseURL+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	switch {
	case h.BearerOverride != "":
		// Real login/refresh token — identity comes only from the token (no
		// X-User-Id), so this exercises the genuine auth path.
		req.Header.Set("Authorization", "Bearer "+h.BearerOverride)
	case h.UserID != uuid.Nil:
		// Real JWT for gateway-routed calls (gateway strips inbound X-User-Id
		// and derives identity from the verified token); X-User-Id kept for
		// services dialed directly. Admin clients get privileged scopes so the
		// gateway's admin/internal gate passes too.
		scopes := ""
		if h.AdminRole != "" {
			scopes = "admin moderator superadmin"
		}
		req.Header.Set("Authorization", "Bearer "+mintToken(h.UserID, scopes))
		req.Header.Set("X-User-Id", h.UserID.String())
	}
	if h.AdminRole != "" {
		req.Header.Set("X-Admin-Role", h.AdminRole)
	}
	if k := internalKey(); k != "" {
		req.Header.Set("X-Internal-Service-Key", k)
	}
	resp, err := h.c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	env := &Envelope{Status: resp.StatusCode}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, env); err != nil {
			// Not all error paths return the envelope shape; surface
			// the raw payload so the test message points at the
			// actual response.
			env.Error = &struct {
				Code    string          `json:"code"`
				Message string          `json:"message"`
				Details json.RawMessage `json:"details,omitempty"`
			}{Code: "RAW", Message: string(raw)}
		}
	}
	return env, nil
}

// MustDo wraps Do with t.Fatal on transport error. Use for steps
// where a non-2xx response is itself the test signal you want to
// inspect (the Envelope still has Status + Error filled in).
func (h *HTTPClient) MustDo(t *testing.T, ctx context.Context, method, path string, body interface{}) *Envelope {
	t.Helper()
	env, err := h.Do(ctx, method, path, body)
	if err != nil {
		t.Fatalf("%s %s: transport error: %v", method, path, err)
	}
	return env
}

// MustOK calls MustDo and additionally fails the test if Status is
// outside [200, 300). Returns the envelope's Data for further parse.
func (h *HTTPClient) MustOK(t *testing.T, ctx context.Context, method, path string, body interface{}) json.RawMessage {
	t.Helper()
	env := h.MustDo(t, ctx, method, path, body)
	if env.Status < 200 || env.Status >= 300 {
		errMsg := ""
		if env.Error != nil {
			errMsg = env.Error.Code + ": " + env.Error.Message
		}
		t.Fatalf("%s %s: expected 2xx, got %d (%s)", method, path, env.Status, errMsg)
	}
	return env.Data
}

// SkipIfDown bails out the test if the relevant base URL doesn't
// answer a /health probe. Lets the suite run cleanly on a dev box
// where only some services are up.
func SkipIfDown(t *testing.T, urls ...string) {
	t.Helper()
	c := &http.Client{Timeout: 2 * time.Second}
	for _, u := range urls {
		if u == "" {
			continue
		}
		resp, err := c.Get(strings.TrimRight(u, "/") + "/health")
		if err != nil || resp.StatusCode >= 500 {
			if resp != nil {
				resp.Body.Close()
			}
			t.Skipf("integration: %s not reachable (set up docker compose first)", u)
			return
		}
		resp.Body.Close()
	}
}

// SkipIfNotIntegration is a belt-and-suspenders gate: even with the
// build tag, a test will skip unless ATPOST_RUN_INTEGRATION=1 so a
// stray `go test -tags integration ./...` from a misconfigured CI
// doesn't try to dial real services.
func SkipIfNotIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("ATPOST_RUN_INTEGRATION") != "1" {
		t.Skip("integration: set ATPOST_RUN_INTEGRATION=1 to run")
	}
}
