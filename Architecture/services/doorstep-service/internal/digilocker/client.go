// Package digilocker is doorstep-service's DigiLocker integration for
// professional verification: an Aadhaar-backed DigiLocker account proves the
// professional's identity and is the ONLY source of their gender (women's
// salon is served by women only, men's salon by men only, and a customer may
// ask for a woman professional in any category).
//
// Copied from food-service internal/digilocker (the Mock/HTTP split, real
// PKCE with the code_verifier sent on the exchange, the state checked by the
// caller against Postgres first, a "disabled" mode for a production that has
// no API Setu registration yet, the mock refused in production) and narrowed
// to what Doorstep needs: the Aadhaar result with its gender.
//
// DPDP minimisation: no Aadhaar number, name or date of birth ever leaves
// this package. The reference is built from the DigiLocker account id, the
// gender is reduced to female | male | other.
package digilocker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Genders recorded from DigiLocker (doorstep.professionals.gender).
const (
	GenderFemale = "female"
	GenderMale   = "male"
	GenderOther  = "other"
)

var (
	// ErrProvider wraps every failure that came from the provider (or the
	// mock's simulated failure); the service answers 503 without detail.
	ErrProvider = errors.New("digilocker provider failure")
	// ErrNoAadhaar means the DigiLocker account has no Aadhaar linked (or the
	// provider returned no gender): the step stays missing.
	ErrNoAadhaar = errors.New("digilocker account has no verified Aadhaar")
)

// Session is the result of a code exchange. Fields are unexported so the
// access token cannot be logged or serialised by accident.
type Session struct {
	accessToken string
	accountID   string
	gender      string
	eaadhaar    bool
}

func (s *Session) String() string   { return "digilocker.Session{redacted}" }
func (s *Session) GoString() string { return s.String() }

// Aadhaar is the verified result the rest of the service may see.
type Aadhaar struct {
	// Reference is opaque ("digilocker:<account id>:ADHAR"); never a number.
	Reference string
	// DocTypeHash is the SHA-256 of the provider's document-type label.
	DocTypeHash string
	// Gender is female, male or other.
	Gender string
	// PhotoMediaID is the Aadhaar photo held in media-service as the
	// professional's own image, the reference the selfie is compared with.
	// nil when the provider integration supplies none (the selfie then stays
	// pending instead of passing).
	PhotoMediaID *uuid.UUID
}

func (a Aadhaar) String() string   { return "digilocker.Aadhaar{redacted}" }
func (a Aadhaar) GoString() string { return a.String() }

// Client is the provider interface.
type Client interface {
	// ExchangeCode swaps an authorization code and its PKCE code_verifier.
	ExchangeCode(ctx context.Context, code, codeVerifier string) (*Session, error)
	// Aadhaar returns the verified Aadhaar result of the session.
	Aadhaar(ctx context.Context, s *Session) (*Aadhaar, error)
}

const (
	ModeMock     = "mock"
	ModeHTTP     = "http"
	ModeDisabled = "disabled"
)

// Default DigiLocker (MeriPehchaan) endpoints; overridable for a partner.
const (
	DefaultAuthorizeURL = "https://digilocker.meripehchaan.gov.in/public/oauth2/1/authorize"
	DefaultTokenURL     = "https://digilocker.meripehchaan.gov.in/public/oauth2/1/token"
)

// HashDocumentType returns the lowercase hex SHA-256 of the doc-type label.
func HashDocumentType(docType string) string {
	sum := sha256.Sum256([]byte(docType))
	return hex.EncodeToString(sum[:])
}

// MapGender reduces the provider's gender code to the stored vocabulary.
// Anything unrecognised is "" (the Aadhaar step fails closed).
func MapGender(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "F", "FEMALE":
		return GenderFemale
	case "M", "MALE":
		return GenderMale
	case "T", "O", "TRANSGENDER", "OTHER":
		return GenderOther
	}
	return ""
}

// HTTPConfig is the API Setu registration.
type HTTPConfig struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	RedirectURI  string
}

// Settings is the DigiLocker wiring the service runs with.
type Settings struct {
	Mode         string
	Client       Client
	AuthorizeURL string
	ClientID     string
	// RedirectURI is the redirect_uri registered with DigiLocker: the link
	// the pro app claims; the app reads code and state from it and calls
	// POST /v1/doorstep/pro/digilocker/callback.
	RedirectURI string
}

// Mock reports whether the mock provider is selected.
func (s Settings) Mock() bool { return s.Mode == ModeMock && s.Client != nil }

// New selects the provider for DIGILOCKER_MODE. "mock" (or unset) is refused
// in production, where it would verify every Aadhaar and let anyone choose
// their gender; "disabled" returns a nil client (routes answer 503); any
// other value is an error so a typo cannot fall through to the mock.
func New(mode string, production bool, cfg HTTPConfig) (Client, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ModeHTTP:
		var missing []string
		for _, f := range []struct{ name, v string }{
			{"DIGILOCKER_TOKEN_URL", cfg.TokenURL}, {"DIGILOCKER_CLIENT_ID", cfg.ClientID},
			{"DIGILOCKER_CLIENT_SECRET", cfg.ClientSecret}, {"redirect_uri", cfg.RedirectURI},
		} {
			if strings.TrimSpace(f.v) == "" {
				missing = append(missing, f.name)
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("digilocker: DIGILOCKER_MODE=http needs %s", strings.Join(missing, ", "))
		}
		return &HTTPClient{cfg: cfg, client: &http.Client{Timeout: 8 * time.Second}}, nil
	case ModeMock, "":
		if production {
			return nil, fmt.Errorf("digilocker: DIGILOCKER_MODE=%q selects the mock, which is refused in production", mode)
		}
		return &MockClient{}, nil
	case ModeDisabled:
		return nil, nil
	}
	return nil, fmt.Errorf("digilocker: unknown DIGILOCKER_MODE %q (want http, mock or disabled)", mode)
}

