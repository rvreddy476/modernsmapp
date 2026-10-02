package service

// Where the going-live facts are read (2 Oct 2026). Every route already
// existed and is called by other services; nothing was added to the services
// read. Every call carries X-Internal-Service-Key and NO user header.
//
//   - Date of birth: identity-profile's service-only identity read
//     GET {PROFILE_SERVICE_URL}/internal/v1/profiles/users/:userId/identity
//     -> {"data":{"user_id","first_name","dob":"YYYY-MM-DD"|null,"dob_source"}}
//     404 = no such account, or one that is deactivated, scheduled for
//     deletion, purged or hidden. Only a date identity stands behind
//     (dob_source registration or profile) counts. The date and the name
//     never reach a log line or an error.
//   - Account age and status: identity user-service
//     GET {IDENTITY_USER_SERVICE_URL}/v1/users/:userId
//     -> {"data":{"id","status","is_verified","created_at","updated_at"}}
//     404 = no such account. Active is status == "active" ("hidden" is a
//     deactivated or deletion-scheduled account).
//   - Posts: post-service GET {POST_SERVICE_URL}/v1/posts/by-author/:id/counts
//     -> {"data":{"<content_type>":n, ..., "total":n}}: the author's posts
//     and videos that are not deleted.
//   - Followers: graph-service GET {GRAPH_SERVICE_URL}/v1/graph/counts/:id
//     -> {"data":{"user_id","follower_count",...}}.
//   - Phone verified: NO internal route exposes it. auth-service's
//     GET /v1/auth/internal/users/:userId answers user_id, email, phone and
//     email_verified only; phone_verified is on /v1/auth/me, which needs the
//     user's own token. Until a route carries it there is no PhoneSource and
//     the requirement is unknown.

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

const (
	eligCallerService = "live-service-v2"
	eligMaxBody       = 64 << 10
)

