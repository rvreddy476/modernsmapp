// Package digilocker is food-service's DigiLocker integration for delivery
// partner verification (Wave 1 B4).
//
// Copied from rider-service/internal/digilocker/client.go (the Mock/HTTP split,
// the DIGILOCKER_MODE switch and the mock refused in production) and extended:
//
//   - real PKCE: ExchangeCode sends the code_verifier; the state is checked by
//     the caller against Postgres before this package is ever called;
//   - IssuedDocuments returns the Aadhaar reference, driving licence and
//     vehicle RC the partner holds;
//   - a "disabled" mode, so production can boot before an API Setu partner
//     registration exists, with the routes answering 503.
//
// DPDP minimisation: an IssuedDocument of kind AADHAAR never carries a number.
// The HTTP client does not read the Aadhaar document's URI at all, and its
// reference is built from the DigiLocker account id, not from the document.
package digilocker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DocumentKind is the kind of issued document this service consumes.
type DocumentKind string

const (
	KindAadhaar        DocumentKind = "AADHAAR"
	KindDrivingLicence DocumentKind = "DRIVING_LICENCE"
	KindVehicleRC      DocumentKind = "VEHICLE_RC"
)

// ErrProvider wraps every failure that came from the provider (or the mock's
// simulated failure), so the service can answer 502 without echoing detail.
var ErrProvider = errors.New("digilocker provider failure")

// IssuedDocument is one document the partner holds in DigiLocker.
type IssuedDocument struct {
	Kind DocumentKind
	// Reference is an opaque provider-side reference. It never embeds a
	// document number.
	Reference string
	// DocType is the provider's document-type label ("ADHAR", "DRVLC",
	// "RVCER"); only its hash is stored.
	DocType string
	// Number is the document number. NEVER populated for AADHAAR.
	Number         string
	NameOnDocument string
	// ValidUntil is the last day the document is valid, when the provider
	// reports it.
	ValidUntil *time.Time
}

// String keeps numbers and names out of any accidental %v.
func (d IssuedDocument) String() string { return "IssuedDocument{" + string(d.Kind) + ", redacted}" }

// GoString keeps them out of %#v.
func (d IssuedDocument) GoString() string { return d.String() }

// Session is the result of a code exchange. Its fields are unexported so the
// access token cannot be logged or serialised by accident.
type Session struct {
	accessToken string
	accountID   string
	name        string
	mockSeed    string
}

func (s *Session) String() string { return "digilocker.Session{redacted}" }

func (s *Session) GoString() string { return s.String() }

// HashDocumentType returns the lowercase hex SHA-256 of the doc-type label.
func HashDocumentType(docType string) string {
	sum := sha256.Sum256([]byte(docType))
	return hex.EncodeToString(sum[:])
}

// Client is the provider interface: MockClient (deterministic, dev only) and
// HTTPClient (DigiLocker via API Setu).
type Client interface {
	// ExchangeCode swaps an authorization code and its PKCE code_verifier for
	// a session.
	ExchangeCode(ctx context.Context, code, codeVerifier string) (*Session, error)
	// IssuedDocuments lists the partner's Aadhaar reference, DL and RC.
	IssuedDocuments(ctx context.Context, session *Session) ([]IssuedDocument, error)
}

const (
	ModeMock     = "mock"
	ModeHTTP     = "http"
	ModeDisabled = "disabled"
)

func normalizeMode(mode string) string { return strings.ToLower(strings.TrimSpace(mode)) }

// HTTPConfig is the API Setu registration. Every field is required in http mode.
type HTTPConfig struct {
	TokenURL           string
	IssuedDocumentsURL string
	ClientID           string
	ClientSecret       string
	RedirectURI        string
}

