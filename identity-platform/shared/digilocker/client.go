// Package digilocker is the ONE DigiLocker (MeriPehchaan / API Setu) client
// for identity-platform: person verification in profile-service uses it, and
// the per-service copies (dating, doorstep, food, rider under
// Architecture/services/*/internal/digilocker) can move to it, or to
// profile-service's person-verification API, later.
//
// Modelled on food-service's and doorstep-service's copies: the Mock/HTTP
// split, the DIGILOCKER_MODE switch with the mock refused in production, a
// "disabled" mode so production can boot before an API Setu registration
// exists, real PKCE (the code_verifier is sent on the exchange; the caller
// checks the single-use state first), and redacted String/GoString on every
// type that carries personal data.
//
// It lives under identity-platform/shared rather than Architecture/shared
// because identity services build with identity-platform as their Docker
// context (services/profile-service/Dockerfile copies only shared/ and the
// service), so they cannot import a package from the Architecture tree.
// It depends on the standard library only, so a copy or a build-context
// change is all an Architecture service would need to use it.
//
// DPDP minimisation: an Identity never carries an Aadhaar number. The HTTP
// client reads the eAadhaar XML for the name, date of birth and photo, and
// keeps only the LAST FOUR digits of its uid attribute (the masked reference
// DigiLocker publishes anyway), which feeds PersonKey and nothing else.
package digilocker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	// ErrProvider wraps every failure that came from the provider (or the
	// mock's simulated failure); callers answer 502/503 without detail.
	ErrProvider = errors.New("digilocker provider failure")
	// ErrNoAadhaar means the DigiLocker account has no Aadhaar linked, or
	// the eAadhaar record lacks a name or date of birth.
	ErrNoAadhaar = errors.New("digilocker account has no usable Aadhaar record")
)

const (
	ModeMock     = "mock"
	ModeHTTP     = "http"
	ModeDisabled = "disabled"
)

// Default DigiLocker (MeriPehchaan) endpoints; overridable per environment.
const (
	DefaultAuthorizeURL = "https://digilocker.meripehchaan.gov.in/public/oauth2/1/authorize"
	DefaultTokenURL     = "https://digilocker.meripehchaan.gov.in/public/oauth2/1/token" //nolint:gosec // G101: public OAuth endpoint URL, not a credential
	DefaultEAadhaarURL  = "https://digilocker.meripehchaan.gov.in/public/oauth2/3/xml/eaadhaar"
)

// MaxPhotoBytes caps the eAadhaar photo accepted from the provider.
const MaxPhotoBytes = 512 << 10

// Session is the result of a code exchange. Its fields are unexported so the
// access token cannot be logged or serialised by accident.
type Session struct {
	accessToken string
	accountID   string
	eaadhaar    bool
	mockCode    string
}

func (s *Session) String() string   { return "digilocker.Session{redacted}" }
func (s *Session) GoString() string { return s.String() }

// Identity is what person verification may see. Never logged: String and
// GoString redact it, and it has no JSON tags on purpose.
type Identity struct {
	// Name as printed on the Aadhaar record.
	Name string
	// DOB is the civil date of birth (UTC midnight).
	DOB time.Time
	// Photo is the Aadhaar photo (JPEG bytes), the reference a selfie is
	// compared with. Empty when the provider returned none.
	Photo []byte
	// AadhaarLast4 is the last four digits of the masked uid, or "".
	AadhaarLast4 string
	// AccountID is the DigiLocker account id.
	AccountID string
}

func (i Identity) String() string   { return "digilocker.Identity{redacted}" }
func (i Identity) GoString() string { return i.String() }

// PersonKey is the canonical, person-level key the caller hashes (with a
// keyed HMAC) to enforce one verified account per person. It is built from
// the Aadhaar record, not the DigiLocker account, so two DigiLocker accounts
// of one person collide: normalised name tokens, date of birth and the last
// four uid digits. Without the last four digits it falls back to the
// DigiLocker account id. Never log or store the returned value.
func (i Identity) PersonKey() (string, error) {
	name := NormaliseName(i.Name)
	if name == "" || i.DOB.IsZero() {
		return "", ErrNoAadhaar
	}
	dob := i.DOB.Format("2006-01-02")
	if i.AadhaarLast4 != "" {
		return "aadhaar.v1\x00" + name + "\x00" + dob + "\x00" + i.AadhaarLast4, nil
	}
	if strings.TrimSpace(i.AccountID) != "" {
		return "digilocker.v1\x00" + strings.TrimSpace(i.AccountID), nil
	}
	return "", ErrNoAadhaar
}

// Client is the provider interface.
type Client interface {
	// ExchangeCode swaps an authorization code and its PKCE code_verifier.
	ExchangeCode(ctx context.Context, code, codeVerifier string) (*Session, error)
	// Identity returns the Aadhaar-backed identity of the session.
	Identity(ctx context.Context, s *Session) (*Identity, error)
}