// ─── Mock ───────────────────────────────────────────────────────────────────

// MockClient verifies every Aadhaar (development only). The gender is chosen
// by the code so a tester can onboard either: a code containing "female",
// "male" or "other" yields that gender, any other code a deterministic one.
// The code "fail" simulates a provider failure, "no-aadhaar" an account with
// no Aadhaar linked.
type MockClient struct{}

func (m *MockClient) ExchangeCode(_ context.Context, code, codeVerifier string) (*Session, error) {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(codeVerifier) == "" {
		return nil, fmt.Errorf("%w: code and code_verifier required", ErrProvider)
	}
	if code == "fail" {
		return nil, fmt.Errorf("%w: simulated failure", ErrProvider)
	}
	sum := sha256.Sum256([]byte("doorstep-digilocker-mock\x00" + code))
	lc := strings.ToLower(code)
	gender := GenderMale
	switch {
	case strings.Contains(lc, "female"):
		gender = GenderFemale
	case strings.Contains(lc, "male"):
		gender = GenderMale
	case strings.Contains(lc, "other"):
		gender = GenderOther
	case sum[0]%2 == 0:
		gender = GenderFemale
	}
	return &Session{accessToken: "mock", accountID: "mock-" + hex.EncodeToString(sum[:6]), gender: gender, eaadhaar: code != "no-aadhaar"}, nil
}

func (m *MockClient) Aadhaar(_ context.Context, s *Session) (*Aadhaar, error) {
	return aadhaarOf(s)
}

func aadhaarOf(s *Session) (*Aadhaar, error) {
	if s == nil || s.accessToken == "" {
		return nil, fmt.Errorf("%w: session required", ErrProvider)
	}
	if !s.eaadhaar || s.gender == "" {
		return nil, ErrNoAadhaar
	}
	if strings.TrimSpace(s.accountID) == "" {
		return nil, fmt.Errorf("%w: no digilocker account id", ErrProvider)
	}
	return &Aadhaar{Reference: "digilocker:" + s.accountID + ":ADHAR", DocTypeHash: HashDocumentType("ADHAR"), Gender: s.gender}, nil
}

// ─── HTTP (DigiLocker via API Setu) ─────────────────────────────────────────

// HTTPClient talks to DigiLocker's OAuth token endpoint. The token response
// carries the account id, the Aadhaar-linked flag ("eaadhaar": "Y") and the
// Aadhaar gender ("M" | "F" | "T"). Its shape follows the published API Setu
// documentation and is pinned only by httptest; it has NOT been exercised
// against a live partner registration. It does not fetch the Aadhaar photo
// (no media-service import route exists yet), so PhotoMediaID stays nil.
type HTTPClient struct {
	cfg    HTTPConfig
	client *http.Client
}

const maxProviderBody = 1 << 20

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	DigiLockerID string `json:"digilockerid"`
	Gender       string `json:"gender"`
	EAadhaar     string `json:"eaadhaar"`
}

func (h *HTTPClient) ExchangeCode(ctx context.Context, code, codeVerifier string) (*Session, error) {
	if code == "" || codeVerifier == "" {
		return nil, fmt.Errorf("%w: code and code_verifier required", ErrProvider)
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", h.cfg.ClientID)
	form.Set("client_secret", h.cfg.ClientSecret)
	form.Set("redirect_uri", h.cfg.RedirectURI)
	form.Set("code_verifier", codeVerifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.cfg.TokenURL, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("%w: build token request", ErrProvider)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: unreachable", ErrProvider)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: status %d", ErrProvider, resp.StatusCode)
	}
	var parsed tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxProviderBody)).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("%w: undecodable response", ErrProvider)
	}
	if parsed.AccessToken == "" {
		return nil, fmt.Errorf("%w: token response had no access_token", ErrProvider)
	}
	return &Session{accessToken: parsed.AccessToken, accountID: parsed.DigiLockerID,
		gender: MapGender(parsed.Gender), eaadhaar: strings.EqualFold(strings.TrimSpace(parsed.EAadhaar), "Y")}, nil
}

func (h *HTTPClient) Aadhaar(_ context.Context, s *Session) (*Aadhaar, error) {
	return aadhaarOf(s)
}

// AuthorizeURL builds the URL the pro app opens. In mock mode it is the
// redirect URI itself carrying a mock code, as if DigiLocker had already
// returned (edit the code to mock-female / mock-male to pick a gender).
func AuthorizeURL(s Settings, state, challenge string) (string, error) {
	if s.Mock() {
		base := s.RedirectURI
		if base == "" {
			base = "http://localhost:8080/doorstep-pro/digilocker"
		}
		u, err := url.Parse(base)
		if err != nil {
			return "", err
		}
		q := u.Query()
		q.Set("code", "mock-female")
		q.Set("state", state)
		u.RawQuery = q.Encode()
		return u.String(), nil
	}
	u, err := url.Parse(s.AuthorizeURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("digilocker: authorize URL is not configured")
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", s.ClientID)
	q.Set("redirect_uri", s.RedirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}
