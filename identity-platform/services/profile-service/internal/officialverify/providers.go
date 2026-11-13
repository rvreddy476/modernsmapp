package officialverify

import (
	"context"
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

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

// RegistryQuery asks a registry about one GSTIN or CIN.
type RegistryQuery struct {
	// Kind is verification.RegistrationGSTIN or RegistrationCIN.
	Kind string
	// Number is the normalised registration number.
	Number string
	// ClaimedName is the applicant's legal name, for providers whose API
	// takes it. It is never trusted as the answer: the service compares it
	// with RegistryRecord.LegalName itself.
	ClaimedName string
}

// RegistryRecord is the registry's answer.
type RegistryRecord struct {
	// Found is false when the registry has no such registration.
	Found bool
	// Active is false for a cancelled, struck-off or suspended one.
	Active bool
	// LegalName is the registered legal name (GST trade/legal name or the
	// MCA company name).
	LegalName string
	Provider  string
}

// ErrRegistryUnavailable: no answer. Never a pass and never a refusal.
var ErrRegistryUnavailable = errors.New("registry is unavailable")

// RegistryVerifier confirms a registration with an authoritative source (a
// GSTN search provider or the MCA master data). No production provider is
// built yet: the founder chooses one (for example a GSP for GSTIN search and
// an MCA21 data provider for CIN), and it becomes a new mode in
// RegistryFromEnv.
type RegistryVerifier interface {
	Lookup(ctx context.Context, q RegistryQuery) (*RegistryRecord, error)
}

// MockRegistry answers from Records, or, with EchoName, confirms every
// registration under the claimed name. Development only: RegistryFromEnv
// refuses it in production, where it would confirm anything.
type MockRegistry struct {
	Records  map[string]RegistryRecord
	EchoName bool
	Err      error
}

// Lookup implements RegistryVerifier.
func (m *MockRegistry) Lookup(_ context.Context, q RegistryQuery) (*RegistryRecord, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	if r, ok := m.Records[q.Number]; ok {
		return &r, nil
	}
	if m.EchoName {
		return &RegistryRecord{Found: true, Active: true, LegalName: q.ClaimedName, Provider: "mock"}, nil
	}
	return &RegistryRecord{Found: false, Provider: "mock"}, nil
}

// ---------------------------------------------------------------------------
// E-mail code delivery
// ---------------------------------------------------------------------------

// CodeMailer delivers the domain-proof code to the signatory address.
type CodeMailer interface {
	SendDomainCode(ctx context.Context, to, organisation, code string, ttl time.Duration) error
}

// MockMailer delivers nothing and records the last code in memory, for
// tests. In a development deployment (ORG_VERIFICATION_EMAIL_MODE=mock) the
// service issues the fixed MockEmailCode instead of a random one, so the
// flow can be walked without a mailbox. Refused in production.
type MockMailer struct {
	Err      error
	LastTo   string
	LastCode string
	Sent     int
}

// SendDomainCode implements CodeMailer.
func (m *MockMailer) SendDomainCode(_ context.Context, to, _, code string, _ time.Duration) error {
	if m.Err != nil {
		return m.Err
	}
	m.LastTo, m.LastCode = to, code
	m.Sent++
	return nil
}

// MockEmailCode is the code a development deployment in email mock mode
// issues. Never used in production (the mock mode is refused there).
const MockEmailCode = "246810"

// ---------------------------------------------------------------------------
// Admin roles (auth-service)
// ---------------------------------------------------------------------------

// ErrRolesUnavailable: the role read failed. Never treated as "is admin".
var ErrRolesUnavailable = errors.New("role check is unavailable")

// AdminRole is the role an approver or reviewer must hold. auth-service
// answers the implication-expanded list, so a superadmin carries it too.
const AdminRole = "admin"

// RoleChecker answers whether a user holds a platform role.
type RoleChecker interface {
	HasRole(ctx context.Context, userID uuid.UUID, role string) (bool, error)
}

// AuthRoleClient reads GET {base}/v1/auth/internal/roles/:userId on
// auth-service with the internal service key.
type AuthRoleClient struct {
	base string
	key  string
	http *http.Client
}

// NewAuthRoleClient returns a client, or nil when base or key is empty.
func NewAuthRoleClient(base, key string) *AuthRoleClient {
	base, key = strings.TrimRight(strings.TrimSpace(base), "/"), strings.TrimSpace(key)
	if base == "" || key == "" {
		return nil
	}
	return &AuthRoleClient{base: base, key: key, http: &http.Client{Timeout: 3 * time.Second}}
}

// HasRole implements RoleChecker. Any failure is ErrRolesUnavailable.
func (c *AuthRoleClient) HasRole(ctx context.Context, userID uuid.UUID, role string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/auth/internal/roles/"+url.PathEscape(userID.String()), nil)
	if err != nil {
		return false, ErrRolesUnavailable
	}
	req.Header.Set("X-Internal-Service-Key", c.key)
	req.Header.Set("X-Caller-Service", "profile-service")
	resp, err := c.http.Do(req)
	if err != nil {
		return false, ErrRolesUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%w: status %d", ErrRolesUnavailable, resp.StatusCode)
	}
	var body struct {
		Data struct {
			UserID string   `json:"user_id"`
			Roles  []string `json:"roles"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return false, ErrRolesUnavailable
	}
	if body.Data.UserID != userID.String() {
		return false, ErrRolesUnavailable
	}
	for _, r := range body.Data.Roles {
		if r == role {
			return true, nil
		}
	}
	return false, nil
}