func (c HTTPConfig) validate() error {
	missing := []string{}
	for _, f := range []struct{ name, value string }{
		{EnvTokenURL, c.TokenURL}, {EnvIssuedDocumentsURL, c.IssuedDocumentsURL},
		{EnvClientID, c.ClientID}, {EnvClientSecret, c.ClientSecret}, {EnvPublicBaseURL + " (redirect_uri)", c.RedirectURI},
	} {
		if strings.TrimSpace(f.value) == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("digilocker: DIGILOCKER_MODE=http needs %s", strings.Join(missing, ", "))
	}
	return nil
}

// New selects the provider client for DIGILOCKER_MODE.
//
// "http" is DigiLocker. "mock", or an unset mode, is refused in production,
// where it would record a verified Aadhaar, DL and RC for every partner who
// asked. "disabled" returns a nil client everywhere (the routes answer 503).
// Any other value is an error: a typo must not fall through to the mock.
func New(mode string, production bool, cfg HTTPConfig) (Client, error) {
	switch normalizeMode(mode) {
	case ModeHTTP:
		if err := cfg.validate(); err != nil {
			return nil, err
		}
		return NewHTTPClient(cfg), nil
	case ModeMock, "":
		if production {
			return nil, fmt.Errorf("digilocker: DIGILOCKER_MODE=%q selects the mock, which is refused unless ENV is local, dev or development", mode)
		}
		return NewMockClient(), nil
	case ModeDisabled:
		return nil, nil
	default:
		return nil, fmt.Errorf("digilocker: unknown DIGILOCKER_MODE %q (want http, mock or disabled)", mode)
	}
}

// ─── Mock ───────────────────────────────────────────────────────────────────

// MockClient passes every verification with deterministic documents derived
// from the code, so two dev riders do not collide on one licence while the
// same code always yields the same numbers. The code "fail" simulates a
// provider failure.
type MockClient struct{}

func NewMockClient() *MockClient { return &MockClient{} }

func (m *MockClient) ExchangeCode(_ context.Context, code, codeVerifier string) (*Session, error) {
	if strings.TrimSpace(code) == "" {
		return nil, errors.New("digilocker mock: code required")
	}
	if strings.TrimSpace(codeVerifier) == "" {
		return nil, errors.New("digilocker mock: code_verifier required")
	}
	if code == "fail" {
		return nil, fmt.Errorf("%w: simulated failure", ErrProvider)
	}
	sum := sha256.Sum256([]byte("food-digilocker-mock\x00" + code))
	return &Session{accessToken: "mock", accountID: "mock-account-" + letters(sum[:8]), name: "Mock Rider", mockSeed: hex.EncodeToString(sum[:])}, nil
}

func (m *MockClient) IssuedDocuments(_ context.Context, s *Session) ([]IssuedDocument, error) {
	if s == nil || s.mockSeed == "" {
		return nil, errors.New("digilocker mock: session required")
	}
	seed, err := hex.DecodeString(s.mockSeed)
	if err != nil || len(seed) < 24 {
		return nil, errors.New("digilocker mock: malformed session")
	}
	u := binary.BigEndian.Uint64(seed[0:8])
	v := binary.BigEndian.Uint64(seed[8:16])
	// Sarathi layout KA + RTO + 2020 + 7-digit serial; state-series RC
	// KA + RTO + two letters + four digits. Both pass shared/kyc.
	dl := fmt.Sprintf("KA%02d2020%07d", 1+u%99, (u/100)%10000000)
	rc := fmt.Sprintf("KA%02d%c%c%04d", 1+v%99, 'A'+byte((v/100)%26), 'A'+byte((v/2600)%26), (v/67600)%10000)
	dlUntil := time.Date(2040, time.June, 30, 0, 0, 0, 0, time.UTC)
	rcUntil := time.Date(2038, time.June, 30, 0, 0, 0, 0, time.UTC)
	tag := letters(seed[16:24])
	return []IssuedDocument{
		{Kind: KindAadhaar, Reference: "mock-ref-aadhaar-" + tag, DocType: "ADHAR", NameOnDocument: s.name},
		{Kind: KindDrivingLicence, Reference: "mock-ref-dl-" + tag, DocType: "DRVLC", Number: dl, NameOnDocument: s.name, ValidUntil: &dlUntil},
		{Kind: KindVehicleRC, Reference: "mock-ref-rc-" + tag, DocType: "RVCER", Number: rc, NameOnDocument: s.name, ValidUntil: &rcUntil},
	}, nil
}

