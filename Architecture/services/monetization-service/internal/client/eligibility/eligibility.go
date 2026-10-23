// Package eligibility reads the facts behind "may this creator offer paid
// memberships?" from the services that own them. Every route already exists
// and is called by another service the same way (live-service-v2's going-live
// trust gate, post-service's standing check); nothing was added to the
// services read.
//
//   - Date of birth: identity-profile's service-only identity read
//     GET {PROFILE_SERVICE_URL}/internal/v1/profiles/users/:userId/identity
//     -> {"data":{"user_id","first_name","dob":"YYYY-MM-DD"|null,"dob_source"}}
//     404 = no such (or a hidden/deleted) account. Only a date identity stands
//     behind (dob_source registration or profile) counts. The date and the
//     name never reach a log line or an error.
//   - Followers: graph-service GET {GRAPH_SERVICE_URL}/v1/graph/counts/:userId
//     -> {"data":{"user_id","follower_count",...}}.
//   - Standing (active strikes): trust-safety
//     GET {TRUST_SAFETY_SERVICE_URL}/v1/internal/standing/:userId with a
//     service token (aud trust_safety, scope trust_safety:standing.read,
//     signed with MONETIZATION_SERVICE_TOKEN_KEY/KID) plus the internal key.
//     trust-safety lists monetization-service on StandingCallers; a
//     deployment must also register its public key there
//     (SERVICE_CALLER_MONETIZATION_SERVICE_{KID,PUBKEY,OPS}). Until it does,
//     every answer is a 403 and the strikes item is reported missing — the
//     check fails closed.
//
// The two read-only GETs carry X-Internal-Service-Key and NO user header.
// Every failure — transport, a non-200, a malformed or foreign body — is an
// error the caller treats as "unknown", never as "satisfied".
package eligibility

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

const (
	callerService = "monetization-service"
	maxBody       = 64 << 10
	timeout       = 3 * time.Second

	// Trust-safety standing.
	StandingPath     = "/v1/internal/standing/"
	StandingAudience = "trust_safety"
	OpStandingRead   = "trust_safety:standing.read"
	tokenTTL         = 2 * time.Minute
)

var (
	_ service.BirthDateSource     = (*BirthDates)(nil)
	_ service.FollowerCountSource = (*Followers)(nil)
	_ service.StandingSource      = (*Standing)(nil)
)

func httpClient() *http.Client {
	return &http.Client{
		Timeout: timeout,
		// Never follow a redirect: that would resend the key somewhere else.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func trimBase(u string) string { return strings.TrimRight(strings.TrimSpace(u), "/") }

// get reads rawURL and returns the status and, for a 200, the body. Errors
// are fixed text plus the status: a body is never quoted.
func get(ctx context.Context, c *http.Client, rawURL string, headers map[string]string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, nil, errors.New("build request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Caller-Service", callerService)
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return 0, nil, errors.New("read body")
	}
	return http.StatusOK, body, nil
}

// --- date of birth ---

// BirthDates is service.BirthDateSource over identity-profile.
type BirthDates struct {
	baseURL, internalKey string
	http                 *http.Client
}

// NewBirthDates returns nil when baseURL is empty (age is then unknown).
func NewBirthDates(baseURL, internalKey string) *BirthDates {
	if trimBase(baseURL) == "" {
		return nil
	}
	return &BirthDates{baseURL: trimBase(baseURL), internalKey: internalKey, http: httpClient()}
}

func (c *BirthDates) BirthDate(ctx context.Context, userID uuid.UUID) (*time.Time, bool, error) {
	status, body, err := get(ctx, c.http, c.baseURL+"/internal/v1/profiles/users/"+userID.String()+"/identity",
		map[string]string{"X-Internal-Service-Key": c.internalKey})
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

// --- followers ---

// Followers is service.FollowerCountSource over graph-service.
type Followers struct {
	baseURL, internalKey string
	http                 *http.Client
}

// NewFollowers returns nil when baseURL is empty.
func NewFollowers(baseURL, internalKey string) *Followers {
	if trimBase(baseURL) == "" {
		return nil
	}
	return &Followers{baseURL: trimBase(baseURL), internalKey: internalKey, http: httpClient()}
}

func (c *Followers) FollowerCount(ctx context.Context, userID uuid.UUID) (int, error) {
	status, body, err := get(ctx, c.http, c.baseURL+"/v1/graph/counts/"+userID.String(),
		map[string]string{"X-Internal-Service-Key": c.internalKey})
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

// --- standing ---

// Standing is service.StandingSource over trust-safety's standing route.
type Standing struct {
	baseURL, internalKey string
	signer               *servicetoken.Signer
	http                 *http.Client
}

// NewStanding returns (nil, nil) when baseURL or the token key/kid is empty
// (strikes are then unknown), and an error for a key that does not load.
func NewStanding(baseURL, internalKey, tokenKID, tokenKeyB64 string) (*Standing, error) {
	if trimBase(baseURL) == "" || strings.TrimSpace(tokenKID) == "" || strings.TrimSpace(tokenKeyB64) == "" {
		return nil, nil
	}
	signer, err := servicetoken.NewSignerFromBase64(callerService, strings.TrimSpace(tokenKID), strings.TrimSpace(tokenKeyB64))
	if err != nil {
		return nil, fmt.Errorf("standing: signing key: %w", err)
	}
	return &Standing{baseURL: trimBase(baseURL), internalKey: internalKey, signer: signer, http: httpClient()}, nil
}

func (c *Standing) Standing(ctx context.Context, userID uuid.UUID) (service.StandingAnswer, error) {
	tok, err := c.signer.Mint(StandingAudience, callerService, []string{OpStandingRead}, nil, tokenTTL)
	if err != nil {
		return service.StandingAnswer{}, errors.New("standing: mint token")
	}
	status, body, err := get(ctx, c.http, c.baseURL+StandingPath+userID.String(), map[string]string{
		"X-Internal-Service-Key":  c.internalKey,
		"X-Service-Authorization": "Bearer " + tok,
	})
	if err != nil {
		return service.StandingAnswer{}, fmt.Errorf("standing: %w", err)
	}
	if status != http.StatusOK {
		return service.StandingAnswer{}, fmt.Errorf("standing: status %d", status)
	}
	var w struct {
		Data *struct {
			Standing      string             `json:"standing"`
			ActiveStrikes *[]json.RawMessage `json:"active_strikes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &w); err != nil || w.Data == nil {
		return service.StandingAnswer{}, errors.New("standing: malformed body")
	}
	switch w.Data.Standing {
	case "ok", "restricted", "suspended":
	default:
		return service.StandingAnswer{}, errors.New("standing: unknown standing")
	}
	// A body without the list is not "no strikes".
	if w.Data.ActiveStrikes == nil {
		return service.StandingAnswer{}, errors.New("standing: no active_strikes")
	}
	return service.StandingAnswer{Standing: w.Data.Standing, ActiveStrikes: len(*w.Data.ActiveStrikes)}, nil
}