// HTTPConfig is the API Setu registration. Every field is required in http mode.
type HTTPConfig struct {
	TokenURL     string
	EAadhaarURL  string
	ClientID     string
	ClientSecret string
	RedirectURI  string
}

func (c HTTPConfig) validate() error {
	missing := []string{}
	for _, f := range []struct{ name, value string }{
		{EnvTokenURL, c.TokenURL}, {EnvEAadhaarURL, c.EAadhaarURL},
		{EnvClientID, c.ClientID}, {EnvClientSecret, c.ClientSecret}, {"redirect URI", c.RedirectURI},
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

func normalizeMode(mode string) string { return strings.ToLower(strings.TrimSpace(mode)) }

// New selects the provider client. "http" is DigiLocker. "mock" (or unset)
// is refused in production, where it would verify anyone. "disabled"
// returns a nil client (the routes answer 503). Any other value is an
// error: a typo must not fall through to the mock.
func New(mode string, production bool, cfg HTTPConfig) (Client, error) {
	switch normalizeMode(mode) {
	case ModeHTTP:
		if err := cfg.validate(); err != nil {
			return nil, err
		}
		return NewHTTPClient(cfg, nil), nil
	case ModeMock, "":
		if production {
			return nil, fmt.Errorf("digilocker: DIGILOCKER_MODE=%q selects the mock, which is refused unless the environment is local, dev or development", mode)
		}
		return NewMockClient(), nil
	case ModeDisabled:
		return nil, nil
	default:
		return nil, fmt.Errorf("digilocker: unknown DIGILOCKER_MODE %q (want http, mock or disabled)", mode)
	}
}

// AuthorizeURL builds the DigiLocker authorize URL with PKCE (S256).
func AuthorizeURL(authorizeURL, clientID, redirectURI, state, challenge string) (string, error) {
	u, err := url.Parse(authorizeURL)
	if err != nil || u.Host == "" {
		return "", errors.New("digilocker: authorize URL is not configured")
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ─── Mock ───────────────────────────────────────────────────────────────────

// MockNamePrefix lets a dev mock code carry the name DigiLocker "returns":
// "mock-name:" + base64url(name). Any other code yields MockDefaultName.
const (
	MockNamePrefix  = "mock-name:"
	MockDefaultName = "Mock Person"
	// MockFailCode simulates a provider failure; MockNoAadhaarCode an
	// account without Aadhaar.
	MockFailCode      = "fail"
	MockNoAadhaarCode = "no-aadhaar"
)

// MockCodeForName is the dev code whose identity carries name.
func MockCodeForName(name string) string {
	return MockNamePrefix + base64.RawURLEncoding.EncodeToString([]byte(name))
}

// MockClient verifies everyone (development only). The identity is
// deterministic per code: the same code is the same person.
type MockClient struct{}

func NewMockClient() *MockClient { return &MockClient{} }

func (m *MockClient) ExchangeCode(_ context.Context, code, codeVerifier string) (*Session, error) {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(codeVerifier) == "" {
		return nil, fmt.Errorf("%w: code and code_verifier required", ErrProvider)
	}
	if code == MockFailCode {
		return nil, fmt.Errorf("%w: simulated failure", ErrProvider)
	}
	sum := sha256.Sum256([]byte("identity-digilocker-mock\x00" + code))
	return &Session{accessToken: "mock", accountID: "mock-" + hex.EncodeToString(sum[:6]), eaadhaar: code != MockNoAadhaarCode, mockCode: code}, nil
}

// mockPhoto is a stand-in reference "photo" (not an image); the mock face
// verifier never decodes it.
var mockPhoto = []byte("mock-digilocker-photo")

func (m *MockClient) Identity(_ context.Context, s *Session) (*Identity, error) {
	if s == nil || s.accessToken == "" {
		return nil, fmt.Errorf("%w: session required", ErrProvider)
	}
	if !s.eaadhaar {
		return nil, ErrNoAadhaar
	}
	name := MockDefaultName
	if rest, ok := strings.CutPrefix(s.mockCode, MockNamePrefix); ok {
		if raw, err := base64.RawURLEncoding.DecodeString(rest); err == nil && strings.TrimSpace(string(raw)) != "" {
			name = string(raw)
		}
	}
	sum := sha256.Sum256([]byte("identity-digilocker-mock-uid\x00" + s.mockCode))
	last4 := fmt.Sprintf("%04d", (int(sum[0])<<8|int(sum[1]))%10000)
	photo := append([]byte(nil), mockPhoto...)
	return &Identity{Name: name, DOB: time.Date(1990, time.January, 1, 0, 0, 0, 0, time.UTC), Photo: photo, AadhaarLast4: last4, AccountID: s.accountID}, nil
}

// ─── HTTP (DigiLocker via API Setu) ─────────────────────────────────────────

// HTTPClient talks to DigiLocker's OAuth token endpoint and its eAadhaar XML
// endpoint. The shapes follow the published API Setu documentation and are
// pinned only by httptest; they have NOT been exercised against a live
// partner registration. The eAadhaar XML signature is not verified here: the
// document is fetched directly from DigiLocker over TLS with the user's own
// access token.
type HTTPClient struct {
	cfg    HTTPConfig
	client *http.Client
}

// NewHTTPClient builds the client. httpClient nil → an 8 s timeout.
func NewHTTPClient(cfg HTTPConfig, httpClient *http.Client) *HTTPClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 8 * time.Second}
	}
	return &HTTPClient{cfg: cfg, client: httpClient}
}

const maxProviderBody = 2 << 20

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	DigiLockerID string `json:"digilockerid"`
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
	body, err := h.do(req)
	if err != nil {
		return nil, err
	}
	var parsed tokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("%w: undecodable token response", ErrProvider)
	}
	if parsed.AccessToken == "" {
		return nil, fmt.Errorf("%w: token response had no access_token", ErrProvider)
	}
	return &Session{accessToken: parsed.AccessToken, accountID: parsed.DigiLockerID,
		eaadhaar: strings.EqualFold(strings.TrimSpace(parsed.EAadhaar), "Y")}, nil
}

