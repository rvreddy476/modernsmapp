package identityroles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/shared/httpclient"
)

// DefaultTimeout bounds a single call to identity.
//
// Five seconds, matching shared/httpclient.Default and commerce-service's
// existing identity contact client. It is deliberately short: nothing on a
// request path waits for this client (see worker.go), so a long timeout would
// buy nothing and would only make a stuck delivery attempt hold a worker slot.
const DefaultTimeout = 5 * time.Second

// Sentinel errors. The distinction that matters is PERMANENT vs RETRYABLE:
// the worker retries the second kind forever (with backoff) and dead-letters
// the first kind immediately, because retrying it would never succeed and
// would only bury the real problem under a retry loop.
var (
	// ErrNotGrantable is identity's 403 ROLE_NOT_GRANTABLE — the role is
	// outside the four ecosystem roles. PERMANENT: a caller bug, not an
	// outage. Retrying cannot fix a misspelled role name.
	ErrNotGrantable = errors.New("identityroles: role is not service-grantable")

	// ErrBadRequest is identity's 400 — malformed user_id, or a missing
	// required field. PERMANENT, for the same reason.
	ErrBadRequest = errors.New("identityroles: identity rejected the request as malformed")

	// ErrUnauthorized is a 401/403 that is NOT ROLE_NOT_GRANTABLE, i.e. the
	// internal service key was missing, wrong, or stripped at the edge.
	// PERMANENT in the sense that retrying with the same key will not help,
	// and it must be loud: it means every grant this service makes is failing.
	ErrUnauthorized = errors.New("identityroles: internal service key rejected")

	// ErrUnavailable is a transport failure, a timeout, or a 5xx. RETRYABLE.
	ErrUnavailable = errors.New("identityroles: identity unavailable")

	// ErrNotConfigured is returned when the client has no base URL. Treated as
	// PERMANENT so a misconfigured deployment dead-letters visibly instead of
	// spinning; see Client.Configured.
	ErrNotConfigured = errors.New("identityroles: client has no base URL")
)

// IsPermanent reports whether retrying err could ever succeed. The worker uses
// it to decide between backoff and dead-letter.
func IsPermanent(err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrUnavailable):
		return false
	default:
		return errors.Is(err, ErrNotGrantable) ||
			errors.Is(err, ErrBadRequest) ||
			errors.Is(err, ErrUnauthorized) ||
			errors.Is(err, ErrNotConfigured)
	}
}

// Client calls identity-auth-service's internal role API.
//
// Every route it uses is guarded by RequireInternalServiceKey and refused at
// the edge, so this only works in-cluster. Construct it with the same
// INTERNAL_SERVICE_KEY the rest of the service already reads.
type Client struct {
	baseURL string
	key     string
	// service names the caller in identity's audit row —
	// "commerce-service", "food-service", "rider-service". identity requires
	// it. It is attribution, not authentication: the shared key proves the
	// caller is inside the cluster, not which service it is.
	service string
	http    *http.Client
}

// NewClient builds a client. baseURL is the in-cluster origin
// (http://identity-auth:8081); an empty baseURL yields a client that reports
// Configured() == false and fails every call with ErrNotConfigured, which is
// what a local dev stack without identity gets.
//
// No tracing transport is wired here on purpose: shared/go.mod does not
// require otelhttp directly and the repo is vendored, so adding it would churn
// vendor/ for every service in the workspace. commerce-service's own
// internal/identity client shows the one-line upgrade when someone wants it.
func NewClient(baseURL, internalKey, service string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		key:     strings.TrimSpace(internalKey),
		service: service,
		http:    httpclient.New(DefaultTimeout),
	}
}

// WithHTTPClient replaces the transport. Tests use it to inject an httptest
// server's client or a deliberately tiny timeout.
func (c *Client) WithHTTPClient(h *http.Client) *Client {
	if h != nil {
		c.http = h
	}
	return c
}

// Configured reports whether the client can reach identity at all.
func (c *Client) Configured() bool { return c != nil && c.baseURL != "" }

// Grant gives userID the role. IDEMPOTENT — identity returns 200 for a role
// the user already holds, so a retried delivery or a re-run backfill is safe.
//
// The role reaches the user's access token on its NEXT MINT, which includes
// POST /v1/auth/refresh. No re-login is needed, but the grant is not instant
// in an already-issued token.
func (c *Client) Grant(ctx context.Context, userID, role, reason string) error {
	return c.mutate(ctx, http.MethodPost, userID, role, reason)
}

