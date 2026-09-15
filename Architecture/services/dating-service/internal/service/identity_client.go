package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// HTTP client for identity-profile's internal identity read (lane D2):
//
//	GET {IDENTITY_PROFILE_SERVICE_URL}/internal/v1/profiles/users/:userId/identity
//	X-Internal-Service-Key: <INTERNAL_SERVICE_KEY>
//	X-Caller-Service: dating-service
//
// The route refuses (403) any request carrying an end-user identity header, so
// this client builds a fresh request with only the headers above; nothing from
// the inbound user request is forwarded.
//
// Response: {"data":{"user_id","first_name","dob":"YYYY-MM-DD"|null,
// "dob_source":"registration"|"profile"|"none"}}.
//
// The birth date and first name are personal data: no log line or error
// produced here ever carries either value.

// IdentityProfileTimeout bounds one identity read.
const IdentityProfileTimeout = 2 * time.Second

// identityCallerService is the self-declared audit label identity logs.
const identityCallerService = "dating-service"

// maxIdentityBody caps the response body read (the contract is ~150 bytes).
const maxIdentityBody = 64 << 10

var (
	// ErrIdentityTransient: identity answered 5xx / 429, timed out, was
	// unreachable, or returned a body that does not match the contract.
	// Retrying later may succeed.
	ErrIdentityTransient = errors.New("identity-profile transient failure")
	// ErrIdentityMisconfigured: identity answered 400 / 401 / 403 (or another
	// unexpected non-5xx status). A deployment problem (wrong key, wrong URL,
	// a user header leaked in); retrying will not help.
	ErrIdentityMisconfigured = errors.New("identity-profile call misconfigured")
)

// HTTPIdentityClient implements IdentityBasicsClient.
type HTTPIdentityClient struct {
	baseURL     string
	internalKey string
	http        *http.Client
	log         *slog.Logger

	misconfigOnce sync.Once
}

// NewHTTPIdentityClient builds the client. baseURL is the identity-profile
// origin (e.g. http://identity-profile:8098); internalKey is dating's
// INTERNAL_SERVICE_KEY. A nil logger uses slog.Default().
func NewHTTPIdentityClient(baseURL, internalKey string, log *slog.Logger) *HTTPIdentityClient {
	if log == nil {
		log = slog.Default()
	}
	return &HTTPIdentityClient{
		baseURL:     strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		internalKey: internalKey,
		http: &http.Client{
			Timeout: IdentityProfileTimeout,
			// Never follow a redirect: it would resend the internal key to
			// wherever the Location points.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		log: log,
	}
}

type identityWire struct {
	Data *struct {
		UserID    string  `json:"user_id"`
		FirstName string  `json:"first_name"`
		DoB       *string `json:"dob"`
		DoBSource string  `json:"dob_source"`
	} `json:"data"`
}

// GetIdentityBasics reads the user's identity basics. A 404 (unknown user, or
// an account that is deactivated, pending deletion, purged or hidden) is the
// typed result Found=false with a nil error.
func (c *HTTPIdentityClient) GetIdentityBasics(ctx context.Context, userID uuid.UUID) (*IdentityBasics, error) {
	endpoint := c.baseURL + "/internal/v1/profiles/users/" + userID.String() + "/identity"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request", ErrIdentityMisconfigured)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Caller-Service", identityCallerService)
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: request: %v", ErrIdentityTransient, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxIdentityBody))

	switch {
	case resp.StatusCode == http.StatusOK:
		if readErr != nil {
			return nil, fmt.Errorf("%w: read body", ErrIdentityTransient)
		}
		return decodeIdentityBasics(body, userID)
	case resp.StatusCode == http.StatusNotFound:
		return &IdentityBasics{Found: false, DOBSource: IdentityDOBSourceNone}, nil
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("%w: status %d", ErrIdentityTransient, resp.StatusCode)
	default:
		c.misconfigOnce.Do(func() {
			// Status and URL path only; the body may echo request details.
			c.log.Error("dating-service: identity-profile refused the internal identity read; "+
				"check IDENTITY_PROFILE_SERVICE_URL and INTERNAL_SERVICE_KEY (logged once)",
				"status", resp.StatusCode, "base_url", c.baseURL)
		})
		return nil, fmt.Errorf("%w: status %d", ErrIdentityMisconfigured, resp.StatusCode)
	}
}

// decodeIdentityBasics maps the 200 body. Every error message is fixed text:
// a json or time parse error can quote the offending value.
func decodeIdentityBasics(body []byte, userID uuid.UUID) (*IdentityBasics, error) {
	var w identityWire
	if err := json.Unmarshal(body, &w); err != nil || w.Data == nil {
		return nil, fmt.Errorf("%w: malformed identity body", ErrIdentityTransient)
	}
	if got, err := uuid.Parse(w.Data.UserID); err != nil || got != userID {
		return nil, fmt.Errorf("%w: identity body is for a different user", ErrIdentityTransient)
	}
	out := &IdentityBasics{Found: true, FirstName: strings.TrimSpace(w.Data.FirstName), DOBSource: IdentityDOBSourceNone}
	if w.Data.DoB == nil || strings.TrimSpace(*w.Data.DoB) == "" {
		return out, nil
	}
	switch w.Data.DoBSource {
	case IdentityDOBSourceRegistration, IdentityDOBSourceProfile:
	default:
		return nil, fmt.Errorf("%w: identity dob_source is not registration or profile", ErrIdentityTransient)
	}
	dob, err := time.Parse("2006-01-02", strings.TrimSpace(*w.Data.DoB))
	if err != nil {
		return nil, fmt.Errorf("%w: identity dob is not YYYY-MM-DD", ErrIdentityTransient)
	}
	out.BirthDate = &dob
	out.DOBSource = w.Data.DoBSource
	return out, nil
}
