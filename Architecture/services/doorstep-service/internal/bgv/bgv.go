// Package bgv is the background-check provider interface. At launch the
// check is a Police Clearance Certificate the professional uploads and an
// admin reviews (mode uploaded_document, the one manual step the founder
// allowed); a vendor (AuthBridge / IDfy / OnGrid) plugs in later behind the
// same interface (mode provider).
//
// Modes (DOORSTEP_BACKGROUND_CHECK_MODE):
//
//	uploaded_document  Initiate answers pending: the admin decides through
//	                   the document review. No webhook, nothing to fetch.
//	mock               Initiate answers clear at once (development only;
//	                   refused in production: it clears everyone).
//	provider           refused everywhere until a vendor is integrated.
//
// A check is valid for Validity from the certificate's issue date.
package bgv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	ModeMock             = "mock"
	ModeUploadedDocument = "uploaded_document"
	ModeProvider         = "provider"
)

// Statuses (doorstep.background_checks.status).
const (
	StatusPending  = "pending"
	StatusClear    = "clear"
	StatusConsider = "consider"
	StatusFailed   = "failed"
	StatusExpired  = "expired"
)

var (
	// ErrNotSupported: the provider has no such operation (no webhook, no
	// fetch). The webhook route answers 404 for it.
	ErrNotSupported = errors.New("bgv: not supported by this provider")
	// ErrSignature: a webhook whose signature does not verify (401).
	ErrSignature = errors.New("bgv: webhook signature invalid")
)

// ValidUntil is the last instant a check from a certificate issued on
// issuedOn is valid (exclusive): twelve calendar months.
func ValidUntil(issuedOn time.Time) time.Time { return issuedOn.AddDate(1, 0, 0) }

// Request starts a check for one professional's document.
type Request struct {
	ProID      uuid.UUID
	DocumentID uuid.UUID
	IssuedOn   time.Time
}

// Result is a provider's answer.
type Result struct {
	Status      string
	ExternalRef string
	ValidFrom   *time.Time
	ValidUntil  *time.Time
}

// Event is a verified webhook.
type Event struct {
	EventID     string
	ExternalRef string
	Result      Result
}

// Provider is a background-check source.
type Provider interface {
	Name() string
	Initiate(ctx context.Context, req Request) (Result, error)
	Fetch(ctx context.Context, externalRef string) (Result, error)
	VerifyWebhook(header http.Header, body []byte) (Event, error)
}

// New selects the provider. mock is refused in production; provider is
// refused everywhere (no vendor exists yet).
func New(mode string, production bool) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ModeUploadedDocument:
		return uploadedDocument{}, nil
	case ModeMock:
		if production {
			return nil, fmt.Errorf("bgv: DOORSTEP_BACKGROUND_CHECK_MODE=mock is refused in production: it clears every professional")
		}
		return mock{}, nil
	case ModeProvider:
		return nil, fmt.Errorf("bgv: DOORSTEP_BACKGROUND_CHECK_MODE=provider has no vendor integrated yet; use uploaded_document")
	}
	return nil, fmt.Errorf("bgv: unknown DOORSTEP_BACKGROUND_CHECK_MODE %q", mode)
}

// uploadedDocument: the admin reviews the certificate.
type uploadedDocument struct{}

func (uploadedDocument) Name() string { return ModeUploadedDocument }

func (uploadedDocument) Initiate(_ context.Context, _ Request) (Result, error) {
	return Result{Status: StatusPending}, nil
}

func (uploadedDocument) Fetch(context.Context, string) (Result, error) {
	return Result{}, ErrNotSupported
}

func (uploadedDocument) VerifyWebhook(http.Header, []byte) (Event, error) {
	return Event{}, ErrNotSupported
}

// mock clears every check at once (development only).
type mock struct{}

func (mock) Name() string { return ModeMock }

func (mock) Initiate(_ context.Context, req Request) (Result, error) {
	from := req.IssuedOn
	until := ValidUntil(from)
	return Result{Status: StatusClear, ValidFrom: &from, ValidUntil: &until}, nil
}

func (mock) Fetch(context.Context, string) (Result, error) { return Result{}, ErrNotSupported }

func (mock) VerifyWebhook(http.Header, []byte) (Event, error) { return Event{}, ErrNotSupported }
