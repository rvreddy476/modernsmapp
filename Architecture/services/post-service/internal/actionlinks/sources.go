package actionlinks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Where the eligibility facts are read (the routes live-service-v2's
// go-live eligibility already reads; nothing was added to the services
// read). Every call carries X-Internal-Service-Key and NO user header.
//
//   - Account age and status: identity user-service
//     GET {IDENTITY_USER_SERVICE_URL}/v1/users/:userId
//     -> {"data":{"id","status","created_at",...}}; 404 = no such account.
//   - Email verified: identity auth-service's internal contact read
//     GET {AUTH_SERVICE_URL}/v1/auth/internal/users/:userId
//     -> {"data":{"user_id","email","email_verified"}}; 404 = no account.
//     Only the flag is kept: the address is never stored, logged or put
//     in an error.
//
// Strikes come from trust-safety's standing route, through the client
// post-service already holds for publication (internal/standing).

// EnvIdentityUserServiceURL and EnvAuthServiceURL name the two origins;
// the defaults are the compose names.
const (
	EnvIdentityUserServiceURL = "IDENTITY_USER_SERVICE_URL"
	EnvAuthServiceURL         = "AUTH_SERVICE_URL"
	DefaultIdentityUserURL    = "http://identity-user:8110"
	DefaultAuthServiceURL     = "http://identity-auth:8081"

	sourceTimeout   = 2 * time.Second
	sourceMaxBody   = 64 << 10
	callerServiceID = "post-service"
)

// AccountInfo is what the account read establishes.
type AccountInfo struct {
	Found     bool
	Active    bool
	CreatedAt time.Time
}

// AccountSource reads an account's status and creation time.
type AccountSource interface {
	Account(ctx context.Context, userID uuid.UUID) (AccountInfo, error)
}

// EmailSource reads whether an account's email address is verified.
type EmailSource interface {
	EmailVerified(ctx context.Context, userID uuid.UUID) (bool, error)
}

func sourceHTTP() *http.Client {
	return &http.Client{
		Timeout:       sourceTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func sourceGet(ctx context.Context, client *http.Client, rawURL, internalKey string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, nil, errors.New("build request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Caller-Service", callerServiceID)
	if internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", internalKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, errors.New("request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, sourceMaxBody))
	if err != nil {
		return 0, nil, errors.New("read body")
	}
	return http.StatusOK, body, nil
}

func trimBase(baseURL string) string { return strings.TrimRight(strings.TrimSpace(baseURL), "/") }

// HTTPAccounts is AccountSource over the identity user-service.
type HTTPAccounts struct {
	baseURL, internalKey string
	http                 *http.Client
}

// NewHTTPAccounts returns nil for an empty baseURL (the fact is then unknown).
func NewHTTPAccounts(baseURL, internalKey string) *HTTPAccounts {
	if trimBase(baseURL) == "" {
		return nil
	}
	return &HTTPAccounts{baseURL: trimBase(baseURL), internalKey: internalKey, http: sourceHTTP()}
}

func (c *HTTPAccounts) Account(ctx context.Context, userID uuid.UUID) (AccountInfo, error) {
	status, body, err := sourceGet(ctx, c.http, c.baseURL+"/v1/users/"+userID.String(), c.internalKey)
	if err != nil {
		return AccountInfo{}, fmt.Errorf("account read: %w", err)
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return AccountInfo{}, nil
	default:
		return AccountInfo{}, fmt.Errorf("account read: status %d", status)
	}
	var w struct {
		Data *struct {
			ID        string     `json:"id"`
			Status    string     `json:"status"`
			CreatedAt *time.Time `json:"created_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &w); err != nil || w.Data == nil {
		return AccountInfo{}, errors.New("account read: malformed body")
	}
	if got, err := uuid.Parse(w.Data.ID); err != nil || got != userID {
		return AccountInfo{}, errors.New("account read: body is for a different user")
	}
	// A missing creation time must never read as "year 1" (old enough).
	if w.Data.Status == "" || w.Data.CreatedAt == nil || w.Data.CreatedAt.IsZero() {
		return AccountInfo{}, errors.New("account read: status or created_at missing")
	}
	return AccountInfo{Found: true, Active: w.Data.Status == "active", CreatedAt: *w.Data.CreatedAt}, nil
}

// HTTPEmails is EmailSource over the identity auth-service.
type HTTPEmails struct {
	baseURL, internalKey string
	http                 *http.Client
}

// NewHTTPEmails returns nil for an empty baseURL (the fact is then unknown).
func NewHTTPEmails(baseURL, internalKey string) *HTTPEmails {
	if trimBase(baseURL) == "" {
		return nil
	}
	return &HTTPEmails{baseURL: trimBase(baseURL), internalKey: internalKey, http: sourceHTTP()}
}

func (c *HTTPEmails) EmailVerified(ctx context.Context, userID uuid.UUID) (bool, error) {
	status, body, err := sourceGet(ctx, c.http, c.baseURL+"/v1/auth/internal/users/"+userID.String(), c.internalKey)
	if err != nil {
		return false, fmt.Errorf("contact read: %w", err)
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("contact read: status %d", status)
	}
	var w struct {
		Data *struct {
			UserID        string `json:"user_id"`
			Email         string `json:"email"`
			EmailVerified *bool  `json:"email_verified"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &w); err != nil || w.Data == nil {
		return false, errors.New("contact read: malformed body")
	}
	if got, err := uuid.Parse(w.Data.UserID); err != nil || got != userID {
		return false, errors.New("contact read: body is for a different user")
	}
	if w.Data.EmailVerified == nil {
		return false, errors.New("contact read: no email_verified")
	}
	if strings.TrimSpace(w.Data.Email) == "" {
		return false, nil
	}
	return *w.Data.EmailVerified, nil
}