// uidData is the one eAadhaar element read. Poa (the address) is not
// represented, so it is never parsed into memory as a field.
type uidData struct {
	UID string `xml:"uid,attr"`
	Poi struct {
		Name string `xml:"name,attr"`
		DOB  string `xml:"dob,attr"`
	} `xml:"Poi"`
	Pht string `xml:"Pht"`
}

func (h *HTTPClient) Identity(ctx context.Context, s *Session) (*Identity, error) {
	if s == nil || s.accessToken == "" {
		return nil, fmt.Errorf("%w: session required", ErrProvider)
	}
	if !s.eaadhaar {
		return nil, ErrNoAadhaar
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.cfg.EAadhaarURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build eAadhaar request", ErrProvider)
	}
	req.Header.Set("Authorization", "Bearer "+s.accessToken)
	body, err := h.do(req)
	if err != nil {
		return nil, err
	}
	data, err := findUIDData(body)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(data.Poi.Name)
	dob, ok := ParseDOB(data.Poi.DOB)
	if name == "" || !ok {
		return nil, ErrNoAadhaar
	}
	id := &Identity{Name: name, DOB: dob, AadhaarLast4: lastFourDigits(data.UID), AccountID: s.accountID}
	if pht := strings.Join(strings.Fields(data.Pht), ""); pht != "" {
		if base64.StdEncoding.DecodedLen(len(pht)) > MaxPhotoBytes {
			return nil, fmt.Errorf("%w: eAadhaar photo too large", ErrProvider)
		}
		photo, err := base64.StdEncoding.DecodeString(pht)
		if err != nil {
			return nil, fmt.Errorf("%w: undecodable eAadhaar photo", ErrProvider)
		}
		id.Photo = photo
	}
	return id, nil
}

// findUIDData returns the first UidData element wherever it sits
// (Certificate > CertificateData > KycRes > UidData in the published shape).
func findUIDData(body []byte) (*uidData, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil, ErrNoAadhaar
		}
		if err != nil {
			return nil, fmt.Errorf("%w: undecodable eAadhaar XML", ErrProvider)
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "UidData" {
			var d uidData
			if err := dec.DecodeElement(&d, &se); err != nil {
				return nil, fmt.Errorf("%w: undecodable eAadhaar XML", ErrProvider)
			}
			return &d, nil
		}
	}
}

// lastFourDigits keeps only the trailing four digits of a (masked) uid.
// A full twelve-digit number, should one ever arrive, is reduced the same
// way and goes no further.
func lastFourDigits(uid string) string {
	digits := make([]byte, 0, 4)
	for i := len(uid) - 1; i >= 0 && len(digits) < 4; i-- {
		c := uid[i]
		if c < '0' || c > '9' {
			break
		}
		digits = append(digits, c)
	}
	if len(digits) != 4 {
		return ""
	}
	return string([]byte{digits[3], digits[2], digits[1], digits[0]})
}

// ParseDOB reads the date formats DigiLocker uses (DD-MM-YYYY, DDMMYYYY,
// DD/MM/YYYY, YYYY-MM-DD).
func ParseDOB(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{"02-01-2006", "02012006", "02/01/2006", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func (h *HTTPClient) do(req *http.Request) ([]byte, error) {
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: unreachable", ErrProvider)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: status %d", ErrProvider, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderBody+1))
	if err != nil {
		return nil, fmt.Errorf("%w: unreadable response", ErrProvider)
	}
	if len(body) > maxProviderBody {
		return nil, fmt.Errorf("%w: response too large", ErrProvider)
	}
	return body, nil
}