// eligHTTP is the client the fact sources share. It never follows a
// redirect: that would resend the internal key somewhere else.
func eligHTTP() *http.Client {
	return &http.Client{
		Timeout:       directoryTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// eligGet reads rawURL and returns the status and, for a 200, the body. The
// errors are fixed text plus the status: a body is never quoted.
func eligGet(ctx context.Context, client *http.Client, rawURL, internalKey string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, nil, errors.New("build request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Caller-Service", eligCallerService)
	if internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", internalKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, eligMaxBody))
	if err != nil {
		return 0, nil, errors.New("read body")
	}
	return http.StatusOK, body, nil
}

func trimBase(baseURL string) string { return strings.TrimRight(strings.TrimSpace(baseURL), "/") }

// --- date of birth ---

// HTTPBirthDates is BirthDateSource over identity-profile.
type HTTPBirthDates struct {
	baseURL, internalKey string
	http                 *http.Client
}

// NewHTTPBirthDates returns nil when baseURL is empty (adult is then unknown).
func NewHTTPBirthDates(baseURL, internalKey string) *HTTPBirthDates {
	if trimBase(baseURL) == "" {
		return nil
	}
	return &HTTPBirthDates{baseURL: trimBase(baseURL), internalKey: internalKey, http: eligHTTP()}
}

func (c *HTTPBirthDates) BirthDate(ctx context.Context, userID uuid.UUID) (*time.Time, bool, error) {
	status, body, err := eligGet(ctx, c.http, c.baseURL+"/internal/v1/profiles/users/"+userID.String()+"/identity", c.internalKey)
	if err != nil {
		return nil, false, fmt.Errorf("identity read: %w", err)
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("identity read: status %d", status)
	}
	var w struct {
		Data *struct {
			UserID    string  `json:"user_id"`
			DoB       *string `json:"dob"`
			DoBSource string  `json:"dob_source"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &w); err != nil || w.Data == nil {
		return nil, false, errors.New("identity read: malformed body")
	}
	if got, err := uuid.Parse(w.Data.UserID); err != nil || got != userID {
		return nil, false, errors.New("identity read: body is for a different user")
	}
	if w.Data.DoB == nil || strings.TrimSpace(*w.Data.DoB) == "" {
		return nil, true, nil
	}
	switch w.Data.DoBSource {
	case "registration", "profile":
	default:
		// A date nobody stands behind is not a verified age.
		return nil, true, nil
	}
	dob, err := time.Parse("2006-01-02", strings.TrimSpace(*w.Data.DoB))
	if err != nil {
		return nil, false, errors.New("identity read: dob is not YYYY-MM-DD")
	}
	return &dob, true, nil
}

// --- account ---

// HTTPAccounts is AccountSource over the identity user-service.
type HTTPAccounts struct {
	baseURL, internalKey string
	http                 *http.Client
}

// NewHTTPAccounts returns nil when baseURL is empty (account_age and
// good_standing are then unknown).
func NewHTTPAccounts(baseURL, internalKey string) *HTTPAccounts {
	if trimBase(baseURL) == "" {
		return nil
	}
	return &HTTPAccounts{baseURL: trimBase(baseURL), internalKey: internalKey, http: eligHTTP()}
}

func (c *HTTPAccounts) Account(ctx context.Context, userID uuid.UUID) (AccountInfo, error) {
	status, body, err := eligGet(ctx, c.http, c.baseURL+"/v1/users/"+userID.String(), c.internalKey)
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
	// Without a status or a creation time the answer says nothing; reading a
	// missing time as "year 1" would make every account old enough.
	if w.Data.Status == "" || w.Data.CreatedAt == nil || w.Data.CreatedAt.IsZero() {
		return AccountInfo{}, errors.New("account read: status or created_at missing")
	}
	return AccountInfo{Found: true, Active: w.Data.Status == "active", CreatedAt: *w.Data.CreatedAt}, nil
}

// --- posts ---

// HTTPPostCounts is PostCountSource over post-service.
type HTTPPostCounts struct {
	baseURL, internalKey string
	http                 *http.Client
}

// NewHTTPPostCounts returns nil when baseURL is empty.
func NewHTTPPostCounts(baseURL, internalKey string) *HTTPPostCounts {
	if trimBase(baseURL) == "" {
		return nil
	}
	return &HTTPPostCounts{baseURL: trimBase(baseURL), internalKey: internalKey, http: eligHTTP()}
}

func (c *HTTPPostCounts) PostCount(ctx context.Context, userID uuid.UUID) (int, error) {
	status, body, err := eligGet(ctx, c.http, c.baseURL+"/v1/posts/by-author/"+userID.String()+"/counts", c.internalKey)
	if err != nil {
		return 0, fmt.Errorf("post counts: %w", err)
	}
	if status != http.StatusOK {
		return 0, fmt.Errorf("post counts: status %d", status)
	}
	var w struct {
		Data map[string]json.Number `json:"data"`
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&w); err != nil || w.Data == nil {
		return 0, errors.New("post counts: malformed body")
	}
	// A body without `total` is not a count of zero.
	total, ok := w.Data["total"]
	if !ok {
		return 0, errors.New("post counts: no total")
	}
	n, err := total.Int64()
	if err != nil || n < 0 {
		return 0, errors.New("post counts: total is not a count")
	}
	return int(n), nil
}

// --- followers ---

// HTTPFollowerCounts is FollowerCountSource over graph-service.
type HTTPFollowerCounts struct {
	baseURL, internalKey string
	http                 *http.Client
}

// NewHTTPFollowerCounts returns nil when baseURL is empty.
func NewHTTPFollowerCounts(baseURL, internalKey string) *HTTPFollowerCounts {
	if trimBase(baseURL) == "" {
		return nil
	}
	return &HTTPFollowerCounts{baseURL: trimBase(baseURL), internalKey: internalKey, http: eligHTTP()}
}

func (c *HTTPFollowerCounts) FollowerCount(ctx context.Context, userID uuid.UUID) (int, error) {
	status, body, err := eligGet(ctx, c.http, c.baseURL+"/v1/graph/counts/"+userID.String(), c.internalKey)
	if err != nil {
		return 0, fmt.Errorf("graph counts: %w", err)
	}
	if status != http.StatusOK {
		return 0, fmt.Errorf("graph counts: status %d", status)
	}
	var w struct {
		Data *struct {
			UserID        string `json:"user_id"`
			FollowerCount *int64 `json:"follower_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &w); err != nil || w.Data == nil {
		return 0, errors.New("graph counts: malformed body")
	}
	if got, err := uuid.Parse(w.Data.UserID); err != nil || got != userID {
		return 0, errors.New("graph counts: body is for a different user")
	}
	if w.Data.FollowerCount == nil || *w.Data.FollowerCount < 0 {
		return 0, errors.New("graph counts: no follower_count")
	}
	return int(*w.Data.FollowerCount), nil
}
