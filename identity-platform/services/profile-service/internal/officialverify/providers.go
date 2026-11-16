package officialverify

import (
	"context"
	"errors"
	"time"
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