// Revoke removes the role. IDEMPOTENT — revoking a role the user does not hold
// is a 200.
//
// NOTE: a revoke does not invalidate access tokens already minted with the
// role; the scope disappears on the next mint (<= ACCESS_TOKEN_TTL). A
// suspension that must bite immediately needs the owning service to check its
// own state too. That is one more reason suspension does not revoke: see the
// package doc.
func (c *Client) Revoke(ctx context.Context, userID, role, reason string) error {
	return c.mutate(ctx, http.MethodDelete, userID, role, reason)
}

type mutateBody struct {
	UserID  string `json:"user_id"`
	Role    string `json:"role"`
	Service string `json:"service"`
	Reason  string `json:"reason,omitempty"`
}

func (c *Client) mutate(ctx context.Context, method, userID, role, reason string) error {
	if !c.Configured() {
		return ErrNotConfigured
	}
	if !IsEcosystemRole(role) {
		// Fail locally rather than spend a round trip learning identity's 403.
		return fmt.Errorf("%w: %q", ErrNotGrantable, role)
	}
	body, err := json.Marshal(mutateBody{
		UserID:  userID,
		Role:    role,
		Service: c.service,
		Reason:  reason,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/v1/auth/internal/roles", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.auth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		// Transport failure, DNS failure, connection refused, or the context
		// deadline firing. All retryable.
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer drain(resp)
	return classify(resp)
}

// Roles returns the user's resolved, implication-expanded roles.
//
// TRAP, and the reason UserExists exists next to it: this endpoint returns 200
// with an empty list for a user id that does not exist in identity at all.
// auth-service's ResolveRoles deliberately never errors — it degrades to the
// env allowlist on a DB failure — so "no roles" and "no such user" are the
// same answer here. Do NOT use this as an existence check.
func (c *Client) Roles(ctx context.Context, userID string) ([]string, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/auth/internal/roles/"+userID, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	c.auth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer drain(resp)
	if err := classify(resp); err != nil {
		return nil, err
	}
	var env struct {
		Data struct {
			UserID string   `json:"user_id"`
			Roles  []string `json:"roles"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("%w: decoding roles: %v", ErrUnavailable, err)
	}
	if env.Data.Roles == nil {
		return []string{}, nil
	}
	return env.Data.Roles, nil
}

// UserExists reports whether userID is a real account in identity.
//
// This is the ONLY honest existence check available over the internal API: it
// hits GET /v1/auth/internal/users/{id}, which 404s for an unknown id. The
// backfill depends on it — commerce_db's `sellers` table carries thousands of
// rows whose user_id is a random UUID minted by integration-test fixtures that
// were wrongly pointed at the live database, and auth.user_roles has NO
// foreign key to auth.users, so a naive backfill would cheerfully create role
// rows for people who do not exist.
//
// A false return is a definite "no such user". An error means we could not
// find out, and a caller must treat that as "do not touch", never as "no".
func (c *Client) UserExists(ctx context.Context, userID string) (bool, error) {
	if !c.Configured() {
		return false, ErrNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/auth/internal/users/"+userID, nil)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	c.auth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer drain(resp)
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if err := classify(resp); err != nil {
		return false, err
	}
	return true, nil
}

func (c *Client) auth(req *http.Request) {
	if c.key != "" {
		req.Header.Set("X-Internal-Service-Key", c.key)
	}
}

// classify turns a response status into one of the sentinels. It reads the
// error envelope's `code` so 403 ROLE_NOT_GRANTABLE (a caller bug) is not
// confused with a 403 from a rejected internal key (an ops problem).
func classify(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	code, msg := errEnvelope(resp)
	switch {
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: status %d %s", ErrUnavailable, resp.StatusCode, msg)
	case resp.StatusCode == http.StatusForbidden && code == "ROLE_NOT_GRANTABLE":
		return fmt.Errorf("%w: %s", ErrNotGrantable, msg)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: status %d %s", ErrUnauthorized, resp.StatusCode, msg)
	case resp.StatusCode == http.StatusTooManyRequests:
		// Rate limited is a "come back later", not a caller bug.
		return fmt.Errorf("%w: status 429 %s", ErrUnavailable, msg)
	default:
		return fmt.Errorf("%w: status %d %s %s", ErrBadRequest, resp.StatusCode, code, msg)
	}
}

// errEnvelope best-effort reads {"error":{"code":..,"message":..}}. A body
// that is not that shape (a proxy's HTML 502, say) yields empty strings rather
// than an error — the status code already told us what we needed.
func errEnvelope(resp *http.Response) (code, message string) {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if err != nil {
		return "", ""
	}
	if json.Unmarshal(body, &env) != nil {
		return "", strings.TrimSpace(string(body))
	}
	return env.Error.Code, env.Error.Message
}

// drain empties and closes the body so the connection can be reused.
func drain(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
	_ = resp.Body.Close()
}