// letters renders bytes as a-z only, so a mock reference never contains a
// digit run that could be read as an identifier.
func letters(b []byte) string {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = 'a' + c%26
	}
	return string(out)
}

// ─── HTTP (DigiLocker via API Setu) ─────────────────────────────────────────

// HTTPClient talks to DigiLocker's OAuth token and issued-documents
// endpoints. Its request and response shapes follow the published API Setu
// documentation and are pinned only by httptest; they have NOT been exercised
// against a live partner registration.
type HTTPClient struct {
	cfg    HTTPConfig
	client *http.Client
}

func NewHTTPClient(cfg HTTPConfig) *HTTPClient {
	return &HTTPClient{cfg: cfg, client: &http.Client{Timeout: 8 * time.Second}}
}

const maxProviderBody = 1 << 20

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	DigiLockerID string `json:"digilockerid"`
	Name         string `json:"name"`
}

func (h *HTTPClient) ExchangeCode(ctx context.Context, code, codeVerifier string) (*Session, error) {
	if code == "" || codeVerifier == "" {
		return nil, errors.New("digilocker: code and code_verifier required")
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
		return nil, fmt.Errorf("digilocker: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var parsed tokenResponse
	if err := h.do(req, &parsed); err != nil {
		return nil, err
	}
	if parsed.AccessToken == "" {
		return nil, fmt.Errorf("%w: token response had no access_token", ErrProvider)
	}
	return &Session{accessToken: parsed.AccessToken, accountID: parsed.DigiLockerID, name: parsed.Name}, nil
}

// issuedItem deliberately parses only the doc type, the URI and the label.
type issuedItem struct {
	DocType string `json:"doctype"`
	URI     string `json:"uri"`
}

func (h *HTTPClient) IssuedDocuments(ctx context.Context, s *Session) ([]IssuedDocument, error) {
	if s == nil || s.accessToken == "" {
		return nil, errors.New("digilocker: session required")
	}
	if strings.TrimSpace(s.accountID) == "" {
		return nil, fmt.Errorf("%w: token response had no digilockerid", ErrProvider)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.cfg.IssuedDocumentsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("digilocker: build issued-documents request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.accessToken)
	var parsed struct {
		Items []issuedItem `json:"items"`
	}
	if err := h.do(req, &parsed); err != nil {
		return nil, err
	}
	var out []IssuedDocument
	for _, it := range parsed.Items {
		docType := strings.ToUpper(strings.TrimSpace(it.DocType))
		ref := "digilocker:" + s.accountID + ":" + docType
		switch docType {
		case "ADHAR":
			// The URI of an Aadhaar document is never read.
			out = append(out, IssuedDocument{Kind: KindAadhaar, Reference: ref, DocType: docType, NameOnDocument: s.name})
		case "DRVLC", "RVCER":
			kind := KindDrivingLicence
			if docType == "RVCER" {
				kind = KindVehicleRC
			}
			i := strings.LastIndex(it.URI, "-")
			if i < 0 || i == len(it.URI)-1 {
				return nil, fmt.Errorf("%w: issued %s has no document id", ErrProvider, docType)
			}
			// ValidUntil is not in the issued list; it stays nil, and the
			// approval gate treats a DL or RC without validity as not valid.
			out = append(out, IssuedDocument{Kind: kind, Reference: ref, DocType: docType, Number: it.URI[i+1:], NameOnDocument: s.name})
		}
	}
	return out, nil
}

func (h *HTTPClient) do(req *http.Request, dst any) error {
	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: unreachable", ErrProvider)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%w: status %d", ErrProvider, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxProviderBody)).Decode(dst); err != nil {
		return fmt.Errorf("%w: undecodable response", ErrProvider)
	}
	return nil
}
